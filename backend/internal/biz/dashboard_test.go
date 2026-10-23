package biz

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// docs/CONSOLE.md, "The dashboard and the players": the open rooms by status,
// the rooms made today, the rounds started today, and one player's friends,
// rounds and open room.

// roomsFor is a database with two games to play, and the rooms' rules over it.
func roomsFor(t *testing.T) (*gorm.DB, *rooms.Service, context.Context) {
	t.Helper()
	db := biztest.DB(t)
	for id, max := range map[string]int{"fixture.xo": 2, "fixture.tap": 8} {
		m := model.Manifest{Name: id, Version: "1.0.0", Entry: "https://games.yourstudio.example/" + id + "/"}
		m.Publisher.Tier = enum.TierPartner
		m.Room.Players.Min, m.Room.Players.Max = 2, max
		m.Room.Host, m.Room.Pace = enum.HostPeer, enum.PaceTurn
		biztest.SeedGame(t, db, id, m)
	}
	return db, rooms.New(db, rooms.Config{
		Alphabet: "ABCDEFGHJKLMNPQRSTUVWXYZ23456789", MaxMembers: 64, LinkURL: "https://gathering.test",
		TicketTTL: time.Minute, AvatarTTL: time.Hour,
	}), context.Background()
}

// check is a call that must not fail: it fails the test on an error and
// answers the value.
func check[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestTheDashboardCountsRoomsAndRounds(t *testing.T) {
	since := time.Now()
	db, rules, ctx := roomsFor(t)
	room := check[*rooms.Room](t)
	zayn, sara := biztest.SeedPlayer(t, db, "", "Zayn"), biztest.SeedPlayer(t, db, "", "Sara")
	ali, omar := biztest.SeedPlayer(t, db, "", "Ali"), biztest.SeedPlayer(t, db, "", "Omar")
	huda := biztest.SeedPlayer(t, db, "", "Huda")

	lobby := room(rules.Create(ctx, zayn.ID, rooms.CreateRequest{Game: "fixture.xo"}))
	room(rules.Join(ctx, sara.ID, lobby.Code, ""))
	playing := room(rules.Create(ctx, ali.ID, rooms.CreateRequest{Game: "fixture.tap"}))
	room(rules.Join(ctx, omar.ID, playing.Code, ""))
	room(rules.Start(ctx, ali.ID, playing.ID))
	room(rules.Finish(ctx, ali.ID, playing.ID, nil))
	room(rules.Start(ctx, ali.ID, playing.ID))
	ended := room(rules.Create(ctx, huda.ID, rooms.CreateRequest{Game: "fixture.xo"}))
	room(rules.End(ctx, huda.ID, ended.ID, nil))

	// Yesterday's room and yesterday's start count for nothing today.
	if err := db.Exec(`UPDATE rooms SET created_at = ? WHERE id = ?`, since.Add(-48*time.Hour), ended.ID).Error; err != nil {
		t.Fatal(err)
	}
	err := db.Exec(`UPDATE logs.room_events SET at = ? WHERE id = (
		SELECT id FROM logs.room_events WHERE room_id = ? AND event = 'started' ORDER BY at LIMIT 1)`,
		since.Add(-48*time.Hour), playing.ID).Error
	if err != nil {
		t.Fatal(err)
	}

	o, err := NewDashboard(db, nil).Overview(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if o.RoomsLobby != 1 || o.RoomsPlaying != 1 || o.RoomsLive != 2 || o.RoomsToday != 2 || o.RoundsToday != 1 {
		t.Fatalf("overview = %+v", o)
	}
}

func TestAPlayersTally(t *testing.T) {
	db, rules, ctx := roomsFor(t)
	room, tallied := check[*rooms.Room](t), check[*Tally](t)
	zayn, sara := biztest.SeedPlayer(t, db, "", "Zayn"), biztest.SeedPlayer(t, db, "", "Sara")
	ali, omar := biztest.SeedPlayer(t, db, "", "Ali"), biztest.SeedPlayer(t, db, "", "Omar")

	befriend := func(a, b *model.Player, status enum.FriendshipStatus) {
		lo, hi := a.ID, b.ID
		if hi < lo {
			lo, hi = hi, lo
		}
		err := db.Exec(`INSERT INTO friendships (player_lo, player_hi, status, requested_by, created_at)
		                VALUES (?, ?, ?, ?, now())`, lo, hi, status, a.ID).Error
		if err != nil {
			t.Fatal(err)
		}
	}
	befriend(zayn, sara, enum.FriendshipAccepted)
	befriend(omar, zayn, enum.FriendshipAccepted)
	befriend(zayn, ali, enum.FriendshipRequested)

	r := room(rules.Create(ctx, zayn.ID, rooms.CreateRequest{Game: "fixture.xo"}))
	room(rules.Join(ctx, sara.ID, r.Code, ""))
	for range 2 {
		room(rules.Start(ctx, zayn.ID, r.ID))
		room(rules.Finish(ctx, zayn.ID, r.ID, []rooms.ScoreIn{{Player: zayn.ID, Score: 1}, {Player: sara.ID, Score: 2}}))
	}

	d := NewDashboard(db, nil)
	tally := tallied(d.Tally(ctx, zayn.ID))
	if tally.Friends != 2 || tally.Rounds != 2 || tally.Room == nil || tally.Room.ID != r.ID || tally.Room.Code != r.Code {
		t.Fatalf("Zayn = %+v", tally)
	}
	if tally := tallied(d.Tally(ctx, ali.ID)); tally.Friends != 0 || tally.Rounds != 0 || tally.Room != nil {
		t.Fatalf("Ali = %+v", tally)
	}

	// A room that has ended is nobody's open room.
	room(rules.End(ctx, zayn.ID, r.ID, nil))
	if tally := tallied(d.Tally(ctx, sara.ID)); tally.Room != nil || tally.Rounds != 2 || tally.Friends != 1 {
		t.Fatalf("Sara after the end = %+v", tally)
	}
}
