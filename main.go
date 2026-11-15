// claudebar (ClaudeBar.app): weekly Claude Code limit left, as a battery in the macOS menu bar.
//
// Data source: the `rate_limits` field that Claude Code itself passes to the status line
// command (statusLine). The program reads no keys or tokens and makes no network requests.
//
//	Claude Code → claudebar statusline → ~/.claude/claudebar.json → claudebar json → ClaudeBar (SwiftUI) → menu bar
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mitranim/gg"
)

const usage = `claudebar <command>

  statusline  statusLine command for ~/.claude/settings.json (set by the menu item
              "Connect to Claude Code"): reads JSON from stdin, saves the limits,
              prints the status line
              -then 'command': pass the same JSON to the previous command and print its output
  json        snapshot for the ClaudeBar app: limits left, pace, forecast;
              starts "fetch" in the background when the API result is older than 2 min
  fetch       one request to the usage API with the Claude Code token (api.anthropic.com only)
  set         settings: set pace hours|days, set night on|off (quiet nights 00–09)
  connect     add statusLine to ~/.claude/settings.json (backup: settings.json.claudebar-backup)
  show        limits and pace as text (check without the menu bar)`

func main() {
	defer gg.Fatal()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	switch cmd {
	case "json":
		fmt.Println(string(gg.JsonBytes(runJson(time.Now()))))
	case "fetch":
		RunFetch()
	case "connect":
		Connect(SettingsPath(), Executable())
	case "set":
		if len(args) != 2 {
			panic(gg.Errf("usage: claudebar-cli set pace hours|days, or set night on|off"))
		}
		WriteConfig(SetConfig(ReadConfig(), args[0], args[1]))
	case "statusline":
		runStatusline(args)
	case "show":
		runShow()
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

// Status line input: only the needed fields, the rest is discarded.
type StatuslineInput struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	RateLimits Limits `json:"rate_limits"`
}

func runStatusline(args []string) {
	flags := flag.NewFlagSet("statusline", flag.ExitOnError)
	then := flags.String("then", "", "previous status line command")
	gg.Try(flags.Parse(args))

	body := gg.Try1(io.ReadAll(os.Stdin))
	now := time.Now()

	// Claude Code runs the status line on every update: a parse failure must not
	// break it, so the error goes to stderr and the line is still printed.
	var input StatuslineInput
	err := gg.Catch(func() {
		input = gg.JsonDecodeTo[StatuslineInput](body)
		saveLimits(StatePath(), input.RateLimits, now)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudebar:", err)
	}

	if *then != "" {
		fmt.Print(runThen(*then, body))
		return
	}
	fmt.Println(statuslineText(input, now, ReadConfig()))
}

func saveLimits(path string, next Limits, now time.Time) {
	if !next.FiveHour.Has() && !next.SevenDay.Has() {
		return
	}
	prev := ReadState(path)
	WriteState(path, State{Limits: MergeLimits(prev.Limits, next, now), CapturedAt: now})
}

func runThen(cmd string, stdin []byte) string {
	proc := exec.Command("/bin/sh", "-c", cmd)
	proc.Stdin = bytes.NewReader(stdin)
	proc.Stderr = os.Stderr
	return string(gg.Try1(proc.Output()))
}

// Empty while spending is on pace: the line appears only when there is something to act on.
func statuslineText(input StatuslineInput, now time.Time, conf Config) string {
	var parts []string
	lim := input.RateLimits

	pace := WeekPace(lim.SevenDay, now, conf)
	if pace.Level >= LevelSoon {
		parts = append(parts, fmt.Sprintf("week %.0f%% left", pace.Left()))
		if !pace.RunOut.IsZero() {
			parts = append(parts, "runs out "+formatWhen(pace.RunOut, now))
		}
	}
	if FiveHourLevel(lim.FiveHour, now) >= LevelSoon {
		parts = append(parts, fmt.Sprintf("5h %.0f%% left", math.Max(0, 100-lim.FiveHour.Used)))
	}
	if max(pace.Level, FiveHourLevel(lim.FiveHour, now)) == LevelBad {
		parts = append(parts, "slow down")
	}
	return strings.Join(parts, " · ")
}

// Freshest of the status line files and the last API result. An error of the
// background fetch is shown, it doesn't stop the snapshot.
func runJson(now time.Time) Snapshot {
	fetchErr := gg.Catch(func() { FetchIfStale(now) })

	dirs := ClaudeDirs()
	usage := ReadState(UsagePath())
	state := CombineStates(NewestState(dirs), usage)

	conf := ReadConfig()
	snap := MakeSnapshot(state, AnyConnected(dirs) || !usage.CapturedAt.IsZero(), now, conf)
	history := ReadHistory(HistoryPath())
	snap.Days = WeekDays(history, "", state.Limits.SevenDay, now, conf)
	for ind, model := range snap.Models {
		snap.Models[ind].Days = WeekDays(history, model.Name, model.Window(), now, conf)
	}
	snap.Error = ReadUsageErr()
	if fetchErr != nil {
		snap.Error = fetchErr.Error()
	}
	return snap
}

func runShow() {
	now := time.Now()
	snap := runJson(now)
	state := CombineStates(NewestState(ClaudeDirs()), ReadState(UsagePath()))
	if snap.Error != "" {
		fmt.Println("API:", snap.Error)
	}
	conf := ReadConfig()
	bar := MenuBar(state.Limits, now, conf)
	fmt.Println(bar.Level.Mark(), bar.Title)
	for _, line := range Details(state.Limits, state.CapturedAt, now, conf) {
		fmt.Println(line)
	}
}
