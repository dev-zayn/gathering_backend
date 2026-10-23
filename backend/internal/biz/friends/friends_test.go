package friends

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

const (
	xo  = "fixture.xo"
	tap = "fixture.tap"
)

// fakePresence is who is here, as the relay and the app's heartbeat leave it.
type fakePresence struct {
	mu    sync.Mutex
	state map[string]players.State
	err   error
}

func (p *fakePresence) set(id string, st players.State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == nil {
		p.state = map[string]players.State{}
	}
	p.state[id] = st
}

func (p *fakePresence) Of(_ context.Context, who []string) (map[string]players.State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	out := make(map[string]players.State, len(who))
	for _, id := range who {
		st, ok := p.state[id]
		if !ok {
			st = players.State{State: players.Offline}
		}
		out[id] = st
	}
	return out, nil
}

type fixture struct {
	t        *testing.T
	s        *Service
	in       *inbox.Inbox
	presence *fakePresence
	db       *gorm.DB
	ctx      context.Context
}

func manifest(name string) model.Manifest {
	m := model.Manifest{Name: name, Version: "1.0.0", Entry: "https://games.yourstudio.example/" + name + "/"}
	m.Publisher.Tier = enum.TierPartner
	m.Room.Players.Min, m.Room.Players.Max = 2, 8
	m.Room.Host, m.Room.Pace = enum.HostPeer, enum.PaceTurn
	return m
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := biztest.DB(t)
	biztest.SeedGame(t, db, xo, manifest("إكس أو"))
	biztest.SeedGame(t, db, tap, manifest("سباق النقر"))
	files := biztest.NewFiles()
	in := inbox.New(db)
	in.Files, in.AvatarTTL = files, time.Hour
	s := New(db, identity.NewPlayerStore(db), in)
	presence := &fakePresence{}
	s.Presence, s.Files, s.AvatarTTL = presence, files, time.Hour
	return &fixture{t: t, s: s, in: in, presence: presence, db: db, ctx: context.Background()}
}

func (f *fixture) player(nickname string) *model.Player {
	f.t.Helper()
	return biztest.SeedPlayer(f.t, f.db, "", nickname)
}

// friends makes a and b friends: a asks, b accepts.
func (f *fixture) friends(a, b *model.Player) {
	f.t.Helper()
	if _, _, err := f.s.Request(f.ctx, a.ID, b.ID); err != nil {
		f.t.Fatalf("request: %v", err)
	}
	if err := f.s.Accept(f.ctx, b.ID, a.ID); err != nil {
		f.t.Fatalf("accept: %v", err)
	}
}

// inbox is the player's whole inbox, newest first.
func (f *fixture) inbox(p *model.Player) []inbox.Notification {
	f.t.Helper()
	got, err := f.in.Feed(f.ctx, p.ID, "", 50, false)
	if err != nil {
		f.t.Fatal(err)
	}
	return got.Items
}

func (f *fixture) row(a, b *model.Player) *model.Friendship {
	f.t.Helper()
	row, err := friendship(f.ctx, f.db, a.ID, b.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

func (f *fixture) card(viewer *model.Player, ref string) *PlayerCard {
	f.t.Helper()
	c, err := f.s.Card(f.ctx, viewer.ID, ref)
	if err != nil {
		f.t.Fatalf("card %s: %v", ref, err)
	}
	return c
}

// room seeds an open room of game with its members, the first the host.
func (f *fixture) room(game, status string, members ...*model.Player) (id, code string) {
	f.t.Helper()
	id, code = ids.New(ids.Room), fmt.Sprintf("K%05d", time.Now().UnixNano()%100000)
	now := data.Now()
	err := f.db.Exec(`INSERT INTO rooms (id, code, game_id, host_id, status, visibility, lan, config, created_at, active_at)
	                  VALUES (?, ?, ?, ?, ?, 'private', false, '{}', ?, ?)`,
		id, code, game, members[0].ID, status, now, now).Error
	if err != nil {
		f.t.Fatal(err)
	}
	for seat, m := range members {
		role := "player"
		if seat == 0 {
			role = "host"
		}
		err := f.db.Exec(`INSERT INTO room_members (room_id, player_id, role, seat, joined_at) VALUES (?, ?, ?, ?, ?)`,
			id, m.ID, role, seat, now).Error
		if err != nil {
			f.t.Fatal(err)
		}
	}
	return id, code
}

func refusal(err error) *apperr.Error {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e
	}
	return nil
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

func kinds(items []inbox.Notification) string {
	out := make([]string, len(items))
	for i, n := range items {
		out[i] = n.Kind
		if n.Read {
			out[i] += "(read)"
		}
	}
	return strings.Join(out, ",")
}

// A request lands in the other's inbox and in both lists; the accept makes
// them friends, tells the asker, and answers the accepter's own notification.
func TestARequestAndItsAccept(t *testing.T) {
	f := newFixture(t)
	zayn, sara := f.player("Zayn"), f.player("Sara")

	standing, created, err := f.s.Request(f.ctx, zayn.ID, sara.ID)
	if err != nil || standing != Requested || !created {
		t.Fatalf("request: %q %v %v", standing, created, err)
	}
	row := f.row(zayn, sara)
	if row == nil || row.Status != enum.FriendshipRequested || row.RequestedBy != zayn.ID || row.RespondedAt != nil {
		t.Fatalf("the row: %+v", row)
	}
	got := f.inbox(sara)
	if len(got) != 1 {
		t.Fatalf("sara's inbox: %s", kinds(got))
	}
	n := got[0]
	if n.Kind != "friend_request" || n.Title != "طلب صداقة" || n.Body != "Zayn يريد يصير صديقك" || n.DeepLink != "" ||
		n.Data["player"] != zayn.ID || n.Actor == nil || n.Actor.ID != zayn.ID || n.Game != nil || n.Read {
		t.Fatalf("the request's notification: %+v", n)
	}
	var key string
	f.db.Raw(`SELECT key FROM notifications WHERE id = ?`, n.ID).Scan(&key)
	if key != "friend_request:"+zayn.ID {
		t.Fatalf("its key: %q", key)
	}

	mine, err := f.s.Requests(f.ctx, zayn.ID)
	if err != nil || len(mine.Outgoing) != 1 || len(mine.Incoming) != 0 || mine.Outgoing[0].Player.ID != sara.ID || mine.Outgoing[0].At == "" {
		t.Fatalf("zayn's requests: %+v %v", mine, err)
	}
	theirs, _ := f.s.Requests(f.ctx, sara.ID)
	if len(theirs.Incoming) != 1 || len(theirs.Outgoing) != 0 || theirs.Incoming[0].Player.ID != zayn.ID {
		t.Fatalf("sara's requests: %+v", theirs)
	}

	if err := f.s.Accept(f.ctx, sara.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	if row := f.row(zayn, sara); row.Status != enum.FriendshipAccepted || row.RespondedAt == nil {
		t.Fatalf("after the accept: %+v", row)
	}
	accepted := f.inbox(zayn)
	if len(accepted) != 1 || accepted[0].Kind != "friend_accepted" || accepted[0].Title != "صرتوا أصدقاء" ||
		accepted[0].Body != "Sara قبل طلب صداقتك" || accepted[0].Data["player"] != sara.ID ||
		accepted[0].Actor == nil || accepted[0].Actor.ID != sara.ID || accepted[0].DeepLink != "" {
		t.Fatalf("zayn's inbox: %+v", accepted)
	}
	if got := kinds(f.inbox(sara)); got != "friend_request(read)" {
		t.Fatalf("sara's own request after she accepted it: %s", got)
	}
	for _, p := range []*model.Player{zayn, sara} {
		list, err := f.s.Friends(f.ctx, p.ID)
		if err != nil || len(list) != 1 || list[0].Since == "" || list[0].Presence.State != players.Offline {
			t.Fatalf("%s's friends: %+v %v", p.Nickname, list, err)
		}
		if reqs, _ := f.s.Requests(f.ctx, p.ID); len(reqs.Incoming)+len(reqs.Outgoing) != 0 {
			t.Fatalf("%s still has requests: %+v", p.Nickname, reqs)
		}
	}
}

// Two players who each tap "add" are friends: the second request is the
// accept, and the first asker hears so rather than being asked back.
func TestTwoWhoEachAskAreFriends(t *testing.T) {
	f := newFixture(t)
	zayn, sara := f.player("Zayn"), f.player("Sara")
	if _, _, err := f.s.Request(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	standing, created, err := f.s.Request(f.ctx, sara.ID, zayn.ID)
	if err != nil || standing != Friends || created {
		t.Fatalf("the second request: %q %v %v", standing, created, err)
	}
	if row := f.row(zayn, sara); row.Status != enum.FriendshipAccepted || row.RequestedBy != zayn.ID {
		t.Fatalf("the row: %+v", row)
	}
	if got := kinds(f.inbox(zayn)); got != "friend_accepted" {
		t.Fatalf("zayn's inbox: %s", got)
	}
	if got := kinds(f.inbox(sara)); got != "friend_request(read)" {
		t.Fatalf("sara's inbox: %s", got)
	}
}

func TestARequestIsRefused(t *testing.T) {
	f := newFixture(t)
	zayn, sara, ali, gone := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Gone")
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, gone.ID).Error; err != nil {
		t.Fatal(err)
	}

	_, _, err := f.s.Request(f.ctx, zayn.ID, zayn.ID)
	wantField(t, "yourself", err, "player", "is_you")
	for _, who := range []string{"ply_nobodyxxxxxxxxxx", "zayn_0001", "", gone.ID} {
		_, _, err = f.s.Request(f.ctx, zayn.ID, who)
		wantCode(t, "nobody: "+who, err, 404, "not_found")
	}

	f.friends(zayn, sara)
	_, _, err = f.s.Request(f.ctx, sara.ID, zayn.ID)
	wantCode(t, "a friend", err, 409, "already_friends")

	if _, _, err := f.s.Request(f.ctx, zayn.ID, ali.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.s.Request(f.ctx, zayn.ID, ali.ID)
	wantCode(t, "asking twice", err, 409, "request_pending")

	// Blocks: the caller's own is named; theirs never is.
	if err := f.s.Block(f.ctx, zayn.ID, ali.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.s.Request(f.ctx, zayn.ID, ali.ID)
	wantCode(t, "a player the caller blocked", err, 403, "blocked")
	_, _, err = f.s.Request(f.ctx, ali.ID, zayn.ID)
	wantCode(t, "a player who blocked the caller", err, 404, "not_found")
	if err := f.s.Block(f.ctx, ali.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.s.Request(f.ctx, zayn.ID, ali.ID)
	wantCode(t, "blocked both ways, from the first", err, 403, "blocked")
	_, _, err = f.s.Request(f.ctx, ali.ID, zayn.ID)
	wantCode(t, "blocked both ways, from the second", err, 403, "blocked")
}

// A decline and a cancel each take the request and its notification away;
// each is for its own side only.
func TestDeclineAndCancel(t *testing.T) {
	f := newFixture(t)
	zayn, sara := f.player("Zayn"), f.player("Sara")

	err := f.s.Decline(f.ctx, sara.ID, zayn.ID)
	wantCode(t, "declining nothing", err, 404, "request_not_found")
	err = f.s.Cancel(f.ctx, zayn.ID, sara.ID)
	wantCode(t, "cancelling nothing", err, 404, "request_not_found")

	if _, _, err := f.s.Request(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "accepting one's own request", f.s.Accept(f.ctx, zayn.ID, sara.ID), 404, "request_not_found")
	wantCode(t, "declining one's own request", f.s.Decline(f.ctx, zayn.ID, sara.ID), 404, "request_not_found")
	wantCode(t, "cancelling theirs", f.s.Cancel(f.ctx, sara.ID, zayn.ID), 404, "request_not_found")

	if err := f.s.Decline(f.ctx, sara.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	if f.row(zayn, sara) != nil || len(f.inbox(sara)) != 0 || len(f.inbox(zayn)) != 0 {
		t.Fatalf("after the decline: %+v, %s, %s", f.row(zayn, sara), kinds(f.inbox(sara)), kinds(f.inbox(zayn)))
	}

	// Asking again is a fresh request; cancelling it takes it back.
	if _, created, err := f.s.Request(f.ctx, zayn.ID, sara.ID); err != nil || !created {
		t.Fatalf("asking again: %v %v", created, err)
	}
	if got := kinds(f.inbox(sara)); got != "friend_request" {
		t.Fatalf("sara's inbox after the second request: %s", got)
	}
	if err := f.s.Cancel(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	if f.row(zayn, sara) != nil || len(f.inbox(sara)) != 0 {
		t.Fatal("the cancel left the request or its notification")
	}
	wantCode(t, "accepting a cancelled request", f.s.Accept(f.ctx, sara.ID, zayn.ID), 404, "request_not_found")
}

func TestRemovingAFriend(t *testing.T) {
	f := newFixture(t)
	zayn, sara, ali := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	wantCode(t, "a stranger", f.s.Remove(f.ctx, zayn.ID, sara.ID), 404, "not_friends")
	if _, _, err := f.s.Request(f.ctx, zayn.ID, ali.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "a request", f.s.Remove(f.ctx, zayn.ID, ali.ID), 404, "not_friends")

	f.friends(zayn, sara)
	before := len(f.inbox(sara)) + len(f.inbox(zayn))
	if err := f.s.Remove(f.ctx, sara.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	if f.row(zayn, sara) != nil {
		t.Fatal("still friends")
	}
	if after := len(f.inbox(sara)) + len(f.inbox(zayn)); after != before {
		t.Fatalf("a removal raised something: %d → %d", before, after)
	}
	wantCode(t, "removing twice", f.s.Remove(f.ctx, zayn.ID, sara.ID), 404, "not_friends")
}

// A block ends the friendship and the requests' notifications, either way;
// blocking twice is blocking once; unblocking restores nothing.
func TestABlockEndsEverything(t *testing.T) {
	f := newFixture(t)
	zayn, sara, ali, gone := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Gone")
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, gone.ID).Error; err != nil {
		t.Fatal(err)
	}
	wantField(t, "blocking yourself", f.s.Block(f.ctx, zayn.ID, zayn.ID), "player", "is_you")
	wantField(t, "unblocking yourself", f.s.Unblock(f.ctx, zayn.ID, zayn.ID), "player", "is_you")
	wantCode(t, "blocking nobody", f.s.Block(f.ctx, zayn.ID, "ply_nobodyxxxxxxxxxx"), 404, "not_found")
	wantCode(t, "blocking the switched off", f.s.Block(f.ctx, zayn.ID, gone.ID), 404, "not_found")
	wantCode(t, "unblocking nobody", f.s.Unblock(f.ctx, zayn.ID, "nope"), 404, "not_found")

	// A friendship, and a request each way to two others.
	f.friends(zayn, sara)
	if _, _, err := f.s.Request(f.ctx, ali.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Block(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Block(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatalf("blocking twice: %v", err)
	}
	if f.row(zayn, sara) != nil {
		t.Fatal("the block left the friendship")
	}
	if err := f.s.Block(f.ctx, zayn.ID, ali.ID); err != nil {
		t.Fatal(err)
	}
	if f.row(zayn, ali) != nil || kinds(f.inbox(zayn)) != "friend_accepted" {
		t.Fatalf("the block left the request or its notification: %+v %s", f.row(zayn, ali), kinds(f.inbox(zayn)))
	}

	// The other way round: the one blocked had asked, and is the one blocking.
	if _, _, err := f.s.Request(f.ctx, sara.ID, ali.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Block(f.ctx, ali.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	if f.row(sara, ali) != nil || len(f.inbox(ali)) != 0 {
		t.Fatal("blocking the asker left the request")
	}

	if err := f.s.Unblock(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Unblock(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatalf("unblocking twice: %v", err)
	}
	if f.row(zayn, sara) != nil {
		t.Fatal("unblocking restored the friendship")
	}
	if c := f.card(zayn, sara.ID); c.Friendship != None {
		t.Fatalf("after the unblock: %s", c.Friendship)
	}
}

// The card: by handle or by id, where the viewer stands, the friends they
// share, and presence between friends only.
func TestTheCard(t *testing.T) {
	f := newFixture(t)
	zayn, sara, ali, mona, gone := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Mona"), f.player("Gone")
	if err := f.db.Exec(`UPDATE players SET avatar_key = 'avatars/sara.png' WHERE id = ?`, sara.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.presence.set(sara.ID, players.State{State: players.Online})
	f.presence.set(ali.ID, players.State{State: players.Online})

	c := f.card(zayn, sara.Handle)
	if c.ID != sara.ID || c.Nickname != "Sara" || c.Handle != sara.Handle || c.Avatar != "" || c.AvatarURL == nil ||
		*c.AvatarURL != "https://signed.test/avatars/sara.png" || c.Friendship != None || c.MutualFriends != 0 || c.Presence != nil {
		t.Fatalf("a stranger's card: %+v", c)
	}
	if byID := f.card(zayn, sara.ID); byID.ID != sara.ID {
		t.Fatalf("by id: %+v", byID)
	}
	if at := f.card(zayn, "@"+strings.ToUpper(sara.Handle)); at.ID != sara.ID {
		t.Fatalf("by @HANDLE: %+v", at)
	}
	if self := f.card(zayn, zayn.Handle); self.Friendship != None || self.MutualFriends != 0 || self.Presence != nil {
		t.Fatalf("one's own card: %+v", self)
	}
	for _, ref := range []string{"nobody_0000", "ply_nobodyxxxxxxxxxx", "", "@"} {
		_, err := f.s.Card(f.ctx, zayn.ID, ref)
		wantCode(t, "nobody: "+ref, err, 404, "not_found")
	}
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, gone.ID).Error; err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Card(f.ctx, zayn.ID, gone.Handle)
	wantCode(t, "switched off", err, 404, "not_found")

	// Requested, then incoming, then friends with presence and shared friends.
	if _, _, err := f.s.Request(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	if c := f.card(zayn, sara.ID); c.Friendship != Requested || c.Presence != nil {
		t.Fatalf("asked: %+v", c)
	}
	if c := f.card(sara, zayn.ID); c.Friendship != Incoming || c.Presence != nil {
		t.Fatalf("asked by: %+v", c)
	}
	if err := f.s.Accept(f.ctx, sara.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	f.friends(zayn, mona)
	f.friends(sara, mona)
	f.friends(ali, mona)
	if c := f.card(zayn, sara.ID); c.Friendship != Friends || c.MutualFriends != 1 || c.Presence == nil ||
		c.Presence.State != players.Online || c.Presence.Room != nil {
		t.Fatalf("a friend's card: %+v", c)
	}
	// Ali shares Mona with Zayn, but they are not friends: no presence.
	if c := f.card(zayn, ali.ID); c.Friendship != None || c.MutualFriends != 1 || c.Presence != nil {
		t.Fatalf("a friend of a friend: %+v", c)
	}
	// A shared friend switched off is not counted.
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, mona.ID).Error; err != nil {
		t.Fatal(err)
	}
	if c := f.card(zayn, ali.ID); c.MutualFriends != 0 {
		t.Fatalf("a switched-off friend counted: %+v", c)
	}

	// Blocked by them: not found. Blocked by the viewer: blocked, even when
	// both blocked, so the viewer can lift theirs.
	if err := f.s.Block(f.ctx, sara.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Card(f.ctx, zayn.ID, sara.Handle)
	wantCode(t, "a player who blocked the viewer", err, 404, "not_found")
	if c := f.card(sara, zayn.ID); c.Friendship != Blocked || c.Presence != nil {
		t.Fatalf("the blocker's view: %+v", c)
	}
	if err := f.s.Block(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	if c := f.card(zayn, sara.ID); c.Friendship != Blocked {
		t.Fatalf("blocked both ways: %+v", c)
	}
}

// Friends are listed in a room first, then online, then offline, each by
// nickname. A room is shown while it is open and the friend still in it.
func TestFriendsAreListedByWhereTheyAre(t *testing.T) {
	f := newFixture(t)
	me := f.player("Zayn")
	ali, bashir, dalia, huda, omar, rana := f.player("Ali"), f.player("bashir"), f.player("Dalia"), f.player("Huda"), f.player("Omar"), f.player("Rana")
	for _, p := range []*model.Player{rana, omar, huda, dalia, bashir, ali} {
		f.friends(me, p)
	}

	open, code := f.room(xo, "playing", huda, f.player("Stranger"))
	ended, _ := f.room(tap, "ended", omar)
	left, _ := f.room(xo, "lobby", f.player("Host"), rana)
	if err := f.db.Exec(`UPDATE room_members SET left_at = now() WHERE room_id = ? AND player_id = ?`, left, rana.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.presence.set(huda.ID, players.State{State: players.InRoom, Room: open})
	f.presence.set(omar.ID, players.State{State: players.InRoom, Room: ended})
	f.presence.set(rana.ID, players.State{State: players.InRoom, Room: left})
	f.presence.set(bashir.ID, players.State{State: players.Online})
	f.presence.set(dalia.ID, players.State{State: players.Online})

	list, err := f.s.Friends(f.ctx, me.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, fr := range list {
		got = append(got, fr.Player.Nickname+":"+fr.Presence.State)
	}
	want := "Huda:in_room,bashir:online,Dalia:online,Omar:online,Rana:online,Ali:offline"
	if strings.Join(got, ",") != want {
		t.Fatalf("the list: %v\nwant %s", got, want)
	}
	room := list[0].Presence.Room
	if room == nil || room.ID != open || room.Code != code || room.Game.ID != xo || room.Game.Name != "إكس أو" ||
		!strings.HasPrefix(room.Game.Icon, "https://signed.test/") {
		t.Fatalf("huda's room: %+v", room)
	}
	for _, fr := range list[1:] {
		if fr.Presence.Room != nil {
			t.Fatalf("%s has a room: %+v", fr.Player.Nickname, fr.Presence.Room)
		}
	}

	// Presence that cannot be read is everybody offline, not a failure.
	f.presence.err = errors.New("redis is away")
	list, err = f.s.Friends(f.ctx, me.ID)
	if err != nil || len(list) != 6 || list[0].Player.Nickname != "Ali" || list[0].Presence.State != players.Offline {
		t.Fatalf("without presence: %+v %v", list, err)
	}

	// A friend switched off is left out.
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, ali.ID).Error; err != nil {
		t.Fatal(err)
	}
	if list, _ := f.s.Friends(f.ctx, me.ID); len(list) != 5 {
		t.Fatalf("a switched-off friend is listed: %d", len(list))
	}
}

// result writes one line of a room's results for the leaderboard to read.
func (f *fixture) result(game string, p *model.Player, score float64, rank int, at time.Time) {
	f.t.Helper()
	err := f.db.Create(&model.Result{
		RoomID: ids.New(ids.Room), PlayerID: p.ID, GameID: game, Code: "K7M2QX", Seat: 0,
		Score: score, Rank: rank, Players: 2, FinishedAt: at.UTC().Truncate(time.Microsecond), SubmittedBy: enum.SubmitterHost,
	}).Error
	if err != nil {
		f.t.Fatal(err)
	}
}

// The leaderboard ranks the caller and their friends with results in the
// game: wins, then the best score, then plays; ties share a rank.
func TestTheLeaderboard(t *testing.T) {
	f := newFixture(t)
	me, sara, ali, mona, omar, huda, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"),
		f.player("Mona"), f.player("Omar"), f.player("Huda"), f.player("Stranger")
	for _, p := range []*model.Player{sara, ali, mona, omar, huda} {
		f.friends(me, p)
	}
	t0 := time.Now().Add(-time.Hour)

	f.result(xo, sara, 10, 1, t0)                   // 2 wins, best 10, 2 plays
	f.result(xo, sara, 3, 1, t0.Add(time.Minute))   //
	f.result(xo, ali, 30, 1, t0)                    // 1 win, best 30, 1 play
	f.result(xo, mona, 30, 1, t0)                   // 1 win, best 30, 2 plays: above Ali
	f.result(xo, mona, 5, 2, t0.Add(2*time.Minute)) //
	f.result(xo, me, 30, 1, t0)                     // 1 win, best 30, 1 play: tied with Ali
	f.result(xo, omar, 50, 2, t0)                   // no wins, best 50
	f.result(xo, stranger, 99, 1, t0)               // not a friend
	f.result(tap, huda, 99, 1, t0)                  // another game
	f.result(xo, huda, 1, 3, t0.Add(3*time.Minute)) // no wins, best 1

	board, err := f.s.Leaderboard(f.ctx, me.ID, xo, "")
	if err != nil {
		t.Fatal(err)
	}
	if board.Game.ID != xo || board.Game.Name != "إكس أو" || board.Game.Icon == "" {
		t.Fatalf("the game: %+v", board.Game)
	}
	var got []string
	for _, e := range board.Entries {
		got = append(got, fmt.Sprintf("%d:%s:%d/%d/%g", e.Rank, e.Player.Nickname, e.Wins, e.Plays, e.Best))
	}
	want := "1:Sara:2/2/10,2:Mona:1/2/30,3:Ali:1/1/30,3:Zayn:1/1/30,5:Omar:0/1/50,6:Huda:0/1/1"
	if strings.Join(got, ",") != want {
		t.Fatalf("the board: %v\nwant %s", got, want)
	}
	for _, e := range board.Entries {
		if e.You != (e.Player.ID == me.ID) {
			t.Fatalf("you on %s: %v", e.Player.Nickname, e.You)
		}
	}
	if huda := board.Entries[5]; huda.LastPlayedAt != t0.Add(3*time.Minute).UTC().Format("2006-01-02T15:04:05Z") {
		t.Fatalf("huda's last played: %s", huda.LastPlayedAt)
	}
	if sara := board.Entries[0]; sara.LastPlayedAt != t0.Add(time.Minute).UTC().Format("2006-01-02T15:04:05Z") {
		t.Fatalf("sara's last played: %s", sara.LastPlayedAt)
	}

	// From Sara's side: her friends are Zayn alone, and she has results.
	board, err = f.s.Leaderboard(f.ctx, sara.ID, xo, "friends")
	if err != nil || len(board.Entries) != 2 || board.Entries[0].Player.ID != sara.ID || !board.Entries[0].You {
		t.Fatalf("sara's board: %+v %v", board, err)
	}
	// Without results of their own, the caller is not on it.
	newcomer := f.player("Newcomer")
	f.friends(newcomer, sara)
	board, err = f.s.Leaderboard(f.ctx, newcomer.ID, xo, "friends")
	if err != nil || len(board.Entries) != 1 || board.Entries[0].Player.ID != sara.ID || board.Entries[0].You {
		t.Fatalf("a newcomer's board: %+v %v", board, err)
	}
	board, err = f.s.Leaderboard(f.ctx, stranger.ID, tap, "")
	if err != nil || len(board.Entries) != 0 {
		t.Fatalf("an empty board: %+v %v", board, err)
	}

	_, err = f.s.Leaderboard(f.ctx, me.ID, xo, "global")
	wantField(t, "another scope", err, "scope", "friends_only")
	_, err = f.s.Leaderboard(f.ctx, me.ID, "fixture.nothing", "friends")
	wantCode(t, "no such game", err, 404, "not_found")
	if err := f.db.Exec(`UPDATE games SET listing = 'suspended' WHERE id = ?`, tap).Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Leaderboard(f.ctx, me.ID, tap, "friends")
	wantCode(t, "a suspended game", err, 404, "not_found")
}

// Among is the rooms' question: which of these are the player's friends,
// with no block either way.
func TestAmong(t *testing.T) {
	f := newFixture(t)
	me, sara, ali, mona, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Mona"), f.player("Stranger")
	f.friends(me, sara)
	f.friends(ali, me)
	f.friends(me, mona)
	if _, _, err := f.s.Request(f.ctx, me.ID, stranger.ID); err != nil {
		t.Fatal(err)
	}
	// A block written beside a friendship (a race the lock rules out) still
	// counts.
	if err := f.db.Create(&model.Block{BlockerID: mona.ID, BlockedID: me.ID, CreatedAt: data.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Among(f.ctx, me.ID, []string{sara.ID, ali.ID, mona.ID, stranger.ID, "nope"})
	if err != nil || len(got) != 2 || !got[sara.ID] || !got[ali.ID] {
		t.Fatalf("among: %v %v", got, err)
	}
	if got, err := f.s.Among(f.ctx, me.ID, nil); err != nil || len(got) != 0 {
		t.Fatalf("among nobody: %v %v", got, err)
	}
}

// Two players asking each other at the same moment end up friends, once:
// the pair's lock lets one in at a time.
func TestTwoRequestsAtOnce(t *testing.T) {
	f := newFixture(t)
	for range 5 {
		zayn, sara := f.player("Zayn"), f.player("Sara")
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, pair := range [][2]*model.Player{{zayn, sara}, {sara, zayn}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, errs[i] = f.s.Request(f.ctx, pair[0].ID, pair[1].ID)
			}()
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("two requests at once: %v, %v", errs[0], errs[1])
		}
		if row := f.row(zayn, sara); row == nil || row.Status != enum.FriendshipAccepted {
			t.Fatalf("after two requests at once: %+v", row)
		}
	}
}
