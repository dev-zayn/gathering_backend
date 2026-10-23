//go:build e2e

package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The games: the console registers a game by URL and publishes it; a player
// finds it in the store and opens it; the game's own server — played here by
// the test — spends the launch code, reads the bridge, renews, and loses it
// all when the player takes the game off the shelf.
//
// The studio's server is the test itself, on loopback: the api is expected to
// run with ALLOW_PRIVATE_HOSTS=true, as a laptop does. E2E_SELF names the
// host an api in a container reaches this machine by (host.docker.internal).
func TestGames(t *testing.T) {
	id := "sweep." + strconv.FormatInt(time.Now().UnixNano(), 36)
	studio := serveStudio(t, id)

	admin := newClient(t)
	admin.origin = console
	admin.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, nil)

	// Registered by its address, unpublished, with the secret shown once.
	var registered struct {
		ID      string `json:"id"`
		Listing string `json:"listing"`
		Secret  string `json:"client_secret"`
		Room    struct {
			Min  int    `json:"min"`
			Host string `json:"host"`
		} `json:"room"`
	}
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 201, &registered)
	if registered.ID != id || registered.Listing != "unpublished" || !strings.HasPrefix(registered.Secret, "sec_") {
		t.Fatalf("registered: %+v", registered)
	}
	if registered.Room.Min != 2 || registered.Room.Host != "authoritative" {
		t.Errorf("the manifest's room did not land: %+v", registered.Room)
	}
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 409, nil)

	// No store copy, no store.
	admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 422, nil)
	admin.call("PATCH", "/v1/admin/games/"+id, map[string]any{"tagline": "لعبة السويب", "category": "brain", "description": "تسجّلها الجولة وتحذفها."}, 200, nil)
	var withIcon map[string]any
	admin.upload("PUT", "/v1/admin/games/"+id+"/icon", "icon.png", "image/png", tinyPNG(t), 200, &withIcon)
	if withIcon["icon"] == "" {
		t.Fatalf("no icon link after the upload: %v", withIcon)
	}
	var published map[string]any
	admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 200, &published)
	if published["listing"] != "published" {
		t.Fatalf("publish: %v", published)
	}
	admin.call("GET", "/v1/admin/games?listing=published", nil, 200, nil)
	admin.call("GET", "/v1/admin/games/"+id, nil, 200, nil)
	var categories struct {
		Categories []string `json:"categories"`
	}
	admin.call("GET", "/v1/admin/categories", nil, 200, &categories)
	if len(categories.Categories) != 8 {
		t.Errorf("categories: %v", categories.Categories)
	}

	// The player finds it, and the first open puts it on the shelf.
	a, pa := player(t, "زين")
	row := storeRow(t, a, id)
	if row["in_library"] != false || row["tagline"] != "لعبة السويب" || row["icon"] == "" {
		t.Fatalf("store row: %v", row)
	}
	// The row carries what the app asks the phone for, so a tile can ask
	// before it opens the game: a list, never null.
	if _, ok := row["permissions"].([]any); !ok {
		t.Fatalf("store row has no permissions list: %v", row)
	}
	var detail map[string]any
	a.call("GET", "/v1/games/"+id, nil, 200, &detail)
	if detail["room"].(map[string]any)["host"] != "authoritative" || detail["in_library"] != false {
		t.Fatalf("detail: %v", detail)
	}

	var opened struct {
		URL         string `json:"url"`
		Mode        string `json:"mode"`
		Orientation string `json:"orientation"`
		ExpiresIn   int    `json:"expires_in"`
	}
	a.call("POST", "/v1/games/"+id+"/session?path=/board", nil, 200, &opened)
	if !strings.HasPrefix(opened.URL, studio+"/play/?") || !strings.Contains(opened.URL, "code=") || opened.Mode != "embedded" {
		t.Fatalf("opened: %+v", opened)
	}
	if storeRow(t, a, id)["in_library"] != true {
		t.Fatal("the first open did not put the game on the shelf")
	}
	code := codeOf(t, opened.URL)

	// The studio's server spends the code — once — and reads the bridge.
	partner := newClient(t)
	var issued struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}
	partner.form("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {id}, "client_secret": {registered.Secret},
	}, 200, &issued)
	if issued.AccessToken == "" || issued.RefreshToken == "" || !strings.Contains(issued.Scope, "identity:read") {
		t.Fatalf("issued: %+v", issued)
	}
	partner.form("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {id}, "client_secret": {registered.Secret},
	}, 400, nil)
	partner.form("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {id}, "client_secret": {"sec_wrong"},
	}, 401, nil)

	partner.bearer = issued.AccessToken
	var who struct {
		Player struct {
			ID       string `json:"id"`
			Nickname string `json:"nickname"`
		} `json:"player"`
		Room  any      `json:"room"`
		Scope []string `json:"scope"`
		Live  bool     `json:"live"`
	}
	partner.call("GET", "/v1/bridge/context", nil, 200, &who)
	if who.Player.ID != pa["id"] || who.Player.Nickname != "زين" || who.Room != nil || !who.Live {
		t.Fatalf("context: %+v", who)
	}
	var info map[string]any
	partner.call("GET", "/oauth/userinfo", nil, 200, &info)
	if info["sub"] != pa["id"] || info["game"] != id {
		t.Fatalf("userinfo: %v", info)
	}

	// A renewal keeps the refresh token and has nobody present.
	var renewed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	partner.form("/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {issued.RefreshToken},
		"client_id": {id}, "client_secret": {registered.Secret},
	}, 200, &renewed)
	if renewed.AccessToken == "" || renewed.RefreshToken != "" {
		t.Fatalf("renewed: %+v", renewed)
	}
	partner.bearer = renewed.AccessToken
	partner.call("GET", "/v1/bridge/context", nil, 200, &who)
	if who.Live {
		t.Error("a renewal invented a person")
	}

	// Signing in from the studio's own site: PKCE, and the redirect we know.
	verifier := randomString(t)
	sum := sha256.Sum256([]byte(verifier))
	authorize := "/oauth/authorize?" + url.Values{
		"client_id": {id}, "redirect_uri": {studio + "/callback"}, "response_type": {"code"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"xyz"},
	}.Encode()
	location := a.redirect("GET", authorize)
	if !strings.HasPrefix(location, studio+"/callback?") || !strings.Contains(location, "state=xyz") {
		t.Fatalf("authorize sent the player to %q", location)
	}
	partner.form("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {codeOf(t, location)},
		"code_verifier": {verifier}, "redirect_uri": {studio + "/callback"},
		"client_id": {id}, "client_secret": {registered.Secret},
	}, 200, nil)
	a.redirectStatus("GET", strings.Replace(authorize, url.QueryEscape(studio+"/callback"), url.QueryEscape("https://evil.example/cb"), 1), 422)

	gameServerResults(t, admin, a, partner, id, pa)

	// Taking the game off the shelf ends every token the game holds, now.
	a.call("DELETE", "/v1/games/"+id, nil, 204, nil)
	partner.call("GET", "/v1/bridge/context", nil, 401, nil)
	partner.bearer = ""
	partner.form("/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {issued.RefreshToken},
		"client_id": {id}, "client_secret": {registered.Secret},
	}, 400, nil)
	a.call("DELETE", "/v1/games/"+id, nil, 404, nil)
	if storeRow(t, a, id)["in_library"] != false {
		t.Fatal("the game is still on the shelf")
	}

	// The kill switch, and the way back; featured leads the store.
	admin.call("POST", "/v1/admin/games/"+id+"/suspend", map[string]any{"reason": "the sweep"}, 200, nil)
	a.call("GET", "/v1/games/"+id, nil, 404, nil)
	admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 200, nil)
	var featured map[string]any
	admin.call("POST", "/v1/admin/games/"+id+"/feature", map[string]any{"featured": true}, 200, &featured)
	if featured["featured"] != true {
		t.Fatalf("feature: %v", featured)
	}
	if storeRow(t, a, id)["featured"] != true {
		t.Fatal("the store does not show the game featured")
	}
	admin.call("POST", "/v1/admin/games/"+id+"/refresh", nil, 200, nil)
	var reach struct {
		Games []map[string]any `json:"games"`
	}
	admin.call("GET", "/v1/admin/games/reach", nil, 200, &reach)
	var secret map[string]any
	admin.call("POST", "/v1/admin/games/"+id+"/secret", nil, 200, &secret)
	if !strings.HasPrefix(fmt.Sprint(secret["client_secret"]), "sec_") {
		t.Fatalf("rolled: %v", secret)
	}
	var log struct {
		Total int `json:"total"`
	}
	admin.call("GET", "/v1/admin/audit?area=games&subject="+id, nil, 200, &log)
	if log.Total < 5 {
		t.Errorf("the games log holds %d lines for %s", log.Total, id)
	}

	// Leave the registry as it was found: suspended, so the next sweep's
	// store is not a shelf of old sweeps.
	admin.call("POST", "/v1/admin/games/"+id+"/suspend", map[string]any{"reason": "the sweep is over"}, 200, nil)

	if fixtures := os.Getenv("E2E_FIXTURES"); fixtures != "" {
		partnerFixture(t, admin, a, pa, strings.TrimRight(fixtures, "/"))
	}
}

// gameServerResults is the game's own server writing a room's results with
// the game token it holds for a: refused for a lobby; then, after the host
// ended the game with scores, its own results replace the host's for good.
// A token without results:write is refused before anything is read, naming
// the scope.
func gameServerResults(t *testing.T, admin, a, server *client, id string, pa map[string]any) {
	t.Helper()
	b, pb := player(t, "Mariam")
	aID, bID := pa["id"].(string), pb["id"].(string)

	var room roomJSON
	a.call("POST", "/v1/rooms", map[string]any{"game": id}, 201, &room)
	var refused errorJSON
	server.call("POST", "/v1/bridge/results", map[string]any{
		"room": room.ID, "scores": []map[string]any{{"player": aID, "score": 1}},
	}, 409, &refused)
	if refused.Error != "room_not_started" {
		t.Fatalf("results for a lobby: %+v", refused)
	}
	t.Logf("ok   the game's server is refused results for a lobby: room_not_started")

	b.call("POST", "/v1/rooms/join", map[string]any{"code": room.Code}, 200, nil)
	a.call("POST", "/v1/rooms/"+room.ID+"/start", nil, 200, nil)
	a.call("POST", "/v1/rooms/"+room.ID+"/end", map[string]any{"scores": []map[string]any{
		{"player": aID, "score": 3}, {"player": bID, "score": 5},
	}}, 200, nil)
	var sheet sheetJSON
	b.call("GET", "/v1/rooms/"+room.ID+"/results", nil, 200, &sheet)
	if len(sheet.Results) != 2 || sheet.Results[0].Player != bID || sheet.Results[0].SubmittedBy != "host" {
		t.Fatalf("the host's results: %+v", sheet)
	}
	t.Logf("ok   the host's end wrote the results (submitted_by host)")

	var posted struct {
		Round   int          `json:"round"`
		Results []resultJSON `json:"results"`
	}
	server.call("POST", "/v1/bridge/results", map[string]any{"room": room.ID, "scores": []map[string]any{
		{"player": aID, "score": 7}, {"player": bID, "score": 2},
	}}, 200, &posted)
	if posted.Round != 1 || len(posted.Results) != 2 || posted.Results[0].Player != aID || posted.Results[0].Rank != 1 ||
		posted.Results[0].SubmittedBy != "game" || posted.Results[0].Nickname != "زين" || posted.Results[1].Rank != 2 {
		t.Fatalf("posted: %+v", posted)
	}
	b.call("GET", "/v1/rooms/"+room.ID+"/results", nil, 200, &sheet)
	if len(sheet.Results) != 2 || sheet.Results[0].Player != aID || sheet.Results[0].Score != 7 ||
		sheet.Results[0].SubmittedBy != "game" || sheet.Results[1].SubmittedBy != "game" {
		t.Fatalf("the room's results after the game's: %+v", sheet)
	}
	var ended roomJSON
	b.call("GET", "/v1/rooms/"+room.ID, nil, 200, &ended)
	if len(ended.Results) != 2 || ended.Results[0].Player != aID || ended.Results[0].Rank != 1 {
		t.Fatalf("the room reads %+v", ended.Results)
	}
	var history historyJSON
	a.call("GET", "/v1/me/history?limit=1", nil, 200, &history)
	if len(history.Items) != 1 || history.Items[0].Room != room.ID || history.Items[0].Round != 1 || history.Items[0].Rank != 1 ||
		history.Items[0].SubmittedBy != "game" || history.Items[0].Score != 7 {
		t.Fatalf("a's history: %+v", history)
	}
	t.Logf("ok   the game's server replaced them: %s first, submitted_by game, in the room, its sheet and the history", aID)

	var invalid errorJSON
	server.call("POST", "/v1/bridge/results", map[string]any{"room": room.ID, "scores": []map[string]any{
		{"player": aID, "score": 1, "rank": 0},
	}}, 422, &invalid)
	if invalid.Fields["scores.0.rank"] != "must_be_positive" {
		t.Fatalf("rank 0: %+v", invalid)
	}
	t.Logf("ok   a rank of 0 is refused: scores.0.rank must_be_positive")
	server.call("POST", "/v1/bridge/results", map[string]any{"room": room.ID, "round": 2, "scores": []map[string]any{
		{"player": aID, "score": 1},
	}}, 422, &invalid)
	if invalid.Fields["round"] != "unknown" {
		t.Fatalf("round 2 of a room that played one: %+v", invalid)
	}
	server.call("POST", "/v1/bridge/results", map[string]any{"room": room.ID, "round": 1, "scores": []map[string]any{
		{"player": aID, "score": 8}, {"player": bID, "score": 2},
	}}, 200, &posted)
	if posted.Round != 1 || len(posted.Results) != 2 || posted.Results[0].Score != 8 {
		t.Fatalf("round 1 named: %+v", posted)
	}
	t.Logf("ok   the bridge takes a round: 1 named, 2 refused as unknown")

	// A game whose manifest never asked for results:write.
	lite := id + ".lite"
	studio := serveStudio(t, lite, "identity:read")
	var registered struct {
		Secret string `json:"client_secret"`
	}
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 201, &registered)
	admin.call("PATCH", "/v1/admin/games/"+lite, map[string]any{"tagline": "بلا نتائج", "category": "brain"}, 200, nil)
	admin.upload("PUT", "/v1/admin/games/"+lite+"/icon", "icon.png", "image/png", tinyPNG(t), 200, nil)
	admin.call("POST", "/v1/admin/games/"+lite+"/publish", nil, 200, nil)
	t.Cleanup(func() {
		admin.call("POST", "/v1/admin/games/"+lite+"/suspend", map[string]any{"reason": "the results sweep is over"}, 200, nil)
	})
	var opened struct {
		URL string `json:"url"`
	}
	a.call("POST", "/v1/games/"+lite+"/session", nil, 200, &opened)
	other := newClient(t)
	var issued struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	other.form("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {codeOf(t, opened.URL)},
		"client_id": {lite}, "client_secret": {registered.Secret},
	}, 200, &issued)
	if strings.Contains(issued.Scope, "results:write") {
		t.Fatalf("the lite game was granted %q", issued.Scope)
	}
	other.bearer = issued.AccessToken
	var scoped errorJSON
	other.call("POST", "/v1/bridge/results", map[string]any{"room": room.ID, "scores": []map[string]any{
		{"player": aID, "score": 1},
	}}, 403, &scoped)
	if scoped.Error != "scope_required" || scoped.Scope != "results:write" {
		t.Fatalf("a token without results:write: %+v", scoped)
	}
	t.Logf("ok   a token without results:write is refused: scope_required, scope results:write")
}

// partnerFixture drives the fixtures' own partner backend: register the xo
// fixture, publish it, have /partner/verify-ticket check a room ticket, then
// open it and let /partner/exchange spend the code. The fixtures hold the
// client secret in their env, so the secret shown at registration here has
// to be the one they were started with; the ticket needs no secret, so it
// goes first.
func partnerFixture(t *testing.T, admin, a *client, pa map[string]any, fixtures string) {
	t.Helper()
	const id = "fixture.xo"

	var registered map[string]any
	req, _ := http.NewRequest("POST", apiURL+"/v1/admin/games", strings.NewReader(`{"url":"`+fixtures+`/xo/"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", console)
	res, err := admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	switch res.StatusCode {
	case 201:
		_ = json.Unmarshal(raw, &registered)
		t.Logf("fixture %s registered; its client secret is %v — set PARTNER_CLIENT_SECRET on the fixtures to it", id, registered["client_secret"])
	case 409:
		t.Logf("fixture %s was already registered", id)
	default:
		t.Fatalf("registering the fixture: %d %s", res.StatusCode, raw)
	}

	var game map[string]any
	admin.call("GET", "/v1/admin/games/"+id, nil, 200, &game)
	if game["listing"] != "published" {
		if game["tagline"] == "" {
			admin.call("PATCH", "/v1/admin/games/"+id, map[string]any{"tagline": "إكس أو، لكن مع صديقك"}, 200, nil)
		}
		if game["icon"] == "" {
			admin.upload("PUT", "/v1/admin/games/"+id+"/icon", "icon.png", "image/png", tinyPNG(t), 200, nil)
		}
		admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 200, nil)
	}

	ticketAtFixture(t, a, pa, fixtures, id)

	var opened struct {
		URL string `json:"url"`
	}
	a.call("POST", "/v1/games/"+id+"/session", nil, 200, &opened)

	body, _ := json.Marshal(map[string]any{"code": codeOf(t, opened.URL)})
	res, err = http.Post(fixtures+"/partner/exchange", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the fixtures' partner backend: %v", err)
	}
	defer res.Body.Close()
	var exchanged struct {
		Token struct {
			Status int             `json:"status"`
			Body   json.RawMessage `json:"body"`
		} `json:"token"`
		Context *struct {
			Status int `json:"status"`
			Body   struct {
				Player struct {
					ID string `json:"id"`
				} `json:"player"`
			} `json:"body"`
		} `json:"context"`
	}
	if err := json.NewDecoder(res.Body).Decode(&exchanged); err != nil {
		t.Fatalf("partner exchange: %v", err)
	}
	if exchanged.Token.Status != 200 {
		t.Fatalf("the partner backend could not spend the code: %d %s — is PARTNER_CLIENT_SECRET on the fixtures the secret this game was registered with?",
			exchanged.Token.Status, exchanged.Token.Body)
	}
	if exchanged.Context == nil || exchanged.Context.Status != 200 || exchanged.Context.Body.Player.ID != pa["id"] {
		t.Fatalf("the partner backend read the wrong player: %+v", exchanged.Context)
	}
	t.Logf("ok   the fixtures' partner backend spent a code and read %s", pa["id"])
}

// ticketAtFixture makes a room for the fixture's game and has the fixtures'
// partner backend verify its ticket against the api's public keys, as a
// studio's server would with no session of ours: valid, with the claims the
// api put in. The same ticket with a claim changed is refused.
func ticketAtFixture(t *testing.T, a *client, pa map[string]any, fixtures, game string) {
	t.Helper()
	var room roomJSON
	a.call("POST", "/v1/rooms", map[string]any{"game": game}, 201, &room)
	var ticket struct {
		Token string `json:"token"`
	}
	a.call("GET", "/v1/rooms/"+room.ID+"/ticket", nil, 200, &ticket)

	var verdict struct {
		Valid  bool   `json:"valid"`
		Error  string `json:"error"`
		Claims struct {
			Sub   string   `json:"sub"`
			Room  string   `json:"room"`
			Role  string   `json:"role"`
			Seat  *int     `json:"seat"`
			Round *int     `json:"round"`
			Aud   []string `json:"aud"`
			Exp   int64    `json:"exp"`
		} `json:"claims"`
	}
	status := postJSON(t, fixtures+"/partner/verify-ticket", map[string]any{"token": ticket.Token}, &verdict)
	c := verdict.Claims
	if status != 200 || !verdict.Valid || c.Sub != pa["id"] || c.Room != room.ID || c.Role != "host" ||
		c.Seat == nil || *c.Seat != 0 || c.Round == nil || *c.Round != 0 || len(c.Aud) != 1 || c.Aud[0] != game ||
		c.Exp <= time.Now().Unix() {
		t.Fatalf("the fixtures verified the ticket as %d %+v", status, verdict)
	}
	t.Logf("ok   the fixtures' partner backend verified a ticket for %s against the JWKS: %s, host, seat 0, round 0", room.ID, c.Sub)

	parts := strings.Split(ticket.Token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(payload), `"role":"host"`, `"role":"player"`, 1)
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(changed)) + "." + parts[2]
	verdict.Valid, verdict.Error = true, ""
	if status := postJSON(t, fixtures+"/partner/verify-ticket", map[string]any{"token": forged}, &verdict); status != 401 ||
		verdict.Valid || verdict.Error != "bad_signature" {
		t.Fatalf("a changed claim: %d %+v", status, verdict)
	}
	t.Logf("ok   the same ticket with a claim changed is refused: bad_signature")

	a.call("POST", "/v1/rooms/"+room.ID+"/end", nil, 200, nil)
}

// postJSON posts a body to somebody other than the api and decodes the
// answer, whatever its status.
func postJSON(t *testing.T, addr string, body, out any) int {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(addr, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%s: %v", addr, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s: %d %s", addr, res.StatusCode, raw)
	}
	return res.StatusCode
}

// serveStudio is the game's server: its manifest, an entry page and the
// callback a sign-in from its own site comes back to. It answers the address
// the api is told to fetch from. The manifest asks for identity:read and
// results:write unless other scopes are named.
func serveStudio(t *testing.T, id string, scopes ...string) string {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{"identity:read", "results:write"}
	}

	addr, self := "127.0.0.1:0", os.Getenv("E2E_SELF")
	if self != "" {
		addr = "0.0.0.0:0"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	srv.Listener = l
	base := "http://" + l.Addr().String()
	if self != "" {
		_, port, _ := net.SplitHostPort(l.Addr().String())
		base = "http://" + self + ":" + port
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/gathering-game.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "name": "لعبة السويب", "version": "1.0.0", "mode": "embedded",
			"entry": base + "/play/", "orientation": "portrait", "splash": false,
			"publisher":     map[string]any{"tier": "partner", "name": "السويب"},
			"scopes":        scopes,
			"permissions":   []string{},
			"redirect_uris": []string{base + "/callback"},
			"room": map[string]any{
				"players": map[string]int{"min": 2, "max": 2},
				"host":    "authoritative", "pace": "turn", "lan": true, "state": "blob",
			},
		})
	})
	mux.HandleFunc("/play/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html lang="ar" dir="rtl"><body style="background:#0f1222;color:#fff">لعبة السويب</body></html>`))
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	srv.Config.Handler = mux
	srv.Start()
	t.Cleanup(srv.Close)
	return base
}

// storeRow is the game's row in the store as one player sees it.
func storeRow(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	var store struct {
		Games []map[string]any `json:"games"`
	}
	c.call("GET", "/v1/games", nil, 200, &store)
	for _, g := range store.Games {
		if g["id"] == id {
			return g
		}
	}
	t.Fatalf("%s is not in the store", id)
	return nil
}

// codeOf is the one-time code a launch put on the URL.
func codeOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("no code on %s", raw)
	}
	return code
}

// tinyPNG is the smallest tile there is: one pixel.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 179, B: 0, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func randomString(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// form posts form-encoded, as an OAuth library does.
func (c *client) form(path string, values url.Values, want int, out any) {
	c.t.Helper()
	req, err := http.NewRequest("POST", apiURL+path, strings.NewReader(values.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.finish(req, want, out)
}

// upload sends one file as the `file` part of a multipart body.
func (c *client) upload(method, path, name, contentType string, data []byte, want int, out any) {
	c.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename="%s"`, name)}
	h["Content-Type"] = []string{contentType}
	part, err := w.CreatePart(h)
	if err != nil {
		c.t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = w.Close()
	req, err := http.NewRequest(method, apiURL+path, &buf)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	c.finish(req, want, out)
}

// redirect follows nothing and answers the Location a 302 points at.
func (c *client) redirect(method, path string) string {
	c.t.Helper()
	res := c.raw(method, path)
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		raw, _ := io.ReadAll(res.Body)
		c.t.Fatalf("%s %s: got %d, want 302: %s", method, path, res.StatusCode, raw)
	}
	return res.Header.Get("Location")
}

func (c *client) redirectStatus(method, path string, want int) {
	c.t.Helper()
	res := c.raw(method, path)
	defer res.Body.Close()
	if res.StatusCode != want {
		raw, _ := io.ReadAll(res.Body)
		c.t.Fatalf("%s %s: got %d, want %d: %s", method, path, res.StatusCode, want, raw)
	}
}

func (c *client) raw(method, path string) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(method, apiURL+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	noFollow := &http.Client{Timeout: 15 * time.Second, Jar: c.http.Jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := noFollow.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

// finish sends a prepared request with the caller's credentials and decodes
// the answer the way call does.
func (c *client) finish(req *http.Request, want int, out any) {
	c.t.Helper()
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		c.t.Fatalf("%s %s: got %d, want %d: %s", req.Method, req.URL.Path, res.StatusCode, want, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: bad JSON: %v: %s", req.Method, req.URL.Path, err, raw)
		}
	}
	c.t.Logf("ok   %s %s → %d", req.Method, req.URL.Path, res.StatusCode)
}
