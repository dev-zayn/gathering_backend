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
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
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
		Rooms:     roomRules(db),
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

// A token minted into a room answers the room while its player is still in
// it: where it stands, the player's place, and who else is there — the
// nicknames with identity:read only. Once they leave, the room is null.
func TestContextNamesTheRoomWhileThePlayerIsInIt(t *testing.T) {
	b, _, player, db := testBridge(t)
	ctx := context.Background()
	friend := biztest.SeedPlayer(t, db, "+9647700000002", "Sara")
	rules := b.Rooms.(*rooms.Service)

	room, err := rules.Create(ctx, player.ID, rooms.CreateRequest{Game: xo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Join(ctx, friend.ID, strings.ToLower(room.Code), ""); err != nil {
		t.Fatal(err)
	}

	in := asking(player.ID, ScopeIdentityRead)
	in.RoomID = room.ID
	got, err := b.Who(ctx, in)
	if err != nil {
		t.Fatalf("Who: %v", err)
	}
	if got.Room == nil || got.Room.ID != room.ID || got.Room.Code != room.Code || got.Room.Status != "lobby" ||
		got.Room.Role != "host" || got.Room.Seat != 0 || len(got.Room.Members) != 2 {
		t.Fatalf("room = %+v", got.Room)
	}
	if m := got.Room.Members[1]; m.ID != friend.ID || m.Nickname != "Sara" || m.Role != "player" || m.Seat != 1 {
		t.Fatalf("second member = %+v", m)
	}

	// Without identity:read the members are ids and places.
	in.Scopes = []string{ScopeResultsWrite}
	got, err = b.Who(ctx, in)
	if err != nil || got.Room == nil || got.Room.Members[0].Nickname != "" {
		t.Fatalf("without the scope: %+v, %v", got.Room, err)
	}

	if err := rules.Leave(ctx, player.ID, room.ID); err != nil {
		t.Fatal(err)
	}
	got, err = b.Who(ctx, in)
	if err != nil || got.Room != nil {
		t.Fatalf("after leaving: %+v, %v", got.Room, err)
	}
}

// The game's server writes a room's results through the bridge, and is
// answered the room's results filtered like everything else the bridge says:
// names and pictures with identity:read only. Without results:write the
// bridge refuses and names the scope; another game's token is told the room
// is not its game's.
func TestTheBridgeWritesResults(t *testing.T) {
	b, _, player, db := testBridge(t)
	ctx := context.Background()
	friend := biztest.SeedPlayer(t, db, "+9647700000002", "Sara")
	rules := b.Rooms.(*rooms.Service)

	room, err := rules.Create(ctx, player.ID, rooms.CreateRequest{Game: xo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Join(ctx, friend.ID, room.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Start(ctx, player.ID, room.ID); err != nil {
		t.Fatal(err)
	}
	scores := []rooms.ScoreIn{{Player: player.ID, Score: 1}, {Player: friend.ID, Score: 4}}

	_, err = b.Results(ctx, asking(friend.ID, ScopeIdentityRead), room.ID, nil, scores)
	if e, ok := err.(*apperr.Error); !ok || e.Status != 403 || e.Code != "scope_required" || e.Scope != ScopeResultsWrite {
		t.Fatalf("without results:write: %v", err)
	}

	written, err := b.Results(ctx, asking(friend.ID, ScopeIdentityRead, ScopeResultsWrite), room.ID, nil, scores)
	if err != nil {
		t.Fatal(err)
	}
	got := written.Lines
	if written.Round != 1 || len(got) != 2 || got[0].Player != friend.ID || got[0].Rank != 1 || got[0].Nickname != "Sara" ||
		got[0].SubmittedBy != "game" || got[1].Player != player.ID || got[1].Rank != 2 ||
		got[1].Nickname != "Zayn" || got[1].Avatar != "a03" {
		t.Fatalf("with identity:read = %+v", written)
	}

	written, err = b.Results(ctx, asking(friend.ID, ScopeResultsWrite), room.ID, nil, scores)
	if err != nil || len(written.Lines) != 2 {
		t.Fatalf("without identity:read = %+v, %v", written, err)
	}
	got = written.Lines
	if got[0].Nickname != "" || got[1].Nickname != "" || got[1].Avatar != "" || got[0].Score != 4 || got[0].Rank != 1 {
		t.Fatalf("without identity:read = %+v", got)
	}

	other := asking(friend.ID, ScopeResultsWrite)
	other.GameID = "fixture.other"
	_, err = b.Results(ctx, other, room.ID, nil, scores)
	if e, ok := err.(*apperr.Error); !ok || e.Code != "invalid_fields" || e.Fields["room"] != "another_game" {
		t.Fatalf("another game's token: %v", err)
	}

	// A round named: the one just finished, while the next plays; a round the
	// room has not reached is unknown.
	if _, err := rules.Finish(ctx, player.ID, room.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Start(ctx, player.ID, room.ID); err != nil {
		t.Fatal(err)
	}
	one, three := 1, 3
	written, err = b.Results(ctx, asking(friend.ID, ScopeResultsWrite), room.ID, &one, scores[:1])
	if err != nil || written.Round != 1 || len(written.Lines) != 1 {
		t.Fatalf("round 1 named = %+v, %v", written, err)
	}
	written, err = b.Results(ctx, asking(friend.ID, ScopeResultsWrite), room.ID, nil, scores)
	if err != nil || written.Round != 2 || len(written.Lines) != 2 {
		t.Fatalf("the round playing = %+v, %v", written, err)
	}
	_, err = b.Results(ctx, asking(friend.ID, ScopeResultsWrite), room.ID, &three, scores)
	if e, ok := err.(*apperr.Error); !ok || e.Code != "invalid_fields" || e.Fields["round"] != "unknown" {
		t.Fatalf("round 3: %v", err)
	}
}

// The context's room says which round it is on: 0 before the first, the
// round playing, the round just finished.
func TestContextNamesTheRound(t *testing.T) {
	b, _, player, db := testBridge(t)
	ctx := context.Background()
	friend := biztest.SeedPlayer(t, db, "+9647700000002", "Sara")
	rules := b.Rooms.(*rooms.Service)
	room, err := rules.Create(ctx, player.ID, rooms.CreateRequest{Game: xo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Join(ctx, friend.ID, room.Code, ""); err != nil {
		t.Fatal(err)
	}
	in := asking(player.ID)
	in.RoomID = room.ID
	roundOf := func() (string, int) {
		t.Helper()
		got, err := b.Who(ctx, in)
		if err != nil || got.Room == nil {
			t.Fatalf("Who: %+v, %v", got, err)
		}
		return got.Room.Status, got.Room.Round
	}

	if status, round := roundOf(); status != "lobby" || round != 0 {
		t.Fatalf("a fresh lobby: %s %d", status, round)
	}
	if _, err := rules.Start(ctx, player.ID, room.ID); err != nil {
		t.Fatal(err)
	}
	if status, round := roundOf(); status != "playing" || round != 1 {
		t.Fatalf("round 1: %s %d", status, round)
	}
	if _, err := rules.Finish(ctx, player.ID, room.ID, nil); err != nil {
		t.Fatal(err)
	}
	if status, round := roundOf(); status != "lobby" || round != 1 {
		t.Fatalf("after round 1: %s %d", status, round)
	}
}
