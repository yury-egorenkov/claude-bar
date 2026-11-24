package main

import (
	"fmt"
	"math"
	"time"
)

// Limit window from the `rate_limits` field of the Claude Code status line.
// ResetsAt == 0: no window (Claude Code has not sent it yet, or dropped it after the reset).
type Window struct {
	Used     float64 `json:"used_percentage"`
	ResetsAt int64   `json:"resets_at"`
}

func (self Window) Has() bool { return self.ResetsAt > 0 }

func (self Window) Reset() time.Time { return time.Unix(self.ResetsAt, 0) }

// The window has not reset yet at `now`.
func (self Window) Live(now time.Time) bool { return self.Has() && now.Before(self.Reset()) }

type Limits struct {
	FiveHour Window `json:"five_hour"`
	SevenDay Window `json:"seven_day"`
}

type Level int

// By how early the limit runs out, in parts of the window (for the week: one part = 1 day;
// the week keeps to the daily budget from now, the 5-hour window to its current rate):
//
//	ok    lasts until the reset
//	soon  runs out before the reset, up to 1/7 of the window early (yellow)
//	bad   more than 1/7 early, or under 10% left (red)
const (
	LevelUnknown Level = iota
	LevelOk
	LevelSoon
	LevelBad
)

var levelNames = [...]string{"unknown", "ok", "soon", "bad"}

// JSON for the app: "unknown", "ok", "soon", "bad".
func (self Level) MarshalText() ([]byte, error) { return []byte(levelNames[self]), nil }

func (self Level) Mark() string {
	switch self {
	case LevelOk:
		return "🟢"
	case LevelSoon:
		return "🟡"
	case LevelBad:
		return "🔴"
	default:
		return "⚪"
	}
}

const (
	Week     = 7 * 24 * time.Hour
	FiveHour = 5 * time.Hour
	// Five-hour window: percentage points above the even pace before any forecast.
	OverMargin = 2.0
	// Week, hours mode: day budgets ahead before it counts (days mode: 0, as soon as
	// tomorrow's tokens are touched).
	AheadMargin = 0.15
	// Quiet nights: 00:00 to this hour holds no budget.
	NightEnd = 9
	// Under this % left the level is bad whatever the pace.
	LeftBad = 10.0
)

// Spending pace of the weekly limit.
type Pace struct {
	Used  float64 // used, %
	Even  float64 // weekly: tokens up to the end of today, %; 5-hour: used at an even pace, %
	Over  float64 // Used − Even, percentage points
	Ahead float64 // weekly: days of tokens beyond today already used
	Reset time.Time
	// When the limit runs out at the current pace; zero: it lasts until the reset.
	RunOut time.Time
	// % per day that can be spent to last until the reset.
	PerDay float64
	Level  Level
}

// Weekly pace. Both modes count "ahead" in day budgets (100/7 % each):
//
//   - hours (default): expected usage grows with the clock, evenly over the active
//     hours (all day, or 09–24 with quiet nights). Yellow once more than AheadMargin
//     day ahead; red more than a day ahead. Keeping to the schedule from now on, the
//     limit runs out at RunOut.
//   - days: today is the day (24 h from the reset time) holding now; on budget while
//     usage stays within the tokens up to the end of today. Yellow once tomorrow's tokens are
//     touched, red more than a day beyond today.
func WeekPace(win Window, now time.Time, conf Config) Pace {
	if !win.Live(now) {
		return Pace{}
	}
	sched := WeekSchedule(win, now.Location(), conf.Night)
	reset := win.Reset()
	budget := sched.Budget()

	out := Pace{Used: win.Used, Reset: reset}
	margin := 0.0
	if conf.Pace == PaceDays {
		today := min(6, int(now.Sub(sched.Days[0][0])/Day))
		out.Even = math.Min(100, float64(today+1)*budget)
	} else {
		out.Even = sched.At(now)
		margin = AheadMargin
	}
	out.Over = out.Used - out.Even
	out.Ahead = out.Over / budget
	// The rest spread over the active days left (15 h a day with quiet nights).
	if active := sched.Active(now, reset); active > 0 {
		out.PerDay = math.Max(0, 100-out.Used) * float64(sched.ActivePerDay()) / float64(active)
	}

	switch {
	case out.Used >= 100:
		out.RunOut = now
	case out.Ahead <= margin:
	case conf.Pace == PaceDays:
		out.RunOut = reset.Add(-time.Duration(out.Ahead * float64(Day))).Round(time.Minute)
	default:
		out.RunOut = sched.When(out.Even + out.Left()).Round(time.Minute)
	}
	out.Level = WeekLevel(out.Ahead, out.Left(), margin)
	return out
}

// Up to `margin` day ahead: as usual. Beyond: yellow. More than a day ahead, or under
// 10% left: red.
func WeekLevel(ahead, left, margin float64) Level {
	switch {
	case left < LeftBad || ahead > 1:
		return LevelBad
	case ahead > margin:
		return LevelSoon
	default:
		return LevelOk
	}
}

// The week's tokens: 7 days of 24 h counted from the reset time (a reset at 14:00 makes
// every day run 14:00 to 14:00), the same budget for each (100/7 %). The day cells and
// the weekly pace both use it. With quiet nights, only 09:00–24:00 count as active time;
// every 24 h holds the same 15 active hours, so day boundaries keep their 100/7 % steps.
type Schedule struct {
	Days  [][2]time.Time
	Night bool
}

func WeekSchedule(win Window, loc *time.Location, night bool) Schedule {
	start := win.Reset().In(loc).Add(-Week)
	out := Schedule{Night: night}
	for ind := range 7 {
		begin := start.Add(time.Duration(ind) * Day)
		out.Days = append(out.Days, [2]time.Time{begin, begin.Add(Day)})
	}
	return out
}

// Active time between `from` and `to`: all of it, or without 00:00–09:00 of every date.
func (self Schedule) Active(from, to time.Time) time.Duration {
	return ActiveTime(from, to, self.Night)
}

func ActiveTime(from, to time.Time, night bool) time.Duration {
	if !to.After(from) {
		return 0
	}
	if !night {
		return to.Sub(from)
	}
	var out time.Duration
	y, m, d := from.Date()
	for day := time.Date(y, m, d, 0, 0, 0, 0, from.Location()); day.Before(to); {
		next := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, day.Location())
		awake := time.Date(day.Year(), day.Month(), day.Day(), NightEnd, 0, 0, 0, day.Location())
		lo, hi := maxTime(from, awake), minTime(to, next)
		if hi.After(lo) {
			out += hi.Sub(lo)
		}
		day = next
	}
	return out
}

// When `left` % runs out at `rate` % per active hour; zero when it lasts until `reset`.
func RunOutAtRate(left, rate float64, now, reset time.Time, night bool) time.Time {
	if left <= 0 {
		return now
	}
	if rate <= 0 {
		return time.Time{}
	}
	need := time.Duration(left / rate * float64(time.Hour))
	if ActiveTime(now, reset, night) <= need {
		return time.Time{}
	}
	return AddActive(now, need, night).Round(time.Minute)
}

// The moment `active` of active time after `from`.
func AddActive(from time.Time, active time.Duration, night bool) time.Time {
	if !night {
		return from.Add(active)
	}
	at := from
	for {
		y, m, d := at.Date()
		awake := time.Date(y, m, d, NightEnd, 0, 0, 0, at.Location())
		next := time.Date(y, m, d+1, 0, 0, 0, 0, at.Location())
		at = maxTime(at, awake)
		if room := next.Sub(at); room >= active {
			return at.Add(active)
		} else {
			active -= room
		}
		at = next
	}
}

func (self Schedule) ActivePerDay() time.Duration {
	if self.Night {
		return Day - NightEnd*time.Hour
	}
	return Day
}

func (self Schedule) start() time.Time { return self.Days[0][0] }
func (self Schedule) end() time.Time   { return self.Days[len(self.Days)-1][1] }

// % of the week the schedule has spent by `at`.
func (self Schedule) At(at time.Time) float64 {
	if !at.Before(self.end()) {
		return 100 // exactly, not a sum with rounding
	}
	return 100 * float64(self.Active(self.start(), at)) / float64(self.Active(self.start(), self.end()))
}

// The moment the schedule reaches `used` % of the week: the first moment with that much
// active time behind it.
func (self Schedule) When(used float64) time.Time {
	if used >= 100 {
		return self.end()
	}
	need := time.Duration(used / 100 * float64(self.Active(self.start(), self.end())))
	at := self.start()
	for at.Before(self.end()) {
		y, m, d := at.Date()
		next := time.Date(y, m, d+1, 0, 0, 0, 0, at.Location())
		if next.After(self.end()) {
			next = self.end()
		}
		got := self.Active(at, next)
		if got >= need {
			if self.Night {
				awake := time.Date(y, m, d, NightEnd, 0, 0, 0, at.Location())
				at = maxTime(at, awake)
			}
			return at.Add(need)
		}
		need -= got
		at = next
	}
	return self.end()
}

// % of the week each day holds.
func (self Schedule) Budget() float64 { return 100 / float64(len(self.Days)) }

// Linear pace of a window of the given length: the five-hour window.
func WindowPace(win Window, length time.Duration, now time.Time) Pace {
	if !win.Live(now) {
		return Pace{}
	}

	reset := win.Reset()
	start := reset.Add(-length)
	elapsed := now.Sub(start)
	left := reset.Sub(now)

	out := Pace{
		Used:  win.Used,
		Even:  100 * clamp01(float64(elapsed)/float64(length)),
		Reset: reset,
	}
	out.Over = out.Used - out.Even
	out.PerDay = math.Max(0, 100-out.Used) / left.Hours() * 24

	if out.Used >= 100 {
		out.RunOut = now
	} else if out.Over > OverMargin && elapsed > 0 {
		// Current rate: Used percent per elapsed, but over at least 1/7 of the window
		// (a day of the week): 4% in the first 3 hours is not a weekly rate.
		span := max(elapsed, length/7)
		runOut := now.Add(time.Duration(float64(span) * (100 - out.Used) / out.Used))
		if runOut.Before(reset) {
			out.RunOut = runOut
		}
	}
	out.Level = paceLevel(out, length)
	return out
}

func paceLevel(pace Pace, length time.Duration) Level {
	part := length / 7
	early := time.Duration(0)
	if !pace.RunOut.IsZero() {
		early = pace.Reset.Sub(pace.RunOut)
	}
	switch {
	case pace.Left() < LeftBad || early > part:
		return LevelBad
	case early > 0:
		return LevelSoon
	default:
		return LevelOk
	}
}

func (self Pace) Left() float64 { return math.Max(0, 100-self.Used) }

func FiveHourLevel(win Window, now time.Time) Level { return WindowPace(win, FiveHour, now).Level }

func clamp01(val float64) float64 { return math.Min(1, math.Max(0, val)) }

// What the menu bar shows: weekly limit left, level (weekly pace and five-hour window), text.
type Bar struct {
	Left  float64
	Level Level
	Title string
}

func MenuBar(lim Limits, now time.Time, conf Config) Bar {
	pace := WeekPace(lim.SevenDay, now, conf)
	level := max(pace.Level, FiveHourLevel(lim.FiveHour, now))
	if pace.Level == LevelUnknown {
		return Bar{Level: LevelUnknown, Title: "—"}
	}
	return Bar{Left: pace.Left(), Level: level, Title: fmt.Sprintf("%.0f%%", pace.Left())}
}

// Menu lines, top to bottom.
func Details(lim Limits, capturedAt time.Time, now time.Time, conf Config) []string {
	var out []string
	pace := WeekPace(lim.SevenDay, now, conf)

	if pace.Level == LevelUnknown {
		out = append(out, "Week: no data yet, waiting for a Claude Code reply")
	} else {
		out = append(out, fmt.Sprintf("Week: %.0f%% left · even pace %.0f%%", pace.Left(), 100-pace.Even))
		switch {
		case pace.Used >= 100:
			out = append(out, "Limit used up")
		case pace.RunOut.IsZero():
			out = append(out, "On budget: lasts until the reset")
		default:
			out = append(out, "Even at the daily budget it runs out: "+formatWhen(pace.RunOut, now))
		}
		out = append(out, fmt.Sprintf("Reset: %s · budget %.0f%% per day", formatWhen(pace.Reset, now), pace.PerDay))
	}

	if lim.FiveHour.Live(now) {
		out = append(out, fmt.Sprintf("5 hours: %.0f%% left · reset %s", math.Max(0, 100-lim.FiveHour.Used), formatWhen(lim.FiveHour.Reset(), now)))
	}

	if !capturedAt.IsZero() {
		out = append(out, fmt.Sprintf("Updated: %s (%s ago)", capturedAt.Format("15:04"), formatAgo(now.Sub(capturedAt))))
	}
	return out
}

// "14:00" for today, otherwise "Thu 14:00".
func formatWhen(val time.Time, now time.Time) string {
	val = val.In(now.Location())
	if sameDay(val, now) {
		return val.Format("15:04")
	}
	return val.Format("Mon 15:04")
}

func sameDay(one, two time.Time) bool {
	y1, m1, d1 := one.Date()
	y2, m2, d2 := two.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func formatAgo(val time.Duration) string {
	switch {
	case val < time.Minute:
		return "<1 min"
	case val < time.Hour:
		return fmt.Sprintf("%d min", int(val/time.Minute))
	case val < 48*time.Hour:
		return fmt.Sprintf("%d h", int(val/time.Hour))
	default:
		return fmt.Sprintf("%d d", int(val/(24*time.Hour)))
	}
}
