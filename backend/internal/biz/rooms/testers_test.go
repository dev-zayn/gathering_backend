package rooms

import (
	"testing"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// underTest gives xo a publisher and an open build seating up to four, and
// makes tester one of its testers.
func (f *fixture) underTest(tester *model.Player) *model.GameBuild {
	f.t.Helper()
	pub := &model.Publisher{ID: "pub_aaaaaaaaaaaaaaaa", Name: "Studio", Email: "s@studio.example", CreatedAt: data.Now()}
	if err := f.db.Create(pub).Error; err != nil {
		f.t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE games SET publisher_id = ? WHERE id = ?`, pub.ID, xo).Error; err != nil {
		f.t.Fatal(err)
	}
	room := manifest("إكس أو", 2, 4, enum.HostAuthoritative, enum.StateBlob, true, true).Room
	now := data.Now()
	b := &model.GameBuild{
		ID: "bld_aaaaaaaaaaaaaaaa", GameID: xo, Status: enum.BuildTesting, Name: "إكس أو التجريبية",
		Version: "2.0.0", ManifestURL: "https://games.yourstudio.example/xo/beta/", Entry: "https://games.yourstudio.example/xo/beta/",
		Manifest: model.Raw(`{}`), FetchedAt: now, Orientation: enum.Portrait, Room: room,
		Category: enum.CategoryOther, CreatedAt: now, UpdatedAt: now,
	}
	if err := f.db.Create(b).Error; err != nil {
		f.t.Fatal(err)
	}
	if err := f.db.Create(&model.Tester{GameID: xo, PlayerID: tester.ID, JoinedAt: now}).Error; err != nil {
		f.t.Fatal(err)
	}
	return b
}

// A tester's room plays the build: its block, its name, testers alone at the
// door. A player who does not test the game makes a live room, which the
// tester may join and play live.
func TestATestersRoomIsATestRoom(t *testing.T) {
	f := newFixture(t)
	tester, other, stranger := f.player("سارة"), f.player("نور"), f.player("علي")
	b := f.underTest(tester)
	if err := f.db.Create(&model.Tester{GameID: xo, PlayerID: other.ID, JoinedAt: data.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	r := f.room(tester, xo)
	if !r.Testing || r.Config.Max != 4 || r.Game.Name != b.Name {
		t.Fatalf("the tester's room %+v; want the build's, testing", r)
	}
	_, err := f.s.Join(f.ctx, stranger.ID, r.Code, "")
	wantCode(t, "a stranger at a test room", err, 403, "testers_only")
	f.join(other, r)

	build, err := f.s.CheckLaunch(f.ctx, r.ID, other.ID, xo)
	if err != nil || build != b.ID {
		t.Fatalf("launching into the test room: %q %v; want the build", build, err)
	}
	if err := f.s.Leave(f.ctx, tester.ID, r.ID); err != nil {
		t.Fatal(err)
	}

	live := f.room(stranger, xo)
	if live.Testing || live.Config.Max != 2 {
		t.Fatalf("a stranger's room %+v; want the live game's", live)
	}
	f.join(tester, live)
	build, err = f.s.CheckLaunch(f.ctx, live.ID, tester.ID, xo)
	if err != nil || build != "" {
		t.Fatalf("a tester launching into a live room: %q %v; want the live game", build, err)
	}
	if seen, _ := games.SeenBy(f.ctx, f.db, stranger.ID, xo); seen.Testing() {
		t.Fatal("a stranger sees the build")
	}
}
