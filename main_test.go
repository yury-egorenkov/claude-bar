package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mitranim/gg"
	"github.com/mitranim/gg/gtest"
)

// Reset on Saturday 10:00 UTC; the week started the previous Saturday 10:00.
var testReset = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

func testWeek(used float64) Window { return Window{Used: used, ResetsAt: testReset.Unix()} }

// The moment when fraction `frac` of the week has passed.
func testAt(frac float64) time.Time {
	return testReset.Add(-Week).Add(time.Duration(frac * float64(Week)))
}

// A moment by calendar date and hour, UTC.
func testTime(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC) }

// The window Sat 14.11 10:00 → Sat 21.11 10:00 UTC has 8 calendar days of 12.5% each.
func TestWeekSchedule(t *testing.T) {
	defer gtest.Catch(t)

	sched := WeekSchedule(testWeek(0), time.UTC)
	gtest.Eq(len(sched.Days), 8)
	gtest.Eq(sched.Budget(), 12.5)
	gtest.Eq(sched.At(testTime(14, 10)), 0.0)
	gtest.Eq(sched.At(testTime(14, 17)), 6.25) // half of Sat's 14 h
	gtest.Eq(sched.At(testTime(15, 0)), 12.5)
	gtest.Eq(sched.At(testTime(18, 12)), 56.25) // 4 days + half of Wed
	gtest.Eq(sched.At(testTime(21, 10)), 100.0)
	gtest.Eq(sched.When(56.25), testTime(18, 12))
	gtest.Eq(sched.When(100), testTime(21, 10))
}

// Wed 18.11 12:00: the schedule has spent 56.25%.
func TestWeekPace_levels(t *testing.T) {
	defer gtest.Catch(t)

	now := testTime(18, 12)
	pace := WeekPace(testWeek(56.25), now)
	gtest.Eq(pace.Even, 56.25)
	gtest.Eq(pace.Level, LevelOk)
	gtest.True(pace.RunOut.IsZero())

	// 0.06 day ahead: within the margin.
	gtest.Eq(WeekPace(testWeek(57), now).Level, LevelOk)
	// 0.3 day ahead: keeping to the budget from now, it runs out Sat 07:00 instead of 10:00.
	pace = WeekPace(testWeek(60), now)
	gtest.Eq(pace.Level, LevelSoon)
	gtest.Eq(pace.RunOut, testTime(21, 7))
	// 1.5 days ahead.
	gtest.Eq(WeekPace(testWeek(75), now).Level, LevelWarn)
	// 2.3 days ahead.
	gtest.Eq(WeekPace(testWeek(85), now).Level, LevelBad)
	// Behind the schedule, but under 10% left.
	gtest.Eq(WeekPace(testWeek(92), testTime(20, 23)).Level, LevelBad)
}

// Saturday's 10 h hold a full day's 12.5%: 11% on Sunday morning is behind the budget.
func TestWeekPace_firstDay(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(WeekPace(testWeek(11), testTime(15, 1)).Level, LevelOk)
	gtest.Eq(WeekPace(testWeek(4), testTime(14, 13)).Level, LevelOk)
	// 20% in the first 3 hours: 1.4 days ahead.
	gtest.Eq(WeekPace(testWeek(20), testTime(14, 13)).Level, LevelWarn)
}

func TestWeekPace_spent(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.9)
	pace := WeekPace(testWeek(100), now)
	gtest.Eq(pace.Level, LevelBad)
	gtest.Eq(pace.RunOut, now)
	gtest.Eq(pace.PerDay, 0.0)
}

func TestWeekPace_unknown(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(WeekPace(Window{}, testAt(0.5)).Level, LevelUnknown)
	gtest.Eq(WeekPace(testWeek(30), testReset.Add(time.Minute)).Level, LevelUnknown)
}

func TestMenuBar(t *testing.T) {
	defer gtest.Catch(t)

	now := testTime(18, 12)
	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(50)}, now), Bar{Left: 50, Level: LevelOk, Title: "50%"})
	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(85)}, now), Bar{Left: 15, Level: LevelBad, Title: "15%"})
	gtest.Eq(MenuBar(Limits{}, now), Bar{Level: LevelUnknown, Title: "—"})

	// The week is fine, but the five-hour window is nearly used up.
	five := Window{Used: 92, ResetsAt: now.Add(time.Hour).Unix()}
	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(50), FiveHour: five}, now).Level, LevelBad)
}

func TestDetails(t *testing.T) {
	defer gtest.Catch(t)

	now := testTime(18, 12)
	gtest.Equal(
		Details(Limits{SevenDay: testWeek(60)}, now.Add(-5*time.Minute), now),
		[]string{
			"Week: 40% left · even pace 44%",
			"Even at the daily budget it runs out: Sat 07:00",
			"Reset: Sat 10:00 · budget 14% per day",
			"Updated: 11:55 (5 min ago)",
		},
	)
}

func TestMergeLimits(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.5)
	old := Window{Used: 10, ResetsAt: now.Add(-time.Hour).Unix()}
	live := Window{Used: 20, ResetsAt: now.Add(time.Hour).Unix()}

	gtest.Eq(MergeLimits(Limits{FiveHour: live, SevenDay: testWeek(40)}, Limits{SevenDay: testWeek(41)}, now),
		Limits{FiveHour: live, SevenDay: testWeek(41)})
	gtest.Eq(MergeLimits(Limits{FiveHour: old}, Limits{}, now), Limits{})
}

func TestState_roundtrip(t *testing.T) {
	defer gtest.Catch(t)

	path := filepath.Join(t.TempDir(), "sub", "state.json")
	gtest.Equal(ReadState(path), State{})

	now := testAt(0.5).Truncate(time.Second)
	saveLimits(path, Limits{SevenDay: testWeek(40)}, now)
	saveLimits(path, Limits{}, now.Add(time.Minute)) // no windows: the file stays unchanged

	state := ReadState(path)
	gtest.Eq(state.Limits, Limits{SevenDay: testWeek(40)})
	gtest.True(state.CapturedAt.Equal(now))
}

func TestStatuslineText(t *testing.T) {
	defer gtest.Catch(t)

	now := testTime(18, 12)
	var input StatuslineInput
	input.Model.DisplayName = "Opus 5.5"
	input.RateLimits = Limits{SevenDay: testWeek(50), FiveHour: Window{Used: 23.5, ResetsAt: now.Add(2 * time.Hour).Unix()}}
	gtest.Eq(statuslineText(input, now), "")

	input.RateLimits.SevenDay = testWeek(60)
	gtest.Eq(statuslineText(input, now), "week 40% left · runs out Sat 07:00")

	// 5-hour window: 3 of 5 h passed, 80% used, runs out 45 min early.
	input.RateLimits.SevenDay = testWeek(85)
	input.RateLimits.FiveHour.Used = 80
	gtest.Eq(statuslineText(input, now), "week 15% left · runs out Thu 16:48 · 5h 20% left · slow down")
}

func TestMakeSnapshot(t *testing.T) {
	defer gtest.Catch(t)

	now := testTime(18, 12)
	five := Window{Used: 40, ResetsAt: now.Add(time.Hour).Unix()}
	state := State{Limits: Limits{SevenDay: testWeek(60), FiveHour: five}, CapturedAt: now.Add(-time.Minute)}

	gtest.Equal(MakeSnapshot(state, true, now), Snapshot{
		Connected: true,
		UpdatedAt: now.Add(-time.Minute).Unix(),
		Level:     LevelSoon,
		Badge:     "40%",
		Week: View{
			Has: true, Left: 40, EvenLeft: 43.75, ResetsAt: testReset.Unix(),
			RunOutAt: testTime(21, 7).Unix(), Ahead: (60 - 56.25) / 12.5, PerDay: 40 / testReset.Sub(now).Hours() * 24, Level: LevelSoon,
		},
		// 4 of 5 hours passed: even pace leaves 20%.
		FiveHour: View{Has: true, Left: 60, EvenLeft: 20, ResetsAt: five.ResetsAt, Level: LevelOk},
		Models:   []ModelView{},
		Days:     []DayView{},
	})

	gtest.Equal(MakeSnapshot(State{}, false, now), Snapshot{Badge: "–", Models: []ModelView{}, Days: []DayView{}})
	gtest.Eq(string(gg.JsonBytes(LevelWarn)), `"warn"`)
}

func TestNewestState(t *testing.T) {
	defer gtest.Catch(t)

	mac, box := t.TempDir(), t.TempDir()
	now := testAt(0.5).Truncate(time.Second)
	saveLimits(filepath.Join(mac, "claudebar.json"), Limits{SevenDay: testWeek(30)}, now)
	saveLimits(filepath.Join(box, "claudebar.json"), Limits{SevenDay: testWeek(35)}, now.Add(time.Minute))

	gtest.Eq(NewestState([]string{mac, box}).Limits.SevenDay.Used, 35.0)
	gtest.Eq(NewestState([]string{mac, filepath.Join(box, "missing")}).Limits.SevenDay.Used, 30.0)

	gtest.False(AnyConnected([]string{mac, box}))
	Connect(filepath.Join(box, "settings.json"), "/x/claudebar-cli")
	gtest.True(AnyConnected([]string{mac, box}))
}

// The window runs Sat 14.11 10:00 → Sat 21.11 10:00 UTC: 8 calendar days,
// the first 14 h long, the last 10 h.
func TestWeekDays(t *testing.T) {
	defer gtest.Catch(t)

	start := testReset.Add(-Week)
	hour := func(h float64) time.Time { return start.Add(time.Duration(h * float64(time.Hour))) }
	at := func(h float64, used float64) Sample {
		return Sample{At: hour(h).Unix(), Used: used, ResetsAt: testReset.Unix()}
	}
	midnight := func(day int) int64 { return time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC).Unix() }

	history := []Sample{
		at(10, 6),  // Sat 20:00
		at(13, 9),  // Sat 23:00, last sample of Sat
		at(20, 14), // Sun 06:00
		at(37, 20), // Sun 23:00, last sample of Sun
		// Mon: the Mac was off, no samples
		at(70, 30), // Tue 08:00
		{At: hour(1).Unix(), Used: 99, ResetsAt: 1},                                // another window: ignored
		{At: hour(1).Unix(), Limit: "Fable", Used: 50, ResetsAt: testReset.Unix()}, // another limit: ignored
	}
	now := hour(71) // Tue 09:00
	days := WeekDays(history, "", testWeek(31), now)

	gtest.Eq(len(days), 8)
	// 8 days, 12.5% each. 31% used consumes Sat, Sun and 6.5 of Mon's 12.5: Mon keeps 52%
	// although it is past; Tue onward untouched.
	const budget = 12.5
	monLeft := (3*budget - 31) / budget
	gtest.Eq(days[0], DayView{Start: start.Unix(), End: midnight(15), State: DayPast, Budget: budget, Spent: 9, Known: true})
	gtest.Eq(days[1], DayView{Start: midnight(15), End: midnight(16), State: DayPast, Budget: budget, Spent: 11, Known: true})
	// Nothing sampled on Mon: its spend shows up on Tue.
	gtest.Eq(days[2], DayView{Start: midnight(16), End: midnight(17), State: DayPast, Budget: budget, Left: monLeft, Spent: 0, Known: true})
	gtest.Eq(days[3], DayView{Start: midnight(17), End: midnight(18), State: DayToday, Budget: budget, Left: 1, TimeLeft: 15.0 / 24, Spent: 11, Known: true})
	gtest.Eq(days[4], DayView{Start: midnight(18), End: midnight(19), State: DayFuture, Budget: budget, Left: 1, TimeLeft: 1})
	gtest.Eq(days[7], DayView{Start: midnight(21), End: testReset.Unix(), State: DayFuture, Budget: budget, Left: 1, TimeLeft: 1})

	// 60% used on Tue 09:00: usage reaches into Wed, a day still ahead in time.
	days = WeekDays(history, "", testWeek(60), now)
	gtest.Eq(days[3].Left, 0.0)
	gtest.True(days[3].Ahead)
	gtest.True(days[4].Ahead)
	gtest.False(days[5].Ahead)

	// History starting on Mon: earlier days are unknown.
	days = WeekDays([]Sample{at(50, 23)}, "", testWeek(31), now)
	gtest.Eq(days[0].Known, false)
	gtest.Eq(days[1].Known, false)
	// The first sample is inside Mon: its start is not covered either.
	gtest.Eq(days[2].Known, false)
	gtest.Eq(days[3].Spent, 8.0)

	gtest.Equal(WeekDays(history, "", Window{}, now), []DayView{})
}

func TestAppendHistory(t *testing.T) {
	defer gtest.Catch(t)

	path := filepath.Join(t.TempDir(), "sub", "history.jsonl")
	now := testAt(0.5)
	AppendHistory(path, now.Add(-9*Day), Sample{At: now.Add(-9 * Day).Unix(), Used: 1})
	AppendHistory(path, now.Add(-time.Hour), Sample{At: now.Add(-time.Hour).Unix(), Used: 40})
	AppendHistory(path, now, Sample{At: now.Unix(), Used: 41}, Sample{At: now.Unix(), Limit: "Fable", Used: 3})

	// The 9-day-old sample is dropped.
	gtest.Equal(ReadHistory(path), []Sample{
		{At: now.Add(-time.Hour).Unix(), Used: 40},
		{At: now.Unix(), Used: 41},
		{At: now.Unix(), Limit: "Fable", Used: 3},
	})
}

func TestWeekDays_model(t *testing.T) {
	defer gtest.Catch(t)

	start := testReset.Add(-Week)
	history := []Sample{
		{At: start.Add(10 * time.Hour).Unix(), Used: 30, ResetsAt: testReset.Unix()},
		{At: start.Add(10 * time.Hour).Unix(), Limit: "Fable", Used: 4, ResetsAt: testReset.Unix()},
	}
	now := start.Add(30 * time.Hour)
	days := WeekDays(history, "Fable", Window{Used: 6, ResetsAt: testReset.Unix()}, now)
	gtest.Eq(days[0].Spent, 4.0)
	gtest.Eq(days[1].Spent, 2.0)
}

func TestCombineStates(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.5)
	line := State{Limits: Limits{SevenDay: testWeek(17)}, CapturedAt: now}
	api := State{Limits: Limits{SevenDay: testWeek(16.6)}, Models: []ModelLimit{{Name: "Fable"}}, CapturedAt: now.Add(-time.Minute)}

	// Same window: the larger value, whichever source is fresher.
	gtest.Equal(CombineStates(line, api), State{Limits: Limits{SevenDay: testWeek(17)}, Models: api.Models, CapturedAt: now})
	api.Limits.SevenDay.Used = 18
	gtest.Eq(CombineStates(line, api).Limits.SevenDay.Used, 18.0)

	// A later reset is a newer window: it wins even with a smaller value.
	api.Limits.SevenDay = Window{Used: 1, ResetsAt: testReset.Add(Week).Unix()}
	gtest.Eq(CombineStates(line, api).Limits.SevenDay, api.Limits.SevenDay)
}

func TestWeekLevel_hysteresis(t *testing.T) {
	defer gtest.Catch(t)

	// Entering soon takes more than 0.15 day ahead, leaving it less than 0.05.
	gtest.Eq(WeekLevel(0.12, 50, LevelOk), LevelOk)
	gtest.Eq(WeekLevel(0.16, 50, LevelOk), LevelSoon)
	gtest.Eq(WeekLevel(0.12, 50, LevelSoon), LevelSoon)
	gtest.Eq(WeekLevel(0.04, 50, LevelSoon), LevelOk)
	// The same between warn and bad.
	gtest.Eq(WeekLevel(1.95, 50, LevelBad), LevelBad)
	gtest.Eq(WeekLevel(1.95, 50, LevelWarn), LevelWarn)
	gtest.Eq(WeekLevel(0.5, 5, LevelOk), LevelBad)
	gtest.Eq(ParseLevel("warn"), LevelWarn)
}
