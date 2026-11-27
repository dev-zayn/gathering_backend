// Command mcp is the developer docs' MCP server, at docs.<domain>/mcp: a
// model building a game for Gathering lists, searches and reads the docs,
// reads the minigame brief, and checks a manifest the way registration will,
// without leaving the conversation it is in.
//
// The docs come from the docs site itself (DOCS_SOURCE): its llms.txt and
// the .md copy of every page that its build writes, cached for a few minutes,
// so the server says what the site says and has nothing of its own to keep
// in step. A manifest is checked by the registry's own fetcher and
// model.Manifest.Check. Stateless, read-only, public: no secret, no database,
// nothing written. The only thing it fetches on a caller's word is a
// manifest URL, through the fetcher that refuses private addresses, one
// caller's calls limited per minute.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is the server's, in the initialize answer.
const version = "1.0.0"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	perMinute, err := strconv.Atoi(env("RATE_PER_MINUTE", "60"))
	if err != nil || perMinute < 1 {
		slog.Error("mcp: RATE_PER_MINUTE must be a positive number")
		os.Exit(1)
	}
	source := strings.TrimRight(env("DOCS_SOURCE", "https://docs.ga3da.fun"), "/")
	docs := newDocs(source, 5*time.Minute, &http.Client{Timeout: 10 * time.Second})
	check := newChecker(env("ALLOW_PRIVATE_HOSTS", "") == "true")

	srv := &http.Server{
		Addr:              ":" + env("PORT", "8095"),
		Handler:           routes(newServer(docs, check), newLimiter(perMinute, time.Minute)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()

	slog.Info("mcp: listening", "addr", srv.Addr, "docs", source)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("mcp: serve", "err", err)
		os.Exit(1)
	}
}

// routes is the MCP endpoint behind the limit, and a health check for the
// container. Stateless with JSON answers: every POST is a whole exchange, so
// any number of instances could stand behind one address and no session
// outlives its request.
func routes(server *mcp.Server, limit *limiter) http.Handler {
	endpoint := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	mux := http.NewServeMux()
	mux.Handle("/mcp", limit.wrap(endpoint))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// instructions is what a client hands its model about this server.
const instructions = `Gathering (گعدة) is a phone app where friends play small HTML games together in private rooms. These are its developer docs, for building a game: the manifest, the window.gathering bridge, rooms and the relay, sign-in, notifications, tournaments, and getting listed.
Start with search_docs for a question, or list_docs to see the pages; read_doc reads one page (or one of its sections). Before writing a whole game, read get_minigame_brief: it holds the rules every game must follow and a template. Before a studio registers a game, run check_manifest on its manifest, by its JSON or its public URL.`

// newServer is the MCP server with its five tools, every one read-only.
func newServer(docs *docs, check *checker) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:       "gathering-docs",
		Title:      "Gathering developer docs",
		Version:    version,
		WebsiteURL: "https://docs.ga3da.fun",
	}, &mcp.ServerOptions{Instructions: instructions})

	closed := false
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_docs",
		Title:       "List the docs",
		Description: "Every page of the Gathering developer docs, in reading order: its name (for read_doc), title, what it covers and its URL. Also the addresses of the minigame brief and of every page in one file.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed},
	}, docs.list)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_docs",
		Title:       "Search the docs",
		Description: "Searches every section of the Gathering developer docs for the words given (a bridge call such as room.end, an error code such as another_game, a field such as room.players, or plain words) and answers the best sections first, each with its page, heading and an excerpt. Read the whole page with read_doc.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed},
	}, docs.search)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_doc",
		Title:       "Read a page",
		Description: "One page of the Gathering developer docs as Markdown, by its name from list_docs (manifest, auth, bridge, rooms, …). With section, only the part under the heading that holds those words.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed},
	}, docs.read)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_minigame_brief",
		Title:       "The minigame brief",
		Description: "The brief for an AI model building a whole Gathering game: what to build and hand back, the MUST and MUST NOT rules, the manifest, the bootstrap to copy as is, a working template, room patterns, error codes, getting it into the store, and the self-check. Read it before writing a game.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed},
	}, docs.brief)

	open := true
	mcp.AddTool(server, &mcp.Tool{
		Name:        "check_manifest",
		Title:       "Check a manifest",
		Description: "Checks a game's manifest (/.well-known/gathering-game.json) exactly as registration in the Gathering console will, and answers everything wrong with it at once. Give the manifest's JSON as text, or its public https URL to also check that it is reachable, public and JSON. Fields Gathering does not read are listed too.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &open},
	}, check.check)

	return server
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
