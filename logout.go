package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mitranim/gg"
)

// Emergency stop: revoke every Claude Code login ClaudeBar can see (the `claude/` folders
// of all projects and the Keychain), the way Claude Code's own /logout revokes its token.
// Containers using those logins lose access at once and need a new /login. Logins made
// separately on servers are not visible here and are not touched.

const (
	RevokeURL = "https://platform.claude.com/v1/oauth/token/revoke"
	// Claude Code's OAuth client.
	ClaudeCodeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

// Starts `claudebar-cli logout-all --auto` in the background: it must not be cut off by
// the app's 5 s limit on `json`.
func StartAutoStop() {
	proc := exec.Command(Executable(), "logout-all", "--auto")
	proc.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	gg.Try(proc.Start())
	gg.Try(proc.Process.Release())
}

// `claudebar-cli logout-all --count`: how many logins would be revoked (for the
// confirmation); without the flag: revoke them all, print what was done.
// `who` names the trigger in the notification: "Emergency stop" or "Auto stop".
func RunLogoutAll(count bool, who string) {
	logins := UniqueLogins(ReadCredentials(ClaudeDirs(), true))
	if count {
		fmt.Println(len(logins))
		return
	}
	var failed []error
	for _, login := range logins {
		err := gg.Catch(func() { RevokeLogin(RevokeURL, login) })
		if err != nil {
			failed = append(failed, err)
		}
	}
	done := fmt.Sprintf("logged out: %d of %d logins revoked", len(logins)-len(failed), len(logins))
	fmt.Println(done)
	if len(failed) > 0 {
		err := gg.Errf("%s; %d failed, first error: %v", done, len(failed), failed[0])
		gg.Try(os.WriteFile(UsageErrPath(), []byte(who+": "+err.Error()), 0o600))
		notify("ClaudeBar: "+who+" incomplete", err.Error())
		panic(err)
	}
	notify("ClaudeBar: "+who, done+". Every Claude Code session using them needs /login.")
}

// Copies of one login (the same refresh token) count once.
func UniqueLogins(all []Credentials) []Credentials {
	var out []Credentials
	seen := map[string]bool{}
	for _, cred := range all {
		key := cred.ClaudeAiOauth.RefreshToken
		if key == "" {
			key = cred.ClaudeAiOauth.AccessToken
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cred)
	}
	return out
}

// The refresh token first (no new access tokens), then the access token in use.
func RevokeLogin(url string, cred Credentials) {
	if token := cred.ClaudeAiOauth.RefreshToken; token != "" {
		revokeToken(url, token, "refresh_token")
	}
	if token := cred.ClaudeAiOauth.AccessToken; token != "" {
		revokeToken(url, token, "access_token")
	}
}

func revokeToken(url, token, hint string) {
	body := gg.JsonBytes(map[string]string{"token": token, "token_type_hint": hint, "client_id": ClaudeCodeClientID})
	req := gg.Try1(http.NewRequest(http.MethodPost, url, bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ClaudeCodeUserAgent)

	client := http.Client{Timeout: 20 * time.Second}
	res := gg.Try1(client.Do(req))
	defer res.Body.Close()
	out := gg.Try1(io.ReadAll(res.Body))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		panic(gg.Errf("revoke %s: HTTP %d: %.200s", hint, res.StatusCode, out))
	}
}

// Auto stop: armed while the current rate runs the weekly limit out more than a day early;
// fires once the condition has held for AutoStopAfter, at most once per weekly window.
const AutoStopAfter = 10 * time.Minute

type AutoStopState struct {
	ResetsAt int64 `json:"resets_at"` // the weekly window it belongs to
	Since    int64 `json:"since"`     // epoch seconds the condition has held since; 0: not armed
	Done     bool  `json:"done"`      // fired in this window
}

func autoStopPath() string { return filepath.Join(ConfigDir(), "auto-stop.json") }

// One step, from `claudebar-cli json` every 20 s: returns the state to keep, whether to
// fire now, and when it would fire (zero when not armed).
func AutoStopStep(prev AutoStopState, conf Config, resetsAt int64, rateRunOut int64, now time.Time) (AutoStopState, bool, time.Time) {
	state := prev
	if state.ResetsAt != resetsAt {
		state = AutoStopState{ResetsAt: resetsAt}
	}
	tooFast := rateRunOut > 0 && resetsAt-rateRunOut > int64(Day/time.Second)
	if !conf.AutoStop || state.Done || !tooFast {
		state.Since = 0
		return state, false, time.Time{}
	}
	if state.Since == 0 {
		state.Since = now.Unix()
	}
	at := time.Unix(state.Since, 0).Add(AutoStopAfter)
	if now.Before(at) {
		return state, false, at
	}
	state.Done = true
	return state, true, at
}

func ReadAutoStop() AutoStopState {
	body, err := os.ReadFile(autoStopPath())
	if os.IsNotExist(err) {
		return AutoStopState{}
	}
	gg.Try(err)
	return gg.JsonDecodeTo[AutoStopState](body)
}

func WriteAutoStop(state AutoStopState) {
	gg.Try(os.MkdirAll(ConfigDir(), 0o700))
	gg.Try(os.WriteFile(autoStopPath(), gg.JsonBytes(state), 0o600))
}

// `claudebar-cli logout-all --auto`: the emergency stop fired by AutoStopStep, in the
// background; the result goes to a macOS notification and the error file.
func RunAutoStop() { RunLogoutAll(false, "Auto stop") }

func notify(title, text string) {
	if runtime.GOOS != "darwin" {
		return
	}
	// A notification that fails to show must not stop the logout or the fetch around it:
	// the same text is also in the panel.
	script := "display notification " + strconv.Quote(text) + " with title " + strconv.Quote(title)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, "claudebar: notification:", err, string(out))
	}
}
