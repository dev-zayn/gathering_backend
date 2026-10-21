package guests

import (
	"context"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// A refresh token is asked the same questions as a launch every time it is
// used: still on the shelf, still published, and the player still somebody
// who may sign in. A switched-off player's token is spent for good.
func TestRenewAsksTheLaunchQuestionsEveryTime(t *testing.T) {
	l, minter, _, player, db := testLaunch(t)
	ctx := context.Background()
	biztest.SeedLibrary(t, db, player.ID, xo)
	other := biztest.SeedPlayer(t, db, "+9647700000002", "Mariam")
	biztest.SeedLibrary(t, db, other.ID, xo)

	for p, refresh := range map[string]string{player.ID: "rft_zayn", other.ID: "rft_mariam"} {
		err := l.Tokens.Create(ctx, &model.GameToken{
			Hash: model.HashCredential(refresh), GameID: xo, PlayerID: p,
			Scopes: []string{"identity:read"}, CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	issued, err := l.Renew(ctx, xo, biztest.SecretFor(xo), "rft_zayn")
	if err != nil {
		t.Fatalf("a current player: %v", err)
	}
	// A renewal keeps the refresh token it used, and nobody is present.
	if issued.RefreshToken != "" || minter.minted()[0].Live {
		t.Fatalf("issued = %+v, live = %v", issued, minter.minted()[0].Live)
	}
	// The scopes are the manifest's as it stands, not the token's.
	if issued.Scope != "identity:read results:write" {
		t.Fatalf("scope = %q", issued.Scope)
	}
	if tok, _ := l.Tokens.Get(ctx, model.HashCredential("rft_zayn")); tok == nil || tok.UsedAt == nil {
		t.Fatal("the renewal was not stamped")
	}

	// Another game's secret cannot renew it, and a token nobody issued is nothing.
	if _, err := l.Renew(ctx, xo, "sec_wrong", "rft_zayn"); codeOf(err) != "invalid_client" {
		t.Fatalf("a wrong secret gave %v", err)
	}
	if _, err := l.Renew(ctx, xo, biztest.SecretFor(xo), "rft_nobody"); codeOf(err) != "invalid_grant" {
		t.Fatalf("an unknown token gave %v", err)
	}

	// Switched off: the token goes with them.
	if err := db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, other.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := l.Renew(ctx, xo, biztest.SecretFor(xo), "rft_mariam"); codeOf(err) != "invalid_grant" {
		t.Fatalf("a disabled player: %v", err)
	}
	if tok, _ := l.Tokens.Get(ctx, model.HashCredential("rft_mariam")); tok != nil {
		t.Fatal("the disabled player's refresh token is still there")
	}

	// Removed from the shelf: the row is gone, so nothing renews.
	if err := l.Games.Remove(ctx, player.ID, xo); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Renew(ctx, xo, biztest.SecretFor(xo), "rft_zayn"); codeOf(err) != "invalid_grant" {
		t.Fatalf("a removed game: %v", err)
	}

	// Suspended: the row may be back, the answer is the same.
	biztest.SeedLibrary(t, db, player.ID, xo)
	if err := l.Tokens.Create(ctx, &model.GameToken{
		Hash: model.HashCredential("rft_again"), GameID: xo, PlayerID: player.ID, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE games SET listing = ? WHERE id = ?`, enum.Suspended, xo).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := l.Renew(ctx, xo, biztest.SecretFor(xo), "rft_again"); codeOf(err) != "invalid_grant" {
		t.Fatalf("a suspended game: %v", err)
	}
	if len(minter.minted()) != 1 {
		t.Fatalf("minted %d tokens, want only the first", len(minter.minted()))
	}
}
