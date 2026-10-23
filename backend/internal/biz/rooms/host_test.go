package rooms

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

func TestStarting(t *testing.T) {
	f := newFixture(t)
	host, friend, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	r := f.room(host, xo)

	_, err := f.s.Start(f.ctx, host.ID, r.ID)
	wantCode(t, "one player of two", err, 409, "too_few_players")
	f.join(friend, r)
	_, err = f.s.Start(f.ctx, stranger.ID, r.ID)
	wantCode(t, "a stranger", err, 403, "not_member")
	_, err = f.s.Start(f.ctx, friend.ID, r.ID)
	wantCode(t, "a player", err, 403, "host_only")

	if r.Round != 0 {
		t.Fatalf("a fresh lobby is round %d", r.Round)
	}
	got, err := f.s.Start(f.ctx, host.ID, r.ID)
	if err != nil || got.Status != "playing" || got.StartedAt == nil || got.Round != 1 {
		t.Fatalf("start: %+v, %v", got, err)
	}
	frames := f.pub.published(r.ID)
	if len(frames) != 1 || len(f.pub.started(r.ID)) != 1 {
		t.Fatalf("frames = %v", kinds(frames))
	}
	if s := frames[0].(Started); s.By != host.ID || s.At != *got.StartedAt || s.Round != 1 {
		t.Fatalf("started = %+v", s)
	}
	_, err = f.s.Start(f.ctx, host.ID, r.ID)
	wantCode(t, "starting twice", err, 409, "room_started")

	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Start(f.ctx, host.ID, r.ID)
	wantCode(t, "an ended room", err, 409, "room_closed")
}

// The end carries the scores; a missing rank is computed by score, highest
// first, ties sharing a rank; a former member may be scored, a stranger not.
// The live state is copied into the row, and the relay is told to end the
// room, which closes every socket.
func TestEndingWithScores(t *testing.T) {
	f := newFixture(t)
	host, a, b, c, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Huda"), f.player("Omar")
	r := f.room(host, tap)
	for _, p := range []string{a.ID, b.ID, c.ID} {
		if _, err := f.s.Join(f.ctx, p, r.Code, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.Leave(f.ctx, c.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	f.start(host, r)

	two := 2
	_, err := f.s.End(f.ctx, a.ID, r.ID, nil)
	wantCode(t, "a player ending", err, 403, "host_only")
	_, err = f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: stranger.ID, Score: 1}})
	wantField(t, "a stranger scored", err, "scores.0.player", "not_a_member")
	_, err = f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: a.ID, Score: 1}, {Player: a.ID, Score: 2}})
	wantField(t, "a player scored twice", err, "scores.1.player", "duplicate")
	zero := 0
	_, err = f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: a.ID, Score: 1, Rank: &zero}})
	wantField(t, "rank zero", err, "scores.0.rank", "must_be_positive")

	got, err := f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{
		{Player: host.ID, Score: 10},
		{Player: a.ID, Score: 30},
		{Player: b.ID, Score: 10},
		{Player: c.ID, Score: 5, Rank: &two}, // left before the end, still scored; the game's own rank kept
	})
	if err != nil {
		t.Fatal(err)
	}
	// The room reads its results ranked, ties in seat order (the host sits
	// in 0, c sat in 3 before leaving); the ended frame keeps the order sent.
	want := []string{a.ID + ":30:1", host.ID + ":10:2", b.ID + ":10:2", c.ID + ":5:2"}
	if len(got.Results) != 4 {
		t.Fatalf("results = %+v", got.Results)
	}
	for i, s := range got.Results {
		if line := s.Player + ":" + itoa(int(s.Score)) + ":" + itoa(s.Rank); line != want[i] {
			t.Fatalf("result %d = %s, want %s", i, line, want[i])
		}
	}
	if got.Status != "ended" || got.EndReason == nil || *got.EndReason != "host" || got.EndedAt == nil {
		t.Fatalf("ended = %+v", got)
	}

	ends := f.pub.ended(r.ID)
	if len(ends) != 1 || ends[0].By == nil || *ends[0].By != host.ID || ends[0].Reason != "host" || ends[0].Round != 1 ||
		len(ends[0].Results) != 4 {
		t.Fatalf("the relay was told %+v", ends)
	}
	sent := []string{host.ID + ":10:2", a.ID + ":30:1", b.ID + ":10:2", c.ID + ":5:2"}
	for i, s := range ends[0].Results {
		if line := s.Player + ":" + itoa(int(s.Score)) + ":" + itoa(s.Rank); line != sent[i] {
			t.Fatalf("ended frame result %d = %s, want %s", i, line, sent[i])
		}
	}

	// Over: nobody's room any more, and nothing more to do in it.
	for _, p := range []string{host.ID, a.ID, b.ID} {
		if mine, _ := f.s.Mine(f.ctx, p); len(mine) != 0 {
			t.Fatalf("%s is still in %d rooms", p, len(mine))
		}
	}
	_, err = f.s.End(f.ctx, host.ID, r.ID, nil)
	wantCode(t, "ending twice", err, 409, "room_closed")
	// The end, then the results it wrote.
	if got := f.events(r.ID); strings.Join(got[len(got)-2:], ",") != "ended,results" {
		t.Fatalf("events = %v", got)
	}

	// Free to make the next one.
	if _, err := f.s.Create(f.ctx, host.ID, CreateRequest{Game: xo}); err != nil {
		t.Fatalf("a room after the end: %v", err)
	}
}

// Ending copies the live state out of the relay into the row: the last data
// and its version, which the room then answers.
func TestEndingKeepsTheLiveState(t *testing.T) {
	f := newFixture(t)
	host := f.player("Zayn")
	r := f.room(host, xo) // state: blob
	f.pub.setState(r.ID, 7, `{"round":3}`)

	got, err := f.s.End(f.ctx, host.ID, r.ID, nil)
	if err != nil || got.StateVersion != 7 {
		t.Fatalf("ended = %+v, %v", got, err)
	}
	var state struct {
		State        json.RawMessage
		StateVersion int
	}
	if err := f.db.Raw(`SELECT state, state_version FROM rooms WHERE id = ?`, r.ID).Scan(&state).Error; err != nil {
		t.Fatal(err)
	}
	var data map[string]int
	if err := json.Unmarshal(state.State, &data); err != nil || data["round"] != 3 || state.StateVersion != 7 {
		t.Fatalf("state kept = %s v%d", state.State, state.StateVersion)
	}
}

// A room with no shared state keeps none when it ends.
func TestEndingARoomWithoutState(t *testing.T) {
	f := newFixture(t)
	host := f.player("Zayn")
	r := f.room(host, tap) // state: none
	f.pub.setState(r.ID, 3, `{"x":1}`)
	got, err := f.s.End(f.ctx, host.ID, r.ID, nil)
	if err != nil || got.StateVersion != 0 || len(got.Results) != 0 || got.Results == nil {
		t.Fatalf("ended = %+v, %v", got, err)
	}
}

func TestHandingOverTheHost(t *testing.T) {
	f := newFixture(t)
	host, friend, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	r := f.room(host, tap)
	f.join(friend, r)

	_, err := f.s.Host(f.ctx, friend.ID, r.ID, friend.ID)
	wantCode(t, "a player taking the host", err, 403, "host_only")
	_, err = f.s.Host(f.ctx, host.ID, r.ID, stranger.ID)
	wantField(t, "to a stranger", err, "player", "not_a_member")

	// To yourself: nothing changes and nobody is told.
	same, err := f.s.Host(f.ctx, host.ID, r.ID, host.ID)
	if err != nil || same.Host != host.ID || len(f.pub.published(r.ID)) != 0 {
		t.Fatalf("to yourself: %+v, %v", same, err)
	}

	got, err := f.s.Host(f.ctx, host.ID, r.ID, friend.ID)
	if err != nil || got.Host != friend.ID || got.You.Role != "player" || got.Members[1].Role != "host" {
		t.Fatalf("handed over: %+v, %v", got, err)
	}
	frames := f.pub.published(r.ID)
	if strings.Join(kinds(frames), ",") != "host,members" {
		t.Fatalf("frames = %v", kinds(frames))
	}
	if h := frames[0].(HostChanged); h.Player != friend.ID || h.By == nil || *h.By != host.ID {
		t.Fatalf("host = %+v", h)
	}
	if _, err := f.s.Start(f.ctx, host.ID, r.ID); codeOf(err) != "host_only" {
		t.Fatalf("the old host started: %v", err)
	}
	if got := f.events(r.ID); strings.Join(got, ",") != "created,joined,host" {
		t.Fatalf("events = %v", got)
	}
}

// The ticket is a JWT the game's server verifies through the JWKS: who, the
// game, the room, the role, the seat — seat 0 included — and it is a room
// ticket by both scope and kind.
func TestTheTicket(t *testing.T) {
	f := newFixture(t)
	host, friend, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	r := f.room(host, xo)
	f.join(friend, r)

	tk, err := f.s.Ticket(f.ctx, host.ID, r.ID)
	if err != nil || tk.ExpiresIn != 600 {
		t.Fatalf("ticket: %+v, %v", tk, err)
	}
	claims, err := f.signer.Parse(tk.Token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != host.ID || claims.Game() != xo || claims.Room != r.ID || claims.Role != "host" ||
		claims.Seat == nil || *claims.Seat != 0 || claims.Round == nil || *claims.Round != 0 ||
		claims.Scope != caller.ScopeRoom || claims.Kind != caller.ScopeRoom {
		t.Fatalf("claims = %+v", claims)
	}
	payload, _ := base64.RawURLEncoding.DecodeString(strings.Split(tk.Token, ".")[1])
	if !strings.Contains(string(payload), `"seat":0`) || !strings.Contains(string(payload), `"round":0`) {
		t.Fatalf("the host's seat or the lobby's round is not in the ticket: %s", payload)
	}
	tk2, _ := f.s.Ticket(f.ctx, friend.ID, r.ID)
	if c2, _ := f.signer.Parse(tk2.Token); c2.Role != "player" || *c2.Seat != 1 || *c2.Round != 0 {
		t.Fatalf("friend's claims = %+v", c2)
	}

	// The round when it was minted: the one playing, then the one just
	// finished while the room waits in its lobby.
	f.start(host, r)
	tk3, _ := f.s.Ticket(f.ctx, friend.ID, r.ID)
	if c3, _ := f.signer.Parse(tk3.Token); c3.Round == nil || *c3.Round != 1 {
		t.Fatalf("a ticket in round 1 = %+v", c3)
	}
	if _, err := f.s.Finish(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.start(host, r)
	tk4, _ := f.s.Ticket(f.ctx, host.ID, r.ID)
	if c4, _ := f.signer.Parse(tk4.Token); c4.Round == nil || *c4.Round != 2 {
		t.Fatalf("a ticket in round 2 = %+v", c4)
	}

	_, err = f.s.Ticket(f.ctx, stranger.ID, r.ID)
	wantCode(t, "a stranger", err, 403, "not_member")
	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Ticket(f.ctx, host.ID, r.ID)
	wantCode(t, "an ended room", err, 409, "room_closed")
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
