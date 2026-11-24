package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitranim/gg"
	"github.com/mitranim/gg/gtest"
)

const testExe = "/Applications/ClaudeBar.app/Contents/MacOS/claudebar"

func TestConnectSettings_empty(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(string(ConnectSettings([]byte(`{}`), testExe)), `{
  "statusLine": {
    "command": "'/Applications/ClaudeBar.app/Contents/MacOS/claudebar' statusline",
    "type": "command"
  }
}
`)
}

func TestConnectSettings_prev(t *testing.T) {
	defer gtest.Catch(t)

	// The previous command is kept, other settings stay.
	prev := []byte(`{"model":"opus","statusLine":{"type":"command","command":"echo 'hi'","padding":1}}`)
	out := ConnectSettings(prev, testExe)
	gtest.Eq(statusLineCommand(out), `'`+testExe+`' statusline -then 'echo '\''hi'\'''`)
	gtest.True(strings.Contains(string(out), `"model": "opus"`))
	gtest.True(strings.Contains(string(out), `"padding": 1`))

	// Reconnecting doesn't nest claudebar inside claudebar.
	gtest.Eq(statusLineCommand(ConnectSettings(out, testExe)), statusLineCommand(out))
}

func TestConnect_files(t *testing.T) {
	defer gtest.Catch(t)

	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	gtest.False(Connected(path))
	Connect(path, testExe)
	gtest.True(Connected(path))

	// Existing file: a backup before writing.
	Connect(path, testExe)
	gtest.True(Connected(path + ".claudebar-backup"))
}

func TestParseUsage(t *testing.T) {
	defer gtest.Catch(t)

	reset := time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC).Unix()
	gtest.Equal(
		ParseUsage([]byte(`{"five_hour":null,"seven_day":{"utilization":2.0,"resets_at":"2026-11-21T13:00:00.243654+00:00","limit_dollars":null},
			"limits":[{"kind":"weekly_all","percent":2,"resets_at":"2026-11-21T13:00:00+00:00","scope":null},
			{"kind":"weekly_scoped","percent":7,"resets_at":"2026-11-21T13:00:00+00:00","scope":{"model":{"id":null,"display_name":"Fable"},"surface":null}}]}`)),
		State{
			Limits: Limits{SevenDay: Window{Used: 2, ResetsAt: reset}},
			Models: []ModelLimit{{Name: "Fable", Window: Window{Used: 7, ResetsAt: reset}}},
		},
	)
}

func TestBestToken(t *testing.T) {
	defer gtest.Catch(t)

	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	cred := func(token string, expires time.Time) Credentials {
		var out Credentials
		out.ClaudeAiOauth.AccessToken = token
		out.ClaudeAiOauth.ExpiresAt = expires.UnixMilli()
		return out
	}

	gtest.Eq(BestToken([]Credentials{cred("old", now.Add(time.Hour)), cred("new", now.Add(5*time.Hour))}, now), "new")
	gtest.PanicStr("token expired", func() { BestToken([]Credentials{cred("x", now.Add(-time.Minute))}, now) })
	gtest.PanicStr("no Claude Code login", func() { BestToken(nil, now) })

	// The token never shows up in formatted errors.
	gtest.False(strings.Contains(fmt.Sprint(cred("secret", now)), "secret"))
}

func TestProjectClaudeDirs(t *testing.T) {
	defer gtest.Catch(t)

	root := t.TempDir()
	write := func(path string) {
		gg.Try(os.MkdirAll(filepath.Dir(path), 0o700))
		gg.Try(os.WriteFile(path, []byte(`{}`), 0o600))
	}
	write(filepath.Join(root, "site", "claude", ".credentials.json"))
	write(filepath.Join(root, "ai", "platform", "claude", "claudebar.json"))
	write(filepath.Join(root, "ai", "deep", "app", "claude", ".credentials.json"))
	write(filepath.Join(root, "empty", "claude", "notes.txt"))                    // no login, no data: skipped
	write(filepath.Join(root, "a", "b", "c", "d", "claude", ".credentials.json")) // too deep

	gtest.Equal(ProjectClaudeDirs(root), []string{
		filepath.Join(root, "site", "claude"),
		filepath.Join(root, "ai", "platform", "claude"),
		filepath.Join(root, "ai", "deep", "app", "claude"),
	})
}

func TestBestToken_freshest(t *testing.T) {
	defer gtest.Catch(t)

	// One container idle since yesterday, another running: the running one's token wins.
	now := time.Date(2026, 11, 18, 10, 50, 0, 0, time.UTC)
	var idle, running Credentials
	idle.ClaudeAiOauth.AccessToken, idle.ClaudeAiOauth.ExpiresAt = "idle", now.Add(-34*time.Hour).UnixMilli()
	running.ClaudeAiOauth.AccessToken, running.ClaudeAiOauth.ExpiresAt = "running", now.Add(8*time.Hour).UnixMilli()
	gtest.Eq(BestToken([]Credentials{idle, running}, now), "running")
}

func TestUniqueLogins(t *testing.T) {
	defer gtest.Catch(t)

	login := func(access, refresh string) Credentials {
		var out Credentials
		out.ClaudeAiOauth.AccessToken, out.ClaudeAiOauth.RefreshToken = access, refresh
		return out
	}
	// The same login copied into two containers, another login, an empty file.
	got := UniqueLogins([]Credentials{login("a1", "r1"), login("a1", "r1"), login("a2", "r2"), {}})
	gtest.Eq(len(got), 2)
}

func TestRevokeLogin(t *testing.T) {
	defer gtest.Catch(t)

	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		body := gg.Try1(io.ReadAll(req.Body))
		bodies = append(bodies, string(body))
	}))
	defer server.Close()

	var cred Credentials
	cred.ClaudeAiOauth.AccessToken, cred.ClaudeAiOauth.RefreshToken = "acc", "ref"
	RevokeLogin(server.URL, cred)
	gtest.Equal(bodies, []string{
		`{"client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e","token":"ref","token_type_hint":"refresh_token"}`,
		`{"client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e","token":"acc","token_type_hint":"access_token"}`,
	})

	failing := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) { rw.WriteHeader(400) }))
	defer failing.Close()
	gtest.PanicStr("HTTP 400", func() { RevokeLogin(failing.URL, cred) })
}

func TestAutoStopStep(t *testing.T) {
	defer gtest.Catch(t)

	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	reset := now.Add(3 * Day).Unix()
	early := now.Add(time.Hour).Unix() // runs out ~3 days early
	on := Config{AutoStop: true}

	// Off: never armed.
	state, fire, at := AutoStopStep(AutoStopState{}, Config{}, reset, early, now)
	gtest.False(fire)
	gtest.True(at.IsZero())

	// On: armed now, fires 10 min later.
	state, fire, at = AutoStopStep(AutoStopState{}, on, reset, early, now)
	gtest.False(fire)
	gtest.True(at.Equal(now.Add(AutoStopAfter)))
	state, fire, _ = AutoStopStep(state, on, reset, early, now.Add(9*time.Minute))
	gtest.False(fire)
	state, fire, _ = AutoStopStep(state, on, reset, early, now.Add(10*time.Minute))
	gtest.True(fire)
	// Once per window.
	_, fire, _ = AutoStopStep(state, on, reset, early, now.Add(20*time.Minute))
	gtest.False(fire)

	// The rate drops before 10 min: disarmed, the count starts over.
	state, _, _ = AutoStopStep(AutoStopState{}, on, reset, early, now)
	state, _, _ = AutoStopStep(state, on, reset, 0, now.Add(5*time.Minute))
	gtest.Eq(state.Since, int64(0))

	// Less than a day early: not armed.
	_, _, at = AutoStopStep(AutoStopState{}, on, reset, now.Add(2*Day+time.Hour).Unix(), now)
	gtest.True(at.IsZero())
}
