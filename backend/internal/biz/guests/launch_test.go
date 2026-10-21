package guests

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
)

const xo = "fixture.xo"

// xoManifest is the game as its studio publishes it.
func xoManifest() model.Manifest {
	m := model.Manifest{
		Name: "إكس أو", Version: "1.0.0", Mode: enum.ModeEmbedded,
		Entry:  "https://games.yourstudio.example/xo/",
		Scopes: []string{"identity:read", "results:write"},
	}
	m.Publisher.Tier = enum.TierPartner
	m.Room.Players.Min, m.Room.Players.Max = 2, 2
	m.Room.Host, m.Room.Pace = enum.HostAuthoritative, enum.PaceTurn
	return m
}

// testLaunch is the launch over a real database with one player and one
// published game, the token minted by a fake that remembers its claims.
func testLaunch(t *testing.T) (*Launch, *fakeMinter, *biztest.Whitelist, *model.Player, *gorm.DB) {
	t.Helper()

	db := biztest.DB(t)
	player := biztest.SeedPlayer(t, db, "+9647700000001", "Zayn")
	biztest.SeedGame(t, db, xo, xoManifest())

	minter := &fakeMinter{}
	wl := &biztest.Whitelist{}
	tokens := identity.NewGameTokenStore(db)
	revoker := &games.Revoker{Tokens: tokens, Whitelist: wl}
	l := &Launch{
		Codes:     identity.NewCodeStore(db),
		Tokens:    tokens,
		Players:   identity.NewPlayerStore(db),
		Games:     games.NewCatalogue(db, revoker, &biztest.Audit{}),
		Minter:    minter,
		Whitelist: wl,
		CodeTTL:   time.Minute,
		TokenTTL:  time.Hour,
	}
	return l, minter, wl, player, db
}

// codeIn is the one-time code a launch put on the URL.
func codeIn(t *testing.T, raw string) string {
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

func codeOf(err error) string {
	var e *apperr.Error
	if as(err, &e) {
		return e.Code
	}
	return ""
}

func as(err error, target **apperr.Error) bool {
	e, ok := err.(*apperr.Error)
	if ok {
		*target = e
	}
	return ok
}

// A deep link is a path on the game's own origin and nothing that could be
// read as somewhere else.
func TestADeepLinkStaysInsideTheGame(t *testing.T) {
	for _, ok := range []string{"", "/", "/room", "/room?code=ABC123", "/a/b#c"} {
		if !DeepLink(ok) {
			t.Errorf("%q was refused", ok)
		}
	}
	for _, bad := range []string{"room", "//evil.example/x", "https://evil.example", "/a\nb", "/\x00"} {
		if DeepLink(bad) {
			t.Errorf("%q was allowed", bad)
		}
	}
}

// The tap: the game lands on the shelf, the frame gets a URL with a code on
// it, and the game's server trades the code for the pair — once.
func TestATapMintsACodeTheGamesServerSpends(t *testing.T) {
	l, minter, wl, player, db := testLaunch(t)
	ctx := context.Background()
	by := Launcher{PlayerID: player.ID, Live: true}

	opened, err := l.Open(ctx, by, xo, "room_7f3k9d2m8q1x4b6n", "/board")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !strings.HasPrefix(opened.URL, "https://games.yourstudio.example/xo/?") || !strings.Contains(opened.URL, "path=%2Fboard") {
		t.Fatalf("url = %s", opened.URL)
	}
	if opened.Mode != "embedded" || opened.Orientation != "portrait" || opened.ExpiresIn != 60 {
		t.Fatalf("opened = %+v", opened)
	}
	grant, _ := l.Games.Grant(ctx, player.ID, xo)
	if !grant.InLibrary {
		t.Fatal("the first open did not put the game on the shelf")
	}
	code := codeIn(t, opened.URL)

	// The server proves who it is first.
	if _, err := l.Spend(ctx, xo, "sec_wrong", code, "", ""); codeOf(err) != "invalid_client" {
		t.Fatalf("a wrong secret gave %v", err)
	}
	// And a code is one game's: another game's server cannot spend it.
	if _, err := l.Spend(ctx, "fixture.tap", biztest.SecretFor(xo), code, "", ""); codeOf(err) != "invalid_client" {
		t.Fatalf("another client gave %v", err)
	}

	issued, err := l.Spend(ctx, xo, biztest.SecretFor(xo), code, "", "")
	if err != nil {
		t.Fatalf("spend: %v", err)
	}
	if issued.AccessToken != "game-token" || !strings.HasPrefix(issued.RefreshToken, "rft_") || issued.ExpiresIn != 3600 {
		t.Fatalf("issued = %+v", issued)
	}
	if issued.Scope != "identity:read results:write" {
		t.Fatalf("scope = %q", issued.Scope)
	}
	claims := minter.minted()
	if len(claims) != 1 || claims[0].GameID != xo || claims[0].PlayerID != player.ID || claims[0].RoomID != "room_7f3k9d2m8q1x4b6n" || !claims[0].Live {
		t.Fatalf("minted %+v", claims)
	}
	if len(wl.AllowedGames) != 1 {
		t.Fatalf("whitelisted %v", wl.AllowedGames)
	}
	if tok, _ := l.Tokens.Get(ctx, model.HashCredential(issued.RefreshToken)); tok == nil || tok.PlayerID != player.ID {
		t.Fatal("the refresh token was not kept")
	}

	// The code is gone the moment it is spent, and a verifier where none was
	// set is a caller inventing one.
	if _, err := l.Spend(ctx, xo, biztest.SecretFor(xo), code, "", ""); codeOf(err) != "invalid_grant" {
		t.Fatalf("spending twice gave %v", err)
	}
	again, _ := l.Open(ctx, by, xo, "", "")
	if _, err := l.Spend(ctx, xo, biztest.SecretFor(xo), codeIn(t, again.URL), "made-up", ""); codeOf(err) != "invalid_grant" {
		t.Fatalf("an invented verifier gave %v", err)
	}

	// Opening again is a play, not a second shelf.
	row, _ := identity.NewPlayerStore(db).ByID(ctx, player.ID)
	if row == nil {
		t.Fatal("the player is gone")
	}
	var plays int
	if err := db.Raw(`SELECT plays FROM library WHERE player_id = ? AND game_id = ?`, player.ID, xo).Scan(&plays).Error; err != nil {
		t.Fatal(err)
	}
	if plays != 2 {
		t.Fatalf("plays = %d", plays)
	}
}

// A code outlives the tap by a minute, and the gate is asked again when it
// is spent: a game removed or suspended in between gets no token.
func TestTheGateIsAskedAgainAtTheExchange(t *testing.T) {
	l, _, _, player, db := testLaunch(t)
	ctx := context.Background()
	by := Launcher{PlayerID: player.ID, Live: true}

	opened, _ := l.Open(ctx, by, xo, "", "")
	if err := l.Games.Remove(ctx, player.ID, xo); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Spend(ctx, xo, biztest.SecretFor(xo), codeIn(t, opened.URL), "", ""); codeOf(err) != "invalid_grant" {
		t.Fatalf("a removed game's code gave %v", err)
	}

	opened, _ = l.Open(ctx, by, xo, "", "")
	if err := db.Exec(`UPDATE games SET listing = ? WHERE id = ?`, enum.Suspended, xo).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := l.Spend(ctx, xo, biztest.SecretFor(xo), codeIn(t, opened.URL), "", ""); codeOf(err) != "invalid_grant" {
		t.Fatalf("a suspended game's code gave %v", err)
	}

	// And a suspended game cannot be opened at all: not found, like one that
	// never existed.
	if _, err := l.Open(ctx, by, xo, "", ""); codeOf(err) != "not_found" {
		t.Fatalf("opening a suspended game gave %v", err)
	}
}

// What rides on the URL is checked before anything is written.
func TestOpenRefusesABadPathOrRoom(t *testing.T) {
	l, _, _, player, _ := testLaunch(t)
	ctx := context.Background()
	by := Launcher{PlayerID: player.ID, Live: true}

	_, err := l.Open(ctx, by, xo, strings.Repeat("r", MaxRoomID+1), "//evil.example")
	var e *apperr.Error
	if !as(err, &e) || e.Code != "invalid_fields" || e.Fields["path"] == "" || e.Fields["room"] == "" {
		t.Fatalf("got %v", err)
	}
	grant, _ := l.Games.Grant(ctx, player.ID, xo)
	if grant.InLibrary {
		t.Fatal("a refused open still put the game on the shelf")
	}
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// The other way in, from the studio's own site: PKCE, and the redirect
// checked before a code is sent anywhere.
func TestAuthorizeIsPKCEAndARedirectWeKnow(t *testing.T) {
	l, minter, _, player, _ := testLaunch(t)
	ctx := context.Background()
	by := Launcher{PlayerID: player.ID, Live: true}
	verifier := strings.Repeat("v", 48)
	ask := AuthorizeRequest{
		ClientID: xo, RedirectURI: "https://games.yourstudio.example/callback",
		ResponseType: "code", Challenge: challengeFor(verifier), ChallengeMethod: "S256", State: "xyz",
	}

	// Somebody else's address gets no redirect at all.
	elsewhere := ask
	elsewhere.RedirectURI = "https://evil.example/callback"
	if _, err := l.Authorize(ctx, by, elsewhere); codeOf(err) != "invalid_fields" {
		t.Fatalf("a foreign redirect gave %v", err)
	}

	// A bad request goes back to the game with the reason, not to a page of ours.
	plain := ask
	plain.ChallengeMethod = "plain"
	res, err := l.Authorize(ctx, by, plain)
	if err != nil || !strings.Contains(res.Redirect, "error=invalid_request") || !strings.Contains(res.Redirect, "state=xyz") {
		t.Fatalf("plain PKCE gave %v %+v", err, res)
	}
	token := ask
	token.ResponseType = "token"
	res, _ = l.Authorize(ctx, by, token)
	if !strings.Contains(res.Redirect, "error=unsupported_response_type") {
		t.Fatalf("response_type=token gave %+v", res)
	}

	res, err = l.Authorize(ctx, by, ask)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if !strings.HasPrefix(res.Redirect, "https://games.yourstudio.example/callback?") || !strings.Contains(res.Redirect, "state=xyz") {
		t.Fatalf("redirect = %s", res.Redirect)
	}
	code := codeIn(t, res.Redirect)

	// The exchange needs the verifier and the same redirect, exactly.
	if _, err := l.Spend(ctx, xo, biztest.SecretFor(xo), code, "wrong", ask.RedirectURI); codeOf(err) != "invalid_grant" {
		t.Fatalf("a wrong verifier gave %v", err)
	}
	res, _ = l.Authorize(ctx, by, ask)
	code = codeIn(t, res.Redirect)
	if _, err := l.Spend(ctx, xo, biztest.SecretFor(xo), code, verifier, "https://games.yourstudio.example/other"); codeOf(err) != "invalid_grant" {
		t.Fatalf("another redirect gave %v", err)
	}
	res, _ = l.Authorize(ctx, by, ask)
	code = codeIn(t, res.Redirect)
	issued, err := l.Spend(ctx, xo, biztest.SecretFor(xo), code, verifier, ask.RedirectURI)
	if err != nil {
		t.Fatalf("spend: %v", err)
	}
	if issued.RefreshToken == "" || len(minter.minted()) != 1 {
		t.Fatalf("issued = %+v, minted %d", issued, len(minter.minted()))
	}
}
