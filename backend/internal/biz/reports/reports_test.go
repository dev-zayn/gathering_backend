package reports

import (
	"context"
	stderrors "errors"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

func service(t *testing.T) (*Service, *gorm.DB, *biztest.Audit) {
	t.Helper()
	db := biztest.DB(t)
	log := &biztest.Audit{}
	return &Service{DB: db, Audit: log}, db, log
}

// refused checks the refusal and answers its field problems.
func refused(t *testing.T, err error, status int, code string) map[string]string {
	t.Helper()
	var e *errors.Error
	if !stderrors.As(err, &e) || e.Status != status || e.Code != code {
		t.Fatalf("got %v, want %d %s", err, status, code)
	}
	return e.Fields
}

func list(t *testing.T, s *Service, status string) *ConsoleReports {
	t.Helper()
	page, err := s.List(context.Background(), status, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// A report is one open row per reporter and player: reporting again replaces
// its words and keeps its place. Somebody else's is a second row, and the list
// counts both against the player. The note stays out of the audit log.
func TestFilingAndReading(t *testing.T) {
	s, db, log := service(t)
	ctx := context.Background()
	sara := biztest.SeedPlayer(t, db, "+9647700000001", "Sara")
	ali := biztest.SeedPlayer(t, db, "+9647700000002", "Ali")
	omar := biztest.SeedPlayer(t, db, "+9647700000003", "Omar")

	if err := s.File(ctx, sara.ID, ali.ID, "name", "  rude name  "); err != nil {
		t.Fatal(err)
	}
	if err := s.File(ctx, sara.ID, ali.ID, "harassment", "keeps inviting me"); err != nil {
		t.Fatal(err)
	}
	if err := s.File(ctx, omar.ID, ali.ID, "photo", ""); err != nil {
		t.Fatal(err)
	}

	page, err := s.List(ctx, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Open != 2 || page.NextCursor != nil {
		t.Fatalf("got %+v", page)
	}
	var mine *ConsoleReport
	for i := range page.Items {
		if page.Items[i].Reporter.ID == sara.ID {
			mine = &page.Items[i]
		}
	}
	if mine == nil || mine.Reason != "harassment" || mine.Note != "keeps inviting me" || mine.Status != "open" ||
		mine.Player.ID != ali.ID || mine.Against != 2 || mine.ResolvedAt != nil {
		t.Fatalf("sara's report reads %+v", mine)
	}

	w := log.Written()
	if len(w) != 3 || w[0].Action != model.ActionReportFiled || w[0].Subject != ali.ID || w[0].Detail["note"] != nil {
		t.Fatalf("the log holds %+v", w)
	}
}

// Every refusal, every field problem at once, and none of them writes a row.
func TestFilingRefusals(t *testing.T) {
	s, db, _ := service(t)
	ctx := context.Background()
	sara := biztest.SeedPlayer(t, db, "+9647700000001", "Sara")
	off := biztest.SeedPlayer(t, db, "+9647700000002", "Off")
	if err := identity.NewPlayerStore(db).SetDisabled(ctx, off.ID, true); err != nil {
		t.Fatal(err)
	}

	long := make([]rune, model.MaxReportNote+1)
	for i := range long {
		long[i] = 'ع'
	}
	fields := refused(t, s.File(ctx, sara.ID, sara.ID, "", string(long)), http.StatusUnprocessableEntity, "invalid_fields")
	if fields["reason"] != "required" || fields["note"] != "too_long" || fields["player"] != "is_you" {
		t.Fatalf("fields %v", fields)
	}
	fields = refused(t, s.File(ctx, sara.ID, off.ID, "spam", ""), http.StatusUnprocessableEntity, "invalid_fields")
	if fields["reason"] != "unknown" {
		t.Fatalf("fields %v", fields)
	}
	refused(t, s.File(ctx, sara.ID, off.ID, "name", ""), http.StatusNotFound, "not_found")
	refused(t, s.File(ctx, sara.ID, "nobody", "name", ""), http.StatusNotFound, "not_found")

	var n int64
	db.Table("reports").Count(&n)
	if n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

// Resolving moves it out of the open list, names who did it, and frees the pair
// to report again. Twice is refused.
func TestResolving(t *testing.T) {
	s, db, log := service(t)
	ctx := context.Background()
	sara := biztest.SeedPlayer(t, db, "+9647700000001", "Sara")
	ali := biztest.SeedPlayer(t, db, "+9647700000002", "Ali")
	admin := biztest.SeedAdmin(t, db, "sub-1", "Zayn")
	if err := s.File(ctx, sara.ID, ali.ID, "name", ""); err != nil {
		t.Fatal(err)
	}
	id := list(t, s, "open").Items[0].ID

	if err := s.Resolve(ctx, admin.ID, id); err != nil {
		t.Fatal(err)
	}
	refused(t, s.Resolve(ctx, admin.ID, id), http.StatusConflict, "report_resolved")
	refused(t, s.Resolve(ctx, admin.ID, "rpt_aaaaaaaaaaaaaaaa"), http.StatusNotFound, "not_found")
	refused(t, s.Resolve(ctx, admin.ID, "nope"), http.StatusNotFound, "not_found")

	page := list(t, s, "")
	if len(page.Items) != 0 || page.Open != 0 {
		t.Fatalf("still open: %+v", page)
	}
	page = list(t, s, "resolved")
	if len(page.Items) != 1 || page.Items[0].ResolvedBy == nil || *page.Items[0].ResolvedBy != "Zayn" ||
		page.Items[0].ResolvedAt == nil || page.Items[0].Against != 0 {
		t.Fatalf("resolved reads %+v", page.Items)
	}
	w := log.Written()
	if w[len(w)-1].Action != model.ActionReportResolved || w[len(w)-1].Actor != admin.ID {
		t.Fatalf("the log holds %+v", w)
	}

	if err := s.File(ctx, sara.ID, ali.ID, "photo", ""); err != nil {
		t.Fatalf("a new report after the first was dealt with: %v", err)
	}
}

// The list is paged by its cursor, and refuses what it never handed out.
func TestTheListPages(t *testing.T) {
	s, db, _ := service(t)
	ctx := context.Background()
	ali := biztest.SeedPlayer(t, db, "+9647700000001", "Ali")
	for _, p := range []struct{ phone, name string }{
		{"+9647700000002", "Sara"}, {"+9647700000003", "Omar"}, {"+9647700000004", "Nour"},
	} {
		r := biztest.SeedPlayer(t, db, p.phone, p.name)
		if err := s.File(ctx, r.ID, ali.ID, "other", ""); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 3; page++ {
		got, err := s.List(ctx, "all", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range got.Items {
			seen[r.ID] = true
		}
		if got.NextCursor == nil {
			break
		}
		cursor = *got.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d reports across the pages", len(seen))
	}

	for _, tc := range []struct {
		status, cursor string
		limit          int
		field, problem string
	}{
		{"x", "", 50, "status", "unknown"},
		{"", "", 0, "limit", "1_to_100"},
		{"", "", 101, "limit", "1_to_100"},
		{"", "%%%", 50, "cursor", "invalid"},
	} {
		_, err := s.List(ctx, tc.status, tc.cursor, tc.limit)
		fields := refused(t, err, http.StatusUnprocessableEntity, "invalid_fields")
		if fields[tc.field] != tc.problem {
			t.Fatalf("%+v got %v", tc, fields)
		}
	}
}
