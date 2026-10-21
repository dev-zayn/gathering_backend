package identity_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

func admins(t *testing.T) *identity.AdminStore {
	t.Helper()
	return identity.NewAdminStore(biztest.DB(t))
}

const subject = "392486038464954371"

// A provider's subject gets an id of ours the first time, and the same one
// after. The subject is kept beside it, not used as it. A new row has not been
// seen yet: that is what tells a first visit.
func TestASubjectGetsOneAdminID(t *testing.T) {
	a := admins(t)
	ctx := context.Background()

	first, err := a.Enroll(ctx, subject, "Sara Ali", "sara@example.iq")
	if err != nil || first == nil || !ids.Is(ids.Admin, first.ID) || first.SeenAt != nil {
		t.Fatalf("first visit: %+v, %v", first, err)
	}
	again, err := a.Enroll(ctx, subject, "Sara Ali", "sara@example.iq")
	if err != nil || again.ID != first.ID {
		t.Fatalf("second visit: %+v, %v; want %q", again, err, first.ID)
	}

	admin, err := a.ByID(ctx, first.ID)
	if err != nil || admin == nil || admin.Subject != subject || admin.Name != "Sara Ali" {
		t.Fatalf("row is %+v, %v", admin, err)
	}
}

// Two first requests at once make one row, and both get its id.
func TestTwoFirstVisitsAtOnceMakeOneAdmin(t *testing.T) {
	a := admins(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	got := make([]string, 8)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			admin, err := a.Enroll(ctx, subject, "Sara", "")
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = admin.ID
		}()
	}
	wg.Wait()

	for _, id := range got {
		if id != got[0] || id == "" {
			t.Fatalf("ids are %v", got)
		}
	}
	all, err := a.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list is %+v, %v", all, err)
	}
}

// The provider renamed somebody: the list follows. The provider not
// answering, an empty name: the list keeps the one it had. Every visit is
// stamped.
func TestTheNameFollowsTheProviderAndSurvivesItBeingDown(t *testing.T) {
	a := admins(t)
	ctx := context.Background()
	admin, err := a.Enroll(ctx, subject, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct{ name, email, want string }{
		{"Sara Ali", "sara@example.iq", "Sara Ali"},
		{"Sara A. Ali", "sara@example.iq", "Sara A. Ali"},
		{"", "", "Sara A. Ali"},
	} {
		if err := a.Seen(ctx, admin.ID, step.name, step.email); err != nil {
			t.Fatal(err)
		}
		row, err := a.ByID(ctx, admin.ID)
		if err != nil || row.Name != step.want || row.Email != "sara@example.iq" || row.SeenAt == nil {
			t.Fatalf("after %q: %+v, %v", step.name, row, err)
		}
	}
}

// An id nobody has is nobody, not an error.
func TestByIDForNobodyIsNil(t *testing.T) {
	admin, err := admins(t).ByID(context.Background(), "adm_nobody")
	if err != nil || admin != nil {
		t.Fatalf("got %+v, %v", admin, err)
	}
}

func TestTheListIsEverybodyWhoCame(t *testing.T) {
	a := admins(t)
	ctx := context.Background()
	var first string
	for i, sub := range []string{subject, "392486038464954999"} {
		admin, err := a.Enroll(ctx, sub, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = admin.ID
		}
		time.Sleep(2 * time.Millisecond) // created_at orders the list
	}

	all, err := a.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != first {
		t.Fatalf("list is %+v", all)
	}
}
