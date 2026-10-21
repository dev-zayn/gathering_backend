package guests

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

func testBridge(t *testing.T) (*Bridge, *biztest.Files, *model.Player, *gorm.DB) {
	t.Helper()

	db := biztest.DB(t)
	player := biztest.SeedPlayer(t, db, "+9647700000001", "Zayn")
	if err := db.Exec(`UPDATE players SET avatar_key = 'a03' WHERE id = ?`, player.ID).Error; err != nil {
		t.Fatal(err)
	}
	biztest.SeedGame(t, db, xo, xoManifest())
	biztest.SeedLibrary(t, db, player.ID, xo)

	store := biztest.NewFiles()
	return &Bridge{
		Players:   identity.NewPlayerStore(db),
		Games:     games.NewCatalogue(db, nil, nil),
		Files:     store,
		AvatarTTL: time.Hour,
	}, store, player, db
}

func asking(playerID string, scopes ...string) Caller {
	return Caller{PlayerID: playerID, GameID: xo, Scopes: scopes, Live: true}
}

func TestContextWithIdentityRead(t *testing.T) {
	b, _, player, _ := testBridge(t)

	got, err := b.Who(context.Background(), asking(player.ID, ScopeIdentityRead))
	if err != nil {
		t.Fatalf("Who: %v", err)
	}
	if got.Player.ID != player.ID || got.Player.Nickname != "Zayn" || got.Player.Handle != player.Handle {
		t.Fatalf("player = %+v", got.Player)
	}
	if got.Player.Avatar != "a03" || got.Player.AvatarURL != "" {
		t.Fatalf("a bundled avatar is its id: %+v", got.Player)
	}
	if got.Room != nil || !got.Live || strings.Join(got.Scopes, " ") != ScopeIdentityRead {
		t.Fatalf("context = %+v", got)
	}
}

// Without identity:read the player is an id and nothing else. This is the
// filtering no game is trusted to do for itself.
func TestContextWithoutIdentityRead(t *testing.T) {
	b, _, player, _ := testBridge(t)

	got, err := b.Who(context.Background(), asking(player.ID, ScopeResultsWrite))
	if err != nil {
		t.Fatalf("Who: %v", err)
	}
	if got.Player.ID != player.ID {
		t.Fatalf("the id should always be there, got %q", got.Player.ID)
	}
	if got.Player.Nickname != "" || got.Player.Handle != "" || got.Player.Avatar != "" {
		t.Fatalf("the person was named without the scope: %+v", got.Player)
	}
}

// An uploaded picture is a key in the bucket, linked on the way out and never
// handed over as a key.
func TestContextSignsAnUploadedAvatar(t *testing.T) {
	b, store, player, db := testBridge(t)
	if err := db.Exec(`UPDATE players SET avatar_key = ? WHERE id = ?`, "avatars/"+player.ID+".png", player.ID).Error; err != nil {
		t.Fatal(err)
	}

	got, err := b.Who(context.Background(), asking(player.ID, ScopeIdentityRead))
	if err != nil {
		t.Fatalf("Who: %v", err)
	}
	if got.Player.AvatarURL != "https://signed.test/avatars/"+player.ID+".png" || got.Player.Avatar != "" {
		t.Fatalf("avatar = %+v", got.Player)
	}
	if store.Signed["avatars/"+player.ID+".png"] != time.Hour {
		t.Fatalf("the link lives %v", store.Signed)
	}
}

// The phone is the sign-in credential and never crosses the bridge under any
// scope. Context has nowhere to put it, and this is what would notice a
// field being added.
func TestContextNeverCarriesThePhone(t *testing.T) {
	b, _, player, _ := testBridge(t)

	got, err := b.Who(context.Background(), asking(player.ID, ScopeIdentityRead, ScopeFriendsRead))
	if err != nil {
		t.Fatalf("Who: %v", err)
	}
	for _, field := range []string{got.Player.ID, got.Player.Nickname, got.Player.Handle, got.Player.Avatar, got.Player.AvatarURL} {
		if strings.Contains(field, "+964") || strings.Contains(field, "7700000001") {
			t.Fatalf("a phone number reached the bridge in %q", field)
		}
	}
}

// A token that is signed and whitelisted, for a game the player has since
// removed, or for a player an admin has switched off. There is nothing to
// describe and nothing to serve.
func TestContextRefusesWhatIsGone(t *testing.T) {
	b, _, player, db := testBridge(t)
	ctx := context.Background()

	if err := db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, player.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := b.Who(ctx, asking(player.ID, ScopeIdentityRead)); codeOf(err) != "invalid_token" {
		t.Fatalf("a disabled player gave %v", err)
	}
	if err := db.Exec(`UPDATE players SET disabled = false WHERE id = ?`, player.ID).Error; err != nil {
		t.Fatal(err)
	}

	if err := b.Games.Remove(ctx, player.ID, xo); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Who(ctx, asking(player.ID)); codeOf(err) != "invalid_token" {
		t.Fatalf("a removed game gave %v", err)
	}
}
