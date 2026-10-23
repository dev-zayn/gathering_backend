package push_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/friends"
	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/guests"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/biz/push"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/fcm"
	"github.com/jamaa/gathering/backend/internal/pkg/fcm/fcmtest"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// docs/PUSH.md: every notification the inbox gains goes to its recipient's
// live phones after its write commits, whatever raised it, in the message
// the app is built to; a token Firebase calls gone takes its phone with it;
// a 429 or a 5xx is tried once more; eight sends at a time; without a key a
// push is a "would push" line.

const xo = "fixture.xo"

// wait is how long a push may take to reach the fake: far longer than it
// ever does, so a slow machine does not fail the test.
const wait = 5 * time.Second

// lines is the log, kept to be read.
type lines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// of is every logged line whose message is msg, read as JSON.
func (l *lines) of(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("a log line that is not JSON: %s", line)
		}
		if m["msg"] == msg {
			out = append(out, m)
		}
	}
	return out
}

type fixture struct {
	t        *testing.T
	ctx      context.Context
	db       *gorm.DB
	fake     *fcmtest.Fake
	logs     *lines
	push     *push.Service
	devices  *identity.Devices
	sessions *identity.SessionStore
	in       *inbox.Inbox
	friends  *friends.Service
	rooms    *rooms.Service
}

// newFixture is the pool over a fresh database, sending to a fake Firebase
// with the fake's key, or with keyless set to no key at all.
func newFixture(t *testing.T, keyless bool, o push.Options) *fixture {
	t.Helper()
	db := biztest.DB(t)
	m := model.Manifest{Name: "إكس أو", Version: "1.0.0", Entry: "https://games.yourstudio.example/xo/"}
	m.Publisher.Tier = enum.TierPartner
	m.Room.Players.Min, m.Room.Players.Max = 2, 8
	m.Room.Host, m.Room.Pace = enum.HostPeer, enum.PaceTurn
	biztest.SeedGame(t, db, xo, m)

	f := &fixture{t: t, ctx: context.Background(), db: db, logs: &lines{},
		devices: identity.NewDevices(db), sessions: identity.NewSessionStore(db)}
	logger := log.NewWithOptions(f.logs, log.Options{Level: log.DebugLevel, Formatter: log.JSONFormatter})

	var sender push.Sender
	if !keyless {
		f.fake = fcmtest.New(t)
		c, err := fcm.New(fcm.Config{ProjectID: "gathering-supergame", Credentials: f.fake.ServiceAccount(), Endpoint: f.fake.URL})
		if err != nil {
			t.Fatal(err)
		}
		sender = c
	}
	if o.Backoff == 0 {
		o.Backoff = 10 * time.Millisecond
	}
	f.push = push.New(db, sender, f.devices, logger, o)
	t.Cleanup(func() { f.push.Close(context.Background()) })

	f.in = inbox.New(db)
	f.in.Pusher = f.push
	f.friends = friends.New(db, identity.NewPlayerStore(db), f.in)
	f.rooms = rooms.New(db, rooms.Config{Alphabet: "ABCDEFGHJKLMNPQRSTUVWXYZ23456789", MaxMembers: 64, TicketTTL: 10 * time.Minute})
	f.rooms.Friends, f.rooms.Inbox = f.friends, f.in
	return f
}

func (f *fixture) player(nickname string) *model.Player {
	f.t.Helper()
	return biztest.SeedPlayer(f.t, f.db, "", nickname)
}

// phone signs the player in on a session of its own and registers a phone
// for it, answering the session and the phone.
func (f *fixture) phone(p *model.Player, platform enum.Platform) (string, *model.Device) {
	f.t.Helper()
	sess := &model.Session{ID: ids.New(ids.Session), PlayerID: p.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := f.sessions.Create(f.ctx, sess); err != nil {
		f.t.Fatal(err)
	}
	d, err := f.devices.Register(f.ctx, sess.ID, p.ID, platform, "fcm-"+sess.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return sess.ID, d
}

// befriend is a asking and b accepting.
func (f *fixture) befriend(a, b *model.Player) {
	f.t.Helper()
	if _, _, err := f.friends.Request(f.ctx, a.ID, b.ID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.friends.Accept(f.ctx, b.ID, a.ID); err != nil {
		f.t.Fatal(err)
	}
}

// raise is a notification straight into the inbox.
func (f *fixture) raise(n inbox.Notice) string {
	f.t.Helper()
	got, err := f.in.Raise(f.ctx, n)
	if err != nil {
		f.t.Fatal(err)
	}
	return got.ID
}

// next waits for the n-th message to a token (1 is the first) and answers it.
func (f *fixture) next(token string, n int) fcmtest.Sent {
	f.t.Helper()
	deadline := time.Now().Add(wait)
	for {
		if got := f.fake.To(token); len(got) >= n {
			return got[n-1]
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("%d messages to the phone after %s, want %d", len(f.fake.To(token)), wait, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// drained is the pool finishing everything handed to it, its workers'
// last steps (forgetting a phone, the log line) included: the close drains.
func (f *fixture) drained() {
	f.t.Helper()
	f.push.Close(context.Background())
}

// rows is the device rows there are.
func (f *fixture) rows() map[string]bool {
	f.t.Helper()
	var all []model.Device
	if err := f.db.Find(&all).Error; err != nil {
		f.t.Fatal(err)
	}
	out := map[string]bool{}
	for _, d := range all {
		out[d.ID] = true
	}
	return out
}

// wantMessage compares a message as it arrived with the one wanted, whole.
func wantMessage(t *testing.T, what string, got fcmtest.Sent, want map[string]any) {
	t.Helper()
	g, _ := json.Marshal(got.Message)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("%s:\n got %s\nwant %s", what, g, w)
	}
}

// shape is the message docs/PUSH.md draws, for the fields given.
func shape(token, title, body string, data map[string]string, collapse string, badge int) map[string]any {
	d := map[string]any{}
	for k, v := range data {
		d[k] = v
	}
	return map[string]any{
		"token":        token,
		"notification": map[string]any{"title": title, "body": body},
		"data":         d,
		"android": map[string]any{"priority": "HIGH",
			"notification": map[string]any{"channel_id": "inbox", "tag": collapse}},
		"apns": map[string]any{"headers": map[string]any{"apns-collapse-id": collapse},
			"payload": map[string]any{"aps": map[string]any{"sound": "default", "badge": badge}}},
	}
}

// A friend's request and its accept, the accept that a second request makes,
// each reach the other player's phone in the message the app is built to:
// the row's words, the row's data, collapsing on the row's key, the badge
// the unread count.
func TestFriendsArePushed(t *testing.T) {
	f := newFixture(t, false, push.Options{})
	zayn, sara, ali := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	_, zaynPhone := f.phone(zayn, enum.PlatformIOS)
	_, saraPhone := f.phone(sara, enum.PlatformAndroid)
	_, aliPhone := f.phone(ali, enum.PlatformAndroid)

	if _, _, err := f.friends.Request(f.ctx, zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	req := f.next(saraPhone.FCMToken, 1)
	id, _ := req.Message["data"].(map[string]any)["notification"].(string)
	if !ids.Is(ids.Notification, id) {
		t.Fatalf("the request's notification: %v", req.Message["data"])
	}
	wantMessage(t, "the request", req, shape(saraPhone.FCMToken, "طلب صداقة", "Zayn يريد يصير صديقك",
		map[string]string{"notification": id, "kind": "friend_request", "player": zayn.ID},
		"friend_request:"+zayn.ID, 1))
	if req.Project != "gathering-supergame" {
		t.Fatalf("sent under %s", req.Project)
	}

	if err := f.friends.Accept(f.ctx, sara.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	acc := f.next(zaynPhone.FCMToken, 1)
	id, _ = acc.Message["data"].(map[string]any)["notification"].(string)
	wantMessage(t, "the accept", acc, shape(zaynPhone.FCMToken, "صرتوا أصدقاء", "Sara قبل طلب صداقتك",
		map[string]string{"notification": id, "kind": "friend_accepted", "player": sara.ID},
		"friend_accepted:"+sara.ID, 1))

	// Two who each ask: the second request is an accept, pushed as one.
	if _, _, err := f.friends.Request(f.ctx, zayn.ID, ali.ID); err != nil {
		t.Fatal(err)
	}
	f.next(aliPhone.FCMToken, 1)
	if _, _, err := f.friends.Request(f.ctx, ali.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	auto := f.next(zaynPhone.FCMToken, 2)
	id, _ = auto.Message["data"].(map[string]any)["notification"].(string)
	wantMessage(t, "the accept a second request made", auto, shape(zaynPhone.FCMToken, "صرتوا أصدقاء", "Ali قبل طلب صداقتك",
		map[string]string{"notification": id, "kind": "friend_accepted", "player": ali.ID},
		"friend_accepted:"+ali.ID, 2))
}

// An invite reaches the friend's phone with the room's link, code and game;
// asked again by another member, the same notification is pushed again under
// the same collapse, so the phone shows one invite, naming who asked last.
func TestAnInviteIsPushedAndPushedAgain(t *testing.T) {
	f := newFixture(t, false, push.Options{})
	zayn, sara, huda := f.player("Zayn"), f.player("Sara"), f.player("Huda")
	_, saraPhone := f.phone(sara, enum.PlatformIOS)
	f.befriend(zayn, sara)
	f.befriend(huda, sara)
	f.befriend(zayn, huda)
	if err := f.in.Read(f.ctx, sara.ID, nil, true); err != nil {
		t.Fatal(err)
	}
	f.next(saraPhone.FCMToken, 2) // the two requests
	before := 2

	r, err := f.rooms.Create(f.ctx, zayn.ID, rooms.CreateRequest{Game: xo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.rooms.Join(f.ctx, huda.ID, r.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rooms.Invite(f.ctx, zayn.ID, r.ID, []string{sara.ID}); err != nil {
		t.Fatal(err)
	}
	first := f.next(saraPhone.FCMToken, before+1)
	id, _ := first.Message["data"].(map[string]any)["notification"].(string)
	data := map[string]string{"notification": id, "kind": "room_invite", "deep_link": "/r/" + r.Code,
		"room": r.ID, "code": r.Code, "game": xo}
	wantMessage(t, "the invite", first, shape(saraPhone.FCMToken, "دعوة للعب", "Zayn يدعوك تلعب إكس أو",
		data, "room_invite:"+r.ID, 1))

	if _, err := f.rooms.Invite(f.ctx, huda.ID, r.ID, []string{sara.ID}); err != nil {
		t.Fatal(err)
	}
	again := f.next(saraPhone.FCMToken, before+2)
	wantMessage(t, "the invite again", again, shape(saraPhone.FCMToken, "دعوة للعب", "Huda يدعوك تلعب إكس أو",
		data, "room_invite:"+r.ID, 1))
}

// A game's own notification carries its link and its game, and collapses on
// the row's id, which its key keeps: the same key raised again replaces the
// push, and a game's key is never a collapse another game or the platform
// could share.
func TestAGamesNotificationIsPushed(t *testing.T) {
	f := newFixture(t, false, push.Options{})
	sara := f.player("Sara")
	_, saraPhone := f.phone(sara, enum.PlatformAndroid)
	biztest.SeedLibrary(t, f.db, sara.ID, xo)
	bridge := &guests.Bridge{Players: identity.NewPlayerStore(f.db), Games: games.NewCatalogue(f.db, nil, nil), Inbox: f.in}
	caller := guests.Caller{PlayerID: sara.ID, GameID: xo, Scopes: []string{guests.ScopeNotifySend}, Live: true}

	raised, err := bridge.Notify(f.ctx, caller, guests.Notice{Title: "دورك يا بطل", Body: "مريم لعبت، دورك",
		DeepLink: "/match/42", Key: "friend_request:" + sara.ID})
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]string{"notification": raised.ID, "kind": "game", "deep_link": "/match/42", "game": xo}
	wantMessage(t, "the game's", f.next(saraPhone.FCMToken, 1),
		shape(saraPhone.FCMToken, "دورك يا بطل", "مريم لعبت، دورك", data, raised.ID, 1))

	again, err := bridge.Notify(f.ctx, caller, guests.Notice{Title: "دورك يا بطل", Body: "لا تتأخر",
		DeepLink: "/match/42", Key: "friend_request:" + sara.ID})
	if err != nil || again.ID != raised.ID || again.Created {
		t.Fatalf("the same key: %+v %v", again, err)
	}
	wantMessage(t, "the game's again", f.next(saraPhone.FCMToken, 2),
		shape(saraPhone.FCMToken, "دورك يا بطل", "لا تتأخر", data, raised.ID, 1))

	// No body, no link: the body is the row's empty one, and no deep_link.
	bare, err := bridge.Notify(f.ctx, caller, guests.Notice{Title: "جولة جديدة"})
	if err != nil {
		t.Fatal(err)
	}
	wantMessage(t, "a bare one", f.next(saraPhone.FCMToken, 3), shape(saraPhone.FCMToken, "جولة جديدة", "",
		map[string]string{"notification": bare.ID, "kind": "game", "game": xo}, bare.ID, 2))
}

// An announcement is one push per recipient's phone, each its own row's,
// with the announcement and the game it opens.
func TestAnAnnouncementIsPushedToEveryPhone(t *testing.T) {
	f := newFixture(t, false, push.Options{})
	admin := biztest.SeedAdmin(t, f.db, "dev:sweep", "Sweep")
	zayn, sara, mona := f.player("Zayn"), f.player("Sara"), f.player("Mona")
	_, zaynPhone := f.phone(zayn, enum.PlatformIOS)
	_, saraIOS := f.phone(sara, enum.PlatformIOS)
	_, saraAndroid := f.phone(sara, enum.PlatformAndroid)

	sent, err := f.in.Announce(f.ctx, admin.ID, inbox.Announce{
		Audience: &inbox.AnnouncementAudience{Kind: inbox.AudiencePlayers, Players: []string{zayn.ID, sara.ID, mona.ID}},
		Title:    "صيانة الليلة", Body: "نرجع بعد ساعة", Game: xo,
	})
	if err != nil || sent.Reach != 3 {
		t.Fatalf("announce: %+v %v", sent, err)
	}
	var rows []model.Notification
	if err := f.db.Where("key = ?", "announcement:"+sent.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	row := map[string]string{}
	for _, r := range rows {
		row[r.PlayerID] = r.ID
	}
	for _, tc := range []struct {
		player string
		token  string
	}{{zayn.ID, zaynPhone.FCMToken}, {sara.ID, saraIOS.FCMToken}, {sara.ID, saraAndroid.FCMToken}} {
		wantMessage(t, "the announcement", f.next(tc.token, 1), shape(tc.token, "صيانة الليلة", "نرجع بعد ساعة",
			map[string]string{"notification": row[tc.player], "kind": "system", "announcement": sent.ID, "game": xo},
			"announcement:"+sent.ID, 1))
	}
	f.drained()
	if n := len(f.fake.Sent()); n != 3 {
		t.Fatalf("%d messages, want one per phone", n)
	}
}

// The badge is the recipient's unread count once the write is there.
func TestTheBadgeIsTheUnreadCount(t *testing.T) {
	f := newFixture(t, false, push.Options{})
	sara := f.player("Sara")
	_, phone := f.phone(sara, enum.PlatformIOS)
	badge := func(s fcmtest.Sent) any {
		return s.Message["apns"].(map[string]any)["payload"].(map[string]any)["aps"].(map[string]any)["badge"]
	}

	first := f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "واحد"})
	if b := badge(f.next(phone.FCMToken, 1)); b != float64(1) {
		t.Fatalf("the first: badge %v", b)
	}
	f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "اثنين"})
	if b := badge(f.next(phone.FCMToken, 2)); b != float64(2) {
		t.Fatalf("the second: badge %v", b)
	}
	if err := f.in.Read(f.ctx, sara.ID, []string{first}, false); err != nil {
		t.Fatal(err)
	}
	f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "ثلاثة"})
	if b := badge(f.next(phone.FCMToken, 3)); b != float64(2) {
		t.Fatalf("after reading one: badge %v", b)
	}
}

// A token Firebase calls gone (UNREGISTERED, a 404 for the token, or
// INVALID_ARGUMENT naming it) takes its phone with it; a bad message, or
// another project's token, keeps it and is logged by the device's id. No
// token is in the log.
func TestAGoneTokenTakesItsPhone(t *testing.T) {
	f := newFixture(t, false, push.Options{})
	sara := f.player("Sara")
	phones := map[string]*model.Device{}
	for _, name := range []string{"unregistered", "not found", "invalid token", "invalid message", "mismatch", "fine"} {
		_, phones[name] = f.phone(sara, enum.PlatformAndroid)
	}
	f.fake.Answer(phones["unregistered"].FCMToken, fcmtest.Unregistered)
	f.fake.Answer(phones["not found"].FCMToken, fcmtest.NotFound)
	f.fake.Answer(phones["invalid token"].FCMToken, fcmtest.InvalidToken)
	f.fake.Answer(phones["invalid message"].FCMToken, fcmtest.InvalidMessage)
	f.fake.Answer(phones["mismatch"].FCMToken, fcmtest.SenderMismatch)

	id := f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	f.drained()
	for name, d := range phones {
		if n := len(f.fake.To(d.FCMToken)); n != 1 {
			t.Fatalf("%s: %d messages", name, n)
		}
	}

	rows := f.rows()
	for name, d := range phones {
		gone := name == "unregistered" || name == "not found" || name == "invalid token"
		if rows[d.ID] == gone {
			t.Fatalf("%s: the phone kept is %v", name, rows[d.ID])
		}
	}
	forgotten := map[string]bool{}
	for _, l := range f.logs.of(t, "push: the phone is gone, forgotten") {
		if l["notification"] != id {
			t.Fatalf("a forgotten phone's line: %v", l)
		}
		forgotten[l["device"].(string)] = true
	}
	failed := map[string]bool{}
	for _, l := range f.logs.of(t, "push failed") {
		failed[l["device"].(string)] = true
	}
	if len(forgotten) != 3 || !forgotten[phones["unregistered"].ID] || !forgotten[phones["not found"].ID] ||
		!forgotten[phones["invalid token"].ID] {
		t.Fatalf("forgotten: %v", forgotten)
	}
	if len(failed) != 2 || !failed[phones["invalid message"].ID] || !failed[phones["mismatch"].ID] {
		t.Fatalf("failed: %v", failed)
	}
	for _, d := range phones {
		if strings.Contains(f.logs.String(), d.FCMToken) {
			t.Fatalf("a token is in the log:\n%s", f.logs.String())
		}
	}
}

// A 429 or a 5xx is tried once more after a short wait: once is enough
// when the second goes through, and a second refusal is the last.
func TestBusyIsTriedOnceMore(t *testing.T) {
	f := newFixture(t, false, push.Options{Backoff: 50 * time.Millisecond})
	sara := f.player("Sara")
	_, recovers := f.phone(sara, enum.PlatformAndroid)
	_, staysBusy := f.phone(sara, enum.PlatformAndroid)
	_, failsThenGone := f.phone(sara, enum.PlatformAndroid)
	f.fake.Answer(recovers.FCMToken, fcmtest.Unavailable)
	f.fake.Answer(staysBusy.FCMToken, fcmtest.QuotaExceeded, fcmtest.QuotaExceeded, fcmtest.QuotaExceeded)
	f.fake.Answer(failsThenGone.FCMToken, fcmtest.Internal, fcmtest.Unregistered)

	f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	f.drained()

	if got := f.fake.To(recovers.FCMToken); len(got) != 2 || got[0].Status != 503 || got[1].Status != 200 {
		t.Fatalf("the one that recovers: %+v", got)
	}
	if got := f.fake.To(staysBusy.FCMToken); len(got) != 2 || got[0].Status != 429 || got[1].Status != 429 {
		t.Fatalf("the one that stays busy was tried %d times, want 2", len(got))
	}
	if got := f.fake.To(failsThenGone.FCMToken); len(got) != 2 || got[0].Status != 500 || got[1].Status != 404 {
		t.Fatalf("the one that fails, then is gone: %+v", got)
	}
	rows := f.rows()
	if !rows[recovers.ID] || !rows[staysBusy.ID] || rows[failsThenGone.ID] {
		t.Fatalf("the phones kept: %v", rows)
	}
	failed := f.logs.of(t, "push failed")
	if len(failed) != 1 || failed[0]["device"] != staysBusy.ID || !strings.Contains(failed[0]["err"].(string), "QUOTA_EXCEEDED") {
		t.Fatalf("failed: %v", failed)
	}
}

// Without a key nothing is sent: each notification is a "would push" line
// with its id and how many live phones it would have reached.
func TestWithoutAKeyAPushIsALogLine(t *testing.T) {
	f := newFixture(t, true, push.Options{})
	zayn, sara := f.player("Zayn"), f.player("Sara")
	f.phone(sara, enum.PlatformIOS)
	f.phone(sara, enum.PlatformAndroid)

	toSara := f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	toZayn := f.raise(inbox.Notice{PlayerID: zayn.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	f.drained()
	got := f.logs.of(t, "would push")
	if len(got) != 2 || got[0]["notification"] != toSara || got[0]["devices"] != float64(2) || got[0]["level"] != "info" ||
		got[1]["notification"] != toZayn || got[1]["devices"] != float64(0) {
		t.Fatalf("would push: %v", got)
	}
	if !strings.Contains(f.logs.String(), `"msg":"would push","notification":"`+toSara+`","devices":2}`) {
		t.Fatalf("the line as written:\n%s", f.logs.String())
	}
}

// A player switched off gets nothing, and neither does a phone whose session
// ended (signed out or revoked, or past its time); the same player's live
// phone does.
func TestASwitchedOffPlayerAndAnEndedSessionGetNothing(t *testing.T) {
	f := newFixture(t, false, push.Options{})
	zayn, sara := f.player("Zayn"), f.player("Sara")
	_, zaynPhone := f.phone(zayn, enum.PlatformAndroid)
	signedOut, outPhone := f.phone(sara, enum.PlatformAndroid)
	expired, expiredPhone := f.phone(sara, enum.PlatformIOS)
	_, livePhone := f.phone(sara, enum.PlatformIOS)
	if err := f.sessions.Revoke(f.ctx, signedOut); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute), expired).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, zayn.ID).Error; err != nil {
		t.Fatal(err)
	}

	f.raise(inbox.Notice{PlayerID: zayn.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	f.drained()
	if n := len(f.fake.To(livePhone.FCMToken)); n != 1 {
		t.Fatalf("the live phone: %d messages", n)
	}
	for name, d := range map[string]*model.Device{"switched off": zaynPhone, "signed out": outPhone, "expired": expiredPhone} {
		if n := len(f.fake.To(d.FCMToken)); n != 0 {
			t.Fatalf("%s: %d messages", name, n)
		}
	}
}

// Eight sends at a time, however many phones are waiting: docs/PUSH.md's
// number, not whatever the package says.
func TestEightSendsAtATime(t *testing.T) {
	const eight = 8
	f := newFixture(t, false, push.Options{})
	sara := f.player("Sara")
	for range 20 {
		f.phone(sara, enum.PlatformAndroid)
	}
	f.fake.Hold()
	f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "أهلا"})

	deadline := time.Now().Add(wait)
	for f.fake.InFlight() < eight && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // room for a ninth, were there one
	if n, most := f.fake.InFlight(), f.fake.MaxInFlight(); n != eight || most != eight {
		t.Fatalf("held: %d in flight, %d at most; want %d", n, most, eight)
	}
	f.fake.Release()
	f.fake.Wait(t, 20, wait)
	if most := f.fake.MaxInFlight(); most != eight {
		t.Fatalf("%d at most, want %d", most, eight)
	}
}

// Close finishes what was handed over, then takes nothing more; a close that
// runs out of time cancels the sends in flight and still returns.
func TestCloseDrainsThenStops(t *testing.T) {
	f := newFixture(t, false, push.Options{})
	sara := f.player("Sara")
	_, phone := f.phone(sara, enum.PlatformAndroid)
	for range 5 {
		f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	}
	f.push.Close(context.Background())
	if n := len(f.fake.To(phone.FCMToken)); n != 5 {
		t.Fatalf("drained %d of 5", n)
	}
	f.raise(inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "بعد الإغلاق"})
	if len(f.logs.of(t, "push after shutdown, dropped")) != 1 || len(f.fake.To(phone.FCMToken)) != 5 {
		t.Fatalf("a push after the close: %s", f.logs.String())
	}

	g := newFixture(t, false, push.Options{})
	mona := g.player("Mona")
	g.phone(mona, enum.PlatformAndroid)
	g.fake.Hold()
	g.raise(inbox.Notice{PlayerID: mona.ID, Kind: enum.NoticeSystem, Title: "أهلا"})
	deadline := time.Now().Add(wait)
	for g.fake.InFlight() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	g.push.Close(ctx)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a held send kept the close %s", took)
	}
	if len(g.logs.of(t, "push failed")) != 0 {
		t.Fatalf("an abandoned send logged as failed: %s", g.logs.String())
	}
}
