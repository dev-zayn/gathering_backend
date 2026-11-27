package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// limiter allows each caller so many requests a window, counted by address.
// Behind Caddy the caller is the first X-Forwarded-For entry: nothing but
// Caddy reaches this server, so the header is Caddy's. One process, so the
// counts are kept in memory; a restart forgets them, which is fine for a
// limit this loose.
type limiter struct {
	per    int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	start time.Time
	count map[string]int
}

func newLimiter(per int, window time.Duration) *limiter {
	return &limiter{per: per, window: window, now: time.Now, count: map[string]int{}}
}

// allow counts one request and says whether it is within the limit, and if
// not, the seconds until the window turns.
func (l *limiter) allow(who string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.start) >= l.window {
		l.start, l.count = now, map[string]int{}
	}
	if l.count[who] >= l.per {
		left := l.window - now.Sub(l.start)
		return false, int((left + time.Second - 1) / time.Second) // whole seconds, rounded up
	}
	l.count[who]++
	return true, 0
}

func (l *limiter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := l.allow(caller(r)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(wait))
			http.Error(w, "too many requests; try again in a minute", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func caller(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		first, _, _ := strings.Cut(fwd, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
