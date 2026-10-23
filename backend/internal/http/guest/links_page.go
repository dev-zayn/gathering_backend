package guest

// The invite link's page itself: its words in both languages, the markup,
// the one style and the one script, and the policy that allows exactly
// those. It looks like the app: the arena's dark ground, the code as the
// hero number in the amber accent, chunky keys. No web font: the app's faces
// are named first for a phone that has them, then the system's own. No other
// face is named: a browser takes Arabic from the first named face that has
// any, and the system's Arabic is the one with every weight.

import (
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"net/url"
	"strconv"
	"strings"
)

// words is everything the page says, in one language: a room's link and a
// tournament's (docs/TOURNAMENTS.md, "The link") each have their own title,
// label, hint and not-found line.
type words struct {
	Lang, Dir string
	Brand     string

	// Join is the room's title; {game} is the game's name.
	Join      string
	CodeLabel string
	Copy      string
	Copied    string
	OpenIt    string
	GetIt     string
	NotFound  string
	TryAgain  string

	// The tournament's: TourJoin's {name} is the tournament's name.
	TourJoin      string
	TourCodeLabel string
	TourOpenIt    string
	TourNotFound  string
}

var arabic = words{
	Lang: "ar", Dir: "rtl", Brand: "گعدة",
	Join:      "انضم لغرفة {game} · گعدة",
	CodeLabel: "رمز الغرفة",
	Copy:      "انسخ الرمز",
	Copied:    "انّسخ الرمز",
	OpenIt:    "عندك گعدة؟ افتحها واكتب الرمز",
	GetIt:     "ما عندك گعدة؟ نزّلها",
	NotFound:  "هالغرفة ما موجودة، أو خلصت",
	TryAgain:  "ما كدرنا نفتح الرابط هسّه. جرّب بعد شوية.",

	TourJoin:      "انضم لبطولة {name} · گعدة",
	TourCodeLabel: "رمز البطولة",
	TourOpenIt:    "عندك گعدة؟ افتحها، ومن البطولات اكتب الرمز",
	TourNotFound:  "هالبطولة ما موجودة، أو خلصت",
}

var english = words{
	Lang: "en", Dir: "ltr", Brand: "Gathering",
	Join:      "Join a {game} room · Gathering",
	CodeLabel: "Room code",
	Copy:      "Copy the code",
	Copied:    "Code copied",
	OpenIt:    "Have Gathering? Open it and type the code.",
	GetIt:     "Don't have Gathering yet?",
	NotFound:  "This room doesn't exist, or it's over.",
	TryAgain:  "We couldn't open this link just now. Try again in a moment.",

	TourJoin:      "Join the {name} tournament · Gathering",
	TourCodeLabel: "Tournament code",
	TourOpenIt:    "Have Gathering? Open it and type the code under Tournaments.",
	TourNotFound:  "This tournament doesn't exist, or it's over.",
}

// prefersEnglish reads Accept-Language: English when its best en tag weighs
// more than its best ar tag, or as much and comes first. Only en and ar
// count, and a weight of 0 refuses a language. No header, or neither
// language named, is Arabic.
func prefersEnglish(header string) bool {
	enQ, arQ := 0.0, 0.0
	enAt, arAt := -1, -1
	for i, entry := range strings.Split(header, ",") {
		lang, q := weighed(entry)
		switch {
		case lang == "en" && q > enQ:
			enQ, enAt = q, i
		case lang == "ar" && q > arQ:
			arQ, arAt = q, i
		}
	}
	switch {
	case enAt < 0:
		return false
	case arAt < 0:
		return true
	}
	return enQ > arQ || (enQ == arQ && enAt < arAt)
}

// weighed is one Accept-Language entry's primary language, lower case, and
// its weight: 1 when it names none, 0 when the weight cannot be read.
func weighed(entry string) (string, float64) {
	tag, params, _ := strings.Cut(entry, ";")
	lang := strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	q := 1.0
	for _, p := range strings.Split(params, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || f < 0 || f > 1 {
			return lang, 0
		}
		q = f
	}
	return lang, q
}

// storeKey is one store the page sends a phone without the app to.
type storeKey struct{ Name, URL string }

// linkCard is what the page's card shows: the heading (a room's game, a
// tournament's name), the line under it (a tournament's game), the code and
// what it is called.
type linkCard struct {
	Heading   string
	Sub       string
	CodeLabel string
	Code      string
}

// pageView is what one form of the page draws. Card is the room's or the
// tournament's, nil on the not-found and try-again forms, which say Message
// instead. OpenIt is the hint under the card.
type pageView struct {
	W       words
	Title   string
	Style   template.CSS
	Script  template.JS
	Card    *linkCard
	OpenIt  string
	Icon    string
	Images  string
	Message string
	Stores  []storeKey
}

// origin is a link's scheme, host and port: where the browser fetches it
// from, and so the one place the page lets a picture come from. "" for a
// link that is not http or https, or whose host a policy cannot name as it
// is.
func origin(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	for _, r := range u.Host {
		if !hostSymbol(r) {
			return ""
		}
	}
	return u.Scheme + "://" + u.Host
}

// hostSymbol is a letter, a digit, a dot, a dash or the port's colon: all a
// host the policy names may hold, and nothing that could end a directive.
func hostSymbol(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == ':'
}

// policy is the page's Content-Security-Policy: nothing but its own style,
// the copy key's script on the form that has the key, and pictures from the
// one host the icon is drawn from.
func policy(script bool, images string) string {
	scripts := "'none'"
	if script {
		scripts = scriptHash
	}
	if images == "" {
		images = "'none'"
	}
	return "default-src 'none'; script-src " + scripts + "; style-src " + styleHash +
		"; img-src " + images + "; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}

// hashOf is a CSP hash source: the inline block whose text is exactly this
// may run, and nothing else inline may.
func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

var (
	styleHash  = hashOf(string(pageStyle))
	scriptHash = hashOf(string(copyScript))
)

// pageStyle is the arena's dark set (gathering_app/DESIGN.md): the deep
// navy ground, the card a step up, white ink, the amber accent for the code
// and the key that acts.
const pageStyle template.CSS = `
:root{color-scheme:dark}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;min-height:100vh;min-height:100svh;display:flex;justify-content:center;background:#0F1222;color:#F4F5FA;font:15px/1.6 "IBM Plex Sans Arabic",system-ui,-apple-system,sans-serif}
main{width:100%;max-width:440px;padding:32px 16px 40px;display:flex;flex-direction:column;gap:16px;text-align:center}
.brand{margin:0 0 8px;color:#FFB300;font:700 24px/1.2 "Lalezar","IBM Plex Sans Arabic",system-ui,sans-serif}
.card{display:flex;flex-direction:column;align-items:center;gap:8px;padding:28px 20px 24px;border-radius:20px;background:#1B2038}
.icon{width:88px;height:88px;margin-bottom:8px;border-radius:20px;background:#171B31;object-fit:cover}
h1{margin:0;font-size:22px;font-weight:700;line-height:1.4}
.label{margin:12px 0 0;color:#A9ADC6;font-size:13.5px;font-weight:600}
.code{margin:0;padding-left:.12em;color:#FFB300;font:600 44px/1.2 "Geist Mono",ui-monospace,"SF Mono",Menlo,Consolas,monospace;font-variant-numeric:tabular-nums;letter-spacing:.12em;-webkit-user-select:all;user-select:all}
.hint{margin:0;color:#A9ADC6}
.key{display:flex;align-items:center;justify-content:center;width:100%;min-height:48px;padding:0 20px;border:1px solid rgba(244,245,250,.14);border-radius:16px;background:#171B31;color:#F4F5FA;font:inherit;font-weight:700;text-decoration:none;cursor:pointer}
.key:active{transform:translateY(1px)}
.primary{margin-top:12px;border-color:#FFB300;background:#FFB300;color:#1A1300;box-shadow:0 4px 0 #E5A000}
.primary:active{background:#E5A000;box-shadow:0 3px 0 #E5A000}
.stores{display:flex;flex-direction:column;gap:12px;margin-top:8px}
[hidden]{display:none!important}
`

// copyScript shows the copy key and makes it copy the code: the clipboard
// where the browser offers one, else the code selected and copied the old
// way. Without it the key stays hidden and the code still selects whole.
const copyScript template.JS = `
(function () {
  var key = document.getElementById("copy");
  var code = document.getElementById("code");
  if (!key || !code) return;
  function done() {
    key.textContent = key.getAttribute("data-done");
  }
  function select() {
    var range = document.createRange();
    range.selectNodeContents(code);
    var picked = window.getSelection();
    picked.removeAllRanges();
    picked.addRange(range);
    try {
      if (document.execCommand("copy")) done();
    } catch (e) {}
  }
  key.addEventListener("click", function () {
    var text = code.textContent.trim();
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, select);
    } else {
      select();
    }
  });
  key.hidden = false;
})();
`

// page is every form of the link's page. The room's form has the game, the
// code, the copy key and its script; the others say their one line. The
// store keys are on every form.
var page = template.Must(template.New("invite-link").Parse(`<!doctype html>
<html lang="{{.W.Lang}}" dir="{{.W.Dir}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<meta name="color-scheme" content="dark">
<meta name="theme-color" content="#0F1222">
<title>{{.Title}}</title>
<style>{{.Style}}</style>
</head>
<body>
<main>
<p class="brand">{{.W.Brand}}</p>
{{- with .Card}}
<section class="card">
{{- if $.Icon}}
<img class="icon" src="{{$.Icon}}" alt="" width="88" height="88">
{{- end}}
{{- with .Heading}}
<h1 dir="auto">{{.}}</h1>
{{- end}}
{{- with .Sub}}
<p class="hint" dir="auto">{{.}}</p>
{{- end}}
<p class="label">{{.CodeLabel}}</p>
<p class="code" id="code" dir="ltr" translate="no">{{.Code}}</p>
<button class="key primary" id="copy" type="button" data-done="{{$.W.Copied}}" hidden>{{$.W.Copy}}</button>
</section>
<p class="hint">{{$.OpenIt}}</p>
{{- else}}
<section class="card">
<h1>{{.Message}}</h1>
</section>
{{- end}}
{{- if .Stores}}
<div class="stores">
<p class="hint">{{.W.GetIt}}</p>
{{- range .Stores}}
<a class="key" href="{{.URL}}">{{.Name}}</a>
{{- end}}
</div>
{{- end}}
</main>
{{- if .Card}}
<script>{{.Script}}</script>
{{- end}}
</body>
</html>
`))
