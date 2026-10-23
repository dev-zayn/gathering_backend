package rooms

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

const (
	xo  = "fixture.xo"  // two seats, the host referees, a rejoin allowed, a Wi-Fi relay allowed
	tap = "fixture.tap" // up to eight, peers, no shared state, no rejoin, no Wi-Fi relay
)

const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func manifest(name string, min, max int, host enum.HostMode, state enum.StateMode, rejoin, lan bool) model.Manifest {
	m := model.Manifest{Name: name, Version: "1.0.0", Entry: "https://games.yourstudio.example/" + name + "/"}
	m.Publisher.Tier = enum.TierPartner
	m.Room.Players.Min, m.Room.Players.Max = min, max
	m.Room.Host, m.Room.Pace, m.Room.State = host, enum.PaceTurn, state
	m.Room.Rejoin, m.Room.LAN = rejoin, lan
	return m
}

// fixture is the rules over a real database, with two games, a publisher and
// presence that remember, and a real signer for tickets.
type fixture struct {
	t        *testing.T
	s        *Service
	pub      *fakePublisher
	presence *fakePresence
	db       *gorm.DB
	signer   *token.Signer
	ctx      context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := biztest.DB(t)
	biztest.SeedGame(t, db, xo, manifest("إكس أو", 2, 2, enum.HostAuthoritative, enum.StateBlob, true, true))
	biztest.SeedGame(t, db, tap, manifest("سباق النقر", 2, 8, enum.HostPeer, enum.StateNone, false, false))

	signer := biztest.Signer(t)
	s := New(db, Config{
		Alphabet: alphabet, MaxMembers: 64, LobbyIdle: 10 * time.Minute, MaxPlay: 2 * time.Hour,
		TicketTTL: 10 * time.Minute, LinkURL: "https://gathering.test/", AvatarTTL: time.Hour,
	})
	f := &fixture{t: t, s: s, pub: newPublisher(), presence: &fakePresence{}, db: db, signer: signer, ctx: context.Background()}
	s.Publisher, s.Presence, s.Minter, s.Files = f.pub, f.presence, identity.NewMinter(signer), biztest.NewFiles()
	return f
}

func (f *fixture) player(nickname string) *model.Player {
	f.t.Helper()
	return biztest.SeedPlayer(f.t, f.db, "", nickname)
}

// room makes a room for game with host in it, and forgets the frames.
func (f *fixture) room(host *model.Player, game string) *Room {
	f.t.Helper()
	r, err := f.s.Create(f.ctx, host.ID, CreateRequest{Game: game})
	if err != nil {
		f.t.Fatalf("create: %v", err)
	}
	f.pub.published(r.ID)
	return r
}

// join puts p in the room, and forgets the frames.
func (f *fixture) join(p *model.Player, r *Room) *Room {
	f.t.Helper()
	got, err := f.s.Join(f.ctx, p.ID, r.Code, "")
	if err != nil {
		f.t.Fatalf("join: %v", err)
	}
	f.pub.published(r.ID)
	return got
}

// events is the room's history in logs.room_events, oldest first.
func (f *fixture) events(roomID string) []string {
	f.t.Helper()
	var out []string
	if err := f.db.Raw(`SELECT event FROM logs.room_events WHERE room_id = ? ORDER BY at, id`, roomID).Scan(&out).Error; err != nil {
		f.t.Fatal(err)
	}
	return out
}

func refusal(err error) *apperr.Error {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

func codeOf(err error) string {
	if e := refusal(err); e != nil {
		return e.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func wantCode(t *testing.T, what string, err error, status int, code string) {
	t.Helper()
	e := refusal(err)
	if e == nil || e.Status != status || e.Code != code {
		t.Fatalf("%s: got %v, want %d %s", what, err, status, code)
	}
}

func wantField(t *testing.T, what string, err error, field, problem string) {
	t.Helper()
	e := refusal(err)
	if e == nil || e.Code != "invalid_fields" || e.Fields[field] != problem {
		t.Fatalf("%s: got %+v, want %s: %s", what, e, field, problem)
	}
}

// Making a room copies the game's room block, puts the host in seat 0, and
// answers the room as docs/ROOMS.md shows it.
func TestARoomIsMadeForAPublishedGame(t *testing.T) {
	f := newFixture(t)
	host := f.player("Zayn")

	r, err := f.s.Create(f.ctx, host.ID, CreateRequest{Game: xo, LAN: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.ID, "room_") || len(r.Code) != 6 || !f.s.validCode(r.Code) {
		t.Fatalf("id %q code %q", r.ID, r.Code)
	}
	if r.Status != "lobby" || r.Visibility != "private" || !r.LAN || r.Host != host.ID {
		t.Fatalf("room = %+v", r)
	}
	want := RoomConfig{Min: 2, Max: 2, Host: "authoritative", Pace: "turn", State: "blob", Rejoin: true, LAN: true}
	if r.Config != want {
		t.Fatalf("config = %+v, want %+v", r.Config, want)
	}
	if r.Game.ID != xo || r.Game.Name != "إكس أو" || r.Game.Orientation != "portrait" ||
		r.Game.Icon != "https://signed.test/games/"+xo+"/icon.png" {
		t.Fatalf("game = %+v", r.Game)
	}
	if len(r.Members) != 1 || r.Members[0].ID != host.ID || r.Members[0].Role != "host" || r.Members[0].Seat != 0 ||
		r.Members[0].Nickname != "Zayn" || r.Members[0].Connected {
		t.Fatalf("members = %+v", r.Members)
	}
	if r.You == nil || r.You.Role != "host" || r.You.Seat != 0 {
		t.Fatalf("you = %+v", r.You)
	}
	if r.Link != "https://gathering.test/r/"+r.Code || r.StateVersion != 0 || r.EndReason != nil ||
		len(r.Results) != 0 || r.Results == nil || r.StartedAt != nil || r.EndedAt != nil || r.CreatedAt == "" {
		t.Fatalf("room = %+v", r)
	}

	mine, err := f.s.Mine(f.ctx, host.ID)
	if err != nil || len(mine) != 1 || mine[0].ID != r.ID {
		t.Fatalf("mine = %+v, %v", mine, err)
	}
	if got := f.events(r.ID); strings.Join(got, ",") != "created" {
		t.Fatalf("events = %v", got)
	}
}

func TestMakingARoomIsRefused(t *testing.T) {
	f := newFixture(t)
	host := f.player("Zayn")

	_, err := f.s.Create(f.ctx, host.ID, CreateRequest{Game: "fixture.nothing"})
	wantCode(t, "an unknown game", err, 404, "not_found")
	if err := f.db.Exec(`UPDATE games SET listing = 'suspended' WHERE id = ?`, tap).Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Create(f.ctx, host.ID, CreateRequest{Game: tap})
	wantCode(t, "a suspended game", err, 404, "not_found")
	if err := f.db.Exec(`UPDATE games SET listing = 'published' WHERE id = ?`, tap).Error; err != nil {
		t.Fatal(err)
	}

	_, err = f.s.Create(f.ctx, host.ID, CreateRequest{Game: tap, LAN: true})
	wantField(t, "a Wi-Fi relay the game does not allow", err, "lan", "not_allowed")
	_, err = f.s.Create(f.ctx, host.ID, CreateRequest{Game: xo, Visibility: "public"})
	wantField(t, "a visibility there is not", err, "visibility", "unknown")

	// One open room per player, and the refusal names it.
	first := f.room(host, xo)
	_, err = f.s.Create(f.ctx, host.ID, CreateRequest{Game: tap})
	wantCode(t, "a second room", err, 409, "in_another_room")
	if refusal(err).Room != first.ID {
		t.Fatalf("the refusal names %q, not %q", refusal(err).Room, first.ID)
	}
}

// A room never holds more than the relay carries, whatever the game says.
func TestARoomIsCappedAtTheRelaysMembers(t *testing.T) {
	f := newFixture(t)
	f.s.cfg.MaxMembers = 4
	r := f.room(f.player("Zayn"), tap)
	if r.Config.Max != 4 || r.Config.Min != 2 {
		t.Fatalf("config = %+v", r.Config)
	}
}

// The code is read the way a friend reads it out: any case, spaces and
// dashes. The joiner takes the lowest free seat, and the room hears it.
func TestJoiningByCode(t *testing.T) {
	f := newFixture(t)
	host, friend := f.player("Zayn"), f.player("Sara")
	r := f.room(host, tap)

	typed := strings.ToLower(r.Code[:3]) + "- " + strings.ToLower(r.Code[3:])
	got, err := f.s.Join(f.ctx, friend.ID, typed, "")
	if err != nil {
		t.Fatalf("join %q: %v", typed, err)
	}
	if got.ID != r.ID || got.You == nil || got.You.Role != "player" || got.You.Seat != 1 || len(got.Members) != 2 {
		t.Fatalf("joined = %+v", got)
	}
	frames := f.pub.published(r.ID)
	if strings.Join(kinds(frames), ",") != "joined,members" {
		t.Fatalf("frames = %v", kinds(frames))
	}
	if j := frames[0].(Joined); j.Player.ID != friend.ID || j.Player.Seat != 1 || j.Player.Nickname != "Sara" {
		t.Fatalf("joined = %+v", j)
	}
	if m := frames[1].(Members); len(m.List) != 2 || m.List[0].ID != host.ID || m.List[1].ID != friend.ID {
		t.Fatalf("members = %+v", m)
	}

	// Again is the room back, unchanged, and nothing to tell anybody.
	again, err := f.s.Join(f.ctx, friend.ID, r.Code, "")
	if err != nil || again.You.Seat != 1 || len(f.pub.published(r.ID)) != 0 {
		t.Fatalf("again: %+v, %v", again, err)
	}
	if got := f.events(r.ID); strings.Join(got, ",") != "created,joined" {
		t.Fatalf("events = %v", got)
	}

	// A third takes seat 2; when seat 1 leaves, the next joiner takes it.
	third, fourth := f.player("Ali"), f.player("Huda")
	if j := f.join(third, r); j.You.Seat != 2 {
		t.Fatalf("third seat = %d", j.You.Seat)
	}
	if err := f.s.Leave(f.ctx, friend.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if j := f.join(fourth, r); j.You.Seat != 1 {
		t.Fatalf("fourth seat = %d", j.You.Seat)
	}
}

func TestJoiningIsRefused(t *testing.T) {
	f := newFixture(t)
	host, friend, third := f.player("Zayn"), f.player("Sara"), f.player("Ali")

	for _, typed := range []string{"", "ABC", "K7M2QX9", "K7M2Q0", "K7M2QI", "K7M2Q!", strings.Repeat("-", 100)} {
		_, err := f.s.Join(f.ctx, friend.ID, typed, "")
		wantField(t, "code "+typed, err, "code", "must_be_6_symbols")
	}
	_, err := f.s.Join(f.ctx, friend.ID, "ZZZZZZ", "")
	wantCode(t, "a code nobody holds", err, 404, "room_not_found")

	r := f.room(host, xo)
	f.join(friend, r)
	_, err = f.s.Join(f.ctx, third.ID, r.Code, "")
	wantCode(t, "a full room", err, 409, "room_full")

	// One open room per player: joining from inside another is refused, and
	// the refusal names the room the player is still in. It is asked before
	// whether this room has space: going back is the useful answer.
	other := f.room(third, tap)
	_, err = f.s.Join(f.ctx, third.ID, r.Code, "")
	wantCode(t, "a room while in another", err, 409, "in_another_room")
	if refusal(err).Room != other.ID {
		t.Fatalf("names %q", refusal(err).Room)
	}

	// Ended: the code was a room, and it is over.
	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Join(f.ctx, friend.ID, r.Code, "")
	wantCode(t, "an ended room", err, 409, "room_closed")
}

// A game's page outside a room joins for its own game: the join names it,
// and a code another game's room holds is refused another_game. Nobody
// joins, nothing is heard or written, and a member of that room is refused
// too. The room's own game joins as any join does; without a game the code
// may be any game's, as before.
func TestJoiningForOneGame(t *testing.T) {
	f := newFixture(t)
	host, member, friend, other := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Huda")
	r := f.room(host, tap)
	f.join(member, r)

	_, err := f.s.Join(f.ctx, friend.ID, r.Code, xo)
	wantCode(t, "another game's code", err, 409, "another_game")
	if mine, err := f.s.Mine(f.ctx, friend.ID); err != nil || len(mine) != 0 {
		t.Fatalf("the refused player's rooms: %+v, %v", mine, err)
	}
	_, err = f.s.Join(f.ctx, member.ID, r.Code, xo)
	wantCode(t, "another game's code, for a member", err, 409, "another_game")
	got, err := f.s.Get(f.ctx, member.ID, r.ID)
	if err != nil || got.You == nil || got.You.Seat != 1 || len(got.Members) != 2 {
		t.Fatalf("after the refusals: %+v, %v", got, err)
	}
	if frames := f.pub.published(r.ID); len(frames) != 0 {
		t.Fatalf("a refusal published %v", kinds(frames))
	}
	if got := f.events(r.ID); strings.Join(got, ",") != "created,joined" {
		t.Fatalf("events after the refusals = %v", got)
	}

	// The room's own game: the code typed however, the lowest free seat, and
	// the room hears it; a member naming it gets the room back.
	joined, err := f.s.Join(f.ctx, friend.ID, strings.ToLower(r.Code), tap)
	if err != nil || joined.ID != r.ID || joined.You == nil || joined.You.Seat != 2 || len(joined.Members) != 3 {
		t.Fatalf("the room's own game: %+v, %v", joined, err)
	}
	if frames := f.pub.published(r.ID); strings.Join(kinds(frames), ",") != "joined,members" {
		t.Fatalf("frames = %v", kinds(frames))
	}
	again, err := f.s.Join(f.ctx, member.ID, r.Code, tap)
	if err != nil || again.You == nil || again.You.Seat != 1 || len(f.pub.published(r.ID)) != 0 {
		t.Fatalf("a member naming the room's game: %+v, %v", again, err)
	}

	// No game: any game's code, as before.
	if got, err := f.s.Join(f.ctx, other.ID, r.Code, ""); err != nil || got.You == nil || got.You.Seat != 3 {
		t.Fatalf("a join without a game: %+v, %v", got, err)
	}
	if got, err := f.s.Join(f.ctx, member.ID, r.Code, ""); err != nil || got.You == nil || got.You.Seat != 1 {
		t.Fatalf("a member without a game: %+v, %v", got, err)
	}
	if got := f.events(r.ID); strings.Join(got, ",") != "created,joined,joined,joined" {
		t.Fatalf("events = %v", got)
	}
}

// The game is asked right after the code is found. A code that cannot be one,
// a code nobody holds and a room that has ended answer as before; then
// another game's room is another_game ahead of everything about the caller,
// each refusal that would have answered otherwise.
func TestAnotherGamesCodeIsAskedRightAfterTheCode(t *testing.T) {
	f := newFixture(t)
	zayn, sara, ali, huda, mona := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Huda"), f.player("Mona")

	_, err := f.s.Join(f.ctx, huda.ID, "ABC", xo)
	wantField(t, "a code that cannot be one", err, "code", "must_be_6_symbols")
	_, err = f.s.Join(f.ctx, huda.ID, "ZZZZZZ", xo)
	wantCode(t, "a code nobody holds", err, 404, "room_not_found")
	gone := f.room(mona, tap)
	if _, err := f.s.End(f.ctx, mona.ID, gone.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Join(f.ctx, huda.ID, gone.Code, xo)
	wantCode(t, "another game's ended room", err, 409, "room_closed")

	duel := f.room(zayn, xo) // two seats
	f.join(sara, duel)
	race := f.room(ali, tap)
	f.join(huda, race)
	if err := f.s.Kick(f.ctx, ali.ID, race.ID, huda.ID); err != nil {
		t.Fatal(err)
	}
	friendsOnly := f.friendsRoom(mona)

	check := func(what string, p *model.Player, r *Room, game, otherwise string) {
		t.Helper()
		_, err := f.s.Join(f.ctx, p.ID, r.Code, "")
		if codeOf(err) != otherwise {
			t.Fatalf("%s without a game: got %v, want %s", what, err, otherwise)
		}
		_, err = f.s.Join(f.ctx, p.ID, r.Code, game)
		wantCode(t, what, err, 409, "another_game")
	}
	check("kicked", huda, race, xo, "kicked")
	check("not the host's friend", huda, friendsOnly, xo, "friends_only")
	check("in another room", ali, duel, tap, "in_another_room")
	check("a full room", huda, duel, tap, "room_full")
	if _, err := f.s.Start(f.ctx, zayn.ID, duel.ID); err != nil {
		t.Fatal(err)
	}
	check("a round under way", huda, duel, tap, "room_started")
}

// A kick is for good: the kicked player never gets back into that room.
func TestAKickedPlayerCannotRejoin(t *testing.T) {
	f := newFixture(t)
	host, friend := f.player("Zayn"), f.player("Sara")
	r := f.room(host, tap)
	f.join(friend, r)
	if err := f.s.Kick(f.ctx, host.ID, r.ID, friend.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Join(f.ctx, friend.ID, r.Code, "")
	wantCode(t, "a kicked player", err, 403, "kicked")
}

// A game under way takes back a member who stepped out, when the game
// allows a rejoin, into the lowest free seat; never a newcomer.
func TestRejoiningAGameUnderWay(t *testing.T) {
	f := newFixture(t)
	host, friend, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali")

	r := f.room(host, xo) // rejoin: true
	f.join(friend, r)
	if _, err := f.s.Start(f.ctx, host.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Leave(f.ctx, friend.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Join(f.ctx, stranger.ID, r.Code, "")
	wantCode(t, "a newcomer to a game under way", err, 409, "room_started")
	back, err := f.s.Join(f.ctx, friend.ID, r.Code, "")
	if err != nil || back.You == nil || back.You.Seat != 1 || back.Status != "playing" {
		t.Fatalf("rejoin: %+v, %v", back, err)
	}
	if got := f.events(r.ID); strings.Join(got, ",") != "created,joined,started,left,joined" {
		t.Fatalf("events = %v", got)
	}

	// No rejoin in the game's manifest: nobody comes back once it started.
	host2, friend2 := f.player("Huda"), f.player("Omar")
	r2 := f.room(host2, tap) // rejoin: false
	f.join(friend2, r2)
	if _, err := f.s.Start(f.ctx, host2.ID, r2.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Leave(f.ctx, friend2.ID, r2.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Join(f.ctx, friend2.ID, r2.Code, "")
	wantCode(t, "a rejoin the game does not allow", err, 409, "room_started")
}

// The host leaving hands the room to the member in the lowest seat, said
// first; the last member leaving ends the room.
func TestLeavingHandsTheRoomOverOrEndsIt(t *testing.T) {
	f := newFixture(t)
	host, second, third := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	r := f.room(host, tap)
	f.join(second, r)
	f.join(third, r)

	if err := f.s.Leave(f.ctx, host.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	frames := f.pub.published(r.ID)
	if strings.Join(kinds(frames), ",") != "host,left,members" {
		t.Fatalf("frames = %v", kinds(frames))
	}
	if h := frames[0].(HostChanged); h.Player != second.ID || h.By != nil {
		t.Fatalf("host = %+v", h)
	}
	if l := frames[1].(Left); l.Player != host.ID || l.Reason != "left" {
		t.Fatalf("left = %+v", l)
	}
	got, err := f.s.Get(f.ctx, second.ID, r.ID)
	if err != nil || got.Host != second.ID || got.You.Role != "host" || len(got.Members) != 2 {
		t.Fatalf("after the host left: %+v, %v", got, err)
	}

	// The old host can still read the room, as somebody who left.
	old, err := f.s.Get(f.ctx, host.ID, r.ID)
	if err != nil || old.You != nil {
		t.Fatalf("the leaver reads %+v, %v", old, err)
	}
	if err := f.s.Leave(f.ctx, host.ID, r.ID); codeOf(err) != "not_member" {
		t.Fatalf("leaving twice gave %v", err)
	}

	if err := f.s.Leave(f.ctx, third.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	f.pub.published(r.ID)
	if err := f.s.Leave(f.ctx, second.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if k := kinds(f.pub.published(r.ID)); strings.Join(k, ",") != "left,ended" {
		t.Fatalf("frames = %v", k)
	}
	ends := f.pub.ended(r.ID)
	if len(ends) != 1 || ends[0].Reason != "empty" || ends[0].By != nil || ends[0].Results == nil {
		t.Fatalf("ended = %+v", ends)
	}
	final, err := f.s.Get(f.ctx, second.ID, r.ID)
	if err != nil || final.Status != "ended" || final.EndReason == nil || *final.EndReason != "empty" || final.EndedAt == nil {
		t.Fatalf("final = %+v, %v", final, err)
	}
	if got := f.events(r.ID); strings.Join(got, ",") != "created,joined,joined,left,host,left,left,ended" {
		t.Fatalf("events = %v", got)
	}
}

// Leaving a room that has already ended takes the caller out of it and tells
// nobody: there is nobody listening.
func TestLeavingAnEndedRoom(t *testing.T) {
	f := newFixture(t)
	host, friend := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(friend, r)
	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.pub.published(r.ID)
	if err := f.s.Leave(f.ctx, friend.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if k := f.pub.published(r.ID); len(k) != 0 {
		t.Fatalf("frames = %v", kinds(k))
	}
	got, err := f.s.Get(f.ctx, friend.ID, r.ID)
	if err != nil || got.You != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestKicking(t *testing.T) {
	f := newFixture(t)
	host, friend, third, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Huda")
	r := f.room(host, tap)
	f.join(friend, r)
	f.join(third, r)

	err := f.s.Kick(f.ctx, stranger.ID, r.ID, friend.ID)
	wantCode(t, "a stranger kicking", err, 403, "not_member")
	err = f.s.Kick(f.ctx, third.ID, r.ID, friend.ID)
	wantCode(t, "a player kicking", err, 403, "host_only")
	err = f.s.Kick(f.ctx, host.ID, r.ID, host.ID)
	wantField(t, "the host kicking themselves", err, "player", "is_you")
	err = f.s.Kick(f.ctx, host.ID, r.ID, stranger.ID)
	wantField(t, "kicking a stranger", err, "player", "not_a_member")

	if err := f.s.Kick(f.ctx, host.ID, r.ID, friend.ID); err != nil {
		t.Fatal(err)
	}
	frames := f.pub.published(r.ID)
	if strings.Join(kinds(frames), ",") != "kicked,left,members" {
		t.Fatalf("frames = %v", kinds(frames))
	}
	if k := frames[0].(Kicked); k.Player != friend.ID || k.By != host.ID {
		t.Fatalf("kicked = %+v", k)
	}
	if l := frames[1].(Left); l.Player != friend.ID || l.Reason != "kicked" {
		t.Fatalf("left = %+v", l)
	}
	if m := frames[2].(Members); len(m.List) != 2 {
		t.Fatalf("members = %+v", m)
	}
	err = f.s.Kick(f.ctx, host.ID, r.ID, friend.ID)
	wantField(t, "kicking twice", err, "player", "not_a_member")

	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	err = f.s.Kick(f.ctx, host.ID, r.ID, third.ID)
	wantCode(t, "kicking in an ended room", err, 409, "room_closed")
	if got := f.events(r.ID); strings.Join(got, ",") != "created,joined,joined,kicked,ended" {
		t.Fatalf("events = %v", got)
	}
}

// A room is for whoever was ever in it; a stranger is told there is no such
// room.
func TestReadingARoom(t *testing.T) {
	f := newFixture(t)
	host, stranger := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)

	_, err := f.s.Get(f.ctx, stranger.ID, r.ID)
	wantCode(t, "a stranger", err, 404, "not_found")
	_, err = f.s.Get(f.ctx, host.ID, "room_aaaaaaaaaaaaaaaa")
	wantCode(t, "no such room", err, 404, "not_found")

	// Connected is the member's presence saying in_room with this room.
	f.presence.set(host.ID, players.State{State: players.InRoom, Room: r.ID})
	got, err := f.s.Get(f.ctx, host.ID, r.ID)
	if err != nil || !got.Members[0].Connected {
		t.Fatalf("got %+v, %v", got, err)
	}
	f.presence.set(host.ID, players.State{State: players.InRoom, Room: "room_another00000000"})
	if got, _ := f.s.Get(f.ctx, host.ID, r.ID); got.Members[0].Connected {
		t.Fatal("presence in another room read as connected here")
	}

	// The live state version is the relay's while the room is open.
	f.pub.setState(r.ID, 4, `{"board":[]}`)
	if got, _ := f.s.Get(f.ctx, host.ID, r.ID); got.StateVersion != 4 {
		t.Fatalf("state_version = %d", got.StateVersion)
	}

	if mine, err := f.s.Mine(f.ctx, stranger.ID); err != nil || len(mine) != 0 || mine == nil {
		t.Fatalf("a stranger's rooms: %+v, %v", mine, err)
	}
}

// An uploaded avatar is linked; a bundled one is its id.
func TestAMemberIsDrawnWithTheirAvatar(t *testing.T) {
	f := newFixture(t)
	host, friend := f.player("Zayn"), f.player("Sara")
	if err := f.db.Exec(`UPDATE players SET avatar_key = 'a03' WHERE id = ?`, host.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE players SET avatar_key = ? WHERE id = ?`, "avatars/"+friend.ID+".png", friend.ID).Error; err != nil {
		t.Fatal(err)
	}
	r := f.room(host, tap)
	got := f.join(friend, r)
	if got.Members[0].Avatar != "a03" || got.Members[0].AvatarURL != nil {
		t.Fatalf("host = %+v", got.Members[0])
	}
	if got.Members[1].Avatar != "" || got.Members[1].AvatarURL == nil ||
		*got.Members[1].AvatarURL != "https://signed.test/avatars/"+friend.ID+".png" {
		t.Fatalf("friend = %+v", got.Members[1])
	}
}

// One open room per player holds when two joins race: the player's lock
// lets one through.
func TestTwoJoinsAtOnceLeaveThePlayerInOneRoom(t *testing.T) {
	f := newFixture(t)
	a, b, racer := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	ra, rb := f.room(a, tap), f.room(b, tap)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, code := range []string{ra.Code, rb.Code} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.s.Join(f.ctx, racer.ID, code, "")
		}()
	}
	wg.Wait()
	ok, refused := 0, 0
	for _, err := range errs {
		switch codeOf(err) {
		case "":
			ok++
		case "in_another_room":
			refused++
		default:
			t.Fatalf("join: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("ok=%d refused=%d", ok, refused)
	}
	if mine, _ := f.s.Mine(f.ctx, racer.ID); len(mine) != 1 {
		t.Fatalf("the racer is in %d rooms", len(mine))
	}
}

// The socket's door: a stranger and a leaver find no room, a kicked player
// is told so, and an ended room is closed.
func TestTheSocketsDoor(t *testing.T) {
	f := newFixture(t)
	host, friend, third, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Huda")
	r := f.room(host, tap)
	f.join(friend, r)
	f.join(third, r)

	if err := f.s.Admit(f.ctx, r.ID, host.ID); err != nil {
		t.Fatalf("the host: %v", err)
	}
	wantCode(t, "a stranger", f.s.Admit(f.ctx, r.ID, stranger.ID), 404, "not_found")
	wantCode(t, "no such room", f.s.Admit(f.ctx, "room_aaaaaaaaaaaaaaaa", host.ID), 404, "not_found")
	if err := f.s.Leave(f.ctx, friend.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "a leaver", f.s.Admit(f.ctx, r.ID, friend.ID), 404, "not_found")
	if err := f.s.Kick(f.ctx, host.ID, r.ID, third.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "a kicked player", f.s.Admit(f.ctx, r.ID, third.ID), 403, "kicked")
	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "an ended room", f.s.Admit(f.ctx, r.ID, host.ID), 409, "room_closed")
	wantCode(t, "kicked from an ended room", f.s.Admit(f.ctx, r.ID, third.ID), 403, "kicked")

	st, err := f.s.Standing(f.ctx, r.ID, third.ID)
	if err != nil || st.Open || st.Current || !st.Kicked || st.Host {
		t.Fatalf("standing = %+v, %v", st, err)
	}
}
