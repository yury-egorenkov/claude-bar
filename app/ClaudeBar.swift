// ClaudeBar: Claude Code plan usage limits in the macOS menu bar.
//
// This file only renders. The numbers come from the Go helper next to this binary
// (`Contents/MacOS/claudebar-cli json`): the usage API (token sent to api.anthropic.com
// only) and the Claude Code status line.

import AppKit
import ServiceManagement
import SwiftUI

// MARK: - Data from `claudebar-cli json`

// By how early the limit runs out (the Go helper decides): ok lasts until the reset;
// soon runs out up to a day early (yellow); bad more than a day early or under 10% left
// (red). For the 5-hour window, in proportion.
enum Level: String, Decodable {
    case unknown, ok, soon, bad

    // On pace the app stays quiet: system accent in the panel, monochrome in the menu bar.
    var color: Color { self == .ok ? .accentColor : Color(nsColor: nsColor) }

    var nsColor: NSColor {
        switch self {
        case .ok: return .controlAccentColor
        case .soon: return .systemYellow
        case .bad: return .systemRed
        case .unknown: return .systemGray
        }
    }

    var alarming: Bool { self == .soon || self == .bad }

    // For text: system yellow is unreadable on a light background, so in the light
    // appearance yellow text is a dark amber.
    var textColor: Color {
        guard self == .soon else { return color }
        return Color(nsColor: NSColor(name: nil) { appearance in
            appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
                ? .systemYellow
                : NSColor(srgbRed: 0x9A / 255, green: 0x67 / 255, blue: 0, alpha: 1)
        })
    }

    var label: String {
        switch self {
        case .soon: return "Runs out early"
        case .bad: return "Slow down"
        default: return ""
        }
    }
}

struct LimitView: Decodable {
    var has = false
    var left: Double = 0
    var evenLeft: Double = 0
    var resetsAt: Int64 = 0
    var runOutAt: Int64 = 0
    var perDay: Double = 0
    var level: Level = .unknown
    var rate: Double = 0
    var rateRunOut: Int64 = 0
}

struct ModelView: Decodable, Identifiable {
    var name = ""
    var left: Double = 0
    var resetsAt: Int64 = 0
    var days: [DayView] = []
    var id: String { name }
}

// One day of the weekly window: tokens over 24 h from the reset time, the clock (state,
// timeLeft) over the calendar date it is labeled with.
struct DayView: Decodable, Identifiable {
    var start: Int64 = 0
    var end: Int64 = 0
    var state = "future"
    var budget: Double = 0
    var left: Double = 0
    var timeLeft: Double = 0
    var over: Double = 0
    var spent: Double = 0
    var known = false
    var id: Int64 { start }
}

struct Snapshot: Decodable {
    var connected = true
    var updatedAt: Int64 = 0
    var level: Level = .unknown
    var badge = "–"
    var week = LimitView()
    var fiveHour = LimitView()
    var models: [ModelView] = []
    var days: [DayView] = []
    var error = ""
    var config = AppConfig()
    var resets: [ResetGrant] = []
    var autoStopAt: Int64 = 0   // auto stop armed: fires then unless the rate drops
}

// A limit reset the account can use ("Reset for free" in the Claude app).
struct ResetGrant: Decodable, Identifiable {
    var id = ""
    var label = ""
    var type = ""          // "weekly": clears the week; "full": gives back what Fable used
    var resetsLeft = 0
    var endsAt = ""        // RFC 3339
    var usableNow = false
    var givesBackWeekly = false
}

// `config` in the snapshot (not `Settings`: that is the SwiftUI scene): pace "hours" (or empty) / "days", quiet nights.
struct AppConfig: Decodable {
    var pace = ""
    var night = false
    var autoStop = false
    var byDays: Bool { pace == "days" }
}

// MARK: - Model

final class Model: ObservableObject {
    @Published var snapshot = Snapshot()
    @Published var error: String?

    private var timer: Timer?
    private let helper = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/claudebar-cli")

    init() {
        refresh()
        let timer = Timer(timeInterval: 20, repeats: true) { [weak self] _ in self?.refresh() }
        RunLoop.main.add(timer, forMode: .common)
        self.timer = timer
    }

    func refresh() {
        do {
            let data = try runHelper(["json"])
            let decoder = JSONDecoder()
            decoder.keyDecodingStrategy = .convertFromSnakeCase
            snapshot = try decoder.decode(Snapshot.self, from: data)
            error = nil
        } catch {
            self.error = "\(error)"
        }
    }

    func connect() {
        do {
            _ = try runHelper(["connect"])
            refresh()
        } catch {
            self.error = "\(error)"
        }
    }

    // `claudebar-cli set pace hours|days`, `set night on|off`.
    func set(_ key: String, _ value: String) {
        do {
            _ = try runHelper(["set", key, value])
            refresh()
        } catch {
            self.error = "\(error)"
        }
    }

    var startAtLogin: Bool {
        get { SMAppService.mainApp.status == .enabled }
        set {
            do {
                if newValue {
                    try SMAppService.mainApp.register()
                } else {
                    try SMAppService.mainApp.unregister()
                }
            } catch {
                self.error = "Start at Login: \(error.localizedDescription)"
            }
            objectWillChange.send()
        }
    }

    // The helper finishes in milliseconds, so it runs on the main thread,
    // with a 5 s limit so a stuck helper can never freeze the menu bar.
    // Uses one limit reset after a confirmation. The helper talks to the network, so it
    // runs off the main thread with a longer limit.
    @Published var resetting = false

    func reset(_ grant: ResetGrant) {
        let alert = NSAlert()
        alert.messageText = "Use this limit reset?"
        alert.informativeText = "\(grant.label)\n\nIt can't be undone."
        alert.addButton(withTitle: "Reset")
        alert.addButton(withTitle: "Cancel")
        NSApp.activate(ignoringOtherApps: true)
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        resetting = true
        DispatchQueue.global().async {
            let result = Result { try self.runHelper(["reset", grant.id], timeout: 60) }
            DispatchQueue.main.async {
                self.resetting = false
                if case .failure(let error) = result { self.error = "Reset: \(error)" }
                self.refresh()
            }
        }
    }

    // Emergency stop: revoke every Claude Code login found on this Mac, after a warning
    // that says how many and what follows.
    func emergencyStop() {
        let count: String
        do {
            // Reading the Keychain may ask for permission: a long limit.
            let data = try runHelper(["logout-all", "--count"], timeout: 60)
            count = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "?"
        } catch {
            self.error = "Emergency stop: \(error)"
            return
        }

        let alert = NSAlert()
        alert.alertStyle = .critical
        alert.messageText = "Log out everywhere?"
        alert.informativeText = """
            Revokes \(count) Claude Code login(s) found on this Mac: the claude/ folders of your projects \
            and the Keychain. Every container using them stops at once and needs /login again.

            Logins made separately on servers are not visible here and are not touched.
            """
        alert.addButton(withTitle: "Log out everywhere")
        alert.addButton(withTitle: "Cancel")
        NSApp.activate(ignoringOtherApps: true)
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        DispatchQueue.global().async {
            let result = Result { try self.runHelper(["logout-all"], timeout: 120) }
            DispatchQueue.main.async {
                switch result {
                case .success(let data):
                    self.error = "Emergency stop: " + (String(data: data, encoding: .utf8) ?? "done")
                        .trimmingCharacters(in: .whitespacesAndNewlines)
                case .failure(let error):
                    self.error = "Emergency stop: \(error)"
                }
                self.refresh()
            }
        }
    }

    private func runHelper(_ args: [String], timeout: Double = 5) throws -> Data {
        // The helper must be a separate file, never this app (APFS ignores case:
        // "claudebar" would be "ClaudeBar").
        if let app = Bundle.main.executableURL, sameFile(app, helper) {
            throw HelperError(message: "helper \(helper.path) is the app itself; rebuild with make install")
        }
        let proc = Process()
        proc.executableURL = helper
        proc.arguments = args
        let out = Pipe()
        let err = Pipe()
        proc.standardOutput = out
        proc.standardError = err
        let done = DispatchSemaphore(value: 0)
        proc.terminationHandler = { _ in done.signal() }
        try proc.run()
        if done.wait(timeout: .now() + timeout) == .timedOut {
            proc.terminate()
            throw HelperError(message: "claudebar-cli \(args.joined(separator: " ")) did not finish in \(Int(timeout)) s")
        }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        if proc.terminationStatus != 0 {
            let msg = String(data: err.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            throw HelperError(message: msg.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        return data
    }
}

func sameFile(_ one: URL, _ two: URL) -> Bool {
    let ids = [one, two].map { try? $0.resourceValues(forKeys: [.fileResourceIdentifierKey]).fileResourceIdentifier }
    guard let first = ids[0], let second = ids[1] else { return false }
    return first.isEqual(second)
}

struct HelperError: Error, CustomStringConvertible {
    var message: String
    var description: String { message.isEmpty ? "claudebar helper failed" : message }
}

// MARK: - App

@main
struct ClaudeBarApp: App {
    @StateObject private var model = Model()

    // Two guards against self-spawning (once, a helper named "claudebar" overwrote
    // "ClaudeBar" on case-insensitive APFS; every copy started another):
    // 1. The app takes no arguments: started with any (e.g. "json"), it exits at once.
    // 2. One copy only: if ClaudeBar is already running, a new copy exits.
    init() {
        if CommandLine.arguments.count > 1 { exit(2) }
        let me = ProcessInfo.processInfo.processIdentifier
        let others = NSRunningApplication
            .runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier ?? "com.claudebar.app")
            .filter { $0.processIdentifier != me }
        if !others.isEmpty { exit(0) }
    }

    var body: some Scene {
        MenuBarExtra {
            Panel(model: model)
        } label: {
            Image(nsImage: badgeImage(text: model.snapshot.badge, level: model.snapshot.level))
        }
        .menuBarExtraStyle(.window)
    }
}

// The settings tabs. The window around them is AppKit (SettingsWindow): native
// preferences tabs, icons with titles in the toolbar.
struct BudgetTab: View {
    @ObservedObject var model: Model

    var body: some View {
        let conf = model.snapshot.config
        Form {
            Picker("Count the budget", selection: Binding(
                get: { conf.byDays ? "days" : "hours" },
                set: { model.set("pace", $0) }
            )) {
                Text("By hours: even over the week's hours").tag("hours")
                Text("By days: within today's tokens").tag("days")
            }
            .pickerStyle(.radioGroup)
            Toggle("Quiet nights 00–09: no budget at night", isOn: Binding(
                get: { conf.night },
                set: { model.set("night", $0 ? "on" : "off") }
            ))
        }
        .formStyle(.grouped)
    }
}

struct ResetsTab: View {
    @ObservedObject var model: Model

    var body: some View {
        Form {
            if model.snapshot.resets.isEmpty {
                Text("No limit resets available")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.snapshot.resets) { grant in
                    ResetRow(model: model, grant: grant)
                }
            }
            Text("A weekly reset clears the week; a Fable reset gives back what Fable used. Reset asks before using one.")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .formStyle(.grouped)
    }
}

struct ProtectionTab: View {
    @ObservedObject var model: Model

    var body: some View {
        let conf = model.snapshot.config
        Form {
            Section {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Emergency stop")
                        Text("Log out every Claude Code login found on this Mac. Every container using them needs /login again.")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    Spacer()
                    Button("Log out everywhere", role: .destructive) { model.emergencyStop() }
                        .tint(.red)
                }
            }
            Section {
                Toggle("Auto stop when running out", isOn: Binding(
                    get: { conf.autoStop },
                    set: { model.set("auto-stop", $0 ? "on" : "off") }
                ))
                Text("The same logout, without asking, when at the current rate the weekly limit runs out more than a day early for 10 minutes in a row. At most once a week; a notification tells the result.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .formStyle(.grouped)
    }
}

struct GeneralTab: View {
    @ObservedObject var model: Model

    var body: some View {
        Form {
            Toggle("Start at Login", isOn: Binding(
                get: { model.startAtLogin },
                set: { model.startAtLogin = $0 }
            ))
        }
        .formStyle(.grouped)
    }
}

// The settings window, made with AppKit: a menu bar app has no app menu, and macOS
// ignores the SwiftUI "show settings" action there. Native preferences tabs (an
// NSTabViewController in toolbar style). One window, reused, opened in the middle of
// the screen under the mouse.
enum SettingsWindow {
    private static var window: NSWindow?
    private static let size = NSSize(width: 500, height: 280)

    static func show(model: Model) {
        // Close the menu bar panel the click came from.
        NSApp.keyWindow?.close()

        if window == nil {
            let tabs = NSTabViewController()
            tabs.tabStyle = .toolbar
            tabs.addTabViewItem(item("Budget", "chart.bar", BudgetTab(model: model)))
            tabs.addTabViewItem(item("Resets", "arrow.counterclockwise", ResetsTab(model: model)))
            tabs.addTabViewItem(item("Protection", "exclamationmark.shield", ProtectionTab(model: model)))
            tabs.addTabViewItem(item("General", "gearshape", GeneralTab(model: model)))

            let made = NSWindow(contentViewController: tabs)
            made.title = "ClaudeBar Settings"
            made.styleMask = [.titled, .closable]
            made.toolbarStyle = .preference
            made.isReleasedWhenClosed = false
            window = made
        }
        guard let window else { return }
        window.setContentSize(size)
        center(window)
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
    }

    private static func item<V: View>(_ title: String, _ symbol: String, _ view: V) -> NSTabViewItem {
        let host = NSHostingController(rootView: view.frame(width: size.width, height: size.height))
        host.preferredContentSize = size
        let tab = NSTabViewItem(viewController: host)
        tab.label = title
        tab.image = NSImage(systemSymbolName: symbol, accessibilityDescription: title)
        return tab
    }

    // NSWindow.center() puts a window above the middle; this is the exact middle of the
    // visible area of the screen with the mouse on it.
    private static func center(_ window: NSWindow) {
        let mouse = NSEvent.mouseLocation
        let screen = NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) } ?? NSScreen.main
        guard let area = screen?.visibleFrame else { return window.center() }
        let frame = window.frame
        window.setFrameOrigin(NSPoint(x: area.midX - frame.width / 2, y: area.midY - frame.height / 2))
    }
}

// MARK: - Menu bar badge

// A rounded rect with the weekly % left inside. On pace: outline, template image,
// so macOS tints it like the system icons. Running out early: filled yellow or red.
func badgeImage(text: String, level: Level) -> NSImage {
    let font = NSFont.monospacedDigitSystemFont(ofSize: 11, weight: .semibold)
    let alarming = level.alarming
    let fill = level.nsColor
    // Dark text on yellow, white on red; the template image is plain black.
    let textColor: NSColor = level == .bad ? .white : (alarming ? NSColor.black.withAlphaComponent(0.85) : .black)
    let attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: textColor]
    let textSize = (text as NSString).size(withAttributes: attrs)
    let size = NSSize(width: max(ceil(textSize.width) + 10, 26), height: 16)

    let image = NSImage(size: size, flipped: false) { rect in
        if alarming {
            fill.setFill()
            NSBezierPath(roundedRect: rect, xRadius: 4, yRadius: 4).fill()
        } else {
            NSColor.black.setStroke()
            let path = NSBezierPath(roundedRect: rect.insetBy(dx: 0.75, dy: 0.75), xRadius: 3.5, yRadius: 3.5)
            path.lineWidth = 1.5
            path.stroke()
        }
        let origin = NSPoint(x: (rect.width - textSize.width) / 2, y: (rect.height - textSize.height) / 2)
        (text as NSString).draw(at: origin, withAttributes: attrs)
        return true
    }
    image.isTemplate = !alarming
    return image
}

// MARK: - Panel

struct Panel: View {
    @ObservedObject var model: Model

    var body: some View {
        let snap = model.snapshot

        VStack(alignment: .leading, spacing: 0) {
            Header(snap: snap)
                .padding(.horizontal, 14)

            Divider().padding(.vertical, 10)

            Group {
                if !snap.connected {
                    ConnectCard(model: model)
                } else if !snap.week.has {
                    EmptyCard()
                } else {
                    Limits(snap: snap, model: model)
                }

                ForEach([snap.error, model.error ?? ""].filter { !$0.isEmpty }, id: \.self) { error in
                    Text(error)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.top, 10)
                }
            }
            .padding(.horizontal, 14)

            Divider().padding(.vertical, 8)

            Actions(model: model, updatedAt: snap.updatedAt)
                .padding(.horizontal, 6)
        }
        .padding(.vertical, 12)
        .frame(width: 320)
        .onAppear { model.refresh() }
    }
}

struct Header: View {
    var snap: Snapshot

    private var stale: Bool {
        snap.updatedAt > 0 && Date().timeIntervalSince1970 - TimeInterval(snap.updatedAt) > 3600
    }

    var body: some View {
        HStack(alignment: .center) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Claude usage")
                    .font(.system(size: 15, weight: .semibold))
                // Data older than an hour: the age in red, the numbers below may be stale.
                Text(snap.updatedAt > 0 ? "Updated \(ago(snap.updatedAt))" : "No data yet")
                    .font(.system(size: 12, weight: stale ? .semibold : .regular))
                    .foregroundStyle(stale ? Color(nsColor: .systemRed) : Color.secondary)
            }
            Spacer()
            if snap.autoStopAt > 0 {
                // Auto stop armed: counts down; it disarms itself if the rate drops.
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    StatusPill(level: .bad, label: "Auto stop in \(clock(snap.autoStopAt - Int64(context.date.timeIntervalSince1970)))")
                }
            } else if snap.level.alarming || (snap.week.has && snap.week.left <= 0) {
                StatusPill(level: snap.level, label: snap.week.has && snap.week.left <= 0 ? "Limit reached" : snap.level.label)
            }
        }
    }
}

struct StatusPill: View {
    var level: Level
    var label: String

    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(level.color).frame(width: 7, height: 7)
            Text(label).font(.system(size: 12, weight: .medium))
        }
        .padding(.horizontal, 9)
        .padding(.vertical, 4)
        .background(Capsule().fill(level.color.opacity(0.15)))
    }
}

struct Limits: View {
    var snap: Snapshot
    @ObservedObject var model: Model

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("PLAN USAGE LIMITS")
                .font(.system(size: 11, weight: .semibold))
                .kerning(0.4)
                .foregroundStyle(.secondary)

            // Weekly limits: a title line and the 7-day strip instead of a bar.
            VStack(alignment: .leading, spacing: 8) {
                WeeklyLimit(title: "Weekly · all models", left: snap.week.left, resetsAt: snap.week.resetsAt,
                            days: snap.days, color: snap.week.level.color)
                // One line in % of the weekly limit; when the limit runs out early, the
                // forecast under it in the level's color.
                if snap.week.left <= 0 {
                    // Limit reached: a live countdown to the reset.
                    TimelineView(.periodic(from: .now, by: 1)) { context in
                        Text("Resets in \(clock(Int64(snap.week.resetsAt) - Int64(context.date.timeIntervalSince1970)))")
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundStyle(Color(nsColor: .systemRed))
                            .monospacedDigit()
                    }
                } else {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(budgetLine)
                            .font(.system(size: 12))
                            .foregroundStyle(.secondary)
                        // Only when the current rate runs out more than a day before the reset.
                        if rateLevel == .bad {
                            Text(rateLine)
                                .font(.system(size: 12, weight: .medium))
                                .foregroundStyle(rateLevel.textColor)
                        }
                    }
                    .monospacedDigit()
                }
            }

            ForEach(snap.models) { model in
                WeeklyLimit(title: "Weekly · \(model.name)", left: model.left, resetsAt: model.resetsAt,
                            days: model.days, color: .accentColor)
            }


            if snap.fiveHour.has {
                LimitRow(title: "5-hour session", left: snap.fiveHour.left, resetsAt: snap.fiveHour.resetsAt,
                         color: snap.fiveHour.level.color)
            }
        }
    }

    // "At 3%/h now: runs out Thu 15:20" — the current rate (last 2 h) carried forward.
    private var rateLine: String {
        let week = snap.week
        if week.left <= 0 { return "Limit reached until the reset" }
        let rate = week.rate < 0.1 ? "<0.1%/h" : String(format: "%.1f%%/h", week.rate)
        if week.rateRunOut == 0 { return "At \(rate) now: lasts until the reset" }
        return "At \(rate) now: runs out \(when(week.rateRunOut))"
    }

    // Yellow when the current rate runs out before the reset, red when more than a day early.
    private var rateLevel: Level {
        let week = snap.week
        if week.left <= 0 { return .bad }
        guard week.rateRunOut > 0 else { return .ok }
        return week.resetsAt - week.rateRunOut > 86400 ? .bad : .soon
    }

    // "Used 28% · 9% over budget · 13% a day": usage, against what the budget allows by
    // now (the pace setting decides), and what is left per day until the reset.
    private var budgetLine: String {
        let week = snap.week
        let used = "Used \(percent(100 - week.left))"
        if week.left <= 0 { return used + " · limit reached" }
        let over = week.evenLeft - week.left
        let budget = abs(over) < 0.5 ? "on budget" : "\(percent(abs(over))) \(over > 0 ? "over" : "under") budget"
        return "\(used) · \(budget) · \(percent(week.perDay)) a day"
    }
}

// One cell per day of the weekly window. Blue is tokens: 100/7 % per 24 h from the reset
// time, consumed from the first day on, whatever the date; the number is the % of that
// budget left, in % of the week. Green is the clock: the time left of the labeled calendar date, to midnight.
// Blue shorter than green turns the spent part orange.
struct DayStrip: View {
    var days: [DayView]

    var body: some View {
        HStack(spacing: 4) {
            ForEach(days) { day in
                DayCell(day: day)
            }
        }
    }
}

struct DayCell: View {
    var day: DayView

    var body: some View {
        let today = day.state == "today"
        let left = min(max(day.left, 0), 1)
        let over = min(max(day.over, 0), 1 - left)
        // Tokens left in this day, in % of the week: a full day shows 14%; the cells add
        // up to the week's % left.
        let leftOfWeek = left * day.budget
        let label = leftOfWeek > 0 && leftOfWeek < 0.5 ? "<1%" : percent(leftOfWeek)

        VStack(spacing: 3) {
            Text(weekday(day.start))
                .font(.system(size: 10, weight: today ? .semibold : .regular))
                .foregroundStyle(today ? HierarchicalShapeStyle.primary : HierarchicalShapeStyle.secondary)
            GeometryReader { geo in
                let fill = geo.size.width * min(left, 1)
                let text = Text(label).font(.system(size: 10, weight: .semibold)).monospacedDigit()

                // Used part from the left; its newest stretch, used ahead of the budget,
                // is orange right next to the blue.
                let overWidth = geo.size.width * over

                ZStack(alignment: .leading) {
                    RoundedRectangle(cornerRadius: 4)
                        .fill(Color.primary.opacity(0.08))
                    Rectangle()
                        .fill(Color(nsColor: .systemOrange).opacity(0.75))
                        .frame(width: overWidth)
                        .offset(x: geo.size.width - fill - overWidth)
                    RoundedRectangle(cornerRadius: 4)
                        .fill(Color.accentColor)
                        .frame(width: fill)
                        .offset(x: geo.size.width - fill)
                    // The number sits across the fill edge: white over the fill,
                    // regular text over the empty part.
                    text
                        .foregroundStyle(.primary)
                        .frame(width: geo.size.width)
                    text
                        .foregroundStyle(.white)
                        .frame(width: geo.size.width)
                        .mask(alignment: .trailing) { Rectangle().frame(width: fill) }
                }
            }
            .frame(height: 16)
            .clipShape(RoundedRectangle(cornerRadius: 4))
            .overlay(
                RoundedRectangle(cornerRadius: 4)
                    .stroke(Color.primary.opacity(today ? 0.45 : 0), lineWidth: 1)
            )
            // Time left of the day, like the budget above: at the right end, the past
            // grows from the left. Blue shorter than green: spending faster than time.
            GeometryReader { geo in
                let width = geo.size.width * min(max(day.timeLeft, 0), 1)
                ZStack(alignment: .leading) {
                    Capsule()
                        .fill(Color.primary.opacity(0.06))
                    Capsule()
                        .fill(Color(nsColor: .systemGreen))
                        .frame(width: width)
                        .offset(x: geo.size.width - width)
                }
            }
            .frame(height: 3)
        }
        .frame(maxWidth: .infinity)
        .help("\(percent(leftOfWeek)) of the week left of this day's \(percent(day.budget))" + (day.known ? " · \(percent(day.spent)) spent on this date" : ""))
    }
}

// One limit reset with its button; using it asks for a confirmation first.
struct ResetRow: View {
    @ObservedObject var model: Model
    var grant: ResetGrant

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(grant.type == "weekly" ? "Weekly reset" : "Fable reset")
                Text("\(grant.resetsLeft) left · expires \(expiry(grant.endsAt))")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button("Reset") { model.reset(grant) }
                .disabled(!grant.usableNow || model.resetting)
        }
        .help(grant.label)
    }

    private func expiry(_ iso: String) -> String {
        let parser = ISO8601DateFormatter()
        guard let date = parser.date(from: iso) else { return iso }
        let fmt = DateFormatter()
        fmt.locale = Locale(identifier: "en_GB")
        fmt.dateFormat = "d MMM"
        return fmt.string(from: date)
    }
}

// Title and "% left · Resets 6d".
struct LimitTitle: View {
    var title: String
    var left: Double
    var resetsAt: Int64

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title).font(.system(size: 14))
            Spacer()
            Text("\(percent(left)) left · Resets \(short(resetsAt))")
                .font(.system(size: 13))
                .foregroundStyle(.secondary)
                .monospacedDigit()
        }
        .help("Resets \(when(resetsAt))")
    }
}

// A weekly limit: title line, then the 7-day strip; a plain bar until the days arrive.
struct WeeklyLimit: View {
    var title: String
    var left: Double
    var resetsAt: Int64
    var days: [DayView]
    var color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            LimitTitle(title: title, left: left, resetsAt: resetsAt)
            if !days.isEmpty {
                DayStrip(days: days)
            } else {
                UsageBar(left: left, evenLeft: nil, color: color)
            }
        }
    }
}

// Title line and a thin bar (the 5-hour window).
struct LimitRow: View {
    var title: String
    var left: Double
    var resetsAt: Int64
    var evenLeft: Double? = nil
    var color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            LimitTitle(title: title, left: left, resetsAt: resetsAt)
            UsageBar(left: left, evenLeft: evenLeft, color: color)
        }
    }
}

// Track with the % left filled at the right end; the spent part grows from the left.
// The tick marks where the spent part would be at an even pace: past the tick means
// spending ahead of pace.
struct UsageBar: View {
    var left: Double
    var evenLeft: Double?
    var color: Color

    var body: some View {
        GeometryReader { geo in
            let width = geo.size.width
            let fill = left > 0 ? max(width * min(max(left, 0), 100) / 100, 6) : 0

            // Every layer has an explicit height: the taller tick must not stretch the bar.
            // What is left sits at the right end; the spent part grows from the left.
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(Color.primary.opacity(0.1))
                    .frame(height: 6)
                Capsule()
                    .fill(color)
                    .frame(width: fill, height: 6)
                    .offset(x: width - fill)
                if let evenLeft {
                    // Where the spent part would end at an even pace.
                    let tick = width * (1 - min(max(evenLeft, 0), 100) / 100)
                    Capsule()
                        .fill(Color.primary.opacity(0.55))
                        .frame(width: 2, height: 12)
                        .offset(x: min(max(tick - 1, 0), width - 2))
                }
            }
            .frame(width: width, height: 6)
            .animation(.easeOut(duration: 0.25), value: left)
        }
        .frame(height: 6)
    }
}

struct ConnectCard: View {
    @ObservedObject var model: Model

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Not connected")
                .font(.system(size: 15, weight: .semibold))
            Text("No Claude Code login found. Connecting adds the status line to ~/.claude/settings.json (with a backup).")
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button("Connect to Claude Code") { model.connect() }
                .buttonStyle(.borderedProminent)
        }
    }
}

struct EmptyCard: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Waiting for data")
                .font(.system(size: 15, weight: .semibold))
            Text("Limits appear after the next Claude Code reply or usage check.")
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

// Menu-style rows, like a native menu.
struct Actions: View {
    @ObservedObject var model: Model
    var updatedAt: Int64

    var body: some View {
        VStack(spacing: 0) {
            MenuRow(icon: "arrow.clockwise", title: "Refresh") { model.refresh() }
            MenuRow(icon: "gearshape", title: "Settings…") { SettingsWindow.show(model: model) }
            Divider().padding(.vertical, 6).padding(.horizontal, 8)
            MenuRow(icon: "globe", title: "ClaudeBar by egorsky.com") {
                if let url = URL(string: "https://egorsky.com") { NSWorkspace.shared.open(url) }
            }
            MenuRow(icon: "xmark.circle", title: "Quit ClaudeBar") { NSApp.terminate(nil) }
        }
    }
}

// Icon at the left edge, title, checkmark at the right edge.
struct MenuRow: View {
    var icon: String
    var title: String
    var tint: Color? = nil
    var checked = false
    var action: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: icon)
                    .font(.system(size: 13))
                    .foregroundStyle(tint ?? .secondary)
                    .frame(width: 18)
                Text(title)
                    .font(.system(size: 14))
                    .foregroundStyle(tint ?? .primary)
                Spacer()
                if checked {
                    Image(systemName: "checkmark")
                        .font(.system(size: 11, weight: .semibold))
                }
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .contentShape(Rectangle())
            .background(RoundedRectangle(cornerRadius: 6).fill(hover ? Color.primary.opacity(0.08) : .clear))
        }
        .buttonStyle(.plain)
        // No keyboard focus ring: the panel opens with the mouse, like a menu.
        .focusable(false)
        .onHover { hover = $0 }
    }
}

// MARK: - Formatting (English, 24-hour)

func percent(_ val: Double) -> String { "\(Int(val.rounded()))%" }

func when(_ epoch: Int64) -> String {
    let date = Date(timeIntervalSince1970: TimeInterval(epoch))
    let fmt = DateFormatter()
    fmt.locale = Locale(identifier: "en_GB")
    fmt.dateFormat = Calendar.current.isDateInToday(date) ? "HH:mm" : "EEE HH:mm"
    return fmt.string(from: date)
}

func weekday(_ epoch: Int64) -> String {
    let fmt = DateFormatter()
    fmt.locale = Locale(identifier: "en_GB")
    fmt.dateFormat = "EEE"
    return fmt.string(from: Date(timeIntervalSince1970: TimeInterval(epoch)))
}

// "6d", "5h", "40m", like the Claude app.
func short(_ epoch: Int64) -> String {
    let secs = max(0, Int(TimeInterval(epoch) - Date().timeIntervalSince1970))
    if secs >= 86400 { return "\(secs / 86400)d" }
    if secs >= 3600 { return "\(secs / 3600)h" }
    return "\(max(1, secs / 60))m"
}

// "2d 04:15:09", "2:54:10".
func clock(_ secs: Int64) -> String {
    let s = max(0, secs)
    let days = s / 86400
    let time = String(format: "%d:%02d:%02d", s % 86400 / 3600, s % 3600 / 60, s % 60)
    return days > 0 ? "\(days)d \(time)" : time
}

func ago(_ epoch: Int64) -> String {
    let secs = max(0, Int(Date().timeIntervalSince1970 - TimeInterval(epoch)))
    if secs < 60 { return "just now" }
    if secs < 3600 { return "\(secs / 60) min ago" }
    if secs < 172800 { return "\(secs / 3600) h ago" }
    return "\(secs / 86400) d ago"
}
