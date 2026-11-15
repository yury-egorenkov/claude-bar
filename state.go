package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mitranim/gg"
)

// File through which the Claude Code status line passes the limits to the menu bar.
// Holds only percentages and reset times: no keys, no paths, no session text.
type State struct {
	Limits Limits `json:"rate_limits"`
	// Per-model weekly limits (e.g. Fable); only the usage API reports them.
	Models     []ModelLimit `json:"models,omitempty"`
	CapturedAt time.Time    `json:"captured_at"`
}

type ModelLimit struct {
	Name   string `json:"name"`
	Window Window `json:"window"`
}

func StatePath() string { return filepath.Join(ClaudeDir(), "claudebar.json") }

func ClaudeDir() string { return filepath.Join(gg.Try1(os.UserHomeDir()), ".claude") }

// Claude Code config dirs the app reads: ~/.claude plus one dir per line of
// ~/.config/claudebar/dirs, e.g. the folder a Claude Code container mounts as its
// ~/.claude (`make install CLAUDE_DIR=...` writes it).
func ClaudeDirs() []string {
	out := []string{ClaudeDir()}
	body, err := os.ReadFile(DirsConfigPath())
	if os.IsNotExist(err) {
		return out
	}
	gg.Try(err)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

func DirsConfigPath() string {
	return filepath.Join(gg.Try1(os.UserHomeDir()), ".config", "claudebar", "dirs")
}

// Limits are per account, so the freshest file wins.
func NewestState(dirs []string) State {
	var out State
	for _, dir := range dirs {
		state := ReadState(filepath.Join(dir, "claudebar.json"))
		if state.CapturedAt.After(out.CapturedAt) {
			out = state
		}
	}
	return out
}

func AnyConnected(dirs []string) bool {
	for _, dir := range dirs {
		if Connected(filepath.Join(dir, "settings.json")) {
			return true
		}
	}
	return false
}

// A missing file is an empty state.
func ReadState(path string) State {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}
	}
	gg.Try(err)
	return gg.JsonDecodeTo[State](body)
}

// Written via a temp file so the menu bar never reads half of it.
func WriteState(path string, val State) {
	gg.Try(os.MkdirAll(filepath.Dir(path), 0o700))
	tmp := path + ".tmp"
	gg.Try(os.WriteFile(tmp, gg.JsonBytes(val), 0o600))
	gg.Try(os.Rename(tmp, path))
}

// New windows replace old ones; a window missing from the new data stays until it resets.
// Claude Code sends no `rate_limits` before the first API response in a session.
func MergeLimits(prev, next Limits, now time.Time) Limits {
	return Limits{
		FiveHour: mergeWindow(prev.FiveHour, next.FiveHour, now),
		SevenDay: mergeWindow(prev.SevenDay, next.SevenDay, now),
	}
}

func mergeWindow(prev, next Window, now time.Time) Window {
	if next.Has() {
		return next
	}
	if prev.Live(now) {
		return prev
	}
	return Window{}
}

// The status line and the usage API may disagree by a point (rounding, a reply not
// sent yet). Taking the freshest made the badge jump back and forth between them.
// Within one window usage only grows, so the larger value is the current one; a window
// with a later reset is a newer window and wins outright. Per-model limits come only
// from the API.
func CombineStates(line, api State) State {
	return State{
		Limits: Limits{
			FiveHour: combineWindow(line.Limits.FiveHour, api.Limits.FiveHour),
			SevenDay: combineWindow(line.Limits.SevenDay, api.Limits.SevenDay),
		},
		Models:     api.Models,
		CapturedAt: maxTime(line.CapturedAt, api.CapturedAt),
	}
}

func combineWindow(one, two Window) Window {
	switch {
	case one.ResetsAt > two.ResetsAt:
		return one
	case two.ResetsAt > one.ResetsAt:
		return two
	default:
		return Window{Used: max(one.Used, two.Used), ResetsAt: one.ResetsAt}
	}
}

func maxTime(one, two time.Time) time.Time {
	if one.After(two) {
		return one
	}
	return two
}

// The weekly level shown last, for WeekLevel's hysteresis. A new window starts fresh.
type ShownLevel struct {
	ResetsAt int64 `json:"resets_at"`
	Level    Level `json:"level"`
}

func shownLevelPath() string { return filepath.Join(ConfigDir(), "level.json") }

func ReadShownLevel(resetsAt int64) Level {
	body, err := os.ReadFile(shownLevelPath())
	if os.IsNotExist(err) {
		return LevelUnknown
	}
	gg.Try(err)
	var shown struct {
		ResetsAt int64  `json:"resets_at"`
		Level    string `json:"level"`
	}
	gg.JsonDecode(body, &shown)
	if shown.ResetsAt != resetsAt {
		return LevelUnknown
	}
	return ParseLevel(shown.Level)
}

func WriteShownLevel(resetsAt int64, level Level) {
	if ReadShownLevel(resetsAt) == level {
		return
	}
	gg.Try(os.MkdirAll(ConfigDir(), 0o700))
	gg.Try(os.WriteFile(shownLevelPath(), gg.JsonBytes(ShownLevel{ResetsAt: resetsAt, Level: level}), 0o600))
}
