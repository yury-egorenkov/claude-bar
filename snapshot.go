package main

import (
	"fmt"
	"math"
	"time"
)

// What the app (ClaudeBar, SwiftUI) shows: `claudebar json`. The app only renders it;
// all math lives here and is covered by tests.
type Snapshot struct {
	Connected bool        `json:"connected"`
	UpdatedAt int64       `json:"updated_at"` // epoch seconds, 0: never
	Level     Level       `json:"level"`      // weekly pace and five-hour window combined
	Badge     string      `json:"badge"`      // menu bar text: weekly % left or "–"
	Week      View        `json:"week"`
	FiveHour  View        `json:"five_hour"`
	Models    []ModelView `json:"models"` // per-model weekly limits, e.g. Fable
	Days      []DayView   `json:"days"`   // the 7 days of the weekly window, spend per day
	Error     string      `json:"error"`  // last usage API error; data may still come from the status line
	Config    Config      `json:"config"` // settings, for the menu checkmarks
	Resets    []Grant     `json:"resets"` // limit resets with one left
	// Auto stop armed: when it fires unless the rate drops (epoch seconds); 0: not armed.
	AutoStopAt int64 `json:"auto_stop_at"`
}

type ModelView struct {
	Name     string    `json:"name"`
	Left     float64   `json:"left"`
	ResetsAt int64     `json:"resets_at"`
	Days     []DayView `json:"days"` // the 7 days of this model's weekly window
}

func (self ModelView) Window() Window { return Window{Used: 100 - self.Left, ResetsAt: self.ResetsAt} }

// One limit window. All percentages are what is LEFT, 0–100.
type View struct {
	Has      bool    `json:"has"`
	Left     float64 `json:"left"`
	EvenLeft float64 `json:"even_left"` // what would be left at an even pace
	ResetsAt int64   `json:"resets_at"`
	RunOutAt int64   `json:"run_out_at"` // 0: lasts until the reset
	Ahead    float64 `json:"ahead"`      // weekly: day budgets ahead of the schedule
	// Weekly: the current rate (% of the week per active hour, last 2 h) and when the
	// limit runs out at it; 0: lasts until the reset.
	Rate       float64 `json:"rate"`
	RateRunOut int64   `json:"rate_run_out"`
	PerDay     float64 `json:"per_day"` // weekly only: % per day to last until the reset
	Level      Level   `json:"level"`
}

func MakeSnapshot(state State, connected bool, now time.Time, conf Config) Snapshot {
	lim := state.Limits
	bar := MenuBar(lim, now, conf)

	out := Snapshot{
		Connected: connected,
		Level:     bar.Level,
		Badge:     bar.Title,
		Week:      makeView(WeekPace(lim.SevenDay, now, conf)),
		Config:    conf,
		FiveHour:  makeView(WindowPace(lim.FiveHour, FiveHour, now)),
	}
	if bar.Level == LevelUnknown {
		out.Badge = "–"
	}
	// Limit reached: the badge counts down to the reset instead of showing 0%.
	if week := lim.SevenDay; week.Live(now) && week.Used >= 100 {
		out.Badge = Countdown(week.Reset().Sub(now))
	}
	out.Models = []ModelView{}
	out.Days = []DayView{}
	out.Resets = []Grant{}
	for _, model := range state.Models {
		if model.Window.Live(now) {
			out.Models = append(out.Models, ModelView{Name: model.Name, Left: math.Max(0, 100-model.Window.Used), ResetsAt: model.Window.ResetsAt, Days: []DayView{}})
		}
	}
	out.FiveHour.Level = FiveHourLevel(lim.FiveHour, now)
	out.FiveHour.PerDay = 0
	if !state.CapturedAt.IsZero() {
		out.UpdatedAt = state.CapturedAt.Unix()
	}
	return out
}

func makeView(pace Pace) View {
	if pace.Level == LevelUnknown {
		return View{}
	}
	out := View{
		Has:      true,
		Left:     pace.Left(),
		EvenLeft: 100 - pace.Even,
		ResetsAt: pace.Reset.Unix(),
		PerDay:   pace.PerDay,
		Ahead:    pace.Ahead,
		Level:    pace.Level,
	}
	if !pace.RunOut.IsZero() {
		out.RunOutAt = pace.RunOut.Unix()
	}
	return out
}

// "2d 4h", "5h 12m", "54m".
func Countdown(left time.Duration) string {
	mins := int(math.Ceil(max(0, left.Minutes())))
	switch {
	case mins >= 24*60:
		return fmt.Sprintf("%dd %dh", mins/(24*60), mins%(24*60)/60)
	case mins >= 60:
		return fmt.Sprintf("%dh %dm", mins/60, mins%60)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}
