package audit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/migrations"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/logstore"
)

// testDB is this package's own database, made from nothing on every run: the
// migrations around this one are still being written, and goose refuses a
// database that has a later one applied before an earlier one lands. Without
// TEST_DATABASE_URL it skips.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn, err := data.ForTest("gathering_audit")
	if err != nil {
		t.Fatal(err)
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	pg, err := data.Open(data.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := pg.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS logs CASCADE`,
		`DROP SCHEMA public CASCADE`,
		`CREATE SCHEMA public`,
	} {
		if err := pg.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.Up(context.Background(), sqlDB); err != nil {
		t.Fatal(err)
	}
	return pg
}

func TestTheLogReadsBackNewestFirstWithItsDetail(t *testing.T) {
	db, ctx := testDB(t), context.Background()
	log := audit.New(db, audit.AreaPlayers)
	now := time.Now().UTC().Truncate(time.Microsecond)

	for i, reason := range []string{"first", "second"} {
		err := log.Write(ctx, model.Entry{
			At: now.Add(time.Duration(i) * time.Second), Actor: "adm_x",
			Action: model.ActionPlayerDisabled, Subject: "ply_y",
			Detail: map[string]any{"reason": reason},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := log.List(ctx, model.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Detail["reason"] != "second" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Area != audit.AreaPlayers || !strings.HasPrefix(got[0].ID, "aud_") {
		t.Errorf("the line came back as %+v", got[0])
	}
	if !got[1].At.Equal(now) {
		t.Errorf("at came back as %s, wrote %s", got[1].At, now)
	}

	// A line with no detail reads back as an empty object, never as nothing.
	if err := log.Write(ctx, model.Entry{Actor: "adm_x", Action: "x", Subject: "y"}); err != nil {
		t.Fatal(err)
	}
	got, err = log.List(ctx, model.Query{Action: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Detail == nil || len(got[0].Detail) != 0 || got[0].At.IsZero() {
		t.Errorf("a bare line came back as %+v", got)
	}
}

// An area's log sees only its own lines, and the console counts every area at
// once.
func TestAreasKeepToThemselves(t *testing.T) {
	db, ctx := testDB(t), context.Background()
	for _, area := range []string{audit.AreaGames, audit.AreaAuth, audit.AreaAuth} {
		err := audit.New(db, area).Write(ctx, model.Entry{Actor: "adm_x", Action: "x", Subject: "y"})
		if err != nil {
			t.Fatal(err)
		}
	}

	games, err := audit.New(db, audit.AreaGames).List(ctx, model.Query{Area: audit.AreaAuth})
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || games[0].Area != audit.AreaGames {
		t.Errorf("the games log read %+v", games)
	}

	reader := audit.NewReader(db)
	all, err := reader.List(ctx, model.Query{})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := reader.List(ctx, model.Query{Area: audit.AreaAuth})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || len(auth) != 2 {
		t.Errorf("the console read %d lines and %d of auth", len(all), len(auth))
	}

	counts, err := reader.Counts(ctx, model.Query{Area: audit.AreaAuth})
	if err != nil {
		t.Fatal(err)
	}
	if counts[audit.AreaGames] != 1 || counts[audit.AreaAuth] != 2 {
		t.Errorf("counts are %v", counts)
	}

	// The reader over every area is for reading. Writing would need an area to
	// write under, and it has none.
	if err := logstore.NewReader(db).Write(ctx, logstore.Entry{ID: "aud_x", Actor: "a", Action: "x", Subject: "y"}); err == nil {
		t.Error("the reader over every area wrote a line")
	}
}

// Search is a substring in any case, over the detail too, and a % in the term is
// a percent sign, not "anything".
func TestSearchMatchesTheCharactersTyped(t *testing.T) {
	db, ctx := testDB(t), context.Background()
	log := audit.New(db, audit.AreaRooms)
	for _, reason := range []string{"Idle 50% of the hour", "Idle 500 minutes"} {
		err := log.Write(ctx, model.Entry{Actor: model.ActorWorker, Action: "x", Subject: "room_y",
			Detail: map[string]any{"reason": reason}})
		if err != nil {
			t.Fatal(err)
		}
	}

	for term, want := range map[string]int{"idle": 2, "50%": 1, "ROOM_": 2, "worker": 2, "nothing": 0} {
		got, err := log.List(ctx, model.Query{Search: term})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Errorf("%q found %d, want %d", term, len(got), want)
		}
	}
}

// Every other condition narrows the same way, and a page is a page.
func TestTheQueryNarrowsAndPages(t *testing.T) {
	db, ctx := testDB(t), context.Background()
	log := audit.New(db, audit.AreaGames)
	now := time.Now().UTC()

	for i, line := range []struct{ actor, action, subject string }{
		{"adm_a", "game.published", "gam_1"},
		{"adm_a", "game.suspended", "gam_1"},
		{"adm_b", "game.published", "gam_2"},
	} {
		err := log.Write(ctx, model.Entry{At: now.Add(time.Duration(i) * time.Second),
			Actor: line.actor, Action: line.action, Subject: line.subject})
		if err != nil {
			t.Fatal(err)
		}
	}

	count := func(q model.Query) int {
		t.Helper()
		got, err := log.List(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if n := count(model.Query{Actor: "adm_a"}); n != 2 {
		t.Errorf("actor found %d", n)
	}
	if n := count(model.Query{Action: "game.published"}); n != 2 {
		t.Errorf("action found %d", n)
	}
	if n := count(model.Query{Actions: []string{"game.published", "game.suspended"}, Subject: "gam_1"}); n != 2 {
		t.Errorf("actions and subject found %d", n)
	}
	if n := count(model.Query{Actor: "adm_b", Action: "game.suspended"}); n != 0 {
		t.Errorf("conditions were not combined: %d", n)
	}

	page, err := log.List(ctx, model.Query{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Action != "game.suspended" {
		t.Errorf("the second page of one is %+v", page)
	}
}

// What is written comes from outside sometimes: a long actor or subject is cut
// to its column rather than losing the line.
func TestALongValueStillWrites(t *testing.T) {
	db, ctx := testDB(t), context.Background()
	log := audit.New(db, audit.AreaAuth)

	long := strings.Repeat("é", 100)
	if err := log.Write(ctx, model.Entry{Actor: long, Action: "x", Subject: long}); err != nil {
		t.Fatal(err)
	}
	got, err := log.List(ctx, model.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Actor != long[:64*2] || got[0].Subject != long[:64*2] {
		t.Errorf("the line came back as %+v", got)
	}
}
