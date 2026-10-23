package rooms

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// start begins the room's game, and forgets the frame.
func (f *fixture) start(host *model.Player, r *Room) {
	f.t.Helper()
	if _, err := f.s.Start(f.ctx, host.ID, r.ID); err != nil {
		f.t.Fatalf("start: %v", err)
	}
	f.pub.published(r.ID)
}

// submit is the game's server posting the room's round's results with a
// token for player, answered the round's results.
func (f *fixture) submit(game string, player *model.Player, roomID string, scores ...ScoreIn) ([]Result, error) {
	f.t.Helper()
	got, err := f.s.SubmitResults(f.ctx, Submission{GameID: game, PlayerID: player.ID, RoomID: roomID, Scores: scores})
	if err != nil {
		return nil, err
	}
	return got.Results, nil
}

// rows is a room's lines in the results table, every round's, by round, then
// ranked, ties in seat order.
func (f *fixture) rows(roomID string) []model.Result {
	f.t.Helper()
	var out []model.Result
	err := f.db.Raw(`SELECT * FROM results WHERE room_id = ? ORDER BY round, rank, seat, player_id`, roomID).Scan(&out).Error
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// endedAt is the room's end as the row keeps it, to the microsecond.
func (f *fixture) endedAt(roomID string) time.Time {
	f.t.Helper()
	var at time.Time
	if err := f.db.Raw(`SELECT ended_at FROM rooms WHERE id = ?`, roomID).Scan(&at).Error; err != nil {
		f.t.Fatal(err)
	}
	return at
}

// copyOf is the room's own copy of its results, rooms.results: nil while it
// is NULL.
func (f *fixture) copyOf(roomID string) model.Scores {
	f.t.Helper()
	var row struct{ Results model.Scores }
	if err := f.db.Raw(`SELECT results FROM rooms WHERE id = ?`, roomID).Scan(&row).Error; err != nil {
		f.t.Fatal(err)
	}
	return row.Results
}

// eventLine is one line of logs.room_events, the detail as text.
type eventLine struct {
	Event    string
	PlayerID string
	Detail   string
}

func (f *fixture) eventLines(roomID string) []eventLine {
	f.t.Helper()
	var out []eventLine
	err := f.db.Raw(`SELECT event, player_id, detail::text AS detail FROM logs.room_events
	                 WHERE room_id = ? ORDER BY at, id`, roomID).Scan(&out).Error
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// resultsEvents is the room's results lines: who wrote them, and how many.
func (f *fixture) resultsEvents(roomID string) []string {
	f.t.Helper()
	var out []string
	for _, e := range f.eventLines(roomID) {
		if e.Event != string(enum.EventResults) {
			continue
		}
		var d struct {
			By     string
			Scores int
		}
		if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s by %s (%s): %d", e.Event, d.By, e.PlayerID, d.Scores))
	}
	return out
}

func line(p *model.Player, score float64, rank int, by string) string {
	return fmt.Sprintf("%s:%g:%d:%s", p.ID, score, rank, by)
}

func lines(want ...string) string { return strings.Join(want, " ") }

func resultLines(rs []Result) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = fmt.Sprintf("%s:%g:%d:%s", r.Player, r.Score, r.Rank, r.SubmittedBy)
	}
	return strings.Join(parts, " ")
}

func scoreLines(ss []model.Score) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = fmt.Sprintf("%s:%g:%d", s.Player, s.Score, s.Rank)
	}
	return strings.Join(parts, " ")
}

func scored(p *model.Player, score float64, rank int) string {
	return fmt.Sprintf("%s:%g:%d", p.ID, score, rank)
}

// The host's end writes one row per score: by the host, finishing at the
// end, every row counting the players ranked, the room's code and each
// player's seat copied in. The room then reads its results from the table,
// ranked, ties in seat order, a player who left included, with their name
// and picture. The end, then the results, are written to the room's history.
func TestTheHostsEndWritesTheResults(t *testing.T) {
	f := newFixture(t)
	host, a, b, c := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Huda")
	if err := f.db.Exec(`UPDATE players SET avatar_key = 'a07' WHERE id = ?`, a.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE players SET avatar_key = ? WHERE id = ?`, "avatars/"+b.ID+".png", b.ID).Error; err != nil {
		t.Fatal(err)
	}
	r := f.room(host, tap)
	for _, p := range []*model.Player{a, b, c} {
		f.join(p, r)
	}
	f.start(host, r)
	if err := f.s.Leave(f.ctx, c.ID, r.ID); err != nil {
		t.Fatal(err)
	}

	got, err := f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{
		{Player: host.ID, Score: 10}, {Player: a.ID, Score: 30}, {Player: b.ID, Score: 10}, {Player: c.ID, Score: 5.5},
	})
	if err != nil {
		t.Fatal(err)
	}

	ended := f.endedAt(r.ID)
	seats := map[string]int{host.ID: 0, a.ID: 1, b.ID: 2, c.ID: 3}
	rows := f.rows(r.ID)
	if len(rows) != 4 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, row := range rows {
		if row.SubmittedBy != enum.SubmitterHost || !row.FinishedAt.Equal(ended) || row.Players != 4 ||
			row.Code != r.Code || row.GameID != tap || row.Seat != seats[row.PlayerID] {
			t.Fatalf("row = %+v (the end at %v)", row, ended)
		}
	}

	want := lines(line(a, 30, 1, "host"), line(host, 10, 2, "host"), line(b, 10, 2, "host"), line(c, 5.5, 4, "host"))
	if l := resultLines(got.Results); l != want {
		t.Fatalf("results = %s\nwant      %s", l, want)
	}
	by := map[string]Result{}
	for _, res := range got.Results {
		by[res.Player] = res
	}
	if by[c.ID].Nickname != "Huda" || by[host.ID].Nickname != "Zayn" {
		t.Fatalf("names: %+v", got.Results)
	}
	if by[a.ID].Avatar != "a07" || by[a.ID].AvatarURL != nil {
		t.Fatalf("a bundled avatar is its id: %+v", by[a.ID])
	}
	if by[b.ID].Avatar != "" || by[b.ID].AvatarURL == nil || *by[b.ID].AvatarURL != "https://signed.test/avatars/"+b.ID+".png" {
		t.Fatalf("an uploaded picture is a signed link: %+v", by[b.ID])
	}

	// The room's own copy is kept as before, in the order sent.
	if s := scoreLines(f.copyOf(r.ID)); s != strings.Join([]string{
		scored(host, 10, 2), scored(a, 30, 1), scored(b, 10, 2), scored(c, 5.5, 4),
	}, " ") {
		t.Fatalf("rooms.results = %s", s)
	}
	ev := f.eventLines(r.ID)
	if ev[len(ev)-2].Event != "ended" || ev[len(ev)-1].Event != "results" {
		t.Fatalf("events = %+v", ev)
	}
	if got := f.resultsEvents(r.ID); strings.Join(got, ";") != "results by host ("+host.ID+"): 4" {
		t.Fatalf("results events = %v", got)
	}
}

// An end without scores writes no results and says nothing about them.
func TestAnEndWithoutScoresWritesNoResults(t *testing.T) {
	f := newFixture(t)
	host := f.player("Zayn")
	r := f.room(host, xo)

	got, err := f.s.End(f.ctx, host.ID, r.ID, nil)
	if err != nil || got.Results == nil || len(got.Results) != 0 {
		t.Fatalf("ended = %+v, %v", got, err)
	}
	if rows := f.rows(r.ID); len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
	if got := f.resultsEvents(r.ID); len(got) != 0 {
		t.Fatalf("results events = %v", got)
	}
}

// The game's server is the stronger word: what it posts replaces the room's
// rows whole, finishing at the room's end, and an ended room's own copy is
// rewritten to match, in the order the game sent. The answer is the room's
// results as they now stand.
func TestTheGamesResultsReplaceTheHosts(t *testing.T) {
	f := newFixture(t)
	host, a := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(a, r)
	f.start(host, r)
	if _, err := f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: host.ID, Score: 1}, {Player: a.ID, Score: 2}}); err != nil {
		t.Fatal(err)
	}

	written, err := f.submit(xo, a, r.ID, ScoreIn{Player: a.ID, Score: 3}, ScoreIn{Player: host.ID, Score: 5})
	if err != nil {
		t.Fatal(err)
	}
	want := lines(line(host, 5, 1, "game"), line(a, 3, 2, "game"))
	if l := resultLines(written); l != want {
		t.Fatalf("answer = %s\nwant     %s", l, want)
	}
	if written[0].Nickname != "Zayn" || written[1].Nickname != "Sara" {
		t.Fatalf("names: %+v", written)
	}

	ended := f.endedAt(r.ID)
	rows := f.rows(r.ID)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, row := range rows {
		if row.SubmittedBy != enum.SubmitterGame || !row.FinishedAt.Equal(ended) || row.Players != 2 {
			t.Fatalf("row = %+v", row)
		}
	}
	if s := scoreLines(f.copyOf(r.ID)); s != scored(a, 3, 2)+" "+scored(host, 5, 1) {
		t.Fatalf("rooms.results = %s", s)
	}
	got, err := f.s.Get(f.ctx, a.ID, r.ID)
	if err != nil || resultLines(got.Results) != want {
		t.Fatalf("the room reads %+v, %v", got, err)
	}
	if ev := f.resultsEvents(r.ID); strings.Join(ev, ";") !=
		"results by host ("+host.ID+"): 2;results by game ("+a.ID+"): 2" {
		t.Fatalf("results events = %v", ev)
	}
}

// Written into a game still playing, the game's results finish when they
// were written and the room keeps playing. The host's end that follows
// leaves them be: its scores are still checked, but not kept, and the end
// carries the game's results in their place, to the row's copy and to the
// sockets alike.
func TestAHostsEndAfterTheGameLeavesItsResults(t *testing.T) {
	f := newFixture(t)
	host, a, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	r := f.room(host, xo)
	f.join(a, r)
	f.start(host, r)

	before := time.Now().Add(-time.Second)
	if _, err := f.submit(xo, host, r.ID, ScoreIn{Player: host.ID, Score: 0}, ScoreIn{Player: a.ID, Score: 1}); err != nil {
		t.Fatal(err)
	}
	rows := f.rows(r.ID)
	if len(rows) != 2 || rows[0].FinishedAt.Before(before) || rows[0].FinishedAt.After(time.Now()) {
		t.Fatalf("rows written into a game under way = %+v", rows)
	}
	written := rows[0].FinishedAt
	if f.copyOf(r.ID) != nil {
		t.Fatalf("a room still playing has a copy: %v", f.copyOf(r.ID))
	}
	want := lines(line(a, 1, 1, "game"), line(host, 0, 2, "game"))
	playing, err := f.s.Get(f.ctx, host.ID, r.ID)
	if err != nil || playing.Status != "playing" || resultLines(playing.Results) != want {
		t.Fatalf("the room under way = %+v, %v", playing, err)
	}
	if frames := f.pub.published(r.ID); len(frames) != 0 {
		t.Fatalf("results were published: %v", kinds(frames))
	}

	_, err = f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: stranger.ID, Score: 1}})
	wantField(t, "a stranger scored", err, "scores.0.player", "not_a_member")

	got, err := f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: host.ID, Score: 9}, {Player: a.ID, Score: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if l := resultLines(got.Results); l != want {
		t.Fatalf("results after the host's end = %s\nwant                          %s", l, want)
	}
	after := f.rows(r.ID)
	if len(after) != 2 || after[0].SubmittedBy != enum.SubmitterGame || !after[0].FinishedAt.Equal(written) {
		t.Fatalf("the host's end touched the game's rows: %+v", after)
	}
	carried := scored(a, 1, 1) + " " + scored(host, 0, 2)
	if s := scoreLines(f.copyOf(r.ID)); s != carried {
		t.Fatalf("rooms.results = %s, want %s", s, carried)
	}
	ends := f.pub.ended(r.ID)
	if len(ends) != 1 || scoreLines(ends[0].Results) != carried {
		t.Fatalf("the ended frame carried %+v", ends)
	}
	if ev := f.resultsEvents(r.ID); strings.Join(ev, ";") != "results by game ("+host.ID+"): 2" {
		t.Fatalf("results events = %v", ev)
	}
}

// Every end carries the game's results once its server has written them:
// the last member leaving, and the sweeper's timeout, as well as the host.
func TestEveryEndCarriesTheGamesResults(t *testing.T) {
	f := newFixture(t)
	host, a := f.player("Zayn"), f.player("Sara")

	r := f.room(host, xo)
	f.join(a, r)
	f.start(host, r)
	if _, err := f.submit(xo, a, r.ID, ScoreIn{Player: a.ID, Score: 2}, ScoreIn{Player: host.ID, Score: 1}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*model.Player{a, host} {
		if err := f.s.Leave(f.ctx, p.ID, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	carried := scored(a, 2, 1) + " " + scored(host, 1, 2)
	ends := f.pub.ended(r.ID)
	if len(ends) != 1 || ends[0].Reason != "empty" || scoreLines(ends[0].Results) != carried {
		t.Fatalf("the empty room's end carried %+v", ends)
	}
	if s := scoreLines(f.copyOf(r.ID)); s != carried {
		t.Fatalf("rooms.results = %s", s)
	}

	// A tie: both first, the host's seat before the player's. The clock
	// finishes the round that ran too long, and the finish carries them.
	r2 := f.room(host, xo)
	f.join(a, r2)
	f.start(host, r2)
	if _, err := f.submit(xo, host, r2.ID, ScoreIn{Player: a.ID, Score: 4}, ScoreIn{Player: host.ID, Score: 4}); err != nil {
		t.Fatal(err)
	}
	swept, err := f.s.Sweep(f.ctx, time.Now().Add(3*time.Hour))
	if err != nil || swept.Timeout != 1 {
		t.Fatalf("swept %+v, %v", swept, err)
	}
	tie := scored(host, 4, 1) + " " + scored(a, 4, 1)
	frames := f.pub.published(r2.ID)
	if len(frames) != 1 || len(f.pub.ended(r2.ID)) != 0 {
		t.Fatalf("the timed-out round sent %v", kinds(frames))
	}
	if fin := frames[0].(Finished); fin.Reason != "timeout" || fin.By != nil || fin.Round != 1 || scoreLines(fin.Results) != tie {
		t.Fatalf("the timed-out round's finish carried %+v", fin)
	}
	if s := scoreLines(f.copyOf(r2.ID)); s != tie {
		t.Fatalf("rooms.results = %s", s)
	}
}

// A missing rank is computed by score, highest first, ties sharing a rank
// and the next skipping past them; a rank the game gave is kept. The lines
// read ranked, ties in seat order, and a new write replaces the old whole.
func TestResultsAreRankedTiesInSeatOrder(t *testing.T) {
	f := newFixture(t)
	host, a, b, c := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Huda")
	r := f.room(host, tap)
	for _, p := range []*model.Player{a, b, c} {
		f.join(p, r)
	}
	f.start(host, r)

	two := 2
	written, err := f.submit(tap, b, r.ID,
		ScoreIn{Player: b.ID, Score: 10}, ScoreIn{Player: host.ID, Score: 30},
		ScoreIn{Player: a.ID, Score: 10}, ScoreIn{Player: c.ID, Score: 5, Rank: &two})
	if err != nil {
		t.Fatal(err)
	}
	want := lines(line(host, 30, 1, "game"), line(a, 10, 2, "game"), line(b, 10, 2, "game"), line(c, 5, 2, "game"))
	if l := resultLines(written); l != want {
		t.Fatalf("ranked = %s\nwant     %s", l, want)
	}

	// 1, 1, 3, and the host's line, not sent this time, is gone.
	written, err = f.submit(tap, b, r.ID, ScoreIn{Player: c.ID, Score: 7}, ScoreIn{Player: a.ID, Score: 7}, ScoreIn{Player: b.ID, Score: 1})
	if err != nil {
		t.Fatal(err)
	}
	want = lines(line(a, 7, 1, "game"), line(c, 7, 1, "game"), line(b, 1, 3, "game"))
	if l := resultLines(written); l != want {
		t.Fatalf("ranked = %s\nwant     %s", l, want)
	}
	rows := f.rows(r.ID)
	if len(rows) != 3 || rows[0].Players != 3 {
		t.Fatalf("rows = %+v", rows)
	}
}

// The refusals, in the order the contract names them: no such room, another
// game's, a token whose player was never in it, a lobby; then the scores.
// A refusal writes nothing. A player who has left still speaks for the room
// they were in; a room whose row is gone is no room.
func TestSubmittingResultsIsRefused(t *testing.T) {
	f := newFixture(t)
	host, a, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali")
	r := f.room(host, xo)
	f.join(a, r)
	one := ScoreIn{Player: host.ID, Score: 1}

	_, err := f.submit(xo, host, "room_aaaaaaaaaaaaaaaa", one)
	wantCode(t, "no such room", err, 404, "not_found")
	_, err = f.submit(tap, stranger, r.ID, one)
	wantField(t, "another game's room", err, "room", "another_game")
	_, err = f.submit(xo, stranger, r.ID, one)
	wantCode(t, "a stranger's token", err, 403, "not_member")
	_, err = f.submit(xo, a, r.ID, one)
	wantCode(t, "a lobby", err, 409, "room_not_started")

	f.start(host, r)
	zero := 0
	_, err = f.submit(xo, a, r.ID)
	wantField(t, "no scores", err, "scores", "required")
	_, err = f.submit(xo, a, r.ID, ScoreIn{Player: stranger.ID, Score: 1})
	wantField(t, "a stranger scored", err, "scores.0.player", "not_a_member")
	_, err = f.submit(xo, a, r.ID, one, one)
	wantField(t, "a player scored twice", err, "scores.1.player", "duplicate")
	_, err = f.submit(xo, a, r.ID, ScoreIn{Player: a.ID, Score: 1, Rank: &zero})
	wantField(t, "rank zero", err, "scores.0.rank", "must_be_positive")
	if rows := f.rows(r.ID); len(rows) != 0 {
		t.Fatalf("a refusal wrote %+v", rows)
	}

	if err := f.s.Leave(f.ctx, a.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.submit(xo, a, r.ID, ScoreIn{Player: a.ID, Score: 1}); err != nil {
		t.Fatalf("a player who left: %v", err)
	}

	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`DELETE FROM rooms WHERE id = ?`, r.ID).Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.submit(xo, host, r.ID, one)
	wantCode(t, "a room whose row is gone", err, 404, "not_found")
}

// The history is the player's own lines, newest first, a finish shared by
// two rooms broken by the room id, a page at a time: next_cursor until the
// last page, which has none.
func TestHistoryPagesNewestFirst(t *testing.T) {
	f := newFixture(t)
	p, other, filler := f.player("Zayn"), f.player("Sara"), f.player("Ali")

	// One round each, scored for the host alone; the filler makes the
	// game's two and is free again once each room ends.
	play := func(host *model.Player, score float64) *Room {
		r := f.room(host, tap)
		f.join(filler, r)
		f.start(host, r)
		if _, err := f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: host.ID, Score: score}}); err != nil {
			t.Fatal(err)
		}
		return r
	}
	base := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	at := []time.Time{base, base.Add(2 * time.Minute), base.Add(2 * time.Minute), base.Add(3 * time.Minute), base.Add(4 * time.Minute)}
	played := make([]*Room, 0, len(at))
	for i := range at {
		r := play(p, float64(i))
		if err := f.db.Exec(`UPDATE results SET finished_at = ? WHERE room_id = ?`, at[i], r.ID).Error; err != nil {
			t.Fatal(err)
		}
		played = append(played, r)
	}
	play(other, 1)

	tie := []string{played[1].ID, played[2].ID}
	sort.Strings(tie)
	want := []string{played[4].ID, played[3].ID, tie[0], tie[1], played[0].ID}

	var seen []string
	cursor, pages := "", 0
	for {
		page, err := f.s.History(f.ctx, p.ID, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, it := range page.Items {
			seen = append(seen, it.Room)
		}
		if page.NextCursor == nil {
			if len(page.Items) != 1 {
				t.Fatalf("the last page = %+v", page.Items)
			}
			break
		}
		if len(page.Items) != 2 || pages > 3 {
			t.Fatalf("page %d = %+v", pages, page)
		}
		cursor = *page.NextCursor
	}
	if pages != 3 || strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("%d pages, rooms %v\nwant %v", pages, seen, want)
	}

	page, err := f.s.History(f.ctx, p.ID, "", 20)
	if err != nil || len(page.Items) != 5 || page.NextCursor != nil {
		t.Fatalf("one page of 20 = %+v, %v", page, err)
	}
	first := page.Items[0]
	if first.Room != played[4].ID || first.Code != played[4].Code || first.Round != 1 || first.Score != 4 || first.Rank != 1 ||
		first.Players != 1 || first.FinishedAt != "2026-10-21T10:04:00Z" || first.SubmittedBy != "host" ||
		first.Game != (Game{ID: tap, Name: "سباق النقر", Icon: "https://signed.test/games/" + tap + "/icon.png", Orientation: "portrait"}) {
		t.Fatalf("the newest item = %+v", first)
	}

	// Exactly a page: no next page promised.
	page, err = f.s.History(f.ctx, p.ID, "", 5)
	if err != nil || len(page.Items) != 5 || page.NextCursor != nil {
		t.Fatalf("a page of exactly 5 = %+v, %v", page, err)
	}

	// Nothing played: a list, empty, and the last page.
	page, err = f.s.History(f.ctx, f.player("Ali").ID, "", 20)
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("nothing played = %+v, %v", page, err)
	}
}

// limit is 1 to 50; a cursor is one the API handed out, or nothing.
func TestHistoryIsRefused(t *testing.T) {
	f := newFixture(t)
	p := f.player("Zayn")

	for _, n := range []int{0, -1, 51} {
		_, err := f.s.History(f.ctx, p.ID, "", n)
		wantField(t, fmt.Sprintf("limit %d", n), err, "limit", "1_to_50")
	}
	enc := base64.RawURLEncoding.EncodeToString
	for _, c := range []string{
		"nope!", enc([]byte("1792573806000000")), enc([]byte("soon.room_aaaaaaaaaaaaaaaa.1")),
		enc([]byte("1792573806000000.ply_aaaaaaaaaaaaaaaa.1")), enc([]byte("-1.room_aaaaaaaaaaaaaaaa.1")),
		// A cursor written before rounds: the finish and the room, no round.
		enc([]byte("1792573806000000.room_aaaaaaaaaaaaaaaa")),
		enc([]byte("1792573806000000.room_aaaaaaaaaaaaaaaa.0")), enc([]byte("1792573806000000.room_aaaaaaaaaaaaaaaa.x")),
		enc([]byte("1792573806000000.room_aaaaaaaaaaaaaaaa.1.2")),
		strings.Repeat("A", 200),
	} {
		_, err := f.s.History(f.ctx, p.ID, c, 20)
		wantField(t, "cursor "+c, err, "cursor", "invalid")
	}
	_, err := f.s.History(f.ctx, p.ID, "nope!", 0)
	if e := refusal(err); e == nil || e.Fields["limit"] != "1_to_50" || e.Fields["cursor"] != "invalid" {
		t.Fatalf("both at once: %+v", e)
	}
}

// A room's sheet answers every member while the room is kept, results or
// not; once the room row is gone, a day after the end, it answers the
// players in the results, for good, and nobody else.
func TestARoomsResultsOutliveTheRoom(t *testing.T) {
	f := newFixture(t)
	host, a, b, stranger := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Omar")
	r := f.room(host, tap)
	f.join(a, r)
	f.join(b, r)
	f.start(host, r)

	sheet, err := f.s.RoomResults(f.ctx, b.ID, r.ID, "")
	if err != nil || sheet.Results == nil || len(sheet.Results) != 0 || sheet.FinishedAt != nil ||
		sheet.Room != r.ID || sheet.Code != r.Code || sheet.Game.ID != tap {
		t.Fatalf("a room under way = %+v, %v", sheet, err)
	}
	_, err = f.s.RoomResults(f.ctx, stranger.ID, r.ID, "")
	wantCode(t, "a stranger", err, 404, "not_found")

	ended, err := f.s.End(f.ctx, host.ID, r.ID, []ScoreIn{{Player: host.ID, Score: 2}, {Player: a.ID, Score: 1}})
	if err != nil {
		t.Fatal(err)
	}
	want := lines(line(host, 2, 1, "host"), line(a, 1, 2, "host"))
	sheet, err = f.s.RoomResults(f.ctx, b.ID, r.ID, "")
	if err != nil || sheet.FinishedAt == nil || *sheet.FinishedAt != *ended.EndedAt || resultLines(sheet.Results) != want {
		t.Fatalf("a member not in the results = %+v, %v", sheet, err)
	}

	// A day later the sweeper deletes the room and its members.
	swept, err := f.s.Sweep(f.ctx, time.Now().Add(25*time.Hour))
	if err != nil || swept.Deleted != 1 {
		t.Fatalf("swept %+v, %v", swept, err)
	}
	if _, err := f.s.Get(f.ctx, host.ID, r.ID); codeOf(err) != "not_found" {
		t.Fatalf("the room is still there: %v", err)
	}
	for _, p := range []*model.Player{host, a} {
		sheet, err := f.s.RoomResults(f.ctx, p.ID, r.ID, "")
		if err != nil || sheet.Code != r.Code || sheet.Game.ID != tap || sheet.Game.Name != "سباق النقر" ||
			sheet.FinishedAt == nil || *sheet.FinishedAt != *ended.EndedAt || resultLines(sheet.Results) != want ||
			sheet.Results[1].Nickname != "Sara" {
			t.Fatalf("%s reads %+v, %v", p.Nickname, sheet, err)
		}
	}
	_, err = f.s.RoomResults(f.ctx, b.ID, r.ID, "")
	wantCode(t, "a member not in the results, once the room is gone", err, 404, "not_found")
	_, err = f.s.RoomResults(f.ctx, stranger.ID, r.ID, "")
	wantCode(t, "a stranger, once the room is gone", err, 404, "not_found")

	page, err := f.s.History(f.ctx, a.ID, "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].Room != r.ID || page.Items[0].Rank != 2 || page.Items[0].Players != 2 {
		t.Fatalf("a's history = %+v, %v", page, err)
	}
}

// A room that ended before the results table has no rows: the room and its
// sheet read its own copy, drawn the same way and written by the host; the
// history, which is the table, does not have it.
func TestARoomEndedBeforeResultsReadsItsOwnCopy(t *testing.T) {
	f := newFixture(t)
	host, a := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(a, r)
	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	copied := fmt.Sprintf(`[{"player":%q,"score":1,"rank":2},{"player":%q,"score":3,"rank":1}]`, host.ID, a.ID)
	if err := f.db.Exec(`UPDATE rooms SET results = ?::jsonb WHERE id = ?`, copied, r.ID).Error; err != nil {
		t.Fatal(err)
	}

	want := lines(line(a, 3, 1, "host"), line(host, 1, 2, "host"))
	got, err := f.s.Get(f.ctx, host.ID, r.ID)
	if err != nil || resultLines(got.Results) != want || got.Results[0].Nickname != "Sara" {
		t.Fatalf("the room reads %+v, %v", got, err)
	}
	sheet, err := f.s.RoomResults(f.ctx, a.ID, r.ID, "")
	if err != nil || resultLines(sheet.Results) != want || sheet.FinishedAt == nil || *sheet.FinishedAt != *got.EndedAt {
		t.Fatalf("its sheet = %+v, %v", sheet, err)
	}
	page, err := f.s.History(f.ctx, a.ID, "", 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("history = %+v, %v", page, err)
	}
}
