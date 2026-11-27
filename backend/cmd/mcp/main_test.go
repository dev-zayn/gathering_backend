package main

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
)

// The docs site as its build writes it: llms.txt naming the pages by their
// public address, a .md per page, the brief beside them.
const llmsTxt = `# Gathering developer docs

> What it is.

## Getting started

- [Introduction](https://docs.example/index.md): Build a game.
- [Rooms](https://docs.example/rooms.md): Who may be in a room.

## Build a game with an AI model

- [The minigame brief for AI models](https://docs.example/guide/MINIGAME_AI_BRIEF.md): one file.
- [The console](https://console.example): where a studio registers.
`

const roomsMd = "# Rooms\n\nA room belongs to one game and one host.\n\n" +
	"## The room's life\n\nA round ends with `room.end({ scores })` and the room goes back to its lobby.\n\n" +
	"```bash\n## not a heading\n```\n\n" +
	"## The ticket\n\nA ticket your server verifies. Refused `another_game` when the code is another game's.\n"

func docsSite(t *testing.T) *httptest.Server {
	t.Helper()
	files := map[string]string{
		"/llms.txt":                   llmsTxt,
		"/index.md":                   "# Introduction\n\nGathering is an app where friends play.\n",
		"/rooms.md":                   roomsMd,
		"/guide/MINIGAME_AI_BRIEF.md": "# Gathering minigame brief (for AI models)\n\n## 3. MUST\n",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// connect is a client talking to the server in memory.
func connect(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	site := docsSite(t)
	return connect(t, newServer(newDocs(site.URL, time.Minute, site.Client()), newChecker(true)))
}

// call is a tool's answer as text, and whether it was refused.
func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func decode[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	return v
}

func TestEveryToolIsListedAndReadOnly(t *testing.T) {
	cs := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	slices.Sort(got)
	want := []string{"check_manifest", "get_minigame_brief", "list_docs", "read_doc", "search_docs"}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func TestListDocs(t *testing.T) {
	out := decode[listOut](t, must(call(t, session(t), "list_docs", nil)))
	var got []string
	for _, p := range out.Pages {
		got = append(got, p.Name+"|"+p.Title+"|"+p.URL)
	}
	want := []string{
		"index|Introduction|https://docs.example/index.md",
		"rooms|Rooms|https://docs.example/rooms.md",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pages = %v, want %v (the guide and the console are not pages)", got, want)
	}
	if out.Brief != "https://docs.example/guide/MINIGAME_AI_BRIEF.md" || out.AllPages != "https://docs.example/llms-full.txt" {
		t.Fatalf("brief %q, all pages %q", out.Brief, out.AllPages)
	}
}

func TestReadDoc(t *testing.T) {
	cs := session(t)
	for _, name := range []string{"rooms", "/rooms", "Rooms.md", "https://docs.example/rooms.md#the-ticket"} {
		if got := must(call(t, cs, "read_doc", map[string]any{"page": name})); got != roomsMd {
			t.Errorf("read_doc %q = %q", name, got)
		}
	}
	if got := must(call(t, cs, "read_doc", map[string]any{"page": "/"})); !strings.HasPrefix(got, "# Introduction") {
		t.Errorf("the home page: %q", got)
	}

	got := must(call(t, cs, "read_doc", map[string]any{"page": "rooms", "section": "TICKET"}))
	if !strings.HasPrefix(got, "## The ticket") || strings.Contains(got, "room.end") {
		t.Errorf("the ticket alone: %q", got)
	}

	if got, refused := call(t, cs, "read_doc", map[string]any{"page": "lobby"}); !refused || !strings.Contains(got, "index, rooms") {
		t.Errorf("an unknown page: %v %q", refused, got)
	}
	if got, refused := call(t, cs, "read_doc", map[string]any{"page": "rooms", "section": "not a heading"}); !refused ||
		!strings.Contains(got, "The room's life; The ticket") {
		t.Errorf("a heading inside a fence is code, and an unknown section names the real ones: %v %q", refused, got)
	}
}

func TestSearchDocs(t *testing.T) {
	cs := session(t)
	out := decode[searchOut](t, must(call(t, cs, "search_docs", map[string]any{"query": "`room.end()`"})))
	if len(out.Results) != 1 || out.Results[0].Page != "rooms" || out.Results[0].Section != "The room's life" ||
		!strings.Contains(out.Results[0].Excerpt, "room.end") {
		t.Fatalf("room.end: %+v", out.Results)
	}

	// The section holding both words comes before one holding one of them
	// more often.
	out = decode[searchOut](t, must(call(t, cs, "search_docs", map[string]any{"query": "ticket another_game"})))
	if len(out.Results) == 0 || out.Results[0].Section != "The ticket" {
		t.Fatalf("ticket another_game: %+v", out.Results)
	}

	out = decode[searchOut](t, must(call(t, cs, "search_docs", map[string]any{"query": "room", "limit": 1})))
	if len(out.Results) != 1 {
		t.Fatalf("limit 1: %d results", len(out.Results))
	}
	out = decode[searchOut](t, must(call(t, cs, "search_docs", map[string]any{"query": "zebra"})))
	if out.Results == nil || len(out.Results) != 0 {
		t.Fatalf("nothing found is an empty list: %+v", out.Results)
	}
	if _, refused := call(t, cs, "search_docs", map[string]any{"query": " ? "}); !refused {
		t.Fatal("no words is refused")
	}
}

func TestGetMinigameBrief(t *testing.T) {
	if got := must(call(t, session(t), "get_minigame_brief", nil)); !strings.HasPrefix(got, "# Gathering minigame brief") {
		t.Fatalf("brief: %q", got)
	}
}

// A manifest registration takes, as the docs' home page shows it.
const goodManifest = `{
  "id": "yourstudio.game",
  "name": "اسم لعبتك",
  "publisher": { "tier": "partner", "name": "اسم استوديوك" },
  "version": "1.0.0",
  "mode": "embedded",
  "entry": "https://games.yourstudio.example/",
  "orientation": "portrait",
  "room": { "players": { "min": 2, "max": 8 }, "host": "peer", "pace": "realtime" }
}`

func TestCheckManifestText(t *testing.T) {
	cs := session(t)
	out := decode[checkOut](t, must(call(t, cs, "check_manifest", map[string]any{"manifest": goodManifest})))
	if !out.OK || len(out.Problems) != 0 || len(out.Ignored) != 0 || !strings.Contains(out.Next, "by its url") {
		t.Fatalf("good: %+v", out)
	}

	// Everything wrong at once, as registration answers it, and the fields
	// nothing reads.
	bad := strings.NewReplacer(
		`"mode": "embedded"`, `"mode": "external"`,
		`"version": "1.0.0",`, `"version": "1.0.0", "scopes": ["orders:write"], "tagline": "x",`,
		`"max": 8`, `"max": 8, "maximum": 9`,
	).Replace(goodManifest)
	out = decode[checkOut](t, must(call(t, cs, "check_manifest", map[string]any{"manifest": bad})))
	if out.OK || out.Problems["mode"] == "" || out.Problems["scopes"] == "" {
		t.Fatalf("bad: %+v", out)
	}
	if !slices.Equal(out.Ignored, []string{"room.players.maximum", "tagline"}) {
		t.Fatalf("ignored = %v", out.Ignored)
	}

	out = decode[checkOut](t, must(call(t, cs, "check_manifest", map[string]any{"manifest": "<!doctype html>"})))
	if out.OK || !strings.HasPrefix(out.Problems["manifest"], "not_json") {
		t.Fatalf("not JSON: %+v", out)
	}

	for _, args := range []map[string]any{{}, {"manifest": goodManifest, "url": "https://x.example"}} {
		if _, refused := call(t, cs, "check_manifest", args); !refused {
			t.Fatalf("one of the two, not %v", slices.Collect(maps.Keys(args)))
		}
	}
}

func TestCheckManifestURL(t *testing.T) {
	game := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/gathering-game.json":
			_, _ = w.Write([]byte(goodManifest))
		case "/spa/.well-known/gathering-game.json":
			_, _ = w.Write([]byte("<!doctype html><title>my game</title>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer game.Close()
	cs := session(t)

	out := decode[checkOut](t, must(call(t, cs, "check_manifest", map[string]any{"url": game.URL})))
	if !out.OK || out.URL != game.URL+"/.well-known/gathering-game.json" || !strings.Contains(out.Next, "Register a game") {
		t.Fatalf("an origin is its well-known address: %+v", out)
	}

	out = decode[checkOut](t, must(call(t, cs, "check_manifest", map[string]any{"url": game.URL + "/spa/"})))
	if out.OK || out.Problems["manifest"] != "not_json" {
		t.Fatalf("a catch-all route's page: %+v", out)
	}

	out = decode[checkOut](t, must(call(t, cs, "check_manifest", map[string]any{"url": game.URL + "/gone.json"})))
	if out.OK || !strings.HasPrefix(out.Problems["url"], "manifest_unreachable") || !strings.Contains(out.Problems["url"], "404") {
		t.Fatalf("a 404: %+v", out)
	}

	out = decode[checkOut](t, must(call(t, cs, "check_manifest", map[string]any{"url": "ftp://x.example/m.json"})))
	if out.OK || out.Problems["url"] == "" {
		t.Fatalf("not http: %+v", out)
	}
}

// Private addresses are refused unless allowed, as at registration.
func TestCheckManifestRefusesPrivateAddresses(t *testing.T) {
	game := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(goodManifest))
	}))
	defer game.Close()
	site := docsSite(t)
	cs := connect(t, newServer(newDocs(site.URL, time.Minute, site.Client()), newChecker(false)))
	out := decode[checkOut](t, must(call(t, cs, "check_manifest", map[string]any{"url": game.URL})))
	if out.OK || !strings.HasPrefix(out.Problems["url"], "manifest_unreachable") {
		t.Fatalf("127.0.0.1 is private: %+v", out)
	}
}

func TestTheDocsKeepTheirCopyWhenTheSiteIsDown(t *testing.T) {
	site := docsSite(t)
	d := newDocs(site.URL, 0, site.Client()) // every call refreshes
	cs := connect(t, newServer(d, newChecker(true)))
	must(call(t, cs, "read_doc", map[string]any{"page": "rooms"}))

	site.Close()
	if got := must(call(t, cs, "read_doc", map[string]any{"page": "rooms"})); got != roomsMd {
		t.Fatalf("kept copy: %q", got)
	}

	down := newDocs(site.URL, time.Minute, site.Client())
	got, refused := call(t, connect(t, newServer(down, newChecker(true))), "list_docs", nil)
	if !refused || !strings.Contains(got, "out of reach") {
		t.Fatalf("no copy at all: %v %q", refused, got)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	l := newLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	h := l.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	hit := func(fwd string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		r.RemoteAddr = "172.18.0.2:5000" // Caddy
		if fwd != "" {
			r.Header.Set("X-Forwarded-For", fwd)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	hit("203.0.113.7")
	hit("203.0.113.7, 172.18.0.2")
	if w := hit("203.0.113.7"); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("third in the minute: %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if w := hit("198.51.100.1"); w.Code != http.StatusNoContent {
		t.Fatalf("another caller: %d", w.Code)
	}
	now = now.Add(time.Minute)
	if w := hit("203.0.113.7"); w.Code != http.StatusNoContent {
		t.Fatalf("the next minute: %d", w.Code)
	}
}

// The way a client reaches it: stateless streamable HTTP at /mcp.
func TestOverHTTP(t *testing.T) {
	site := docsSite(t)
	srv := httptest.NewServer(routes(newServer(newDocs(site.URL, time.Minute, site.Client()), newChecker(true)), newLimiter(100, time.Minute)))
	defer srv.Close()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if got := cs.InitializeResult().ServerInfo.Name; got != "gathering-docs" {
		t.Fatalf("server %q", got)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "check_manifest") {
		t.Fatal("no instructions")
	}
	out := decode[listOut](t, must(call(t, cs, "list_docs", nil)))
	if len(out.Pages) != 2 {
		t.Fatalf("pages over HTTP: %+v", out)
	}

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("healthz: %v %v", err, res)
	}
}

// The scopes registration reads from its table are the ones this server
// checks against.
func TestKnownScopesAreTheTables(t *testing.T) {
	pg := biztest.DB(t)
	var keys []string
	if err := pg.Raw(`SELECT key FROM scopes ORDER BY key`).Scan(&keys).Error; err != nil {
		t.Fatal(err)
	}
	want := slices.Sorted(maps.Keys(knownScopes))
	if !slices.Equal(keys, want) {
		t.Fatalf("the scopes table holds %v; knownScopes in manifest.go says %v", keys, want)
	}
}

func must(text string, refused bool) string {
	if refused {
		panic("refused: " + text)
	}
	return text
}
