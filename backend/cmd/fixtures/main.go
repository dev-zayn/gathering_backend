// Command fixtures is a development stand-in for game studios: it serves a
// directory of games, each with its manifest at /.well-known/gathering-game.json,
// so a laptop stack has something to register and open. The games are the
// workspace's gathering_games/, one folder each: a plain one (xo, tap) served
// as it is, a built one (the arcade's) from its dist/. It also runs a tiny
// "partner backend" under /partner/: a game's own server that receives the
// launch code on its entry page, spends it at the api's token endpoint with
// its client secret, asks the bridge who arrived, and verifies a room ticket
// against the api's public keys. That is the proof the partner model works
// end to end, and the sweep drives it.
package main

import (
	"bytes"
	"encoding/json"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	cfg := config{
		dir:      env("FIXTURES_DIR", "/app/games"),
		port:     env("PORT", "8091"),
		public:   strings.TrimRight(env("PUBLIC_URL", "http://localhost:8091"), "/"),
		api:      strings.TrimRight(env("API_URL", "http://localhost:8080"), "/"),
		clientID: env("PARTNER_CLIENT_ID", "fixture.xo"),
		secret:   os.Getenv("PARTNER_CLIENT_SECRET"),
	}
	if cfg.secret == "" {
		log.Printf("fixtures: PARTNER_CLIENT_SECRET is not set; /partner/exchange will be refused by the api until it is")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	client := &http.Client{Timeout: 10 * time.Second}
	partner := &partner{
		cfg: cfg, client: client,
		tickets: newTickets(cfg.api+"/.well-known/jwks.json", cfg.clientID, client),
	}
	mux.HandleFunc("/partner/entry", partner.entry)
	mux.HandleFunc("/partner/exchange", partner.exchange)
	mux.HandleFunc("/partner/verify-ticket", partner.verifyTicket)
	mux.Handle("/", &games{dir: cfg.dir, public: cfg.public})

	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("fixtures: serving %s on :%s as %s, api at %s", cfg.dir, cfg.port, cfg.public, cfg.api)
	log.Fatal(srv.ListenAndServe())
}

type config struct {
	dir, port, public, api string
	clientID, secret       string
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// games serves the directory. A manifest's entry has to be an absolute
// address, and the address the api reaches us on differs between a laptop
// and the compose network, so ${PUBLIC_URL} in a page or a document is
// filled in as it is served. Directories are never listed.
type games struct {
	dir    string
	public string
}

func (g *games) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	clean := g.built(path.Clean("/" + r.URL.Path))
	file := filepath.Join(g.dir, filepath.FromSlash(clean))
	info, err := os.Stat(file)
	if err == nil && info.IsDir() {
		file = filepath.Join(file, "index.html")
		info, err = os.Stat(file)
	}
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Named rather than guessed: the system's MIME table differs between a
	// laptop and the image, and a module script with the wrong type does not
	// run.
	switch filepath.Ext(file) {
	case ".json":
		w.Header().Set("Content-Type", "application/json")
		data = bytes.ReplaceAll(data, []byte("${PUBLIC_URL}"), []byte(g.public))
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data = bytes.ReplaceAll(data, []byte("${PUBLIC_URL}"), []byte(g.public))
	case ".js", ".mjs":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(file), info.ModTime(), bytes.NewReader(data))
}

// built answers a game that has a dist/ from it: that folder is a project, its
// sources and node_modules beside the build, and only what it built is the
// game. /outsider/assets/x.js is then outsider/dist/assets/x.js.
func (g *games) built(clean string) string {
	id, rest, _ := strings.Cut(strings.TrimPrefix(clean, "/"), "/")
	if id == "" {
		return clean
	}
	if info, err := os.Stat(filepath.Join(g.dir, id, "dist")); err == nil && info.IsDir() {
		return "/" + id + "/dist/" + rest
	}
	return clean
}

// partner is the game's own server, as small as one can be: an entry page
// that shows the code arrived, the exchange that spends it, and the check
// that a room ticket is ours and for this game.
type partner struct {
	cfg     config
	client  *http.Client
	tickets *tickets
}

// entry is where a launch lands when the manifest's entry points here. It
// shows the code arrived and goes no further: spending it is a server's job,
// and a page that does it has put the secret in a browser.
func (p *partner) entry(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	shown := "لم يصل رمز دخول."
	if code != "" {
		shown = "وصل رمز الدخول (" + html.EscapeString(short(code)) + "…). الخادم يستبدله الآن."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, `<!doctype html><html lang="ar" dir="rtl"><head><meta charset="utf-8">`+
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>خادم الشريك</title>`+
		`<style>html,body{margin:0;min-height:100%;background:#0f1222;color:#f4f4f8;font-family:system-ui,sans-serif}`+
		`main{min-height:100vh;display:grid;place-items:center;padding:24px;text-align:center}p{color:#aeb3cc}</style></head>`+
		`<body><main><div><h1>خادم الشريك</h1><p>`+shown+`</p>`+
		`<p>المسار: <code>`+html.EscapeString(r.URL.Query().Get("path"))+`</code></p></div></main></body></html>`)
}

// exchangeRequest is what the sweep, or a page of ours, hands the server:
// the code from the launch, and the PKCE half when the launch was a sign-in
// from the studio's own site.
type exchangeRequest struct {
	Code        string `json:"code"`
	Verifier    string `json:"code_verifier"`
	RedirectURI string `json:"redirect_uri"`
}

// hop is one call to the api, as it answered.
type hop struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// exchange spends the code at the api with the client secret, then asks the
// bridge who arrived, and answers both as they came back so the sweep can
// read them. Nothing is kept: a fixture has no players to remember.
func (p *partner) exchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in exchangeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || in.Code == "" {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {in.Code},
		"client_id":     {p.cfg.clientID},
		"client_secret": {p.cfg.secret},
	}
	if in.Verifier != "" {
		form.Set("code_verifier", in.Verifier)
	}
	if in.RedirectURI != "" {
		form.Set("redirect_uri", in.RedirectURI)
	}
	token, err := p.post(p.cfg.api+"/oauth/token", form)
	if err != nil {
		http.Error(w, `{"error":"api_unreachable"}`, http.StatusBadGateway)
		return
	}

	out := struct {
		Token   hop  `json:"token"`
		Context *hop `json:"context"`
	}{Token: token}

	if token.Status == http.StatusOK {
		var issued struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.Unmarshal(token.Body, &issued)
		ctxHop, err := p.get(p.cfg.api+"/v1/bridge/context", issued.AccessToken)
		if err != nil {
			http.Error(w, `{"error":"api_unreachable"}`, http.StatusBadGateway)
			return
		}
		out.Context = &ctxHop
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (p *partner) post(addr string, form url.Values) (hop, error) {
	res, err := p.client.PostForm(addr, form)
	if err != nil {
		return hop{}, err
	}
	defer res.Body.Close()
	return read(res)
}

func (p *partner) get(addr, bearer string) (hop, error) {
	req, err := http.NewRequest(http.MethodGet, addr, nil)
	if err != nil {
		return hop{}, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	res, err := p.client.Do(req)
	if err != nil {
		return hop{}, err
	}
	defer res.Body.Close()
	return read(res)
}

// read keeps the body as JSON when it is, and quotes it when it is not, so
// the answer always parses.
func read(res *http.Response) (hop, error) {
	body, err := io.ReadAll(io.LimitReader(res.Body, 256<<10))
	if err != nil {
		return hop{}, err
	}
	if !json.Valid(body) {
		body, _ = json.Marshal(string(body))
	}
	return hop{Status: res.StatusCode, Body: body}, nil
}

func short(code string) string {
	if len(code) > 8 {
		return code[:8]
	}
	return code
}
