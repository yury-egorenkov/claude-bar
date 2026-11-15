// ClaudeBar: Claude Code plan usage limits in the macOS menu bar.
//
// This file only renders. The numbers come from the Go helper next to this binary
// (`Contents/MacOS/claudebar-cli json`): the usage API (token sent to api.anthropic.com
// only) and the Claude Code status line.

import AppKit
import ServiceManagement
import SwiftUI

// MARK: - Data from `claudebar-cli json`

// By how early the limit runs out at the current pace (the Go helper decides):
// ok lasts until the reset; soon up to a day early (yellow); warn more than a day (orange);
// bad more than 2 days or under 10% left (red). For the 5-hour window, in proportion.
enum Level: String, Decodable {
    case unknown, ok, soon, warn, bad

    // On pace the app stays quiet: system accent in the panel, monochrome in the menu bar.
    var color: Color { self == .ok ? .accentColor : Color(nsColor: nsColor) }

    var nsColor: NSColor {
        switch self {
        case .ok: return .controlAccentColor
        case .soon: return .systemYellow
        case .warn: return .systemOrange
        case .bad: return .systemRed
        case .unknown: return .systemGray
        }
    }

    var alarming: Bool { self == .soon || self == .warn || self == .bad }

    var label: String {
        switch self {
        case .soon: return "Tight"
        case .warn: return "Ahead of pace"
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
}

struct ModelView: Decodable, Identifiable {
    var name = ""
    var left: Double = 0
    var resetsAt: Int64 = 0
    var days: [DayView] = []
    var id: String { name }
}

// One calendar day of the weekly window; state: past, today, future. The first and the
// last day are cut by the reset time, so the budget is each day's share of the week.
struct DayView: Decodable, Identifiable {
    var start: Int64 = 0
    var end: Int64 = 0
    var state = "future"
    var budget: Double = 0
    var left: Double = 0
    var timeLeft: Double = 0
    var ahead = false
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
    private func runHelper(_ args: [String]) throws -> Data {
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
        if done.wait(timeout: .now() + 5) == .timedOut {
            proc.terminate()
            throw HelperError(message: "claudebar-cli \(args.joined(separator: " ")) did not finish in 5 s")
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

// MARK: - Menu bar badge

// A rounded rect with the weekly % left inside. On pace: outline, template image,
// so macOS tints it like the system icons. Running out early: filled yellow, orange or red.
func badgeImage(text: String, level: Level) -> NSImage {
    let font = NSFont.monospacedDigitSystemFont(ofSize: 11, weight: .semibold)
    let alarming = level.alarming
    let fill = level.nsColor
    // Dark text on yellow and orange, white on red; the template image is plain black.
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
                    Limits(snap: snap)
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

    var body: some View {
        HStack(alignment: .center) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Claude usage")
                    .font(.system(size: 15, weight: .semibold))
                Text(snap.updatedAt > 0 ? "Updated \(ago(snap.updatedAt))" : "No data yet")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if snap.level.alarming || (snap.week.has && snap.week.left <= 0) {
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
                if let forecast {
                    Text(forecast)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(snap.week.level.color)
                }
                Text(dayNote)
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
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

    private var forecast: String? {
        let week = snap.week
        if week.left <= 0 { return "Used up until the reset" }
        if week.runOutAt == 0 || !week.level.alarming { return nil }
        return "Runs out \(when(week.runOutAt)) even at the daily budget"
    }

    // "Today 3% of 14% · 15% a day for 4 days"
    private var dayNote: String {
        let week = snap.week
        let secs = max(0, TimeInterval(week.resetsAt) - Date().timeIntervalSince1970)
        let daysLeft = max(1, Int((secs / 86400).rounded(.up)))
        let budget = "\(percent(week.perDay)) a day for \(daysLeft) \(daysLeft == 1 ? "day" : "days")"
        guard let today = snap.days.first(where: { $0.state == "today" }), today.known else { return budget }
        return "Today \(percent(today.spent)) of \(percent(today.budget)) · \(budget)"
    }
}

// One cell per calendar day of the weekly window (8 when the reset is not at midnight).
// Blue is tokens: the week's budget laid over the days, consumed from the first day on,
// whatever the date; the number is the % of that day's budget left. Green is the clock:
// the time left of that day. Usage ahead of time turns the spent part orange.
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
        // Usage ate into this day ahead of its time: the spent part turns orange.
        let track: Color = day.ahead ? Color(nsColor: .systemOrange).opacity(0.6) : Color.primary.opacity(0.08)
        // % of the day's budget left, inside the cell.
        let label = percent(left * 100)

        VStack(spacing: 3) {
            Text(weekday(day.start))
                .font(.system(size: 10, weight: today ? .semibold : .regular))
                .foregroundStyle(today ? HierarchicalShapeStyle.primary : HierarchicalShapeStyle.secondary)
            GeometryReader { geo in
                let fill = geo.size.width * min(left, 1)
                let text = Text(label).font(.system(size: 10, weight: .semibold)).monospacedDigit()

                ZStack(alignment: .leading) {
                    RoundedRectangle(cornerRadius: 4)
                        .fill(track)
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
        .help("Budget \(percent(day.budget)) of the week, \(percent(left * 100)) of it left" + (day.known ? " · \(percent(day.spent)) spent on this day" : ""))
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
            MenuRow(title: "Refresh") { model.refresh() }
            MenuRow(title: "Start at Login", checked: model.startAtLogin) { model.startAtLogin.toggle() }
            Divider().padding(.vertical, 6).padding(.horizontal, 8)
            MenuRow(title: "ClaudeBar by egorsky.com") {
                if let url = URL(string: "https://egorsky.com") { NSWorkspace.shared.open(url) }
            }
            MenuRow(title: "Quit ClaudeBar") { NSApp.terminate(nil) }
        }
    }
}

// Every row keeps the checkmark column, so titles line up like in a native menu.
struct MenuRow: View {
    var title: String
    var checked = false
    var action: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                Image(systemName: "checkmark")
                    .font(.system(size: 11, weight: .semibold))
                    .opacity(checked ? 1 : 0)
                    .frame(width: 12)
                Text(title).font(.system(size: 14))
                Spacer()
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

func ago(_ epoch: Int64) -> String {
    let secs = max(0, Int(Date().timeIntervalSince1970 - TimeInterval(epoch)))
    if secs < 60 { return "just now" }
    if secs < 3600 { return "\(secs / 60) min ago" }
    if secs < 172800 { return "\(secs / 3600) h ago" }
    return "\(secs / 86400) d ago"
}
