package files

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func local(t *testing.T) *Local {
	t.Helper()
	l, err := NewLocal(filepath.Join(t.TempDir(), "files"), "http://localhost:8080/")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// A key goes in, a link comes out, and the link serves the bytes: the whole of
// what a laptop needs from a bucket.
func TestALocalFileIsPutLinkedAndServed(t *testing.T) {
	l := local(t)
	ctx := context.Background()

	if err := l.Put(ctx, "games/fixture.xo/icon.png", "image/png", []byte("png bytes")); err != nil {
		t.Fatal(err)
	}
	u, err := l.URL(ctx, "games/fixture.xo/icon.png", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if u != "http://localhost:8080/files/games/fixture.xo/icon.png" {
		t.Fatalf("url = %q", u)
	}

	srv := httptest.NewServer(l.Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/files/games/fixture.xo/icon.png")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("served %d", res.StatusCode)
	}

	if err := l.Delete(ctx, "games/fixture.xo/icon.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.dir, "games", "fixture.xo", "icon.png")); !os.IsNotExist(err) {
		t.Fatal("the file is still there after delete")
	}
	// Deleting what is already gone is not a failure: it is gone either way.
	if err := l.Delete(ctx, "games/fixture.xo/icon.png"); err != nil {
		t.Fatal(err)
	}
}

// A directory is never listed: what is in the bucket is nobody's to browse,
// and the laptop stand-in keeps that rule.
func TestADirectoryIsNotListed(t *testing.T) {
	l := local(t)
	if err := l.Put(context.Background(), "games/fixture.xo/icon.png", "image/png", []byte("png")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(l.Handler())
	defer srv.Close()

	for _, p := range []string{"/files/", "/files/games/", "/files/games/fixture.xo/", "/files/games/fixture.xo"} {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			t.Errorf("%s was listed", p)
		}
	}
}

// A key is made by us, so one that climbs out of the directory is a bug, and
// a bug must not become a write somewhere else.
func TestAKeyCannotLeaveTheDirectory(t *testing.T) {
	l := local(t)
	ctx := context.Background()
	for _, key := range []string{"../outside.png", "games/../../outside.png", "", "/", "games/"} {
		if err := l.Put(ctx, key, "image/png", []byte("x")); err == nil {
			t.Errorf("%q was written", key)
		}
		if _, err := l.URL(ctx, key, time.Hour); err == nil {
			t.Errorf("%q was linked", key)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(l.dir), "outside.png")); err == nil {
		t.Fatal("a file landed outside the directory")
	}
}
