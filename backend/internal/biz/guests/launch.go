package guests

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// Launch turns a player's session into a credential a guest may hold. The
// frame only ever holds a sixty-second code, and the grant is checked at
// mint and again at the exchange.
type Launch struct {
	Codes     *identity.CodeStore
	Tokens    *identity.GameTokenStore
	Players   *identity.PlayerStore
	Games     *games.Catalogue
	Minter    identity.Minter
	Whitelist identity.Whitelist

	CodeTTL  time.Duration
	TokenTTL time.Duration
}

// Opened is the answer to the tap: where to load, and how the frame should
// open. The auth flow does not know the difference between a portrait game
// and a landscape one; the app does.
type Opened struct {
	URL         string
	Mode        string
	Splash      bool
	Orientation string
	ExpiresIn   int
}

// Launcher is who is asking, from the session token. Nothing here comes from
// the request body — who is tapping is in the token.
type Launcher struct {
	PlayerID string
	Live     bool
}

// MaxRoomID is a room id's width: room_ and sixteen characters.
const MaxRoomID = 24

// Open is the tap inside the app. The first tap on a game puts it on the
// shelf; then a code is minted and the URL to load handed back, the tokens
// minted on the far side, backend to backend. roomID is the room the game
// opens into, when it does, and rides on the code into the token; path is
// where inside the game the player is going — a notification's deep link —
// and rides beside the code.
func (l *Launch) Open(ctx context.Context, by Launcher, gameID, roomID, path string) (*Opened, error) {
	bad := map[string]string{}
	if !DeepLink(path) {
		bad["path"] = "must be a path inside the game, like /room"
	}
	// That the room is the player's, and this game's, is the rooms' rule and
	// arrives with them; until then a room is a name the code carries.
	if len(roomID) > MaxRoomID {
		bad["room"] = "too_long"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	if _, err := l.Games.Open(ctx, by.PlayerID, gameID); err != nil {
		return nil, err
	}
	grant, err := l.eligible(ctx, by.PlayerID, gameID)
	if err != nil {
		return nil, err
	}

	code, err := l.mintCode(ctx, by, grant, roomID, "", "")
	if err != nil {
		return nil, err
	}

	entry, err := url.Parse(grant.Game.Entry)
	if err != nil || entry.Host == "" {
		return nil, errors.Internal(fmt.Errorf("games: %s has no usable entry", gameID))
	}
	q := entry.Query()
	q.Set("code", code)
	if path != "" {
		q.Set("path", path)
	}
	entry.RawQuery = q.Encode()

	return &Opened{
		URL:         entry.String(),
		Mode:        string(model.ModeOf(grant.Game)),
		Splash:      grant.Game.Splash,
		Orientation: string(grant.Game.Orientation),
		ExpiresIn:   int(l.CodeTTL.Seconds()),
	}, nil
}

// DeepLink is the rule for a path handed to a game on open: empty, or a path
// on the game's own origin. "//host" would be an address, and a scheme is one
// too; both would let a notification send a player somewhere else.
func DeepLink(path string) bool {
	if path == "" {
		return true
	}
	if len(path) > 512 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return false
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Authorized is where /oauth/authorize ends up: a place to redirect to,
// whether it worked or not.
type Authorized struct {
	Redirect string
}

// Authorize is the other way in, from the studio's own site. No consent
// screen, and no install step either: signing in there counts as the first
// tap, the same as inside the app.
func (l *Launch) Authorize(ctx context.Context, by Launcher, in AuthorizeRequest) (*Authorized, error) {
	// A game that is not published is not found, and the redirect is checked
	// against the game before anything is sent to it; so the row is written
	// only once the game is known to be real and the player is signing in to it.
	if _, err := l.Games.Open(ctx, by.PlayerID, in.ClientID); err != nil {
		return nil, err
	}
	grant, err := l.Games.Grant(ctx, by.PlayerID, in.ClientID)
	if err != nil {
		return nil, err
	}

	// The redirect is checked before anything else can be sent to it. An open
	// redirect here would hand a player's code to whoever asked.
	if !grant.Game.RedirectAllowed(in.RedirectURI) {
		return nil, errors.Invalid(map[string]string{"redirect_uri": "not_registered"})
	}
	if in.ResponseType != "code" {
		return &Authorized{Redirect: back(in, "unsupported_response_type")}, nil
	}
	if in.Challenge == "" || in.ChallengeMethod != "S256" {
		return &Authorized{Redirect: back(in, "invalid_request")}, nil
	}

	// Everything after this failure is the game's business to explain: it
	// shows its own page.
	if err := l.check(grant); err != nil {
		return &Authorized{Redirect: back(in, "access_denied")}, nil
	}

	code, err := l.mintCode(ctx, by, grant, "", in.Challenge, in.RedirectURI)
	if err != nil {
		return nil, err
	}

	target, _ := url.Parse(in.RedirectURI)
	q := target.Query()
	q.Set("code", code)
	if in.State != "" {
		q.Set("state", in.State)
	}
	target.RawQuery = q.Encode()
	return &Authorized{Redirect: target.String()}, nil
}

// AuthorizeRequest is the standard set of query parameters, under their
// standard names, so a studio uses a library instead of reading our docs.
type AuthorizeRequest struct {
	ClientID        string
	RedirectURI     string
	ResponseType    string
	Challenge       string
	ChallengeMethod string
	State           string

	// Resource is RFC 8707: the address the client means to spend the token
	// at, which is what stops one meant for us being replayed elsewhere.
	Resource string
}

// Issued is what a game's backend receives. The refresh token is absent on a
// renewal: the one it used is still good.
type Issued struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scope        string
}

// Spend exchanges the code for the pair. It is the first place the game
// proves who it is, and the last place the code exists.
func (l *Launch) Spend(ctx context.Context, gameID, secret, rawCode, verifier, redirectURI string) (*Issued, error) {
	if _, err := l.Games.Authenticate(ctx, gameID, secret); err != nil {
		return nil, err
	}

	code, err := l.Codes.Spend(ctx, model.HashCredential(rawCode), time.Now())
	if err != nil {
		return nil, errors.Internal(err)
	}
	// Expired, already spent, or never real. One answer for all three: which
	// it was is not something a caller guessing should learn.
	if code == nil {
		return nil, errInvalidGrant
	}
	if code.GameID != gameID {
		return nil, errInvalidGrant
	}
	if !model.VerifyPKCE(code.Challenge, verifier) {
		return nil, errInvalidGrant
	}
	if code.RedirectURI != "" && code.RedirectURI != redirectURI {
		return nil, errInvalidGrant
	}

	// The gate is asked again here, not just at mint time: a player can
	// remove the game, or an admin suspend it, in the second between the
	// redirect and the exchange.
	grant, err := l.Games.Grant(ctx, code.PlayerID, gameID)
	if err != nil {
		return nil, err
	}
	if err := l.check(grant); err != nil {
		return nil, errInvalidGrant
	}

	refresh := ids.New(ids.Refresh)
	err = l.Tokens.Create(ctx, &model.GameToken{
		Hash:      model.HashCredential(refresh),
		GameID:    gameID,
		PlayerID:  code.PlayerID,
		Scopes:    code.Scopes,
		CreatedAt: time.Now(),
	})
	if err != nil {
		return nil, errors.Internal(err)
	}

	issued, err := l.mint(ctx, identity.GameClaims{
		GameID: gameID, PlayerID: code.PlayerID, RoomID: code.RoomID,
		Scopes: code.Scopes, Live: code.Live,
	})
	if err != nil {
		return nil, err
	}
	issued.RefreshToken = refresh
	return issued, nil
}

// Renew keeps a game's backend signed in while the game stays on the shelf.
// Nobody is present, so no elevated scope comes back, and no room either: a
// room is where the player was when they opened the game, not where they are
// an hour later.
func (l *Launch) Renew(ctx context.Context, gameID, secret, refresh string) (*Issued, error) {
	if _, err := l.Games.Authenticate(ctx, gameID, secret); err != nil {
		return nil, err
	}

	hash := model.HashCredential(refresh)
	tok, err := l.Tokens.Get(ctx, hash)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if tok == nil || tok.GameID != gameID {
		return nil, errInvalidGrant
	}

	// There is no revoked column because these are the check, asked every
	// time: the game still on the shelf and still published, the same gate as
	// a launch, and the player still somebody who may sign in.
	grant, err := l.Games.Grant(ctx, tok.PlayerID, gameID)
	if err != nil {
		return nil, err
	}
	if err := l.check(grant); err != nil {
		return nil, errInvalidGrant
	}

	player, err := l.Players.ByID(ctx, tok.PlayerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if player == nil || !player.CanSignIn() {
		// Gone or switched off. The token is spent for good, not just today:
		// being switched back on later is a new sign-in to the game.
		if err := l.Tokens.Delete(ctx, hash); err != nil {
			return nil, errors.Internal(err)
		}
		return nil, errInvalidGrant
	}

	if err := l.Tokens.Touch(ctx, hash, time.Now()); err != nil {
		return nil, errors.Internal(err)
	}

	// The scopes are read now rather than replayed: the manifest may have
	// moved, and nobody is present, so elevated is gone either way.
	return l.mint(ctx, identity.GameClaims{
		GameID: gameID, PlayerID: tok.PlayerID, Scopes: grant.Effective(false), Live: false,
	})
}

func (l *Launch) mint(ctx context.Context, in identity.GameClaims) (*Issued, error) {
	access, jti, err := l.Minter.MintGame(in, l.TokenTTL)
	if err != nil {
		return nil, err
	}

	// Listed so that removing the game reaches this token now rather than
	// when it expires. Removing is the revocation, and an hour is not
	// "immediately".
	if err := l.Whitelist.AllowGame(ctx, jti, in.GameID, in.PlayerID, l.TokenTTL); err != nil {
		return nil, errors.Internal(err)
	}

	return &Issued{
		AccessToken: access,
		ExpiresIn:   int(l.TokenTTL.Seconds()),
		Scope:       strings.Join(in.Scopes, " "),
	}, nil
}

func (l *Launch) mintCode(ctx context.Context, by Launcher, grant *model.Grant, roomID, challenge, redirect string) (string, error) {
	raw := ids.New(ids.Code)
	now := time.Now()

	err := l.Codes.Create(ctx, &model.Code{
		Hash:        model.HashCredential(raw),
		GameID:      grant.Game.ID,
		PlayerID:    by.PlayerID,
		RoomID:      roomID,
		Scopes:      grant.Effective(by.Live),
		Live:        by.Live,
		Challenge:   challenge,
		RedirectURI: redirect,
		CreatedAt:   now,
		ExpiresAt:   now.Add(l.CodeTTL),
	})
	if err != nil {
		return "", errors.Internal(err)
	}
	return raw, nil
}

// eligible is the in-app path's version of the checks, where the player sees
// the reason rather than being redirected somewhere that hides it.
func (l *Launch) eligible(ctx context.Context, playerID, gameID string) (*model.Grant, error) {
	grant, err := l.Games.Grant(ctx, playerID, gameID)
	if err != nil {
		return nil, err
	}
	if err := l.check(grant); err != nil {
		return nil, err
	}
	return grant, nil
}

// check is the single gate, and the one place it is asked: no credential
// without the game on the shelf. not_in_library is also the answer for a
// suspended game, whose row is there and counts for nothing.
func (l *Launch) check(grant *model.Grant) error {
	if !grant.InLibrary {
		return errors.NotFound("not_in_library")
	}
	return nil
}

// back sends the player to the game with the failure attached, so the game
// shows its own explanation rather than one of ours.
func back(in AuthorizeRequest, reason string) string {
	target, err := url.Parse(in.RedirectURI)
	if err != nil {
		return in.RedirectURI
	}
	q := target.Query()
	q.Set("error", reason)
	if in.State != "" {
		q.Set("state", in.State)
	}
	target.RawQuery = q.Encode()
	return target.String()
}

// errInvalidGrant is the one answer for a code that was expired, spent, faked
// or belongs to somebody else. OAuth names it, and it is deliberately
// uninformative.
var errInvalidGrant = errors.BadRequest("invalid_grant")
