package rooms

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// docs/ROOMS.md, "Rounds": a room plays again and again. A start begins a
// round, a finish puts the room back in its lobby with the same code, members
// and sockets, and only an end closes it. Results are per round.

// finish is the host finishing the round, and forgets the frames.
func (f *fixture) finish(host *model.Player, r *Room, scores ...ScoreIn) *Room {
	f.t.Helper()
	got, err := f.s.Finish(f.ctx, host.ID, r.ID, scores)
	if err != nil {
		f.t.Fatalf("finish: %v", err)
	}
	f.pub.published(r.ID)
	return got
}

// roundRows is one round's lines in the results table, ranked, ties in seat
// order.
func (f *fixture) roundRows(roomID string, round int) []model.Result {
	f.t.Helper()
	var out []model.Result
	err := f.db.Raw(`SELECT * FROM results WHERE room_id = ? AND round = ? ORDER BY rank, seat, player_id`,
		roomID, round).Scan(&out).Error
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// detailOf is the detail of the room's last event of a kind, as text.
func (f *fixture) detailOf(roomID string, kind enum.RoomEvent) string {
	f.t.Helper()
	lines := f.eventLines(roomID)
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Event == string(kind) {
			return lines[i].Detail
		}
	}
	f.t.Fatalf("no %s event in %+v", kind, lines)
	return ""
}

// roundNo is a round named in a submission.
func roundNo(n int) *int { return &n }

// Every start begins a round: the round counts on, the event and the frame
// say which, and the relay resets the shared state with the start. A finish
// puts the room back in its lobby with the same code and members, and the
// host starts the next round there.
func TestEveryStartBeginsARound(t *testing.T) {
	f := newFixture(t)
	host, friend := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(friend, r)

	for want := 1; want <= 3; want++ {
		f.pub.setState(r.ID, 5, `{"board":"x"}`)
		got, err := f.s.Start(f.ctx, host.ID, r.ID)
		if err != nil {
			t.Fatalf("start %d: %v", want, err)
		}
		if got.Status != "playing" || got.Round != want || got.Code != r.Code || len(got.Members) != 2 ||
			got.StateVersion != 0 || got.StartedAt == nil {
			t.Fatalf("round %d = %+v", want, got)
		}
		starts := f.pub.started(r.ID)
		if len(starts) != want || starts[want-1].Round != want || starts[want-1].By != host.ID ||
			starts[want-1].At != *got.StartedAt {
			t.Fatalf("the relay was told %+v", starts)
		}
		if detail := f.detailOf(r.ID, enum.EventStarted); !strings.Contains(detail, `"round": `+itoa(want)) ||
			!strings.Contains(detail, `"members": 2`) {
			t.Fatalf("started detail = %s", detail)
		}

		back := f.finish(host, r)
		if back.Status != "lobby" || back.Round != want || back.Code != r.Code || len(back.Members) != 2 ||
			back.You == nil || back.You.Role != "host" {
			t.Fatalf("after round %d = %+v", want, back)
		}
		if mine, err := f.s.Mine(f.ctx, friend.ID); err != nil || len(mine) != 1 || mine[0].ID != r.ID {
			t.Fatalf("the friend's room after round %d: %+v, %v", want, mine, err)
		}
	}
	if len(f.pub.ended(r.ID)) != 0 {
		t.Fatal("a finish ended the room")
	}
	if got := f.events(r.ID); strings.Join(got, ",") !=
		"created,joined,started,finished,started,finished,started,finished" {
		t.Fatalf("events = %v", got)
	}
}

// A finish is the host's, from a round under way, refused in the host-only
// order, then room_not_started, then the scores. It writes the round's
// results, carries them in finished in the order sent, keeps the room's copy,
// and closes nobody's socket.
func TestFinishingARound(t *testing.T) {
	f := newFixture(t)
	host, a, b, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Omar")
	r := f.room(host, tap)
	f.join(a, r)
	f.join(b, r)

	_, err := f.s.Finish(f.ctx, stranger.ID, r.ID, nil)
	wantCode(t, "a stranger", err, 403, "not_member")
	_, err = f.s.Finish(f.ctx, a.ID, r.ID, nil)
	wantCode(t, "a player", err, 403, "host_only")
	_, err = f.s.Finish(f.ctx, host.ID, r.ID, []ScoreIn{{Player: stranger.ID, Score: 1}})
	wantCode(t, "a fresh lobby, before the scores", err, 409, "room_not_started")

	f.start(host, r)
	_, err = f.s.Finish(f.ctx, a.ID, r.ID, []ScoreIn{{Player: stranger.ID, Score: 1}})
	wantCode(t, "a player, before the scores", err, 403, "host_only")
	_, err = f.s.Finish(f.ctx, host.ID, r.ID, []ScoreIn{{Player: stranger.ID, Score: 1}})
	wantField(t, "a stranger scored", err, "scores.0.player", "not_a_member")
	_, err = f.s.Finish(f.ctx, host.ID, r.ID, []ScoreIn{{Player: a.ID, Score: 1}, {Player: a.ID, Score: 2}})
	wantField(t, "a player scored twice", err, "scores.1.player", "duplicate")
	zero := 0
	_, err = f.s.Finish(f.ctx, host.ID, r.ID, []ScoreIn{{Player: a.ID, Score: 1, Rank: &zero}})
	wantField(t, "rank zero", err, "scores.0.rank", "must_be_positive")
	if len(f.pub.published(r.ID)) != 0 || len(f.rows(r.ID)) != 0 {
		t.Fatal("a refused finish published or wrote something")
	}

	got, err := f.s.Finish(f.ctx, host.ID, r.ID, []ScoreIn{
		{Player: a.ID, Score: 2}, {Player: host.ID, Score: 9}, {Player: b.ID, Score: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := lines(line(host, 9, 1, "host"), line(a, 2, 2, "host"), line(b, 2, 2, "host"))
	if got.Status != "lobby" || got.Round != 1 || got.Code != r.Code || got.EndReason != nil || got.EndedAt != nil ||
		len(got.Members) != 3 || resultLines(got.Results) != want {
		t.Fatalf("finished = %+v", got)
	}
	frames := f.pub.published(r.ID)
	if len(frames) != 1 {
		t.Fatalf("frames = %v", kinds(frames))
	}
	sent := strings.Join([]string{scored(a, 2, 2), scored(host, 9, 1), scored(b, 2, 2)}, " ")
	fin := frames[0].(Finished)
	if fin.By == nil || *fin.By != host.ID || fin.Round != 1 || fin.Reason != "host" || fin.At == "" ||
		scoreLines(fin.Results) != sent {
		t.Fatalf("finished frame = %+v", fin)
	}
	if len(f.pub.ended(r.ID)) != 0 {
		t.Fatal("a finish ended the room")
	}
	rows := f.roundRows(r.ID, 1)
	if len(rows) != 3 || rows[0].Round != 1 || rows[0].SubmittedBy != enum.SubmitterHost || rows[0].Players != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	if s := scoreLines(f.copyOf(r.ID)); s != sent {
		t.Fatalf("rooms.results = %s", s)
	}
	ev := f.eventLines(r.ID)
	if ev[len(ev)-2].Event != "finished" || ev[len(ev)-1].Event != "results" || ev[len(ev)-2].PlayerID != host.ID {
		t.Fatalf("events = %+v", ev)
	}
	if d := ev[len(ev)-2].Detail; !strings.Contains(d, `"round": 1`) || !strings.Contains(d, `"reason": "host"`) ||
		!strings.Contains(d, `"scores": 3`) {
		t.Fatalf("finished detail = %s", d)
	}
	if d := ev[len(ev)-1].Detail; !strings.Contains(d, `"round": 1`) || !strings.Contains(d, `"by": "host"`) {
		t.Fatalf("results detail = %s", d)
	}

	// Back in the lobby there is no round to finish; a finish with no scores
	// writes none and still says the round is over.
	_, err = f.s.Finish(f.ctx, host.ID, r.ID, nil)
	wantCode(t, "a lobby between rounds", err, 409, "room_not_started")
	f.start(host, r)
	empty := f.finish(host, r)
	if empty.Round != 2 || empty.Results == nil || len(empty.Results) != 0 || len(f.roundRows(r.ID, 2)) != 0 {
		t.Fatalf("an empty finish = %+v", empty)
	}
	if c := f.copyOf(r.ID); c == nil || len(c) != 0 {
		t.Fatalf("rooms.results after an empty round = %v", c)
	}

	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Finish(f.ctx, host.ID, r.ID, nil)
	wantCode(t, "an ended room", err, 409, "room_closed")
}

// The game's server is the stronger word, round by round: what it wrote for
// a round stands against the host's finish, which carries it; another round
// is the host's again; and writing a round that is not the latest leaves
// the room's copy alone.
func TestTheGamesResultsComeFirstPerRound(t *testing.T) {
	f := newFixture(t)
	host, a := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(a, r)

	f.start(host, r)
	if _, err := f.submit(xo, a, r.ID, ScoreIn{Player: a.ID, Score: 4}, ScoreIn{Player: host.ID, Score: 4}); err != nil {
		t.Fatal(err)
	}
	written := f.roundRows(r.ID, 1)[0].FinishedAt
	got, err := f.s.Finish(f.ctx, host.ID, r.ID, []ScoreIn{{Player: host.ID, Score: 1}, {Player: a.ID, Score: 0}})
	if err != nil {
		t.Fatal(err)
	}
	tie := scored(host, 4, 1) + " " + scored(a, 4, 1)
	if fin := f.pub.published(r.ID)[0].(Finished); scoreLines(fin.Results) != tie {
		t.Fatalf("the finish carried %+v", fin)
	}
	if resultLines(got.Results) != lines(line(host, 4, 1, "game"), line(a, 4, 1, "game")) {
		t.Fatalf("the room reads %+v", got.Results)
	}
	if rows := f.roundRows(r.ID, 1); len(rows) != 2 || rows[0].SubmittedBy != enum.SubmitterGame || !rows[0].FinishedAt.Equal(written) {
		t.Fatalf("the host's finish touched the game's rows: %+v", rows)
	}
	if ev := f.resultsEvents(r.ID); strings.Join(ev, ";") != "results by game ("+a.ID+"): 2" {
		t.Fatalf("results events = %v", ev)
	}

	// Round 2 is the host's: the game wrote nothing for it.
	f.start(host, r)
	got = f.finish(host, r, ScoreIn{Player: host.ID, Score: 1}, ScoreIn{Player: a.ID, Score: 3})
	if got.Round != 2 || resultLines(got.Results) != lines(line(a, 3, 1, "host"), line(host, 1, 2, "host")) {
		t.Fatalf("round 2 = %+v", got)
	}
	finished2 := f.roundRows(r.ID, 2)[0].FinishedAt

	// The game's server rewrites round 1 later: its rows keep their finish,
	// and the room's copy, round 2's, stays.
	two := f.copyOf(r.ID)
	re, err := f.s.SubmitResults(f.ctx, Submission{GameID: xo, PlayerID: host.ID, RoomID: r.ID, Round: roundNo(1),
		Scores: []ScoreIn{{Player: host.ID, Score: 7}, {Player: a.ID, Score: 1}}})
	if err != nil || re.Round != 1 || resultLines(re.Results) != lines(line(host, 7, 1, "game"), line(a, 1, 2, "game")) {
		t.Fatalf("round 1 rewritten = %+v, %v", re, err)
	}
	if rows := f.roundRows(r.ID, 1); !rows[0].FinishedAt.Equal(written) || rows[0].Score != 7 {
		t.Fatalf("round 1's rows = %+v", rows)
	}
	if scoreLines(f.copyOf(r.ID)) != scoreLines(two) {
		t.Fatalf("an earlier round rewrote the copy: %v", f.copyOf(r.ID))
	}

	// Round 2, the latest, waiting in the lobby: by default, its own finish
	// kept, the copy rewritten to match, in the order sent.
	re, err = f.s.SubmitResults(f.ctx, Submission{GameID: xo, PlayerID: a.ID, RoomID: r.ID,
		Scores: []ScoreIn{{Player: a.ID, Score: 5}, {Player: host.ID, Score: 6}}})
	if err != nil || re.Round != 2 {
		t.Fatalf("round 2 by default = %+v, %v", re, err)
	}
	if rows := f.roundRows(r.ID, 2); !rows[0].FinishedAt.Equal(finished2) || rows[0].SubmittedBy != enum.SubmitterGame {
		t.Fatalf("round 2's rows = %+v", rows)
	}
	if s := scoreLines(f.copyOf(r.ID)); s != scored(a, 5, 2)+" "+scored(host, 6, 1) {
		t.Fatalf("rooms.results = %s", s)
	}
	if room, _ := f.s.Get(f.ctx, a.ID, r.ID); room.Status != "lobby" || resultLines(room.Results) !=
		lines(line(host, 6, 1, "game"), line(a, 5, 2, "game")) {
		t.Fatalf("the room reads %+v", room)
	}
	if len(f.pub.published(r.ID)) != 0 {
		t.Fatal("results from the game's server were published")
	}
}

// The game's server names a round, or takes the room's: the one playing, or
// the one just finished. Before the first round there is nothing to write; a
// round that is not one is refused with the scores' problems, all at once.
// A round under way keeps playing.
func TestTheBridgesRound(t *testing.T) {
	f := newFixture(t)
	host, a := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(a, r)
	one := []ScoreIn{{Player: host.ID, Score: 1}}
	submit := func(rd *int, scores []ScoreIn) (*Submitted, error) {
		return f.s.SubmitResults(f.ctx, Submission{GameID: xo, PlayerID: a.ID, RoomID: r.ID, Round: rd, Scores: scores})
	}

	_, err := submit(nil, one)
	wantCode(t, "round 0", err, 409, "room_not_started")
	_, err = submit(roundNo(1), one)
	wantCode(t, "round 1 of a room in round 0", err, 409, "room_not_started")

	f.start(host, r)
	before := time.Now().Add(-time.Second)
	got, err := submit(nil, one)
	if err != nil || got.Round != 1 {
		t.Fatalf("the round playing = %+v, %v", got, err)
	}
	if rows := f.roundRows(r.ID, 1); len(rows) != 1 || rows[0].FinishedAt.Before(before) {
		t.Fatalf("written into a round under way = %+v", rows)
	}
	if room, _ := f.s.Get(f.ctx, host.ID, r.ID); room.Status != "playing" || f.copyOf(r.ID) != nil {
		t.Fatalf("the round stopped: %+v", room)
	}
	for _, bad := range []int{0, -1, 2} {
		_, err = submit(roundNo(bad), []ScoreIn{{Player: "ply_nobody00000000000", Score: 1}})
		e := refusal(err)
		if e == nil || e.Fields["round"] != "unknown" || e.Fields["scores.0.player"] != "not_a_member" {
			t.Fatalf("round %d: %+v", bad, e)
		}
	}
	_, err = submit(roundNo(5), nil)
	if e := refusal(err); e == nil || e.Fields["round"] != "unknown" || e.Fields["scores"] != "required" {
		t.Fatalf("round 5 with no scores: %+v", e)
	}

	// Waiting in the lobby, the room's round is the one just finished.
	f.finish(host, r)
	got, err = submit(nil, []ScoreIn{{Player: a.ID, Score: 3}})
	if err != nil || got.Round != 1 || resultLines(got.Results) != line(a, 3, 1, "game") {
		t.Fatalf("the round just finished = %+v, %v", got, err)
	}
	f.start(host, r)
	got, err = submit(roundNo(1), []ScoreIn{{Player: host.ID, Score: 2}})
	if err != nil || got.Round != 1 || len(f.roundRows(r.ID, 2)) != 0 {
		t.Fatalf("round 1 given while round 2 plays = %+v, %v", got, err)
	}

	// The last round of a room that has ended finishes at the room's end.
	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err = submit(roundNo(2), []ScoreIn{{Player: host.ID, Score: 8}})
	if err != nil || got.Round != 2 {
		t.Fatalf("round 2 of the ended room = %+v, %v", got, err)
	}
	if rows := f.roundRows(r.ID, 2); len(rows) != 1 || !rows[0].FinishedAt.Equal(f.endedAt(r.ID)) {
		t.Fatalf("round 2's rows = %+v", rows)
	}
	if s := scoreLines(f.copyOf(r.ID)); s != scored(host, 8, 1) {
		t.Fatalf("rooms.results = %s", s)
	}
}

// An end during a round finishes it: the scores are the round's, ended
// carries them with the round, and nothing says finished. An end in the
// lobby takes no scores, and carries the latest round's results.
func TestEndingDuringARoundAndBetweenThem(t *testing.T) {
	f := newFixture(t)
	host, a := f.player("Zayn"), f.player("Sara")

	r := f.room(host, xo)
	f.join(a, r)
	_, err := f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: a.ID, Score: 1}})
	wantField(t, "scores in a fresh lobby", err, "scores", "not_playing")
	f.start(host, r)
	f.finish(host, r, ScoreIn{Player: host.ID, Score: 1}, ScoreIn{Player: a.ID, Score: 2})
	f.start(host, r)
	got, err := f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: host.ID, Score: 5}, {Player: a.ID, Score: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ended" || got.Round != 2 || resultLines(got.Results) != lines(line(host, 5, 1, "host"), line(a, 3, 2, "host")) {
		t.Fatalf("ended = %+v", got)
	}
	frames := f.pub.published(r.ID)
	if strings.Join(kinds(frames), ",") != "ended" {
		t.Fatalf("frames = %v", kinds(frames))
	}
	if e := frames[0].(Ended); e.Round != 2 || e.Reason != "host" || scoreLines(e.Results) != scored(host, 5, 1)+" "+scored(a, 3, 2) {
		t.Fatalf("ended frame = %+v", e)
	}
	if rows := f.roundRows(r.ID, 2); len(rows) != 2 || rows[0].Round != 2 {
		t.Fatalf("round 2's rows = %+v", rows)
	}
	if rows := f.roundRows(r.ID, 1); len(rows) != 2 || rows[0].PlayerID != a.ID {
		t.Fatalf("round 1's rows = %+v", rows)
	}
	ev := f.eventLines(r.ID)
	if ev[len(ev)-2].Event != "ended" || ev[len(ev)-1].Event != "results" || !strings.Contains(ev[len(ev)-2].Detail, `"round": 2`) {
		t.Fatalf("events = %+v", ev)
	}
	if strings.Count(strings.Join(f.events(r.ID), ","), "finished") != 1 {
		t.Fatalf("an end during a round wrote finished: %v", f.events(r.ID))
	}

	// In the lobby: scores refused; none carries the latest round's.
	r2 := f.room(host, xo)
	f.join(a, r2)
	f.start(host, r2)
	f.finish(host, r2, ScoreIn{Player: a.ID, Score: 9}, ScoreIn{Player: host.ID, Score: 1})
	_, err = f.s.End(f.ctx, host.ID, r2.ID, []ScoreIn{{Player: a.ID, Score: 1}})
	wantField(t, "scores between rounds", err, "scores", "not_playing")
	got, err = f.s.End(f.ctx, host.ID, r2.ID, []ScoreIn{})
	if err != nil {
		t.Fatal(err)
	}
	carried := scored(a, 9, 1) + " " + scored(host, 1, 2)
	if e := f.pub.ended(r2.ID); len(e) != 1 || e[0].Round != 1 || scoreLines(e[0].Results) != carried {
		t.Fatalf("the lobby's end carried %+v", e)
	}
	if got.Round != 1 || resultLines(got.Results) != lines(line(a, 9, 1, "host"), line(host, 1, 2, "host")) {
		t.Fatalf("the room ended in its lobby reads %+v", got)
	}
	if len(f.roundRows(r2.ID, 1)) != 2 || len(f.rows(r2.ID)) != 2 {
		t.Fatalf("the lobby's end wrote rows: %+v", f.rows(r2.ID))
	}
	if d := f.detailOf(r2.ID, enum.EventEnded); !strings.Contains(d, `"round": 1`) || !strings.Contains(d, `"scores": 2`) {
		t.Fatalf("ended detail = %s", d)
	}
}

// The last member leaving mid-round finishes that round inside the end: the
// game's results for it, when its server wrote them. Leaving the lobby
// between rounds carries the latest round's.
func TestTheLastMemberLeavingFinishesTheRound(t *testing.T) {
	f := newFixture(t)
	host, a := f.player("Zayn"), f.player("Sara")

	r := f.room(host, xo)
	f.join(a, r)
	f.start(host, r)
	f.finish(host, r, ScoreIn{Player: host.ID, Score: 1})
	f.start(host, r)
	if _, err := f.submit(xo, a, r.ID, ScoreIn{Player: a.ID, Score: 6}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*model.Player{a, host} {
		if err := f.s.Leave(f.ctx, p.ID, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	if e := f.pub.ended(r.ID); len(e) != 1 || e[0].Reason != "empty" || e[0].Round != 2 || scoreLines(e[0].Results) != scored(a, 6, 1) {
		t.Fatalf("the empty room's end carried %+v", e)
	}
	if s := scoreLines(f.copyOf(r.ID)); s != scored(a, 6, 1) {
		t.Fatalf("rooms.results = %s", s)
	}

	r2 := f.room(host, xo)
	f.join(a, r2)
	f.start(host, r2)
	f.finish(host, r2, ScoreIn{Player: host.ID, Score: 2}, ScoreIn{Player: a.ID, Score: 1})
	for _, p := range []*model.Player{host, a} {
		if err := f.s.Leave(f.ctx, p.ID, r2.ID); err != nil {
			t.Fatal(err)
		}
	}
	if e := f.pub.ended(r2.ID); len(e) != 1 || e[0].Round != 1 || scoreLines(e[0].Results) != scored(host, 2, 1)+" "+scored(a, 1, 2) {
		t.Fatalf("the empty lobby's end carried %+v", e)
	}
}

// Between rounds the room is a lobby like any other: somebody who stepped
// out during a round comes back whatever the game says about a rejoin, and
// the next round counts them in.
func TestALobbyBetweenRoundsTakesPlayersBack(t *testing.T) {
	f := newFixture(t)
	host, a, b := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	r := f.room(host, tap) // rejoin: false
	f.join(a, r)
	f.join(b, r)
	f.start(host, r)
	if err := f.s.Leave(f.ctx, a.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Join(f.ctx, a.ID, r.Code, "")
	wantCode(t, "a rejoin during a round the game does not allow", err, 409, "room_started")

	f.finish(host, r)
	back := f.join(a, r)
	if back.You == nil || back.Status != "lobby" || back.Round != 1 || len(back.Members) != 3 {
		t.Fatalf("back between rounds: %+v", back)
	}
	newcomer := f.player("Huda")
	if got := f.join(newcomer, r); len(got.Members) != 4 {
		t.Fatalf("a newcomer between rounds: %+v", got)
	}
	f.start(host, r)
	got := f.finish(host, r, ScoreIn{Player: a.ID, Score: 1}, ScoreIn{Player: newcomer.ID, Score: 2})
	if got.Round != 2 || len(got.Results) != 2 {
		t.Fatalf("round 2 = %+v", got)
	}
}

// The Room's results are its round's: none while a round plays, unless the
// game wrote them early, and none for a round that finished without any —
// never the round before.
func TestTheRoomShowsItsRoundsResults(t *testing.T) {
	f := newFixture(t)
	host, a := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(a, r)
	f.start(host, r)
	f.finish(host, r, ScoreIn{Player: host.ID, Score: 1}, ScoreIn{Player: a.ID, Score: 2})

	f.start(host, r)
	playing, err := f.s.Get(f.ctx, host.ID, r.ID)
	if err != nil || playing.Round != 2 || playing.Results == nil || len(playing.Results) != 0 {
		t.Fatalf("round 2 under way = %+v, %v", playing, err)
	}
	back := f.finish(host, r)
	if back.Round != 2 || back.Results == nil || len(back.Results) != 0 {
		t.Fatalf("round 2 finished without scores = %+v", back)
	}
}

// One history item per round, newest first, then by room, then by round; the
// cursor carries the round, so a page can end between two rounds of a room.
func TestHistoryIsPerRound(t *testing.T) {
	f := newFixture(t)
	p, q := f.player("Zayn"), f.player("Sara")

	r := f.room(p, tap)
	f.join(q, r)
	for i := 1; i <= 3; i++ {
		f.start(p, r)
		f.finish(p, r, ScoreIn{Player: p.ID, Score: float64(i)}, ScoreIn{Player: q.ID, Score: 2})
	}
	// Rounds 2 and 3 finish at the same moment: the round breaks the tie.
	same := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	if err := f.db.Exec(`UPDATE results SET finished_at = ? WHERE room_id = ? AND round >= 2`, same, r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE results SET finished_at = ? WHERE room_id = ? AND round = 1`, same.Add(-time.Hour), r.ID).Error; err != nil {
		t.Fatal(err)
	}

	var seen []string
	cursor := ""
	for page := 0; ; page++ {
		got, err := f.s.History(f.ctx, p.ID, cursor, 1)
		if err != nil || page > 3 {
			t.Fatalf("page %d: %+v, %v", page, got, err)
		}
		for _, it := range got.Items {
			seen = append(seen, itoa(it.Round)+":"+itoa(int(it.Score))+":"+itoa(it.Rank)+":"+itoa(it.Players))
			if it.Room != r.ID || it.Code != r.Code {
				t.Fatalf("item = %+v", it)
			}
		}
		if got.NextCursor == nil {
			break
		}
		cursor = *got.NextCursor
		raw, _ := base64.RawURLEncoding.DecodeString(cursor)
		if strings.Count(string(raw), ".") != 2 {
			t.Fatalf("a cursor without its round: %s", raw)
		}
	}
	if strings.Join(seen, " ") != "2:2:1:2 3:3:1:2 1:1:2:2" {
		t.Fatalf("history = %v", seen)
	}
}

// A round's sheet: the round asked for, or the latest with results; rounds
// lists them. A member reads every round while the room is kept, results or
// not; once the room row is gone, a player reads the rounds they are in, and
// the default is the latest of those.
func TestARoundsSheet(t *testing.T) {
	f := newFixture(t)
	host, a, b, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Omar")
	r := f.room(host, tap)
	f.join(a, r)
	f.join(b, r)
	f.start(host, r)
	f.finish(host, r, ScoreIn{Player: host.ID, Score: 1}, ScoreIn{Player: a.ID, Score: 5})
	f.start(host, r)
	f.finish(host, r, ScoreIn{Player: host.ID, Score: 4}, ScoreIn{Player: b.ID, Score: 2})
	f.start(host, r)

	sheet, err := f.s.RoomResults(f.ctx, a.ID, r.ID, "")
	if err != nil || sheet.Round != 2 || itoaAll(sheet.Rounds) != "[1,2]" ||
		resultLines(sheet.Results) != lines(line(host, 4, 1, "host"), line(b, 2, 2, "host")) || sheet.FinishedAt == nil ||
		sheet.Code != r.Code || sheet.Game.ID != tap {
		t.Fatalf("the default = %+v, %v", sheet, err)
	}
	sheet, err = f.s.RoomResults(f.ctx, b.ID, r.ID, "1")
	if err != nil || sheet.Round != 1 || resultLines(sheet.Results) != lines(line(a, 5, 1, "host"), line(host, 1, 2, "host")) {
		t.Fatalf("round 1 = %+v, %v", sheet, err)
	}
	sheet, err = f.s.RoomResults(f.ctx, b.ID, r.ID, "3")
	if err != nil || sheet.Round != 3 || sheet.Results == nil || len(sheet.Results) != 0 || sheet.FinishedAt != nil {
		t.Fatalf("round 3, under way = %+v, %v", sheet, err)
	}
	for _, bad := range []string{"0", "-1", "x", "1.5", "+1", " 1", "9999999999"} {
		_, err = f.s.RoomResults(f.ctx, a.ID, r.ID, bad)
		wantField(t, "round "+bad, err, "round", "must_be_positive")
	}
	_, err = f.s.RoomResults(f.ctx, stranger.ID, r.ID, "1")
	wantCode(t, "a stranger", err, 404, "not_found")

	// The room ends, and a day later it is gone.
	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Sweep(f.ctx, time.Now().Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	sheet, err = f.s.RoomResults(f.ctx, a.ID, r.ID, "")
	if err != nil || sheet.Round != 1 || itoaAll(sheet.Rounds) != "[1]" || resultLines(sheet.Results) !=
		lines(line(a, 5, 1, "host"), line(host, 1, 2, "host")) {
		t.Fatalf("a, in round 1 alone, once the room is gone = %+v, %v", sheet, err)
	}
	_, err = f.s.RoomResults(f.ctx, a.ID, r.ID, "2")
	wantCode(t, "a round a is not in, once the room is gone", err, 404, "not_found")
	sheet, err = f.s.RoomResults(f.ctx, host.ID, r.ID, "")
	if err != nil || sheet.Round != 2 || itoaAll(sheet.Rounds) != "[1,2]" {
		t.Fatalf("the host, in both = %+v, %v", sheet, err)
	}
	_, err = f.s.RoomResults(f.ctx, host.ID, r.ID, "3")
	wantCode(t, "a round with no results, once the room is gone", err, 404, "not_found")
}

// itoaAll is a list of numbers as JSON.
func itoaAll(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = itoa(n)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
