package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// put writes a file under dir, making its folders.
func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGamesServesPlainAndBuiltGames(t *testing.T) {
	dir := t.TempDir()
	// A plain game: the folder is the page.
	put(t, dir, "xo/index.html", "xo page")
	put(t, dir, "xo/.well-known/gathering-game.json", `{"entry": "${PUBLIC_URL}/xo/"}`)
	// A built game: a project whose dist/ is the page.
	put(t, dir, "outsider/index.html", "outsider source")
	put(t, dir, "outsider/src/main.tsx", "source")
	put(t, dir, "outsider/dist/index.html", "outsider page")
	put(t, dir, "outsider/dist/assets/index.js", "script")
	put(t, dir, "outsider/dist/.well-known/gathering-game.json", `{"entry": "${PUBLIC_URL}/outsider/"}`)

	srv := httptest.NewServer(&games{dir: dir, public: "http://fixtures:8091"})
	defer srv.Close()

	for _, c := range []struct {
		path string
		code int
		body string
	}{
		{"/xo/", http.StatusOK, "xo page"},
		{"/xo/.well-known/gathering-game.json", http.StatusOK, `{"entry": "http://fixtures:8091/xo/"}`},
		{"/outsider/", http.StatusOK, "outsider page"},
		{"/outsider/assets/index.js", http.StatusOK, "script"},
		{"/outsider/.well-known/gathering-game.json", http.StatusOK, `{"entry": "http://fixtures:8091/outsider/"}`},
		// The project around the build is not the game.
		{"/outsider/src/main.tsx", http.StatusNotFound, ""},
		{"/outsider/dist/index.html", http.StatusNotFound, ""},
		{"/outsider/../xo/", http.StatusOK, "xo page"},
		{"/nothing/", http.StatusNotFound, ""},
	} {
		res, err := http.Get(srv.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != c.code {
			t.Errorf("%s: status %d, want %d", c.path, res.StatusCode, c.code)
			continue
		}
		if c.code == http.StatusOK && string(body) != c.body {
			t.Errorf("%s: body %q, want %q", c.path, body, c.body)
		}
	}
}
