package authn

import (
	"net/http"
	"strings"
)

// Cookie is how the console's session travels instead of an Authorization
// header. HttpOnly, so no script on the page — ours, an extension's, an
// injected one — can read it; the browser sends it and nothing else touches
// it. SameSite=Strict keeps other sites from sending it at all.
//
// The browser sends a cookie by itself, which is what a forged request rides
// on. So a request carrying it must come from one of Origins: SameSite treats
// a sibling subdomain as the same site, and this does not.
type Cookie struct {
	Name    string
	Secure  bool
	Origins []string
}

// NewCookie names the console's cookie. Secure, it takes the __Host- prefix,
// which the browser only accepts on https with no Domain and Path=/ — so no
// subdomain can set one over it. A laptop on Safari, which will not keep a
// Secure cookie from http://localhost, turns Secure off.
func NewCookie(secure bool, origins []string) Cookie {
	name := "gathering_admin"
	if secure {
		name = "__Host-gathering_admin"
	}
	return Cookie{Name: name, Secure: secure, Origins: origins}
}

// Issue is the session, as the browser is told to keep it: for this browser
// session only, so closing the browser ends it as well as signing out.
func (c Cookie) Issue(value string) http.Cookie {
	return http.Cookie{
		Name: c.Name, Value: value, Path: "/",
		HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteStrictMode,
	}
}

// Clear tells the browser to drop it.
func (c Cookie) Clear() http.Cookie {
	out := c.Issue("")
	out.MaxAge = -1
	return out
}

// Read is the cookie's value in a Cookie header, or empty.
func (c Cookie) Read(header string) string {
	if c.Name == "" || header == "" {
		return ""
	}
	cookies, err := http.ParseCookie(header)
	if err != nil {
		return ""
	}
	for _, k := range cookies {
		if k.Name == c.Name {
			return k.Value
		}
	}
	return ""
}

// From reports whether a request carrying the cookie may be acted on. A
// browser names the origin on every cross-origin request and on every
// same-origin one that changes something, so an origin must be one of ours;
// a read with none is a same-origin GET, which no other site can make.
func (c Cookie) From(origin, method string) bool {
	if origin == "" {
		return method == http.MethodGet || method == http.MethodHead
	}
	for _, o := range c.Origins {
		if strings.TrimSpace(o) == origin {
			return true
		}
	}
	return false
}
