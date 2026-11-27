package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// checker checks a manifest the way registration does (games.Registry's
// read and parse): the same URL rules, the same fetcher, the same
// model.Manifest.Check. A few fetches at a time: each may wait ten seconds
// on somebody else's server.
type checker struct {
	fetcher *games.HTTPFetcher
	slots   chan struct{}
}

func newChecker(allowPrivate bool) *checker {
	return &checker{fetcher: games.NewHTTPFetcher(allowPrivate), slots: make(chan struct{}, 4)}
}

// knownScopes is the scopes table, which the api reads at registration and
// this server cannot: it holds no database. TestKnownScopesAreTheTables
// fails when a migration changes the table and this does not follow.
var knownScopes = map[string]bool{
	"identity:read": true,
	"friends:read":  true,
	"notify:send":   true,
	"results:write": true,
}

type checkIn struct {
	Manifest string `json:"manifest,omitempty" jsonschema:"the manifest's JSON as text; give this or url"`
	URL      string `json:"url,omitempty" jsonschema:"the manifest's public URL, or the game's origin (the well-known path is added); give this or manifest"`
}

type checkOut struct {
	OK       bool              `json:"ok" jsonschema:"true when registration would take the manifest"`
	URL      string            `json:"url,omitempty" jsonschema:"the address that was fetched"`
	Problems map[string]string `json:"problems,omitempty" jsonschema:"everything registration would refuse, by field"`
	Ignored  []string          `json:"ignored,omitempty" jsonschema:"fields Gathering does not read: misspelt, or not part of a manifest"`
	Next     string            `json:"next" jsonschema:"what to do now"`
}

func (c *checker) check(ctx context.Context, _ *mcp.CallToolRequest, in checkIn) (*mcp.CallToolResult, checkOut, error) {
	text, raw := strings.TrimSpace(in.Manifest), strings.TrimSpace(in.URL)
	if (text == "") == (raw == "") {
		return nil, checkOut{}, errors.New("give the manifest's JSON as manifest, or its URL as url: one of the two")
	}

	var out checkOut
	var m *model.Manifest
	var body []byte
	if raw != "" {
		url, ok := model.ManifestURL(raw)
		if !ok {
			out.Problems = map[string]string{"url": "must be an http or https URL"}
			return nil, out.finish(false), nil
		}
		out.URL = url
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return nil, checkOut{}, ctx.Err()
		}
		got, b, err := c.fetcher.Fetch(ctx, url)
		<-c.slots
		if err != nil {
			out.Problems = refused(err)
			return nil, out.finish(false), nil
		}
		m, body = got, b
	} else {
		body = []byte(text)
		if len(body) > games.MaxSize {
			out.Problems = map[string]string{"manifest": fmt.Sprintf("larger than %d bytes", games.MaxSize)}
			return nil, out.finish(false), nil
		}
		m = &model.Manifest{}
		if err := json.Unmarshal(body, m); err != nil {
			out.Problems = map[string]string{"manifest": "not_json: " + err.Error()}
			return nil, out.finish(false), nil
		}
	}

	out.Ignored = ignored(body)
	if bad := m.Check(knownScopes); len(bad) > 0 {
		out.Problems = bad
	}
	return nil, out.finish(raw != ""), nil
}

func (o checkOut) finish(fetched bool) checkOut {
	o.OK = len(o.Problems) == 0
	switch {
	case !o.OK:
		o.Next = "Fix every problem listed: registration refuses the manifest until none is left, and answers them all at once as this does. The fields are at https://docs.ga3da.fun/manifest.md."
	case fetched:
		o.Next = "Registration would take it. A studio registers it in the console, https://console.ga3da.fun, under My games → Register a game, with this URL; testers, review and publishing follow (https://docs.ga3da.fun/listing.md)."
	default:
		o.Next = "The document is right. Once it is hosted, check it again by its url: it must answer 200 with this JSON, publicly, within ten seconds."
	}
	return o
}

// refused is a fetch's failure in the words registration would answer.
func refused(err error) map[string]string {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return map[string]string{"url": err.Error()}
	}
	if len(e.Fields) > 0 {
		return e.Fields
	}
	why := e.Code
	if e.Cause != nil {
		why += ": " + e.Cause.Error()
	}
	return map[string]string{"url": why}
}

// ignored lists the keys of a manifest that no field of model.Manifest
// reads, by path: room.players.maximum, tagline. JSON ignores them, so does
// registration, and a model that wrote one expected it to mean something.
func ignored(body []byte) []string {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return nil
	}
	var out []string
	walk(doc, reflect.TypeFor[model.Manifest](), "", &out)
	slices.Sort(out)
	return out
}

func walk(obj map[string]any, t reflect.Type, prefix string, out *[]string) {
	fields := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields[name] = f.Type
		}
	}
	for key, value := range obj {
		ft, ok := fields[key]
		if !ok {
			*out = append(*out, prefix+key)
			continue
		}
		if inner, isObj := value.(map[string]any); isObj && ft.Kind() == reflect.Struct {
			walk(inner, ft, prefix+key+".", out)
		}
	}
}
