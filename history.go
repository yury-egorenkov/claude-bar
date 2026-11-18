package main

import (
	"bufio"
	"bytes"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/mitranim/gg"
)

// Weekly usage over time, for the per-day strips. The API reports only the current
// percentages, so every successful fetch adds samples here (every 2 min or less):
// one for all models and one per model limit (e.g. Fable).

const (
	Day = 24 * time.Hour
	// Samples older than this are dropped on write: one week plus a margin.
	historyKeep = 8 * Day
)

type Sample struct {
	At       int64   `json:"at"`              // epoch seconds
	Limit    string  `json:"limit,omitempty"` // model name; empty: all models
	Used     float64 `json:"used"`            // weekly % used at that moment
	ResetsAt int64   `json:"resets_at"`       // identifies the weekly window
}

func HistoryPath() string { return filepath.Join(ConfigDir(), "history.jsonl") }

func ReadHistory(path string) []Sample {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	gg.Try(err)

	var out []Sample
	scan := bufio.NewScanner(bytes.NewReader(body))
	for scan.Scan() {
		if len(bytes.TrimSpace(scan.Bytes())) > 0 {
			out = append(out, gg.JsonDecodeTo[Sample](scan.Bytes()))
		}
	}
	gg.Try(scan.Err())
	return out
}

// Rewrites the file through a temp file: old samples dropped, the new ones appended.
func AppendHistory(path string, now time.Time, add ...Sample) {
	var buf bytes.Buffer
	for _, old := range ReadHistory(path) {
		if now.Sub(time.Unix(old.At, 0)) < historyKeep {
			buf.Write(gg.JsonBytes(old))
			buf.WriteByte('\n')
		}
	}
	for _, sample := range add {
		buf.Write(gg.JsonBytes(sample))
		buf.WriteByte('\n')
	}

	gg.Try(os.MkdirAll(filepath.Dir(path), 0o700))
	tmp := path + ".tmp"
	gg.Try(os.WriteFile(tmp, buf.Bytes(), 0o600))
	gg.Try(os.Rename(tmp, path))
}

type DayState int

const (
	DayPast DayState = iota
	DayToday
	DayFuture
)

var dayStateNames = [...]string{"past", "today", "future"}

func (self DayState) MarshalText() ([]byte, error) { return []byte(dayStateNames[self]), nil }

// One day of the weekly window, labeled by the calendar date it starts on.
//
// Two independent measures per day:
//   - Left: tokens. 24 h from the reset time (14:00 to 14:00 for a reset at 14:00),
//     100/7 % of the week each. Usage consumes the days in order, whatever the date:
//     unspent budget of a past day stays in that day until usage reaches it.
//   - TimeLeft: the clock, nothing computed: the share of the labeled calendar date still
//     ahead, midnight to midnight, whatever the settings. 0 for past dates, 1 for future
//     ones. State (today = bold) follows it.
type DayView struct {
	Start    int64    `json:"start"` // epoch seconds, inside the window
	End      int64    `json:"end"`
	State    DayState `json:"state"`     // by the calendar date
	Budget   float64  `json:"budget"`    // % of the week this day holds: 100/7
	Left     float64  `json:"left"`      // share of this day's budget not consumed yet, 0–1
	TimeLeft float64  `json:"time_left"` // share of the day's time still ahead, 0–1
	// Share of this day's budget used ahead of the budget for now (the part of the used
	// tokens past the point the budget allows by now), 0–1: drawn orange.
	Over  float64 `json:"over"`
	Spent float64 `json:"spent"` // % of the week actually spent on that calendar date (history)
	Known bool    `json:"known"` // false: no samples cover the day (before the first sample)
}

// Days of one weekly limit: `limit` is a model name, empty for all models.
// The current moment counts as a sample, so today is up to date even between fetches.
func WeekDays(history []Sample, limit string, week Window, now time.Time, conf Config) []DayView {
	if !week.Live(now) {
		return []DayView{}
	}
	loc := now.Location()
	start := week.Reset().In(loc).Add(-Week)

	var samples []Sample
	for _, sample := range history {
		if sample.Limit == limit && sample.ResetsAt == week.ResetsAt && sample.At <= now.Unix() {
			samples = append(samples, sample)
		}
	}
	samples = append(samples, Sample{At: now.Unix(), Limit: limit, Used: week.Used, ResetsAt: week.ResetsAt})

	sched := WeekSchedule(week, loc, conf.Night)
	budget := sched.Budget()
	// The point on the week's 0–100 scale the budget allows by now (hours or days mode).
	allowed := WeekPace(week, now, conf).Even

	out := []DayView{}
	for ind, bound := range sched.Days {
		begin, end := bound[0], bound[1]
		day := DayView{Start: begin.Unix(), End: end.Unix(), Budget: budget}
		// Tokens: this day's budget spans [ind·budget, (ind+1)·budget] on the week's
		// 0–100 scale, in the 24 h from the reset time.
		from, to := float64(ind)*budget, float64(ind+1)*budget
		day.Left = clamp01((to - week.Used) / budget)
		// Used tokens of this day past the allowed point.
		day.Over = clamp01((math.Min(week.Used, to) - math.Max(allowed, from)) / budget)

		// Clock: the calendar date the day is labeled with, midnight to midnight.
		y, m, d := begin.Date()
		calBegin := time.Date(y, m, d, 0, 0, 0, 0, loc)
		calEnd := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
		upTo := calEnd
		switch {
		case !now.Before(calEnd):
			day.State = DayPast
		case !now.Before(calBegin):
			day.State = DayToday
			day.TimeLeft = float64(calEnd.Sub(now)) / float64(calEnd.Sub(calBegin))
			upTo = now
		default:
			day.State = DayFuture
			day.TimeLeft = 1
		}

		// Actual spend on that calendar date, from the history.
		if day.State != DayFuture {
			usedFrom, okFrom := usedAt(samples, start, calBegin)
			usedTo, okTo := usedAt(samples, start, upTo)
			day.Known = okFrom && okTo
			if day.Known {
				day.Spent = max(0, usedTo-usedFrom)
			}
		}
		out = append(out, day)
	}
	return out
}

// Weekly % used at `at`: 0 at the window start (the reset), otherwise the last sample
// at or before `at`. Unknown before the first sample.
func usedAt(samples []Sample, start time.Time, at time.Time) (float64, bool) {
	if !at.After(start) {
		return 0, true
	}
	used, ok := 0.0, false
	for _, sample := range samples {
		if sample.At <= at.Unix() {
			used, ok = sample.Used, true
		}
	}
	return used, ok
}

const (
	// The current rate is measured over this much of the latest history.
	RateWindow = 2 * time.Hour
	// Less history than this: the week's average rate instead.
	rateMinSpan = 15 * time.Minute
)

// Weekly usage per active hour right now, in % of the week: over the last RateWindow
// of samples, or the week's average when the history is too short. Active hours skip
// 00:00–09:00 with quiet nights.
func CurrentRate(history []Sample, week Window, now time.Time, night bool) float64 {
	if !week.Live(now) {
		return 0
	}
	start := week.Reset().Add(-Week)
	since := now.Add(-RateWindow)

	var first Sample
	found := false
	for _, sample := range history {
		at := time.Unix(sample.At, 0)
		if sample.Limit == "" && sample.ResetsAt == week.ResetsAt && !at.Before(since) && !at.After(now) {
			first, found = sample, true
			break
		}
	}

	from, used := start, 0.0
	if found && now.Sub(time.Unix(first.At, 0)) >= rateMinSpan {
		from, used = time.Unix(first.At, 0), first.Used
	}
	hours := ActiveTime(from, now, night).Hours()
	if hours <= 0 {
		return 0
	}
	return math.Max(0, week.Used-used) / hours
}
