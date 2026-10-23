package rooms

import (
	"strings"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// The clock ends a lobby nobody is connected to and nobody has touched,
// finishes a round run past its limit (the room goes back to its lobby, and
// ends once that lobby idles), and deletes what ended a day ago — and a lobby
// with somebody connected is kept however old its active_at.
func TestTheSweeper(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	idle, kept, long, fresh := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Huda")
	friend := f.player("Omar")

	rIdle := f.room(idle, tap)
	rKept := f.room(kept, tap)
	rLong := f.room(long, xo)
	f.join(friend, rLong)
	f.start(long, rLong)
	rFresh := f.room(fresh, tap)

	old := now.Add(-time.Hour)
	for _, id := range []string{rIdle.ID, rKept.ID} {
		if err := f.db.Exec(`UPDATE rooms SET active_at = ? WHERE id = ?`, old, id).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Exec(`UPDATE rooms SET started_at = ? WHERE id = ?`, now.Add(-3*time.Hour), rLong.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.presence.set(kept.ID, players.State{State: players.InRoom, Room: rKept.ID})

	swept, err := f.s.Sweep(f.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Idle != 1 || swept.Timeout != 1 || swept.Deleted != 0 {
		t.Fatalf("swept = %+v", swept)
	}
	if ends := f.pub.ended(rIdle.ID); len(ends) != 1 || ends[0].Reason != "idle" || ends[0].By != nil ||
		ends[0].Round != 0 || ends[0].Results == nil || len(ends[0].Results) != 0 {
		t.Fatalf("the idle lobby ended as %+v", ends)
	}
	// The long round is finished, not ended: back in the lobby, no socket
	// closed, by nobody, for running too long.
	if ends := f.pub.ended(rLong.ID); len(ends) != 0 {
		t.Fatalf("the long round ended its room: %+v", ends)
	}
	frames := f.pub.published(rLong.ID)
	if len(frames) != 1 {
		t.Fatalf("the long round sent %v", kinds(frames))
	}
	if fin := frames[0].(Finished); fin.Reason != "timeout" || fin.By != nil || fin.Round != 1 ||
		fin.Results == nil || len(fin.Results) != 0 || fin.At == "" {
		t.Fatalf("finished = %+v", fin)
	}
	var detail string
	if err := f.db.Raw(`SELECT detail::text FROM logs.room_events WHERE room_id = ? AND event = 'finished' AND player_id = ''`,
		rLong.ID).Scan(&detail).Error; err != nil || !strings.Contains(detail, `"reason": "timeout"`) ||
		!strings.Contains(detail, `"round": 1`) {
		t.Fatalf("the clock's finish wrote %q, %v", detail, err)
	}
	for id, status := range map[string]string{rIdle.ID: "ended", rKept.ID: "lobby", rLong.ID: "lobby", rFresh.ID: "lobby"} {
		var got string
		if err := f.db.Raw(`SELECT status FROM rooms WHERE id = ?`, id).Scan(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != status {
			t.Fatalf("%s is %s, want %s", id, got, status)
		}
	}
	if got := f.events(rIdle.ID); got[len(got)-1] != "ended" {
		t.Fatalf("events = %v", got)
	}
	var who string
	if err := f.db.Raw(`SELECT player_id FROM logs.room_events WHERE room_id = ? AND event = 'ended'`, rIdle.ID).Scan(&who).Error; err != nil || who != "" {
		t.Fatalf("the clock's end names %q, %v", who, err)
	}

	// A second pass finds nothing new: each change happens once, and the
	// finished round's lobby is fresh.
	again, err := f.s.Sweep(f.ctx, now)
	if err != nil || again.Any() {
		t.Fatalf("again = %+v, %v", again, err)
	}

	// Its lobby idles like any other, counted from the finish: nobody
	// connected for LobbyIdle, and it ends, carrying the latest round's
	// results (none: the game's server wrote nothing).
	later, err := f.s.Sweep(f.ctx, time.Now().Add(11*time.Minute))
	if err != nil || later.Idle != 2 || later.Timeout != 0 {
		t.Fatalf("later = %+v, %v", later, err)
	}
	if ends := f.pub.ended(rLong.ID); len(ends) != 1 || ends[0].Reason != "idle" || ends[0].Round != 1 ||
		ends[0].Results == nil || len(ends[0].Results) != 0 {
		t.Fatalf("the finished round's lobby ended as %+v", ends)
	}

	// A day after it ended, a room goes, its members with it.
	if err := f.db.Exec(`UPDATE rooms SET ended_at = ? WHERE id = ?`, now.Add(-25*time.Hour), rIdle.ID).Error; err != nil {
		t.Fatal(err)
	}
	swept, err = f.s.Sweep(f.ctx, now)
	if err != nil || swept.Deleted != 1 {
		t.Fatalf("swept = %+v, %v", swept, err)
	}
	var left int64
	if err := f.db.Raw(`SELECT count(*) FROM room_members WHERE room_id = ?`, rIdle.ID).Scan(&left).Error; err != nil || left != 0 {
		t.Fatalf("%d members left behind, %v", left, err)
	}
	if _, err := f.s.Get(f.ctx, idle.ID, rIdle.ID); codeOf(err) != "not_found" {
		t.Fatalf("a deleted room reads as %v", err)
	}
}

// A lobby the sweeper listed and somebody joined in the meantime is kept:
// the end re-checks under the row lock.
func TestTheSweeperRechecksUnderTheLock(t *testing.T) {
	f := newFixture(t)
	host := f.player("Zayn")
	r := f.room(host, tap)
	cutoff := time.Now().Add(-time.Minute)
	ended, err := f.s.sweepEnd(f.ctx, r.ID, enum.EndIdle, func(row *model.Room) bool { return row.ActiveAt.Before(cutoff) })
	if err != nil || ended {
		t.Fatalf("ended=%v err=%v", ended, err)
	}
}
