// Package ws is the relay's door: GET /v1/rooms/{room}/socket, upgraded to a
// WebSocket once everything that can refuse it has had its say as a plain
// HTTP answer. It holds no rules: who may open a socket is the rooms', what
// travels over it is the relay's. The WebSocket library lives here and only
// here.
package ws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"github.com/jamaa/gathering/backend/internal/biz/relay"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// Subprotocol is what a client offers and the server selects. An upgrade
// that does not offer it is refused: a client that does not name the
// protocol does not know the frames.
const Subprotocol = "gathering.v1"

// bearerPrefix carries the session token as a second offered subprotocol,
// for a browser, which cannot set headers on a WebSocket.
const bearerPrefix = "bearer."

// Path is the socket's route.
const Path = "/v1/rooms/{room}/socket"

// Handler is the socket's route.
type Handler struct {
	// Resolve is the same resolver every guarded call goes through: the
	// whitelisted jti, the kind of token.
	Resolve authn.Resolver

	// Rooms says who may open a socket to a room; Hub runs it once open.
	Rooms *rooms.Service
	Hub   *relay.Hub
}

// Register puts the route straight onto the router, outside the API: an
// upgrade is not an operation and has no body to describe.
func (h *Handler) Register(api huma.API) {
	api.Adapter().Handle(&huma.Operation{Method: http.MethodGet, Path: Path}, func(ctx huma.Context) {
		r, w := humachi.Unwrap(ctx)
		h.serve(w, r, ctx.Param("room"))
	})
}

// serve refuses before upgrading — 426 not a WebSocket, 400 no subprotocol,
// 401 no session, 403 the wrong token, 422 a bad after, then the rooms'
// answer — and only then upgrades and hands the socket to the hub.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, roomID string) {
	if h.Hub == nil || h.Rooms == nil || h.Resolve == nil {
		refuse(w, apperr.New(http.StatusServiceUnavailable, "relay_unavailable"))
		return
	}
	if !upgrade(r) {
		refuse(w, apperr.New(http.StatusUpgradeRequired, "upgrade_required"))
		return
	}
	offered := tokens(r.Header.Values("Sec-WebSocket-Protocol"))
	if !contains(offered, Subprotocol) {
		refuse(w, apperr.BadRequest("subprotocol_required"))
		return
	}

	authorization := r.Header.Get("Authorization")
	if authorization == "" {
		if t := bearerOf(offered); t != "" {
			authorization = "Bearer " + t
		}
	}
	session, reason, err := h.Resolve(r.Context(), authorization)
	if e, ok := err.(*apperr.Error); ok && e.Status < http.StatusInternalServerError {
		refuse(w, e)
		return
	}
	if err != nil {
		refuse(w, apperr.Upstream("identity_unavailable", err))
		return
	}
	if session == nil {
		refuse(w, apperr.Unauthorized(reason))
		return
	}
	if !session.Is(caller.ScopeSession) {
		refuse(w, apperr.Forbidden("wrong_token"))
		return
	}

	after, ok := afterOf(r.URL.Query().Get("after"))
	if !ok {
		refuse(w, apperr.Invalid(map[string]string{"after": "must_be_a_whole_number"}))
		return
	}
	if !ids.Is(ids.Room, roomID) {
		refuse(w, apperr.NotFound("not_found"))
		return
	}
	if err := h.Rooms.Admit(r.Context(), roomID, session.Subject); err != nil {
		refuse(w, err)
		return
	}

	// The bearer is the credential, offered explicitly on every connect;
	// nothing ambient rides along, so there is no cross-site request to
	// forge and any origin may connect.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{Subprotocol},
		InsecureSkipVerify: true,
	})
	if err != nil {
		return // Accept has answered
	}
	// Frames are bounded by the relay, which drops an oversized one and
	// keeps the socket; the library's own limit would close it instead.
	c.SetReadLimit(-1)

	// The socket outlives the request: its context keeps the request's
	// values and drops its cancellation.
	h.Hub.Serve(context.WithoutCancel(r.Context()), socket{c: c}, roomID, session.Subject, after)
}

// socket is the library's connection as the relay drives it.
type socket struct{ c *websocket.Conn }

func (s socket) Next(ctx context.Context, max int) ([]byte, error) {
	_, r, err := s.c.Reader(ctx)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > max {
		// Read to its end, so the next frame starts where it should.
		if _, err := io.Copy(io.Discard, r); err != nil {
			return nil, err
		}
		return nil, relay.ErrTooLarge
	}
	return b, nil
}

func (s socket) Send(ctx context.Context, frame []byte) error {
	return s.c.Write(ctx, websocket.MessageText, frame)
}

func (s socket) Close(code int, reason string) error {
	return s.c.Close(websocket.StatusCode(code), reason)
}

// upgrade is a request asking for a WebSocket.
func upgrade(r *http.Request) bool {
	return contains(tokens(r.Header.Values("Upgrade")), "websocket") &&
		contains(tokens(r.Header.Values("Connection")), "upgrade")
}

// tokens splits comma-separated header values.
func tokens(values []string) []string {
	var out []string
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// bearerOf is the session token offered as a subprotocol, if one was.
func bearerOf(offered []string) string {
	for _, p := range offered {
		if t, ok := strings.CutPrefix(p, bearerPrefix); ok && t != "" {
			if raw, ok := token.Bearer("Bearer " + t); ok {
				return raw
			}
		}
	}
	return ""
}

// afterOf reads ?after=: absent is a first connect.
func afterOf(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n >= 0
}

// refuse is the API's one error body, before there is a socket to send
// anything on.
func refuse(w http.ResponseWriter, err error) {
	e, ok := err.(*apperr.Error)
	if !ok {
		e = apperr.Internal(err)
	}
	b, _ := json.Marshal(e)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_, _ = w.Write(b)
}
