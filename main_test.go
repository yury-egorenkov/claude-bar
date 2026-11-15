package main

import (
	"math"
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

// A moment by hours since the window start (Sat 14.11 10:00 UTC).
func testHour(h float64) time.Time {
	return testReset.Add(-Week).Add(time.Duration(h * float64(time.Hour)))
}

// A var, not a const: constant expressions are folded exactly and differ from runtime math.
var testBudget = 100 / float64(7)

var testDays = Config{Pace: PaceDays}

// The window Sat 14.11 10:00 → Sat 21.11 10:00 UTC: 7 days of 24 h, 100/7 % each.
func TestWeekSchedule(t *testing.T) {
	defer gtest.Catch(t)

	sched := WeekSchedule(testWeek(0), time.UTC, false)
	gtest.Eq(len(sched.Days), 7)
	gtest.Eq(sched.Days[1], [2]time.Time{testHour(24), testHour(48)})
	gtest.Eq(sched.Budget(), testBudget)
	gtest.Eq(calendarDays(testHour(0), testHour(84)), 3)  // Sat 14 → Tue 17
	gtest.Eq(calendarDays(testHour(0), testHour(166)), 7) // Sat 21 08:00, before the reset
}

// Tue 17.11 22:00: today is the 4th day, tokens up to the end of today are 4/7 = 57.1%.
func TestWeekPace_levels(t *testing.T) {
	defer gtest.Catch(t)

	now := testHour(84)
	pace := WeekPace(testWeek(50), now, testDays)
	gtest.Eq(pace.Even, 4*testBudget)
	gtest.Eq(pace.Level, LevelOk)
	gtest.True(pace.RunOut.IsZero())

	// Within today's tokens.
	gtest.Eq(WeekPace(testWeek(57), now, testDays).Level, LevelOk)
	// 0.2 day of tomorrow's tokens used: yellow; keeping to the budget from tomorrow,
	// it runs out 4.8 h early, Sat 05:12.
	pace = WeekPace(testWeek(60), now, testDays)
	gtest.Eq(pace.Level, LevelSoon)
	gtest.True(pace.RunOut.Equal(testHour(163.2)))
	// 0.9 day beyond today: still yellow; over a day: red.
	gtest.Eq(WeekPace(testWeek(70), now, testDays).Level, LevelSoon)
	gtest.Eq(WeekPace(testWeek(72), now, testDays).Level, LevelBad)
	// Within the tokens, but under 10% left.
	gtest.Eq(WeekPace(testWeek(92), testHour(160), testDays).Level, LevelBad)
}

// The first day holds 14.3% however much of it is left.
func TestWeekPace_firstDay(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(WeekPace(testWeek(10), testHour(3), testDays).Level, LevelOk)
	gtest.Eq(WeekPace(testWeek(20), testHour(3), testDays).Level, LevelSoon)
	gtest.Eq(WeekPace(testWeek(30), testHour(3), testDays).Level, LevelBad)
	// Sat 21 before the reset: every day's tokens are available.
	gtest.Eq(WeekPace(testWeek(85), testHour(166), testDays).Level, LevelOk)
}

func TestWeekPace_spent(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.9)
	pace := WeekPace(testWeek(100), now, testDays)
	gtest.Eq(pace.Level, LevelBad)
	gtest.Eq(pace.RunOut, now)
	gtest.Eq(pace.PerDay, 0.0)
}

func TestWeekPace_unknown(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(WeekPace(Window{}, testAt(0.5), testDays).Level, LevelUnknown)
	gtest.Eq(WeekPace(testWeek(30), testReset.Add(time.Minute), testDays).Level, LevelUnknown)
}

func TestMenuBar(t *testing.T) {
	defer gtest.Catch(t)

	now := testHour(84)
	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(50)}, now, testDays), Bar{Left: 50, Level: LevelOk, Title: "50%"})
	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(85)}, now, testDays), Bar{Left: 15, Level: LevelBad, Title: "15%"})
	gtest.Eq(MenuBar(Limits{}, now, testDays), Bar{Level: LevelUnknown, Title: "—"})

	// The week is fine, but the five-hour window is nearly used up.
	five := Window{Used: 92, ResetsAt: now.Add(time.Hour).Unix()}
	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(50), FiveHour: five}, now, testDays).Level, LevelBad)
}

func TestDetails(t *testing.T) {
	defer gtest.Catch(t)

	now := testHour(84) // Tue 22:00
	gtest.Equal(
		Details(Limits{SevenDay: testWeek(60)}, now.Add(-5*time.Minute), now, testDays),
		[]string{
			"Week: 40% left · even pace 43%",
			"Even at the daily budget it runs out: Sat 05:12",
			"Reset: Sat 10:00 · budget 11% per day",
			"Updated: 21:55 (5 min ago)",
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

	now := testHour(84) // Tue 22:00
	var input StatuslineInput
	input.Model.DisplayName = "Opus 5.5"
	input.RateLimits = Limits{SevenDay: testWeek(50), FiveHour: Window{Used: 23.5, ResetsAt: now.Add(2 * time.Hour).Unix()}}
	gtest.Eq(statuslineText(input, now, testDays), "")

	input.RateLimits.SevenDay = testWeek(60)
	gtest.Eq(statuslineText(input, now, testDays), "week 40% left · runs out Sat 05:12")

	// 5-hour window: 3 of 5 h passed, 80% used, runs out 75 min early.
	input.RateLimits.SevenDay = testWeek(85)
	input.RateLimits.FiveHour.Used = 80
	gtest.Eq(statuslineText(input, now, testDays), "week 15% left · runs out Thu 11:12 · 5h 20% left · slow down")
}

func TestMakeSnapshot(t *testing.T) {
	defer gtest.Catch(t)

	now := testHour(84)
	five := Window{Used: 40, ResetsAt: now.Add(time.Hour).Unix()}
	state := State{Limits: Limits{SevenDay: testWeek(60), FiveHour: five}, CapturedAt: now.Add(-time.Minute)}

	gtest.Equal(MakeSnapshot(state, true, now, testDays), Snapshot{
		Connected: true,
		UpdatedAt: now.Add(-time.Minute).Unix(),
		Level:     LevelSoon,
		Badge:     "40%",
		Week: View{
			Has: true, Left: 40, EvenLeft: 100 - 4*testBudget, ResetsAt: testReset.Unix(),
			RunOutAt: testHour(163.2).Round(time.Minute).Unix(), Ahead: (60 - 4*testBudget) / testBudget, PerDay: 40 * float64(Day) / float64(testReset.Sub(now)), Level: LevelSoon,
		},
		// 4 of 5 hours passed: even pace leaves 20%.
		FiveHour: View{Has: true, Left: 60, EvenLeft: 20, ResetsAt: five.ResetsAt, Level: LevelOk},
		Models:   []ModelView{},
		Days:     []DayView{},
		Config:   testDays,
	})

	gtest.Equal(MakeSnapshot(State{}, false, now, testDays), Snapshot{Badge: "–", Models: []ModelView{}, Days: []DayView{}, Config: testDays})
	gtest.Eq(string(gg.JsonBytes(LevelSoon)), `"soon"`)
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

// The window runs Sat 14.11 10:00 → Sat 21.11 10:00 UTC: 7 days of 24 h from 10:00.
func TestWeekDays(t *testing.T) {
	defer gtest.Catch(t)

	at := func(h float64, used float64) Sample {
		return Sample{At: testHour(h).Unix(), Used: used, ResetsAt: testReset.Unix()}
	}
	history := []Sample{
		at(10, 6),  // Sat 20:00
		at(23, 14), // Sun 09:00
		at(30, 20), // Sun 16:00
		at(47, 23), // Mon 09:00
		at(75, 30), // Tue 13:00
		{At: testHour(1).Unix(), Used: 99, ResetsAt: 1},                                // another window: ignored
		{At: testHour(1).Unix(), Limit: "Fable", Used: 50, ResetsAt: testReset.Unix()}, // another limit: ignored
	}
	now := testHour(76) // Tue 17.11 14:00
	days := WeekDays(history, "", testWeek(31), now, testDays)

	gtest.Eq(len(days), 7)
	// Tokens: 31% used consumes days 0 and 1 and 2.4 of day 2's 14.3, so day 2 keeps 83%
	// although it is past; day 3 onward untouched.
	// Clock and spend: calendar dates Sat 14, Sun 15, Mon 16 are past, Tue 17 is today
	// with 10 of 24 h left.
	day := func(ind int) (int64, int64) {
		return testHour(float64(24 * ind)).Unix(), testHour(float64(24 * (ind + 1))).Unix()
	}
	s0, e0 := day(0)
	s1, e1 := day(1)
	s2, e2 := day(2)
	s3, e3 := day(3)
	s4, e4 := day(4)
	gtest.Eq(days[0], DayView{Start: s0, End: e0, State: DayPast, Budget: testBudget, Spent: 6, Known: true})
	gtest.Eq(days[1], DayView{Start: s1, End: e1, State: DayPast, Budget: testBudget, Spent: 14, Known: true})
	gtest.Eq(days[2], DayView{Start: s2, End: e2, State: DayPast, Budget: testBudget, Left: (3*testBudget - 31) / testBudget, Spent: 3, Known: true})
	gtest.Eq(days[3], DayView{Start: s3, End: e3, State: DayToday, Budget: testBudget, Left: 1, TimeLeft: 10.0 / 24, Spent: 8, Known: true})
	gtest.Eq(days[4], DayView{Start: s4, End: e4, State: DayFuture, Budget: testBudget, Left: 1, TimeLeft: 1})

	// 60% used: today's tokens are gone with 42% of the day left, and day 4 is eaten into.
	days = WeekDays(history, "", testWeek(60), now, testDays)
	gtest.Eq(days[3].Left, 0.0)
	gtest.True(days[3].Ahead)
	gtest.True(days[4].Ahead)
	gtest.False(days[5].Ahead)

	// History starting on Mon 12:00: earlier dates are unknown.
	days = WeekDays([]Sample{at(50, 23)}, "", testWeek(31), now, testDays)
	gtest.Eq(days[0].Known, false)
	gtest.Eq(days[1].Known, false)
	// The first sample is inside Mon: its start is not covered either.
	gtest.Eq(days[2].Known, false)
	gtest.Eq(days[3].Spent, 8.0)

	gtest.Equal(WeekDays(history, "", Window{}, now, testDays), []DayView{})
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
	days := WeekDays(history, "Fable", Window{Used: 6, ResetsAt: testReset.Unix()}, now, testDays)
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

func TestWeekLevel(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(WeekLevel(-0.04, 50, 0), LevelOk)
	gtest.Eq(WeekLevel(0, 50, 0), LevelOk)
	gtest.Eq(WeekLevel(0.01, 50, 0), LevelSoon)
	gtest.Eq(WeekLevel(1, 50, 0), LevelSoon)
	gtest.Eq(WeekLevel(1.01, 50, 0), LevelBad)
	gtest.Eq(WeekLevel(-0.5, 5, 0), LevelBad)
}

// Hours mode, Tue 17.11 22:00, half the week: the schedule has spent 50%.
func TestWeekPace_hours(t *testing.T) {
	defer gtest.Catch(t)

	now := testHour(84)
	hours := Config{}
	pace := WeekPace(testWeek(50), now, hours)
	gtest.Eq(pace.Even, 50.0)
	gtest.Eq(pace.Level, LevelOk)
	gtest.True(pace.RunOut.IsZero())

	// 0.07 day ahead: within the margin.
	gtest.Eq(WeekPace(testWeek(51), now, hours).Level, LevelOk)
	// 0.35 day ahead: keeping to the schedule, it runs out Sat 01:36 instead of 10:00.
	pace = WeekPace(testWeek(55), now, hours)
	gtest.Eq(pace.Level, LevelSoon)
	gtest.True(pace.RunOut.Equal(testHour(159.6)))
	// 0.9 day ahead: yellow; 1.4: red.
	gtest.Eq(WeekPace(testWeek(62.8), now, hours).Level, LevelSoon)
	gtest.Eq(WeekPace(testWeek(70), now, hours).Level, LevelBad)
}

// Quiet nights: 09:00–24:00 active, 15 h in every 24 h, 105 h in the week.
func TestSchedule_night(t *testing.T) {
	defer gtest.Catch(t)

	sched := WeekSchedule(testWeek(0), time.UTC, true)
	near := func(one, two float64) bool { return math.Abs(one-two) < 1e-9 }

	// Sat 10:00 → Sun 00:00: 14 active hours; the night adds nothing.
	gtest.True(near(sched.At(testHour(14)), 100*14.0/105))
	gtest.Eq(sched.At(testHour(23)), sched.At(testHour(14)))
	// Every day boundary (10:00) is a whole day budget.
	gtest.True(near(sched.At(testHour(24)), testBudget))
	gtest.True(near(sched.At(testHour(72)), 3*testBudget))
	// Back from the percentage to the moment: the night is skipped.
	gtest.Eq(sched.When(100*14.0/105+0.001).Hour(), 9)
	gtest.Eq(sched.When(sched.At(testHour(30))).Round(time.Second), testHour(30))
	gtest.Eq(sched.ActivePerDay(), 15*time.Hour)
}

func TestWeekPace_night(t *testing.T) {
	defer gtest.Catch(t)

	// Wed 18.11 05:00, the night after 3 full days and Tue 10:00–24:00: expected usage
	// stays at the Tue midnight value until 09:00.
	night := Config{Night: true}
	at5 := WeekPace(testWeek(50), testHour(91), night)
	at8 := WeekPace(testWeek(50), testHour(94), night)
	gtest.Eq(at5.Even, at8.Even)
	// By then 59 of 105 active hours are behind (56.2%), more than 91 of 168 hours (54.2%).
	gtest.True(math.Abs(at5.Even-100*59.0/105) < 1e-9)
	gtest.True(at5.Even > WeekPace(testWeek(50), testHour(91), Config{}).Even)
}

// The green line is the plain clock: quiet nights do not change it.
func TestWeekDays_clock(t *testing.T) {
	defer gtest.Catch(t)

	// Tue 17.11 14:00: 10 of today's 24 h are left, with or without quiet nights.
	plain := WeekDays(nil, "", testWeek(31), testHour(76), Config{})
	night := WeekDays(nil, "", testWeek(31), testHour(76), Config{Night: true})
	gtest.Eq(plain[3].State, DayToday)
	gtest.Eq(plain[3].TimeLeft, 10.0/24)
	gtest.Eq(night[3].TimeLeft, 10.0/24)
}

func TestSetConfig(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(SetConfig(Config{}, "pace", "days"), Config{Pace: PaceDays})
	gtest.Eq(SetConfig(Config{Pace: PaceDays}, "night", "on"), Config{Pace: PaceDays, Night: true})
	gtest.PanicStr("unknown setting", func() { SetConfig(Config{}, "pace", "weeks") })
}
