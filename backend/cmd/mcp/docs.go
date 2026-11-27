package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// docs is the docs site as the model reads it: the pages llms.txt lists, each
// page's Markdown, and the brief, fetched together and kept for ttl. A failed
// refresh keeps the copy it had, so the site being redeployed is not the
// server being down; with no copy at all, a call says the docs are out of
// reach.
type docs struct {
	source string
	ttl    time.Duration
	client *http.Client

	mu      sync.Mutex
	got     *snapshot
	fetched time.Time
}

type snapshot struct {
	pages    []page
	markdown map[string]string // by page name
	brief    string
}

type page struct {
	Name        string `json:"name" jsonschema:"the name read_doc takes"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url" jsonschema:"the page as Markdown"`
}

func newDocs(source string, ttl time.Duration, client *http.Client) *docs {
	return &docs{source: source, ttl: ttl, client: client}
}

// The brief sits beside the site, built in gathering_docs.
const briefPath = "/guide/MINIGAME_AI_BRIEF.md"

func (d *docs) load(ctx context.Context) (*snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.got != nil && time.Since(d.fetched) < d.ttl {
		return d.got, nil
	}
	s, err := d.fetchAll(ctx)
	if err != nil {
		if d.got != nil {
			slog.Warn("mcp: docs refresh failed, keeping the copy", "err", err)
			d.fetched = time.Now() // try again after another ttl, not on every call
			return d.got, nil
		}
		return nil, fmt.Errorf("the docs are out of reach just now (%v); try again in a minute, or read %s/llms.txt", err, d.source)
	}
	d.got, d.fetched = s, time.Now()
	return s, nil
}

// A page in llms.txt: "- [Title](https://…/name.md): what it covers".
// Only the site's own pages, at its root; the guide and the console are not.
var listed = regexp.MustCompile(`(?m)^- \[([^\]]+)\]\((https?://[^/)]+/([a-z0-9-]+)\.md)\): (.*)$`)

func (d *docs) fetchAll(ctx context.Context) (*snapshot, error) {
	index, err := d.get(ctx, "/llms.txt")
	if err != nil {
		return nil, err
	}
	s := &snapshot{markdown: map[string]string{}}
	for _, m := range listed.FindAllStringSubmatch(index, -1) {
		p := page{Title: m[1], URL: m[2], Name: m[3], Description: m[4]}
		md, err := d.get(ctx, "/"+p.Name+".md")
		if err != nil {
			return nil, err
		}
		s.pages = append(s.pages, p)
		s.markdown[p.Name] = md
	}
	if len(s.pages) == 0 {
		return nil, fmt.Errorf("%s/llms.txt lists no pages", d.source)
	}
	if s.brief, err = d.get(ctx, briefPath); err != nil {
		return nil, err
	}
	return s, nil
}

func (d *docs) get(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.source+path, nil)
	if err != nil {
		return "", err
	}
	res, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %d", path, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return string(body), err
}

// The tools.

type listOut struct {
	Pages    []page `json:"pages"`
	Brief    string `json:"brief" jsonschema:"the minigame brief for AI models (get_minigame_brief)"`
	AllPages string `json:"all_pages" jsonschema:"every page in one file"`
}

func (d *docs) list(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listOut, error) {
	s, err := d.load(ctx)
	if err != nil {
		return nil, listOut{}, err
	}
	origin := originOf(s.pages[0].URL)
	return nil, listOut{Pages: s.pages, Brief: origin + briefPath, AllPages: origin + "/llms-full.txt"}, nil
}

type readIn struct {
	Page    string `json:"page" jsonschema:"the page's name from list_docs, such as rooms or bridge; its URL or path also works"`
	Section string `json:"section,omitempty" jsonschema:"optional: words from a heading on the page, to read only that section"`
}

func (d *docs) read(ctx context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, any, error) {
	s, err := d.load(ctx)
	if err != nil {
		return nil, nil, err
	}
	name := pageName(in.Page)
	md, ok := s.markdown[name]
	if !ok {
		return nil, nil, fmt.Errorf("no page %q; the pages are %s", in.Page, strings.Join(names(s.pages), ", "))
	}
	if in.Section != "" {
		want := strings.ToLower(strings.TrimSpace(in.Section))
		var headings []string
		for _, sec := range sections(md) {
			if sec.heading == "" {
				continue
			}
			if strings.Contains(strings.ToLower(sec.heading), want) {
				return text(sec.text), nil, nil
			}
			headings = append(headings, sec.heading)
		}
		return nil, nil, fmt.Errorf("no section of %s has %q in its heading; its sections are: %s", name, in.Section, strings.Join(headings, "; "))
	}
	return text(md), nil, nil
}

func (d *docs) brief(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	s, err := d.load(ctx)
	if err != nil {
		return nil, nil, err
	}
	return text(s.brief), nil, nil
}

type searchIn struct {
	Query string `json:"query" jsonschema:"what to look for: a bridge call, an error code, a field or plain words"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many sections to answer, 1 to 20; 6 when left out"`
}

type hit struct {
	Page    string `json:"page" jsonschema:"the page's name, for read_doc"`
	Title   string `json:"title" jsonschema:"the page's title"`
	Section string `json:"section" jsonschema:"the section's heading, for read_doc's section"`
	URL     string `json:"url"`
	Excerpt string `json:"excerpt"`
}

type searchOut struct {
	Results []hit `json:"results"`
}

func (d *docs) search(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
	s, err := d.load(ctx)
	if err != nil {
		return nil, searchOut{}, err
	}
	terms := termsOf(in.Query)
	if len(terms) == 0 {
		return nil, searchOut{}, fmt.Errorf("give some words to search for")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 6
	}
	limit = min(limit, 20)

	type scored struct {
		hit
		matched, score int
	}
	var found []scored
	for _, p := range s.pages {
		for _, sec := range sections(s.markdown[p.Name]) {
			heading, body := strings.ToLower(sec.heading), strings.ToLower(sec.text)
			matched, score := 0, 0
			for _, t := range terms {
				n := strings.Count(body, t) + 4*strings.Count(heading, t)
				if n > 0 {
					matched++
					score += n
				}
			}
			if matched == 0 {
				continue
			}
			found = append(found, scored{
				hit:     hit{Page: p.Name, Title: p.Title, Section: sec.heading, URL: p.URL, Excerpt: excerpt(sec.text, terms)},
				matched: matched,
				score:   score,
			})
		}
	}
	// The sections holding the most of the words first, then the most often.
	slices.SortStableFunc(found, func(a, b scored) int {
		if a.matched != b.matched {
			return b.matched - a.matched
		}
		return b.score - a.score
	})
	out := searchOut{Results: []hit{}}
	for _, f := range found[:min(limit, len(found))] {
		out.Results = append(out.Results, f.hit)
	}
	return nil, out, nil
}

// A page's sections: the text before its first "## " heading, then each
// "## " heading with what follows it, the heading's line included. A "## "
// inside a code fence is code, not a heading.
type section struct{ heading, text string }

func sections(md string) []section {
	var out []section
	cur := section{}
	var b strings.Builder
	fenced := false
	flush := func() {
		cur.text = strings.TrimSpace(b.String())
		if cur.text != "" {
			out = append(out, cur)
		}
		b.Reset()
	}
	for line := range strings.SplitSeq(md, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if !fenced && strings.HasPrefix(line, "## ") {
			flush()
			cur = section{heading: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return out
}

// The words of a query, lowercased, quotes and trailing punctuation off, so
// "room.end()" finds room.end and `another_game` finds another_game.
func termsOf(q string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		w = strings.Trim(w, "`'\"“”‘’()[]{},;:!?")
		w = strings.TrimSuffix(w, ".")
		if len([]rune(w)) >= 2 && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// Some 400 characters around the first word found, cut at spaces.
func excerpt(text string, terms []string) string {
	flat := strings.Join(strings.Fields(text), " ")
	lower := strings.ToLower(flat)
	at := -1
	for _, t := range terms {
		if i := strings.Index(lower, t); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	start, end := max(0, at-150), min(len(flat), max(0, at)+250)
	for start > 0 && flat[start] != ' ' {
		start--
	}
	for end < len(flat) && flat[end] != ' ' {
		end++
	}
	out := strings.TrimSpace(flat[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(flat) {
		out += "…"
	}
	return out
}

// "rooms", "/rooms", "rooms.md", "https://docs…/rooms.md" and "/" are all a
// page's name; the home page is "index".
func pageName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.IndexByte(s, '/'); j >= 0 {
			s = s[j:]
		} else {
			s = ""
		}
	}
	s, _, _ = strings.Cut(s, "#")
	s = strings.TrimSuffix(strings.Trim(s, "/"), ".md")
	if s == "" {
		return "index"
	}
	return s
}

func originOf(url string) string {
	if i := strings.Index(url, "://"); i >= 0 {
		if j := strings.IndexByte(url[i+3:], '/'); j >= 0 {
			return url[:i+3+j]
		}
	}
	return url
}

func names(pages []page) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.Name
	}
	return out
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
