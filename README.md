# ClaudeBar

A macOS menu bar app that shows how much of your Claude plan usage limits is left (weekly, per model, 5-hour) and whether you are spending too fast. It warns before the weekly limit runs out ahead of the reset.

Made by [egorsky.com](https://egorsky.com). MIT License.

<img src="screenshot.png" width="400" alt="ClaudeBar on budget: outline badge, day cells for all models and for Fable"> <img src="screenshot-ahead.png" width="400" alt="ClaudeBar ahead of budget: orange badge, Ahead of pace pill and the run-out forecast">

## Menu bar

A rounded rect with the weekly % left inside. Its color shows how far usage is ahead of the daily budget (the same budget the day cells show: the week split evenly over its calendar days), counted in days:

| Look | Ahead of the daily budget |
|------|---------------------------|
| Outline, system color | less than 0.15 of a day |
| Yellow | up to 1 day |
| Orange | 1 to 2 days |
| Red | more than 2 days, or under 10% left |
| `–` | no data yet |

One day ahead means that even keeping to the daily budget from now on, the limit runs out a day before the reset. To leave a level, the value has to fall 0.1 day below its threshold, so a value sitting on a threshold does not flip the color back and forth. The 5-hour window has its own rule: red or orange when, at the current rate, it runs out before its reset; the worse of the two wins.

## Panel

Click the badge:

```
Claude usage                         ● Ahead of pace   (only when running out early)
Updated just now
────────────────────────────────────────────
PLAN USAGE LIMITS
Weekly · all models         62% left · Resets 3d
 Sat   Sun   Mon   Tue   Wed   Thu   Fri   Sat
[ 0%] [ 0%] [ 0%] [96%] [100%][100%][100%][100%]  tokens: budget left per day
               ▔▔▔▔ ▔▔▔▔ ▔▔▔▔ ▔▔▔▔ ▔▔▔▔  green: time left of the day
Runs out Fri 18:20 even at the daily budget       (only when ahead)
Today 3% of 14% · 15% a day for 4 days
Weekly · Fable             100% left · Resets 3d
 Sat   Sun   Mon   Tue   Wed   Thu   Fri   Sat
[100%][100%][100%][100%][100%][100%][100%][100%]
5-hour session              76% left · Resets 2h
[░░░░░░██████████████████]
────────────────────────────────────────────
Refresh
✓ Start at Login
ClaudeBar by egorsky.com
Quit ClaudeBar
```

Weekly limits (all models and each model) show a 7-day strip instead of a bar; the 5-hour window keeps a bar. Both show what is left at the right end; the spent part grows from the left, like time through the week.

### Days

Cells are calendar days, midnight to midnight; today's name is bold and switches at midnight. A week that resets at 14:00 covers 8 days; the first and the last are cut by the reset time (10 h and 14 h). Every day holds the same token budget: 100% split evenly, 12.5% for 8 days.

Each cell has two measures, independent of each other:

- **Blue: tokens.** The week's budget is laid over the days in order, an equal share each, and usage consumes it from the first day on, whatever the date. Unspent budget of a past day stays in that day until usage reaches it. The number inside is the % of that day's budget left. When usage has consumed more of a day than its time has passed (by more than 15% of the day), the spent part of that cell turns orange: you are ahead of time.
- **Green: the clock.** The thin line under each cell is the time left of that day: empty for past days, shrinking through today, full for future days.

Both keep what is left at the right end. Hover a cell for its budget and that calendar day's actual spend.

The cells need only the current percentage. For the actual spend per calendar day (the hover and the "Today" line) the app keeps a history: every fetch appends `{at, limit, used, resets_at}` to `~/.config/claudebar/history.jsonl` for all models and for each model limit (kept for 8 days). A day's spend is the difference between the last samples at its start and at its end. Nothing is sampled while the Mac is off; that spend lands on the day the Mac is back.

## How it works

```
usage API (api.anthropic.com) ─┐
                               ├→ claudebar-cli json → ClaudeBar → menu bar
Claude Code status line ───────┘
```

Two sources. Within one window usage only grows, so where they disagree by a point (rounding, a reply not sent yet) the larger value is used:

1. **Usage API**: the endpoint Claude Code itself calls (`api.anthropic.com/api/oauth/usage`). It returns weekly, per-model and 5-hour limits, including usage from claude.ai. It is asked at most every 2 minutes, in the background. The token is Claude Code's OAuth token, from `.credentials.json` in the Claude config dirs (Claude Code on Linux) or the macOS Keychain (Claude Code on the Mac). It is only read, never refreshed: refreshing rotates the refresh token and would log Claude Code out, so an expired token waits until Claude Code renews it.
2. **Status line**: the `rate_limits` field that Claude Code passes to the status line command after each reply. No token involved.

| Part | What it does |
|------|--------------|
| `ClaudeBar` (SwiftUI, `app/ClaudeBar.swift`) | runs `claudebar-cli json` every 20 s and when the panel opens; draws the badge and the panel. Rendering only |
| `claudebar-cli json` (Go) | pace, forecast, daily budget, levels, days: everything the app shows. Starts `claudebar-cli fetch` in the background when the API result is older than 2 min |
| `claudebar-cli fetch` (Go) | one usage API request. Result in `~/.config/claudebar/usage.json`, error in `usage-error.txt`, a weekly sample appended to `history.jsonl` |
| `claudebar-cli statusline` (Go) | status line command: keeps only `rate_limits`, writes `~/.claude/claudebar.json` (mode 600). Prints a line only when a limit runs out before its reset (`week 25% left · runs out Thu 02:00 · slow down`), otherwise nothing |

All math is in Go and covered by tests. Both binaries live in `ClaudeBar.app/Contents/MacOS/`.

## Security

| Check | Result |
|-------|--------|
| Claude Code token | read from `.credentials.json` or the Keychain; sent only to `api.anthropic.com` (the one URL in the code, `UsageURL` in `usage.go`); never written anywhere, never refreshed, never printed in errors |
| Network | one request every 2 minutes or less to `api.anthropic.com`. No analytics, no update checks, no other hosts. The Swift app makes no requests |
| Files it writes | `~/.claude/claudebar.json`, `~/.config/claudebar/usage.json`, `history.jsonl`: percentages, reset times, model names, timestamps. No token. Paths, session text and `transcript_path` from the status line input are discarded while parsing |
| External programs | `/usr/bin/security` (Keychain, only when no live token is found in files); `/bin/sh` only with `statusline -then` (your previous status line command) |
| What it changes | `~/.claude/settings.json` only via the Connect button, after a backup to `settings.json.claudebar-backup`; the login item only via Start at Login |
| Dependencies | Go: [`mitranim/gg`](https://github.com/mitranim/gg). Swift: system frameworks only |

## Install

Requires macOS 13+, Go and the Xcode Command Line Tools (`xcode-select --install`).

```bash
git clone https://github.com/<owner>/claude-bar && cd claude-bar
make install
```

This builds `ClaudeBar.app`, signs it ad hoc (no certificate), copies it to `/Applications` and starts it. If Claude Code is logged in on this Mac, the badge fills in within a few seconds from the usage API.

Optional: click the badge → **Connect to Claude Code** (shown when no login is found) to also use the status line. It adds `statusLine` to `~/.claude/settings.json` and saves the old file as `settings.json.claudebar-backup`; an existing status line keeps working, its command runs via `-then`. Key order in `settings.json` becomes alphabetical.

### Claude Code in a container

If Claude Code runs in a Linux container whose `~/.claude` is a folder on the Mac:

1. Inside the container, in this folder: `make container`. Builds the Linux helper into the container's `~/.claude/bin/` and connects its status line.
2. On the Mac: `make install CLAUDE_DIR=/path/to/that/folder`. Writes the folder to `~/.config/claudebar/dirs`; the app then reads its `claudebar.json` and `.credentials.json` too.

## Make targets

| Target | What it does |
|--------|--------------|
| `make install` | build, copy to `/Applications`, start |
| `make app` | build `build/ClaudeBar.app` only |
| `make run` / `make stop` | start / stop the installed app |
| `make debug` | run the app in the foreground, output to `build/run.log` |
| `make show` | limits as text, without the app |
| `make test` | `go vet` and tests |
| `make container` | inside a Linux container with Claude Code: build the helper, connect the status line |
| `make uninstall` | remove the app, `~/.claude/claudebar.json` and `~/.config/claudebar` |
| `make git.push` | publish this folder as its own GitHub repo (see below) |

Builds keep the full output, with tool versions, in `build/build.log`.

## Publishing

`make git.push` copies the committed files of this folder (`git archive`, no history of the parent repo) into `PUBLISH_DIR` (default `~/Projects/claude-bar`), commits them there and pushes. The first run creates the public GitHub repo `claude-bar` (`GH_REPO`) with `gh repo create`; later runs push an update commit. Requires [`gh`](https://cli.github.com) logged in (`gh auth login`).

```bash
make git.push                                       # author: your git config
make git.push NAME="Name" EMAIL="mail@example.com"  # explicit author
make git.push MSG="Day strip numbers inside"        # commit message
```

## Uninstall

Turn off Start at Login in the panel first if it is on, then:

```bash
make uninstall
```

and restore `~/.claude/settings.json` from `settings.json.claudebar-backup` if you used Connect.

## Alternatives

| Project | Data source | Difference |
|---------|-------------|------------|
| [ClaudeUsageBar](https://github.com/Artzainnn/ClaudeUsageBar) | claude.ai session cookie, pasted by hand | the cookie gives full account access |
| [OpenUsage](https://github.com/robinebers/openusage) | the same usage API | many providers; analytics on by default; auto-update |
| [AI Token Monitor](https://github.com/soulduse/ai-token-monitor) | local session logs + the usage API | cost in $, Tauri app, optional leaderboard and chat |
| [Claude Code Usage Monitor](https://github.com/Maciek-roboblog/Claude-Code-Usage-Monitor) | status line `rate_limits` + the usage API | terminal UI |
