package rooms

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jamaa/gathering/backend/internal/biz/friends"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// withFriends gives the rules the real friends and the real inbox, over the
// fixture's database.
func (f *fixture) withFriends() (*friends.Service, *inbox.Inbox) {
	f.t.Helper()
	in := inbox.New(f.db)
	fr := friends.New(f.db, identity.NewPlayerStore(f.db), in)
	f.s.Friends, f.s.Inbox = fr, in
	return fr, in
}

// befriend makes a and b friends: a asks, b accepts.
func befriend(t *testing.T, fr *friends.Service, f *fixture, a, b *model.Player) {
	t.Helper()
	if _, _, err := fr.Request(f.ctx, a.ID, b.ID); err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := fr.Accept(f.ctx, b.ID, a.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
}

// friendsRoom makes a friends-only room of the tap race with host in it.
func (f *fixture) friendsRoom(host *model.Player) *Room {
	f.t.Helper()
	r, err := f.s.Create(f.ctx, host.ID, CreateRequest{Game: tap, Visibility: "friends"})
	if err != nil {
		f.t.Fatalf("create: %v", err)
	}
	f.pub.published(r.ID)
	return r
}

// A friends-only room has the host's friends alone — the room's current
// host — and nobody either side of a block with the host. The check comes
// after kicked and before in_another_room.
func TestAFriendsOnlyRoomHasTheHostsFriends(t *testing.T) {
	f := newFixture(t)
	fr, _ := f.withFriends()
	zayn, sara, ali, mona, omar, huda, bashir, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"),
		f.player("Mona"), f.player("Omar"), f.player("Huda"), f.player("Bashir"), f.player("Stranger")
	for _, p := range []*model.Player{sara, ali, mona, omar, bashir} {
		befriend(t, fr, f, zayn, p)
	}
	befriend(t, fr, f, sara, huda)

	r := f.friendsRoom(zayn)
	if r.Visibility != "friends" {
		t.Fatalf("the room: %+v", r)
	}
	_, err := f.s.Join(f.ctx, stranger.ID, r.Code, "")
	wantCode(t, "a stranger", err, 403, "friends_only")
	if got := f.join(sara, r); got.You == nil || len(got.Members) != 2 {
		t.Fatalf("a friend joining: %+v", got)
	}

	// A block either way, whoever made it, is a closed door, named alike.
	if err := fr.Block(f.ctx, zayn.ID, ali.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Join(f.ctx, ali.ID, r.Code, "")
	wantCode(t, "a friend the host blocked", err, 403, "friends_only")
	if err := f.db.Create(&model.Block{BlockerID: mona.ID, BlockedID: zayn.ID, CreatedAt: data.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Join(f.ctx, mona.ID, r.Code, "")
	wantCode(t, "a friend who blocked the host", err, 403, "friends_only")

	// Before in_another_room: a stranger elsewhere is told the door is shut.
	f.room(stranger, xo)
	_, err = f.s.Join(f.ctx, stranger.ID, r.Code, "")
	wantCode(t, "a stranger in another room", err, 403, "friends_only")

	// After kicked: a kicked friend who is no longer a friend is still kicked.
	f.join(omar, r)
	if err := f.s.Kick(f.ctx, zayn.ID, r.ID, omar.ID); err != nil {
		t.Fatal(err)
	}
	if err := fr.Remove(f.ctx, zayn.ID, omar.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Join(f.ctx, omar.ID, r.Code, "")
	wantCode(t, "kicked, and no longer a friend", err, 403, "kicked")

	// A member who left passes the door again to come back.
	f.join(bashir, r)
	if err := f.s.Leave(f.ctx, bashir.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := fr.Remove(f.ctx, bashir.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Join(f.ctx, bashir.ID, r.Code, "")
	wantCode(t, "a member who left and is no longer a friend", err, 403, "friends_only")

	// The host is the room's current one: Sara's friends now, not Zayn's.
	if _, err := f.s.Host(f.ctx, zayn.ID, r.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	f.join(huda, r)
	befriend(t, fr, f, zayn, bashir)
	_, err = f.s.Join(f.ctx, bashir.ID, r.Code, "")
	wantCode(t, "the old host's friend", err, 403, "friends_only")

	// Without friends at all, a friends-only room has nobody but its host.
	f.s.Friends = nil
	_, err = f.s.Join(f.ctx, bashir.ID, r.Code, "")
	wantCode(t, "no friends to ask", err, 403, "friends_only")
}

// A private room is anybody's with the code: blocks do not shut it.
func TestAPrivateRoomIgnoresBlocks(t *testing.T) {
	f := newFixture(t)
	fr, _ := f.withFriends()
	zayn, sara := f.player("Zayn"), f.player("Sara")
	if err := fr.Block(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	r := f.room(zayn, tap)
	if r.Visibility != "private" {
		t.Fatalf("the default visibility: %s", r.Visibility)
	}
	f.join(sara, r)
}

// An invite asks friends to the room: each gets a room_invite whose link
// joins by the code. Every refusal comes back at once, and nobody is asked
// unless all pass.
func TestInvites(t *testing.T) {
	f := newFixture(t)
	fr, in := f.withFriends()
	zayn, sara, ali, mona, omar, huda, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"),
		f.player("Mona"), f.player("Omar"), f.player("Huda"), f.player("Stranger")
	for _, p := range []*model.Player{sara, ali, omar, huda} {
		befriend(t, fr, f, zayn, p)
	}
	befriend(t, fr, f, huda, sara)
	if err := fr.Block(f.ctx, omar.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	r := f.room(zayn, tap)
	f.join(huda, r)

	_, err := f.s.Invite(f.ctx, zayn.ID, r.ID, nil)
	wantField(t, "nobody", err, "players", "1_to_20")
	many := make([]string, 21)
	for i := range many {
		many[i] = sara.ID
	}
	_, err = f.s.Invite(f.ctx, zayn.ID, r.ID, many)
	wantField(t, "21", err, "players", "1_to_20")
	_, err = f.s.Invite(f.ctx, stranger.ID, r.ID, []string{sara.ID})
	wantCode(t, "a stranger to the room", err, 403, "not_member")
	_, err = f.s.Invite(f.ctx, zayn.ID, "room_nothingxxxxxxxx", []string{sara.ID})
	wantCode(t, "no such room", err, 403, "not_member")

	_, err = f.s.Invite(f.ctx, zayn.ID, r.ID, []string{sara.ID, mona.ID, huda.ID, omar.ID, zayn.ID, "nope"})
	e := refusal(err)
	if e == nil || e.Code != "invalid_fields" || len(e.Fields) != 5 || e.Fields["players.1"] != "not_friends" ||
		e.Fields["players.2"] != "already_member" || e.Fields["players.3"] != "not_friends" ||
		e.Fields["players.4"] != "already_member" || e.Fields["players.5"] != "not_friends" {
		t.Fatalf("the refusals: %+v", e)
	}
	if feed, _ := in.Feed(f.ctx, sara.ID, "", 50, false); len(onlyKind(feed.Items, "room_invite")) != 0 {
		t.Fatal("a refused invite reached somebody")
	}

	invited, err := f.s.Invite(f.ctx, zayn.ID, r.ID, []string{sara.ID, ali.ID, sara.ID})
	if err != nil || strings.Join(invited, ",") != sara.ID+","+ali.ID {
		t.Fatalf("invited: %v %v", invited, err)
	}
	feed, err := in.Feed(f.ctx, sara.ID, "", 50, false)
	if err != nil {
		t.Fatal(err)
	}
	invites := onlyKind(feed.Items, "room_invite")
	if len(invites) != 1 {
		t.Fatalf("sara's invites: %+v", invites)
	}
	n := invites[0]
	if n.Title != "دعوة للعب" || n.Body != "Zayn يدعوك تلعب سباق النقر" || n.DeepLink != "/r/"+r.Code ||
		n.Data["room"] != r.ID || n.Data["code"] != r.Code || n.Data["game"] != tap || n.Read ||
		n.Actor == nil || n.Actor.ID != zayn.ID || n.Game == nil || n.Game.ID != tap || feed.Items[0].ID != n.ID {
		t.Fatalf("the invite: %+v", n)
	}

	// Read, then asked again by another member: the same line, back at the
	// top, unread, naming who asked last.
	if err := in.Read(f.ctx, sara.ID, []string{n.ID}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Invite(f.ctx, huda.ID, r.ID, []string{sara.ID}); err != nil {
		t.Fatal(err)
	}
	feed, _ = in.Feed(f.ctx, sara.ID, "", 50, false)
	invites = onlyKind(feed.Items, "room_invite")
	if len(invites) != 1 || invites[0].ID != n.ID || invites[0].Read || feed.Items[0].ID != n.ID ||
		invites[0].Actor == nil || invites[0].Actor.ID != huda.ID || invites[0].Body != "Huda يدعوك تلعب سباق النقر" {
		t.Fatalf("the invite again: %+v", invites)
	}

	// The link's code joins, as any join by code does.
	code := strings.TrimPrefix(invites[0].DeepLink, "/r/")
	if got := f.join(sara, &Room{Code: code, ID: r.ID}); got.ID != r.ID {
		t.Fatalf("joined %s", got.ID)
	}

	// A member who left asks nobody; an ended room asks nobody.
	if err := f.s.Leave(f.ctx, huda.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Invite(f.ctx, huda.ID, r.ID, []string{ali.ID})
	wantCode(t, "a member who left", err, 403, "not_member")
	if _, err := f.s.End(f.ctx, zayn.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Invite(f.ctx, zayn.ID, r.ID, []string{ali.ID})
	wantCode(t, "an ended room", err, 409, "room_closed")
}

func onlyKind(items []inbox.Notification, kind string) []inbox.Notification {
	var out []inbox.Notification
	for _, n := range items {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

// Without an inbox the invite is checked and answered, and nobody is told.
func TestAnInviteWithoutAnInbox(t *testing.T) {
	f := newFixture(t)
	fr, _ := f.withFriends()
	f.s.Inbox = nil
	zayn, sara := f.player("Zayn"), f.player("Sara")
	befriend(t, fr, f, zayn, sara)
	r := f.room(zayn, tap)
	invited, err := f.s.Invite(f.ctx, zayn.ID, r.ID, []string{sara.ID})
	if err != nil || fmt.Sprint(invited) != fmt.Sprint([]string{sara.ID}) {
		t.Fatalf("invited: %v %v", invited, err)
	}
}
