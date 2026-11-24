package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mitranim/gg"
)

// Limit resets ("Reset for free" in the Claude app): one-off grants that clear the weekly
// limit or give back what Fable used. Read with the request Claude Code makes when it
// offers a reset; used only when the user clicks Reset in the panel and confirms.
// Anthropic rate-limits the read hard, so it runs at most once an hour.

const (
	GrantsURL  = "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1"
	ProfileURL = "https://api.anthropic.com/api/oauth/profile"
	// Grants are offered only to Claude Code ("surface"), and not to old versions.
	ClaudeCodeUserAgent = "claude-cli/2.1.325 (external, cli)"
	GrantsEvery         = time.Hour
)

type Grant struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Type      string    `json:"type"` // "weekly": clears the week; "full": gives back Fable
	Left      int       `json:"resets_left"`
	EndsAt    time.Time `json:"ends_at"`
	UsableNow bool      `json:"usable_now"`
	Weekly    bool      `json:"gives_back_weekly"`
}

type Grants struct {
	Eligible  bool      `json:"eligible"`
	List      []Grant   `json:"grants"`
	CheckedAt time.Time `json:"checked_at"`
}

func GrantsPath() string { return filepath.Join(ConfigDir(), "grants.json") }

func ReadGrants() Grants {
	body, err := os.ReadFile(GrantsPath())
	if os.IsNotExist(err) {
		return Grants{}
	}
	gg.Try(err)
	return gg.JsonDecodeTo[Grants](body)
}

// Grants with a reset left, for the panel.
func (self Grants) Available() []Grant {
	out := []Grant{}
	for _, grant := range self.List {
		if grant.Left > 0 {
			out = append(out, grant)
		}
	}
	return out
}

func FetchGrantsIfStale(token string, now time.Time) {
	if now.Sub(ReadGrants().CheckedAt) < GrantsEvery {
		return
	}
	FetchGrants(token, now)
}

func FetchGrants(token string, now time.Time) {
	body := claudeRequest(http.MethodGet, GrantsURL, token, nil)
	grants := ParseGrants(body)
	grants.CheckedAt = now
	gg.Try(os.MkdirAll(ConfigDir(), 0o700))
	gg.Try(os.WriteFile(GrantsPath(), gg.JsonBytes(grants), 0o600))
}

func ParseGrants(body []byte) Grants {
	var res struct {
		Grants Grants `json:"cedar_ember"`
	}
	gg.Try(json.Unmarshal(body, &res))
	return res.Grants
}

// `claudebar-cli reset <grant id>`: uses one reset, then reads usage and grants again.
func RunReset(grantID string) {
	now := time.Now()
	token := ReadToken(ClaudeDirs(), now)

	var grant Grant
	for _, each := range ReadGrants().List {
		if each.ID == grantID {
			grant = each
		}
	}
	if grant.Left <= 0 || !grant.UsableNow {
		panic(gg.Errf("no reset %q available now", grantID))
	}

	var profile struct {
		Organization struct {
			UUID string `json:"uuid"`
		} `json:"organization"`
	}
	gg.Try(json.Unmarshal(claudeRequest(http.MethodGet, ProfileURL, token, nil), &profile))
	if profile.Organization.UUID == "" {
		panic(gg.Errf("no organization in the Claude profile"))
	}

	claim := gg.JsonBytes(map[string]string{"program": "cedar_ember", "grant_id": grantID, "request_id": newUUID()})
	url := fmt.Sprintf("https://api.anthropic.com/api/organizations/%s/reset_rate_limits", profile.Organization.UUID)
	claudeRequest(http.MethodPost, url, token, claim)

	FetchGrants(token, now)
	state := FetchUsage(token)
	state.CapturedAt = now
	WriteState(UsagePath(), state)
}

// A request to api.anthropic.com as Claude Code; the body of a 2xx answer.
func claudeRequest(method, url, token string, body []byte) []byte {
	req := gg.Try1(http.NewRequest(method, url, bytes.NewReader(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ClaudeCodeUserAgent)

	client := http.Client{Timeout: 25 * time.Second}
	res := gg.Try1(client.Do(req))
	defer res.Body.Close()
	out := gg.Try1(io.ReadAll(res.Body))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		panic(gg.Errf("%s %s: HTTP %d: %.200s", method, req.URL.Path, res.StatusCode, out))
	}
	return out
}

func newUUID() string {
	var buf [16]byte
	gg.Try1(rand.Read(buf[:]))
	buf[6] = buf[6]&0x0f | 0x40
	buf[8] = buf[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
