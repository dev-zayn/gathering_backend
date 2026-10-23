// Package http is the HTTP surface and holds no rules of its own. Which folder a
// route is in is who may call it: player, admin or guest.
package http

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// New returns the router to serve and the API to register operations on.
func New(name, version string, log *applog.Logger, origins ...string) (chi.Router, huma.API) {
	r := chi.NewRouter()

	// CORS, and only for the origins named. An empty list is a deployment where
	// the API is the only origin there is.
	if len(origins) > 0 {
		r.Use(allowOrigins(origins))
	}

	r.Use(middleware.RequestID)
	// No RealIP: it rewrites RemoteAddr from headers a client sets, and nothing
	// here reads RemoteAddr anyway.
	r.Use(requestLogger(log))
	r.Use(recoverer)
	// The states vocabulary is 150 KB of Arabic and compresses to a fifth of that.
	// Nothing else we serve is big enough to care either way. Neither this nor
	// the deadline touches a room's socket: it has no body to compress, and it
	// is meant to outlive any request.
	r.Use(unlessUpgrade(middleware.Compress(5)))
	r.Use(unlessUpgrade(middleware.Timeout(30 * time.Second)))

	cfg := huma.DefaultConfig("Gathering "+name, version)
	cfg.DocsPath = "/docs"
	cfg.OpenAPIPath = "/openapi"

	// Huma stamps a "$schema" key and a describedBy header into every response.
	// Both go, and the hook too — clearing only the transformer lets it come back.
	cfg.Transformers = nil
	cfg.CreateHooks = nil

	api := humachi.New(r, cfg)
	health(api, name, version)
	return r, api
}

// unlessUpgrade keeps a middleware off a WebSocket upgrade. The timeout's
// 504 written after a socket closed would land on a hijacked connection, and
// its deadline would cut every socket off after thirty seconds.
func unlessUpgrade(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}

// requestLogger attaches the request id to the logger for the whole request and
// records one line per request when it finishes.
func requestLogger(log *applog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := middleware.GetReqID(r.Context())
			l := log.With("request_id", id)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			defer func() {
				l.Debug("request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"ms", time.Since(start).Milliseconds(),
				)
			}()

			next.ServeHTTP(ww, r.WithContext(applog.Into(r.Context(), l)))
		})
	}
}

// recoverer turns a panic into the same internal_error body any other failure
// would produce, with the panic itself in the logs and never in the response.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				applog.From(r.Context()).Error("panic", "value", rec)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal_error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func health(api huma.API, name, version string) {
	type out struct {
		Body struct {
			Service string `json:"service"`
			Version string `json:"version"`
			Status  string `json:"status"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Liveness",
		Tags:        []string{"internal"},
	}, func(ctx context.Context, _ *struct{}) (*out, error) {
		o := &out{}
		o.Body.Service = name
		o.Body.Version = version
		o.Body.Status = "ok"
		return o, nil
	})
}

// statusError is how an *errors.Error reaches the wire, with the same fields and
// tags so the body stays { "error": … } and Huma adds no envelope.
type statusError apperr.Error

func (e *statusError) Error() string  { return e.Code }
func (e *statusError) GetStatus() int { return e.Status }

func init() {
	// Every failure Huma reports leaves through here, so the whole API has one
	// body shape.
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		for _, err := range errs {
			var e *apperr.Error
			if errors.As(err, &e) {
				return (*statusError)(e)
			}
		}
		out := &apperr.Error{Status: status, Code: codeFor(status, errs)}
		if f := fieldsOf(errs); len(f) > 0 {
			out.Code = "invalid_fields"
			out.Fields = f
		}
		return (*statusError)(out)
	}

	// The body of a 500 says only internal_error, so the reason is written to
	// the log here, with the request's id, or it is lost.
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		se := huma.NewError(status, msg, errs...)
		if se.GetStatus() >= http.StatusInternalServerError {
			cause := errors.Join(errs...)
			var e *apperr.Error
			if errors.As(cause, &e) && e.Cause != nil {
				cause = e.Cause
			}
			applog.From(ctx.Context()).Error("request failed",
				"method", ctx.Method(), "path", ctx.URL().Path,
				"status", se.GetStatus(), "err", cause)
		}
		return se
	}
}

// fieldsOf turns Huma's validation complaints into our fields map, naming each
// field as the client sent it rather than as Huma located it.
func fieldsOf(errs []error) map[string]string {
	var f map[string]string
	for _, err := range errs {
		var d *huma.ErrorDetail
		if !errors.As(err, &d) {
			continue
		}
		if f == nil {
			f = map[string]string{}
		}
		loc := strings.TrimPrefix(d.Location, "body.")
		if loc == "" {
			loc = "body"
		}
		f[loc] = d.Message
	}
	return f
}

// codeFor names the failures Huma raises before a handler runs. Request bodies
// that do not parse or do not validate are the common ones.
func codeFor(status int, errs []error) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "invalid_fields"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusTooManyRequests:
		return "too_many_requests"
	default:
		return "internal_error"
	}
}

// Serve runs the router until the process is asked to stop, then gives requests
// in flight ten seconds to finish.
func Serve(ctx context.Context, addr string, h http.Handler, log *applog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			done <- err
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}

// allowOrigins answers preflight for the named origins only. Exact, no wildcard:
// an origin that can read the console's token can read every player's record.
func allowOrigins(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.TrimSpace(o)] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Max-Age", "600")
				// Vary, or a cache hands one origin's answer to another.
				h.Add("Vary", "Origin")
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
