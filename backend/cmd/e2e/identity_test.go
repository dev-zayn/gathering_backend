//go:build e2e

package e2e

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Two players sign up, edit their records, find each other, rotate a session
// and sign out. The console signs in as a dev admin and reads the numbers.
func TestIdentity(t *testing.T) {
	a, pa := player(t, "زين")
	b, pb := player(t, "Mariam")

	for _, p := range []map[string]any{pa, pb} {
		if p["handle"] == "" || p["id"] == "" {
			t.Fatalf("player without id or handle: %v", p)
		}
	}
	if !strings.HasPrefix(pb["handle"].(string), "mariam_") {
		t.Errorf("handle should come from the nickname: %v", pb["handle"])
	}

	// Me, and an edit.
	var me map[string]any
	a.call("GET", "/v1/me", nil, 200, &me)
	if me["nickname"] != "زين" {
		t.Errorf("me: %v", me)
	}
	a.call("PATCH", "/v1/me", map[string]any{"bio": "ألعب كل مساء", "language": "en"}, 200, &me)
	if me["bio"] != "ألعب كل مساء" || me["language"] != "en" {
		t.Errorf("edit did not land: %v", me)
	}
	a.call("PATCH", "/v1/me", map[string]any{"nickname": "x"}, 422, nil)
	a.call("PATCH", "/v1/me", map[string]any{"handle": pb["handle"]}, 409, nil)

	// Presence, search, the card.
	a.call("PUT", "/v1/me/presence", nil, 204, nil)
	var found struct {
		Players []map[string]any `json:"players"`
	}
	b.call("GET", "/v1/players/search?q="+pa["handle"].(string)[:3], nil, 200, &found)
	if len(found.Players) == 0 {
		t.Errorf("search found nobody for %v", pa["handle"])
	}
	var card map[string]any
	b.call("GET", "/v1/players/"+pa["handle"].(string), nil, 200, &card)
	if card["id"] != pa["id"] || card["friendship"] != "none" {
		t.Errorf("card: %v", card)
	}
	b.call("GET", "/v1/players/nobody_0000", nil, 404, nil)

	// The device.
	a.call("PUT", "/v1/me/device", map[string]any{"platform": "android", "token": "fcm-" + pa["id"].(string)}, 200, nil)
	a.call("DELETE", "/v1/me/device", nil, 204, nil)

	// Rotation: the new pair works, the spent one is refused and kills the chain.
	var rotated struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	old := pa["refresh_token"].(string)
	a.call("POST", "/v1/auth/refresh", map[string]any{"refresh_token": old}, 200, &rotated)
	a.bearer = rotated.AccessToken
	a.call("GET", "/v1/me", nil, 200, nil)
	a.call("POST", "/v1/auth/refresh", map[string]any{"refresh_token": old}, 401, nil)
	a.call("GET", "/v1/me", nil, 401, nil)

	// Sign-out ends the session for good.
	b.call("POST", "/v1/auth/logout", nil, 204, nil)
	b.call("GET", "/v1/me", nil, 401, nil)

	// Google and Apple (docs/SSO.md): a provider with client ids on this api
	// (E2E_SSO, comma-separated) refuses a token it does not stand behind; one
	// without says it is not here. A real token needs a phone, so the sweep
	// stops at the refusal.
	on := strings.Split(os.Getenv("E2E_SSO"), ",")
	for _, provider := range []string{"google", "apple"} {
		var refused map[string]any
		if slices.Contains(on, provider) {
			newClient(t).call("POST", "/v1/auth/sso/"+provider, map[string]any{"token": "x"}, 401, &refused)
			if refused["error"] != "invalid_provider_token" {
				t.Errorf("sso %s: %v", provider, refused)
			}
		} else {
			newClient(t).call("POST", "/v1/auth/sso/"+provider, map[string]any{"token": "x"}, 501, &refused)
			if refused["error"] != "not_supported" {
				t.Errorf("sso %s: %v", provider, refused)
			}
		}
	}
	t.Logf("ok   sso: a junk token refused by each provider configured (%v), not_supported by the rest", on)
}

func TestConsole(t *testing.T) {
	c := newClient(t)
	c.origin = console
	var opened map[string]any
	c.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, &opened)

	var me map[string]any
	c.call("GET", "/v1/admin/me", nil, 200, &me)
	if me["name"] != "sweep" {
		t.Errorf("admin me: %v", me)
	}
	var dash map[string]any
	c.call("GET", "/v1/admin/dashboard", nil, 200, &dash)
	if dash["players"].(float64) < 2 {
		t.Errorf("dashboard should count the sweep's players: %v", dash)
	}
	var list struct {
		Players []map[string]any `json:"players"`
	}
	// With nothing typed the list is everybody, the newest first
	// (docs/CONSOLE.md, "The dashboard and the players").
	c.call("GET", "/v1/admin/players?limit=20", nil, 200, &list)
	if len(list.Players) < 2 {
		t.Fatalf("nothing typed listed %d players", len(list.Players))
	}
	for i := 1; i < len(list.Players); i++ {
		if list.Players[i-1]["created_at"].(string) < list.Players[i]["created_at"].(string) {
			t.Fatalf("not newest first at %d: %v then %v", i, list.Players[i-1]["created_at"], list.Players[i]["created_at"])
		}
	}
	t.Logf("ok   the players list with nothing typed: %d, newest first", len(list.Players))
	c.call("GET", "/v1/admin/players?q=mariam", nil, 200, &list)
	if len(list.Players) == 0 {
		t.Fatalf("players list empty")
	}
	// Switching off is TestSwitchingAPlayerOff's, on players of its own: it
	// ends their sessions, which would pull the rug from under another test.
	c.call("GET", "/v1/admin/audit?area=players", nil, 200, nil)
	c.call("GET", "/v1/admin/admins", nil, 200, nil)
	c.call("DELETE", "/v1/admin/session", nil, 204, nil)
	c.call("GET", "/v1/admin/me", nil, 401, nil)
}

// A player deletes their account (docs/ACCOUNTS.md): every session ends at
// once, the refresh token with it, and the old @name finds nobody.
func TestDeletingAnAccount(t *testing.T) {
	c, p := player(t, "Leaving")
	handle, _ := p["handle"].(string)
	refresh, _ := p["refresh_token"].(string)

	c.call("DELETE", "/v1/me", nil, 204, nil)
	c.call("GET", "/v1/me", nil, 401, nil)
	newClient(t).call("POST", "/v1/auth/refresh", map[string]any{"refresh_token": refresh}, 401, nil)

	other, _ := player(t, "Looker")
	other.call("GET", "/v1/players/"+handle, nil, 404, nil)
	t.Logf("ok   delete: 204, the token and the refresh token dead, @%s finds nobody", handle)
}

// A player reports another, and an admin reads it and marks it dealt with
// (docs/REPORTS.md). The sweep touches only the report it made.
func TestReportingAPlayer(t *testing.T) {
	a, _ := player(t, "Reporter")
	_, b := player(t, "Reported")
	id, _ := b["id"].(string)

	a.call("POST", "/v1/players/"+id+"/report", map[string]any{"reason": "spam"}, 422, nil)
	a.call("POST", "/v1/players/ply_aaaaaaaaaaaaaaaa/report", map[string]any{"reason": "name"}, 404, nil)
	a.call("POST", "/v1/players/"+id+"/report", map[string]any{"reason": "name", "note": "e2e"}, 204, nil)
	a.call("POST", "/v1/players/"+id+"/report", map[string]any{"reason": "harassment", "note": "e2e"}, 204, nil)

	c := newClient(t)
	c.origin = console
	var opened map[string]any
	c.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, &opened)
	var page struct {
		Items []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
			Player struct {
				ID string `json:"id"`
			} `json:"player"`
		} `json:"items"`
	}
	c.call("GET", "/v1/admin/reports?status=open&limit=100", nil, 200, &page)
	report := ""
	for _, r := range page.Items {
		if r.Player.ID == id {
			if report != "" || r.Reason != "harassment" {
				t.Fatalf("reporting twice did not replace: %+v", page.Items)
			}
			report = r.ID
		}
	}
	if report == "" {
		t.Fatalf("the report is not in the list: %+v", page.Items)
	}
	c.call("POST", "/v1/admin/reports/"+report+"/resolve", nil, 204, nil)
	c.call("POST", "/v1/admin/reports/"+report+"/resolve", nil, 409, nil)
	t.Logf("ok   report: filed, replaced, read in the console, resolved once")
}
