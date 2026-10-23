package inbox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

const xo = "fixture.xo"

type fixture struct {
	t   *testing.T
	in  *Inbox
	db  *gorm.DB
	ctx context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := biztest.DB(t)
	m := model.Manifest{Name: "إكس أو", Version: "1.0.0", Entry: "https://games.yourstudio.example/xo/"}
	m.Publisher.Tier = enum.TierPartner
	m.Room.Players.Min, m.Room.Players.Max = 2, 2
	m.Room.Host, m.Room.Pace = enum.HostAuthoritative, enum.PaceTurn
	biztest.SeedGame(t, db, xo, m)
	in := New(db)
	in.Files, in.AvatarTTL = biztest.NewFiles(), time.Hour
	return &fixture{t: t, in: in, db: db, ctx: context.Background()}
}

func (f *fixture) player(nickname string) *model.Player {
	f.t.Helper()
	return biztest.SeedPlayer(f.t, f.db, "", nickname)
}

func (f *fixture) raise(n Notice) *Raised {
	f.t.Helper()
	got, err := f.in.Raise(f.ctx, n)
	if err != nil {
		f.t.Fatalf("raise: %v", err)
	}
	return got
}

// at puts a notification at a time of the test's choosing: raises a moment
// apart are a microsecond apart at best.
func (f *fixture) at(id string, when time.Time) {
	f.t.Helper()
	if err := f.db.Exec(`UPDATE notifications SET created_at = ? WHERE id = ?`, when, id).Error; err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) row(id string) model.Notification {
	f.t.Helper()
	var n model.Notification
	if err := f.db.Take(&n, "id = ?", id).Error; err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) feed(playerID, cursor string, limit int, unread bool) *Feed {
	f.t.Helper()
	got, err := f.in.Feed(f.ctx, playerID, cursor, limit, unread)
	if err != nil {
		f.t.Fatalf("feed: %v", err)
	}
	return got
}

func wantField(t *testing.T, what string, err error, field, problem string) {
	t.Helper()
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != "invalid_fields" || e.Fields[field] != problem {
		t.Fatalf("%s: got %v, want %s: %s", what, err, field, problem)
	}
}

func idsOf(items []Notification) []string {
	out := make([]string, len(items))
	for i, n := range items {
		out[i] = n.ID
	}
	return out
}

// A raise without a key is always a new line; the feed draws who caused it
// and which game it is about, the pictures signed, and the data as it was
// raised.
func TestARaiseIsANewLine(t *testing.T) {
	f := newFixture(t)
	sara, zayn := f.player("Sara"), f.player("Zayn")
	if err := f.db.Exec(`UPDATE players SET avatar_key = 'a07' WHERE id = ?`, zayn.ID).Error; err != nil {
		t.Fatal(err)
	}

	first := f.raise(Notice{
		PlayerID: sara.ID, Kind: enum.NoticeRoomInvite, ActorID: zayn.ID, GameID: xo,
		Title: "دعوة للعب", Body: "Zayn يدعوك تلعب إكس أو", DeepLink: "/r/K7M2QX",
		Data: map[string]any{"room": "room_7f3k9d2m8q1x4b6n", "code": "K7M2QX", "game": xo},
	})
	second := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	if !first.Created || !second.Created || first.ID == second.ID || !ids.Is(ids.Notification, first.ID) {
		t.Fatalf("two raises: %+v %+v", first, second)
	}
	f.at(first.ID, time.Now().Add(-time.Minute))

	got := f.feed(sara.ID, "", 20, false)
	if len(got.Items) != 2 || got.Unread != 2 || got.NextCursor != nil {
		t.Fatalf("feed: %+v", got)
	}
	plain, invite := got.Items[0], got.Items[1]
	if plain.ID != second.ID || plain.Actor != nil || plain.Game != nil || plain.Read || len(plain.Data) != 0 ||
		plain.DeepLink != "" || plain.Body != "" {
		t.Fatalf("a notification with no actor and no game: %+v", plain)
	}
	if invite.Kind != "room_invite" || invite.Title != "دعوة للعب" || invite.DeepLink != "/r/K7M2QX" ||
		invite.Data["code"] != "K7M2QX" || invite.Data["room"] != "room_7f3k9d2m8q1x4b6n" || invite.Data["game"] != xo {
		t.Fatalf("the invite: %+v", invite)
	}
	if invite.Actor == nil || invite.Actor.ID != zayn.ID || invite.Actor.Nickname != "Zayn" ||
		invite.Actor.Handle != zayn.Handle || invite.Actor.Avatar != "a07" || invite.Actor.AvatarURL != nil {
		t.Fatalf("the actor: %+v", invite.Actor)
	}
	if invite.Game == nil || invite.Game.ID != xo || invite.Game.Name != "إكس أو" ||
		!strings.HasPrefix(invite.Game.Icon, "https://signed.test/") {
		t.Fatalf("the game: %+v", invite.Game)
	}
}

// A key already in the inbox for the recipient and the game updates that
// line in place: the same id, the new words, data and actor, back at the
// top, unread.
func TestAKeyBringsTheLineBackToTheTop(t *testing.T) {
	f := newFixture(t)
	sara, zayn, ali := f.player("Sara"), f.player("Zayn"), f.player("Ali")

	invite := Notice{
		PlayerID: sara.ID, Kind: enum.NoticeRoomInvite, ActorID: zayn.ID, GameID: xo,
		Title: "دعوة للعب", Body: "Zayn يدعوك تلعب إكس أو", DeepLink: "/r/K7M2QX",
		Data: map[string]any{"code": "K7M2QX"}, Key: "room_invite:room_7f3k9d2m8q1x4b6n",
	}
	first := f.raise(invite)
	f.at(first.ID, time.Now().Add(-time.Hour))
	other := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	f.at(other.ID, time.Now().Add(-time.Minute))
	if err := f.in.Read(f.ctx, sara.ID, []string{first.ID}, false); err != nil {
		t.Fatal(err)
	}
	if got := f.feed(sara.ID, "", 20, false); got.Unread != 1 || got.Items[0].ID != other.ID || !got.Items[1].Read {
		t.Fatalf("before the second invite: %+v", got)
	}

	invite.ActorID, invite.Body = ali.ID, "Ali يدعوك تلعب إكس أو"
	again := f.raise(invite)
	if again.Created || again.ID != first.ID {
		t.Fatalf("the second invite: %+v, want %s updated", again, first.ID)
	}
	got := f.feed(sara.ID, "", 20, false)
	top := got.Items[0]
	if got.Unread != 2 || top.ID != first.ID || top.Read || top.Body != "Ali يدعوك تلعب إكس أو" ||
		top.Actor == nil || top.Actor.ID != ali.ID {
		t.Fatalf("after the second invite: %+v", got)
	}
	var n int64
	f.db.Model(&model.Notification{}).Where("player_id = ?", sara.ID).Count(&n)
	if n != 2 {
		t.Fatalf("the inbox holds %d lines, want 2", n)
	}
}

// A key is the recipient's and the game's: the same key for somebody else,
// for another game, or for the platform is another line.
func TestAKeyIsPerRecipientAndGame(t *testing.T) {
	f := newFixture(t)
	sara, zayn := f.player("Sara"), f.player("Zayn")
	m := model.Manifest{Name: "سباق النقر", Version: "1.0.0", Entry: "https://games.yourstudio.example/tap/"}
	m.Publisher.Tier = enum.TierPartner
	m.Room.Players.Min, m.Room.Players.Max = 2, 8
	m.Room.Host, m.Room.Pace = enum.HostPeer, enum.PaceRealtime
	biztest.SeedGame(t, f.db, "fixture.tap", m)

	seen := map[string]bool{}
	for _, n := range []Notice{
		{PlayerID: sara.ID, Kind: enum.NoticeGame, GameID: xo, Title: "دورك", Key: "turn"},
		{PlayerID: zayn.ID, Kind: enum.NoticeGame, GameID: xo, Title: "دورك", Key: "turn"},
		{PlayerID: sara.ID, Kind: enum.NoticeGame, GameID: "fixture.tap", Title: "دورك", Key: "turn"},
		{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "دورك", Key: "turn"},
	} {
		got := f.raise(n)
		if !got.Created || seen[got.ID] {
			t.Fatalf("%+v: %+v", n, got)
		}
		seen[got.ID] = true
	}
	if again := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "مرة ثانية", Key: "turn"}); again.Created {
		t.Fatalf("the platform's key raised again: %+v", again)
	}
}

// The feed is newest first, a page at a time; the cursor carries on where
// the page ended, the last page says so, and unread is the whole count.
func TestTheFeedPages(t *testing.T) {
	f := newFixture(t)
	sara, zayn := f.player("Sara"), f.player("Zayn")
	base := time.Now().Add(-time.Hour)
	var want []string
	for i := range 5 {
		got := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: fmt.Sprintf("رسالة %d", i)})
		f.at(got.ID, base.Add(time.Duration(i)*time.Minute))
		want = append([]string{got.ID}, want...)
	}
	// Two at the same moment: the id breaks the tie.
	twin := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "توأم"})
	f.at(twin.ID, base.Add(2*time.Minute))
	f.raise(Notice{PlayerID: zayn.ID, Kind: enum.NoticeSystem, Title: "لغيرها"})
	if err := f.in.Read(f.ctx, sara.ID, []string{want[0]}, false); err != nil {
		t.Fatal(err)
	}

	// The expected order: newest first, ties by id.
	order := append([]string{}, want[:2]...)
	mid := []string{want[2], twin.ID}
	if mid[1] < mid[0] {
		mid[0], mid[1] = mid[1], mid[0]
	}
	order = append(append(order, mid...), want[3:]...)

	var got []string
	cursor, pages := "", 0
	for {
		page := f.feed(sara.ID, cursor, 2, false)
		pages++
		if page.Unread != 5 {
			t.Fatalf("page %d says %d unread, want 5", pages, page.Unread)
		}
		got = append(got, idsOf(page.Items)...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if pages > 5 {
			t.Fatal("the cursor never ends")
		}
	}
	if pages != 3 || strings.Join(got, ",") != strings.Join(order, ",") {
		t.Fatalf("paged %d pages: %v, want %v", pages, got, order)
	}

	unread := f.feed(sara.ID, "", 20, true)
	if len(unread.Items) != 5 || unread.Items[0].ID != order[1] || unread.Unread != 5 {
		t.Fatalf("unread only: %+v", idsOf(unread.Items))
	}
	badge := f.feed(sara.ID, "", 1, false)
	if len(badge.Items) != 1 || badge.Unread != 5 || badge.NextCursor == nil {
		t.Fatalf("the badge's read: %+v", badge)
	}
}

func TestTheFeedRefusesWhatItNeverHandedOut(t *testing.T) {
	f := newFixture(t)
	sara := f.player("Sara")
	for _, limit := range []int{0, -1, 51} {
		_, err := f.in.Feed(f.ctx, sara.ID, "", limit, false)
		wantField(t, fmt.Sprintf("limit %d", limit), err, "limit", "1_to_50")
	}
	for _, cursor := range []string{
		"nope",
		base64.RawURLEncoding.EncodeToString([]byte("123.room_7f3k9d2m8q1x4b6n")),
		base64.RawURLEncoding.EncodeToString([]byte("x.ntf_7f3k9d2m8q1x4b6n")),
		base64.RawURLEncoding.EncodeToString([]byte("-5.ntf_7f3k9d2m8q1x4b6n")),
		strings.Repeat("a", 200),
	} {
		_, err := f.in.Feed(f.ctx, sara.ID, cursor, 20, false)
		wantField(t, "cursor "+cursor, err, "cursor", "invalid")
	}
}

// Marking read: the ones named, the caller's only, an earlier read kept; or
// all of them.
func TestMarkingRead(t *testing.T) {
	f := newFixture(t)
	sara, zayn := f.player("Sara"), f.player("Zayn")
	a := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "١"})
	b := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "٢"})
	c := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "٣"})
	theirs := f.raise(Notice{PlayerID: zayn.ID, Kind: enum.NoticeSystem, Title: "لزين"})

	err := f.in.Read(f.ctx, sara.ID, nil, false)
	wantField(t, "no ids", err, "ids", "required")
	many := make([]string, 101)
	for i := range many {
		many[i] = a.ID
	}
	err = f.in.Read(f.ctx, sara.ID, many, false)
	wantField(t, "101 ids", err, "ids", "up_to_100")

	earlier := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	if err := f.db.Exec(`UPDATE notifications SET read_at = ? WHERE id = ?`, earlier, b.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.in.Read(f.ctx, sara.ID, []string{a.ID, b.ID, theirs.ID, "ntf_nobodyxxxxxxxxx"}, false); err != nil {
		t.Fatal(err)
	}
	if f.row(a.ID).ReadAt == nil || f.row(c.ID).ReadAt != nil || f.row(theirs.ID).ReadAt != nil {
		t.Fatal("read the wrong ones")
	}
	if got := f.row(b.ID).ReadAt; got == nil || !got.Equal(earlier) {
		t.Fatalf("an earlier read moved to %v", got)
	}

	if err := f.in.Read(f.ctx, sara.ID, nil, true); err != nil {
		t.Fatal(err)
	}
	if got := f.feed(sara.ID, "", 20, false); got.Unread != 0 {
		t.Fatalf("after all: %d unread", got.Unread)
	}
	if f.row(theirs.ID).ReadAt != nil {
		t.Fatal("all read somebody else's")
	}
}

// Withdraw takes the line a key names out of the inbox; Settle marks it
// read. Neither touches another key, another recipient or another game.
func TestWithdrawAndSettleByKey(t *testing.T) {
	f := newFixture(t)
	sara, zayn := f.player("Sara"), f.player("Zayn")
	req := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeFriendRequest, ActorID: zayn.ID, Title: "طلب صداقة", Key: "friend_request:" + zayn.ID})
	game := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeGame, GameID: xo, Title: "دورك", Key: "friend_request:" + zayn.ID})
	other := f.raise(Notice{PlayerID: zayn.ID, Kind: enum.NoticeFriendRequest, ActorID: sara.ID, Title: "طلب صداقة", Key: "friend_request:" + sara.ID})

	if err := f.in.Settle(f.ctx, sara.ID, "", "friend_request:"+zayn.ID); err != nil {
		t.Fatal(err)
	}
	if f.row(req.ID).ReadAt == nil || f.row(game.ID).ReadAt != nil {
		t.Fatal("settle marked the wrong line")
	}
	if err := f.in.Withdraw(f.ctx, sara.ID, "", "friend_request:"+zayn.ID); err != nil {
		t.Fatal(err)
	}
	var left []string
	f.db.Model(&model.Notification{}).Pluck("id", &left)
	want := []string{game.ID, other.ID}
	slices.Sort(left)
	slices.Sort(want)
	if !slices.Equal(left, want) {
		t.Fatalf("after the withdraw: %v, want %v", left, want)
	}
}

// The housekeeping keeps 90 days, counted from the last raise.
func TestTheSweepForgetsTheOld(t *testing.T) {
	f := newFixture(t)
	sara := f.player("Sara")
	old := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "قديم"})
	f.at(old.ID, time.Now().Add(-Retention-time.Hour))
	kept := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "جديد"})
	f.at(kept.ID, time.Now().Add(-Retention+time.Hour))

	n, err := f.in.Sweep(f.ctx, time.Now().Add(-Retention))
	if err != nil || n != 1 {
		t.Fatalf("sweep: %d, %v", n, err)
	}
	if got := f.feed(sara.ID, "", 20, false); len(got.Items) != 1 || got.Items[0].ID != kept.ID {
		t.Fatalf("after the sweep: %v", idsOf(got.Items))
	}
}
