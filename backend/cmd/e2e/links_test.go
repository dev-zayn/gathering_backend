//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The invite link (docs/JOINING.md), as a browser and a phone read it from
// the api: on a laptop the link host is the api itself, and anywhere else the
// link host routes /r/* and /.well-known/* to it. Neither the page nor the
// documents may answer with a redirect, so this client follows none.
var noRedirects = &http.Client{
	Timeout:       15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// fetch is one plain GET: the status, the headers and the body as text.
func fetch(t *testing.T, path string, headers ...string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, apiURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := noRedirects.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(raw)
}

// linkPage reads the link's page and checks what every form of it carries: a
// page, never the API's error body, not cached, under a strict policy.
func linkPage(t *testing.T, path string, want int, headers ...string) string {
	t.Helper()
	status, h, body := fetch(t, path, headers...)
	if status != want {
		t.Fatalf("GET %s: got %d, want %d: %s", path, status, want, body)
	}
	if ct, cc := h.Get("Content-Type"), h.Get("Cache-Control"); ct != "text/html; charset=utf-8" || cc != "no-store" {
		t.Fatalf("GET %s: Content-Type %q, Cache-Control %q", path, ct, cc)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "default-src 'none';") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("GET %s: the policy %q", path, csp)
	}
	if !strings.HasPrefix(body, "<!doctype html>") {
		t.Fatalf("GET %s: not a page: %s", path, body)
	}
	t.Logf("ok   GET %s → %d", path, status)
	return body
}

// wellKnown reads one of the two documents the phones check: JSON, an
// hour's cache, straight from the api. Answers the body.
func wellKnown(t *testing.T, path string) []byte {
	t.Helper()
	status, h, body := fetch(t, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d: %s", path, status, body)
	}
	if ct, cc := h.Get("Content-Type"), h.Get("Cache-Control"); ct != "application/json" || cc != "max-age=3600" {
		t.Fatalf("GET %s: Content-Type %q, Cache-Control %q", path, ct, cc)
	}
	t.Logf("ok   GET %s → %d %s", path, status, strings.TrimSpace(body))
	return []byte(body)
}

// inviteLink is the room's link, its page in both languages and the not-found
// form, and the two documents: their empty forms on a laptop, the claim
// wherever the app's identifiers are configured.
func inviteLink(t *testing.T, room roomJSON, hidden ...string) {
	t.Helper()
	if !strings.HasSuffix(room.Link, "/r/"+room.Code) {
		t.Fatalf("the room's link: %q", room.Link)
	}

	// Read as a friend types the code: lower case, a dash.
	typed := strings.ToLower(room.Code[:3] + "-" + room.Code[3:])
	page := linkPage(t, "/r/"+typed, http.StatusOK)
	if !strings.Contains(page, `<html lang="ar" dir="rtl">`) || !strings.Contains(page, ">"+room.Code+"<") {
		t.Fatalf("the room's page lacks its code: %s", page)
	}
	for _, s := range append(hidden, room.ID, room.Host, room.Visibility) {
		if strings.Contains(page, s) {
			t.Fatalf("the room's page says %q: %s", s, page)
		}
	}
	if en := linkPage(t, "/r/"+room.Code, http.StatusOK, "Accept-Language", "en-US,en;q=0.9,ar;q=0.5"); !strings.Contains(en, `<html lang="en" dir="ltr">`) ||
		!strings.Contains(en, ">"+room.Code+"<") {
		t.Fatalf("the room's page in English: %s", en)
	}
	t.Logf("ok   the link %s opens the room's page, in Arabic and in English", room.Link)

	// Nothing that cannot be a code is a room.
	if gone := linkPage(t, "/r/not-a-code", http.StatusNotFound); strings.Contains(gone, `id="code"`) {
		t.Fatalf("a bad code's page shows a code: %s", gone)
	}

	var android []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace    string   `json:"namespace"`
			PackageName  string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	raw := wellKnown(t, "/.well-known/assetlinks.json")
	if err := json.Unmarshal(raw, &android); err != nil || android == nil {
		t.Fatalf("assetlinks.json is not a list: %v: %s", err, raw)
	}
	for _, s := range android {
		if len(s.Relation) != 1 || s.Relation[0] != "delegate_permission/common.handle_all_urls" ||
			s.Target.Namespace != "android_app" || s.Target.PackageName == "" || len(s.Target.Fingerprints) == 0 {
			t.Fatalf("assetlinks.json: %s", raw)
		}
	}

	var apple struct {
		AppLinks *struct {
			Details []struct {
				AppIDs     []string            `json:"appIDs"`
				Components []map[string]string `json:"components"`
			} `json:"details"`
		} `json:"applinks"`
	}
	raw = wellKnown(t, "/.well-known/apple-app-site-association")
	if err := json.Unmarshal(raw, &apple); err != nil || apple.AppLinks == nil || apple.AppLinks.Details == nil {
		t.Fatalf("apple-app-site-association: %v: %s", err, raw)
	}
	for _, d := range apple.AppLinks.Details {
		if len(d.AppIDs) != 1 || !strings.Contains(d.AppIDs[0], ".") || len(d.Components) != 2 || d.Components[0]["/"] != "/r/*" || d.Components[1]["/"] != "/t/*" {
			t.Fatalf("apple-app-site-association: %s", raw)
		}
	}
	t.Logf("ok   assetlinks.json (%d statements) and apple-app-site-association (%d apps), never behind a redirect",
		len(android), len(apple.AppLinks.Details))
}
