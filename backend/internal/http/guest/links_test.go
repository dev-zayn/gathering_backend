package guest_test

// The invite link's page and the two documents the phones check, through
// the router the api serves (docs/JOINING.md): the page over the rooms'
// rules on a real database (these skip without TEST_DATABASE_URL), the
// documents from configuration alone.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apphttp "github.com/jamaa/gathering/backend/internal/http"
	"github.com/jamaa/gathering/backend/internal/http/guest"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

const xo = "fixture.xo"

// stores is a deployment with both stores' addresses.
var stores = guest.AppLinks{
	AppStoreURL:  "https://apps.apple.com/app/id6000000000",
	PlayStoreURL: "https://play.google.com/store/apps/details?id=app.gathering.jamaa",
}

// router is the api's router, everything mounted over d.
func router(d apphttp.Deps) http.Handler {
	r, api := apphttp.New("gathering", "test", applog.New("gathering"))
	apphttp.Mount(api, d)
	return r
}

// linkRooms is the rooms' rules over a real database holding the xo game,
// its icon signed by a stand-in bucket at https://signed.test.
func linkRooms(t *testing.T) (*rooms.Service, *gorm.DB) {
	t.Helper()
	db := biztest.DB(t)
	m := model.Manifest{Name: "إكس أو", Version: "1.0.0", Entry: "https://games.yourstudio.example/xo/"}
	m.Publisher.Tier = enum.TierPartner
	m.Room.Players.Min, m.Room.Players.Max = 2, 2
	m.Room.Host, m.Room.Pace, m.Room.State, m.Room.Rejoin = enum.HostAuthoritative, enum.PaceTurn, enum.StateBlob, true
	biztest.SeedGame(t, db, xo, m)

	rules := rooms.New(db, rooms.Config{
		Alphabet: "ABCDEFGHJKLMNPQRSTUVWXYZ23456789", MaxMembers: 64,
		TicketTTL: 10 * time.Minute, LinkURL: "https://go.gathering.test", AvatarTTL: time.Hour,
	})
	rules.Files = biztest.NewFiles()
	return rules, db
}

// page is one answer of the link's page.
type page struct {
	t      *testing.T
	path   string
	status int
	header http.Header
	body   string
}

func open(t *testing.T, h http.Handler, path string, headers ...string) page {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return page{t: t, path: path, status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

// is checks the status and what every form carries: a page, never the API's
// error body, with its headers, and a policy that allows the page's own
// style, and its script where it has one, by their hashes and nothing else.
func (p page) is(status int) page {
	p.t.Helper()
	if p.status != status {
		p.t.Fatalf("%s: %d, want %d:\n%s", p.path, p.status, status, p.body)
	}
	if !strings.HasPrefix(p.body, "<!doctype html>") {
		p.t.Fatalf("%s: not a page:\n%s", p.path, p.body)
	}
	for k, v := range map[string]string{
		"Content-Type":           "text/html; charset=utf-8",
		"Cache-Control":          "no-store",
		"Vary":                   "Accept-Language",
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := p.header.Get(k); got != v {
			p.t.Errorf("%s: %s %q, want %q", p.path, k, got, v)
		}
	}

	csp := p.header.Get("Content-Security-Policy")
	for _, d := range []string{"default-src 'none'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, d+";") && !strings.HasSuffix(csp, d) {
			p.t.Errorf("%s: the policy lacks %s: %s", p.path, d, csp)
		}
	}
	style, ok := inline(p.body, "style")
	if !ok || directive(csp, "style-src") != hashOf(style) {
		p.t.Errorf("%s: style-src %q does not allow the page's style", p.path, directive(csp, "style-src"))
	}
	if script, ok := inline(p.body, "script"); ok {
		if directive(csp, "script-src") != hashOf(script) {
			p.t.Errorf("%s: script-src %q does not allow the page's script", p.path, directive(csp, "script-src"))
		}
	} else if directive(csp, "script-src") != "'none'" {
		p.t.Errorf("%s: no script on the page, and script-src %q", p.path, directive(csp, "script-src"))
	}
	if strings.Contains(p.body, " style=") || strings.Contains(p.body, " onclick=") {
		p.t.Errorf("%s: an inline style or handler no policy here allows", p.path)
	}
	return p
}

// has fails the test on any of these the page does not say.
func (p page) has(parts ...string) page {
	p.t.Helper()
	for _, s := range parts {
		if !strings.Contains(p.body, s) {
			p.t.Errorf("%s: the page lacks %q:\n%s", p.path, s, p.body)
		}
	}
	return p
}

// lacks fails the test on any of these the page does say.
func (p page) lacks(parts ...string) page {
	p.t.Helper()
	for _, s := range parts {
		if s != "" && strings.Contains(p.body, s) {
			p.t.Errorf("%s: the page says %q", p.path, s)
		}
	}
	return p
}

// images is the policy's img-src.
func (p page) images() string {
	return directive(p.header.Get("Content-Security-Policy"), "img-src")
}

// inline is the text of the page's one element of this tag.
func inline(body, tag string) (string, bool) {
	_, rest, ok := strings.Cut(body, "<"+tag+">")
	if !ok {
		return "", false
	}
	text, _, ok := strings.Cut(rest, "</"+tag+">")
	return text, ok
}

// directive is one directive's value in a policy.
func directive(csp, name string) string {
	for _, d := range strings.Split(csp, ";") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(d), name+" "); ok {
			return v
		}
	}
	return ""
}

func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// The page follows the room: 200 in its lobby, while a round plays and
// between rounds, however the code is typed; 404 once it ends, and for a
// code no open room holds or that cannot be one. It shows the game and the
// code, in Arabic or English, and nothing else about the room.
func TestInviteLinkPage(t *testing.T) {
	rules, db := linkRooms(t)
	h := router(apphttp.Deps{Rooms: rules, AppLinks: stores})
	ctx := context.Background()

	host := biztest.SeedPlayer(t, db, "", "زين")
	member := biztest.SeedPlayer(t, db, "", "Mariam")
	room, err := rules.Create(ctx, host.ID, rooms.CreateRequest{Game: xo})
	if err != nil {
		t.Fatal(err)
	}
	if room.Link != "https://go.gathering.test/r/"+room.Code {
		t.Fatalf("the room's link: %s", room.Link)
	}
	path := "/r/" + room.Code
	leaks := []string{room.ID, host.ID, host.Nickname, host.Handle, member.ID, member.Nickname, member.Handle,
		"private", "friends", "lobby", "playing", "ended"}

	// The lobby, in Arabic: the game, the code in an LTR island with its
	// copy key, the line for a phone with the app, the stores' keys.
	lobby := open(t, h, path).is(http.StatusOK).has(
		`<html lang="ar" dir="rtl">`,
		`<title>انضم لغرفة إكس أو · گعدة</title>`,
		`<meta name="robots" content="noindex">`,
		`<img class="icon" src="https://signed.test/games/fixture.xo/icon.png"`,
		`<h1 dir="auto">إكس أو</h1>`,
		`<p class="code" id="code" dir="ltr" translate="no">`+room.Code+`</p>`,
		`<button class="key primary" id="copy" type="button" data-done="انّسخ الرمز" hidden>انسخ الرمز</button>`,
		"عندك گعدة؟ افتحها واكتب الرمز",
		"ما عندك گعدة؟ نزّلها",
		`<a class="key" href="https://apps.apple.com/app/id6000000000">App Store</a>`,
		`<a class="key" href="https://play.google.com/store/apps/details?id=app.gathering.jamaa">Google Play</a>`,
	).lacks(leaks...)
	if lobby.images() != "https://signed.test" {
		t.Errorf("img-src %q, want the icon's host alone", lobby.images())
	}

	// Typed as a friend reads it out: the same page.
	for _, typed := range []string{
		strings.ToLower(room.Code[:3] + "-" + room.Code[3:]),
		"%20" + strings.ToLower(room.Code[:3]) + "%20" + room.Code[3:] + "%20",
		room.Code[:2] + "--" + room.Code[2:],
	} {
		if p := open(t, h, "/r/"+typed).is(http.StatusOK); p.body != lobby.body {
			t.Errorf("typed %q: another page:\n%s", typed, p.body)
		}
	}

	// English when the browser prefers it to Arabic; Arabic when it does not.
	open(t, h, path, "Accept-Language", "en-US,en;q=0.9,ar;q=0.8").is(http.StatusOK).has(
		`<html lang="en" dir="ltr">`,
		`<title>Join a إكس أو room · Gathering</title>`,
		`>Room code<`,
		`data-done="Code copied" hidden>Copy the code</button>`,
		"Have Gathering? Open it and type the code.",
		"Don&#39;t have Gathering yet?",
		`id="code" dir="ltr" translate="no">`+room.Code+`<`,
	).lacks(append(leaks, "رمز الغرفة")...)
	open(t, h, path, "Accept-Language", "ar-IQ,ar;q=0.9,en;q=0.8").is(http.StatusOK).has(`<html lang="ar" dir="rtl">`)

	// A friends-only room's page is any room's page.
	other := biztest.SeedPlayer(t, db, "", "Ali")
	closed, err := rules.Create(ctx, other.ID, rooms.CreateRequest{Game: xo, Visibility: "friends"})
	if err != nil {
		t.Fatal(err)
	}
	friendsOnly := open(t, h, "/r/"+closed.Code).is(http.StatusOK)
	if strings.ReplaceAll(friendsOnly.body, closed.Code, "CODE") != strings.ReplaceAll(lobby.body, room.Code, "CODE") {
		t.Errorf("a friends-only room's page differs:\n%s", friendsOnly.body)
	}

	// A round under way, then the lobby between rounds: still the room's page.
	if _, err := rules.Join(ctx, member.ID, room.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Start(ctx, host.ID, room.ID); err != nil {
		t.Fatal(err)
	}
	if p := open(t, h, path).is(http.StatusOK); p.body != lobby.body {
		t.Errorf("while a round plays:\n%s", p.body)
	}
	if _, err := rules.Finish(ctx, host.ID, room.ID, nil); err != nil {
		t.Fatal(err)
	}
	if p := open(t, h, path).is(http.StatusOK); p.body != lobby.body {
		t.Errorf("between rounds:\n%s", p.body)
	}

	// Ended: the not-found form, in either language. No game, no code, no
	// copy key, no script, no picture; the stores' keys stay.
	if _, err := rules.End(ctx, host.ID, room.ID, nil); err != nil {
		t.Fatal(err)
	}
	gone := open(t, h, path).is(http.StatusNotFound).has(
		`<html lang="ar" dir="rtl">`,
		`<title>گعدة</title>`,
		`<h1>هالغرفة ما موجودة، أو خلصت</h1>`,
		`href="https://apps.apple.com/app/id6000000000"`,
		`href="https://play.google.com/store/apps/details?id=app.gathering.jamaa"`,
	).lacks(append(leaks, room.Code, "إكس أو", `id="code"`, `id="copy"`, "<script", "<img", "افتحها واكتب الرمز")...)
	if gone.images() != "'none'" {
		t.Errorf("not found: img-src %q", gone.images())
	}
	open(t, h, path, "Accept-Language", "en").is(http.StatusNotFound).has(
		`<html lang="en" dir="ltr">`, `<title>Gathering</title>`, "This room doesn&#39;t exist, or it&#39;s over.",
	).lacks(room.Code)

	// Nothing that cannot be a code, and no code no open room holds, is a
	// room: the same not-found form, never the API's error body.
	free := "ZZZZZZ"
	if free == closed.Code {
		free = "YYYYYY"
	}
	for _, code := range []string{free, "nope", "O0I1OO", strings.Repeat("A", 70), "%3Cscript%3E"} {
		if p := open(t, h, "/r/"+code).is(http.StatusNotFound); p.body != gone.body {
			t.Errorf("%s: another page:\n%s", code, p.body)
		}
	}

	// Without the stores' addresses, no keys and no line over them; with one,
	// that one.
	open(t, h, "/r/"+closed.Code).is(http.StatusOK)
	open(t, router(apphttp.Deps{Rooms: rules}), "/r/"+closed.Code).is(http.StatusOK).
		lacks(`class="stores"`, "نزّلها", "App Store", "Google Play")
	open(t, router(apphttp.Deps{Rooms: rules, AppLinks: guest.AppLinks{PlayStoreURL: stores.PlayStoreURL}}), path).
		is(http.StatusNotFound).has("نزّلها", "Google Play").lacks("App Store")
}

// A failure on our side is a page too: the try-again form, 500, and still
// nothing but a page.
func TestInviteLinkSaysTryAgain(t *testing.T) {
	// A process wired without the rooms.
	open(t, router(apphttp.Deps{}), "/r/K7M2QX").is(http.StatusInternalServerError).has(
		`<html lang="ar" dir="rtl">`, "<h1>ما كدرنا نفتح الرابط هسّه. جرّب بعد شوية.</h1>",
	).lacks("K7M2QX", "<script")

	// A database that does not answer.
	rules, db := linkRooms(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	open(t, router(apphttp.Deps{Rooms: rules, AppLinks: stores}), "/r/K7M2QX", "Accept-Language", "en").
		is(http.StatusInternalServerError).
		has(`<html lang="en" dir="ltr">`, "We couldn&#39;t open this link just now. Try again in a moment.", "Google Play").
		lacks("K7M2QX", "internal_error")
}

// The two documents, exactly as the phones read them: their empty forms
// until the claim is configured, application/json, an hour's cache, and
// never behind a redirect.
func TestWellKnownDocuments(t *testing.T) {
	const pkg, bundle = "app.gathering.jamaa", "app.gathering.jamaa"
	android := `[{"relation":["delegate_permission/common.handle_all_urls"],"target":{"namespace":"android_app",` +
		`"package_name":"app.gathering.jamaa","sha256_cert_fingerprints":["14:6D:E9:83","2F:19:AD:EB"]}}]`
	apple := `{"applinks":{"details":[{"appIDs":["ABCDE12345.app.gathering.jamaa"],"components":[{"/":"/r/*"},{"/":"/t/*"}]}]}}`
	const noAndroid, noApple = `[]`, `{"applinks":{"details":[]}}`
	prints := []string{"14:6D:E9:83", "2F:19:AD:EB"}

	for _, tc := range []struct {
		name           string
		links          guest.AppLinks
		android, apple string
	}{
		{"unconfigured", guest.AppLinks{AndroidPackage: pkg, IOSBundle: bundle}, noAndroid, noApple},
		{"configured", guest.AppLinks{AndroidPackage: pkg, SHA256: prints, AppleTeamID: "ABCDE12345", IOSBundle: bundle}, android, apple},
		{"android only", guest.AppLinks{AndroidPackage: pkg, SHA256: prints, IOSBundle: bundle}, android, noApple},
		{"ios only", guest.AppLinks{AndroidPackage: pkg, AppleTeamID: "ABCDE12345", IOSBundle: bundle}, noAndroid, apple},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := router(apphttp.Deps{AppLinks: tc.links})
			for _, doc := range []struct{ path, want string }{
				{"/.well-known/assetlinks.json", tc.android},
				{"/.well-known/apple-app-site-association", tc.apple},
			} {
				// A browser asks for HTML first; the phones ask for anything.
				for _, accept := range []string{"", "text/html,application/xhtml+xml,*/*;q=0.8"} {
					req := httptest.NewRequest(http.MethodGet, doc.path, nil)
					if accept != "" {
						req.Header.Set("Accept", accept)
					}
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						t.Fatalf("%s: %d: %s", doc.path, rec.Code, rec.Body)
					}
					if ct, cc := rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"); ct != "application/json" || cc != "max-age=3600" {
						t.Errorf("%s: Content-Type %q, Cache-Control %q", doc.path, ct, cc)
					}
					var got, want any
					if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
						t.Fatalf("%s: %v: %s", doc.path, err, rec.Body)
					}
					if err := json.Unmarshal([]byte(doc.want), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s:\n got %s\nwant %s", doc.path, strings.TrimSpace(rec.Body.String()), doc.want)
					}
				}
			}
		})
	}
}
