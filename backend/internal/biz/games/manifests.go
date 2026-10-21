package games

// Reading what a game publishes about itself. It is the only place this
// binary talks to somebody else's server.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/outbound"
)

// MaxSize is generous for a document that describes one game, and small
// enough that a misconfigured server cannot hand us a disk image.
const MaxSize = 256 << 10

type HTTPFetcher struct{ c *http.Client }

// NewHTTPFetcher builds the fetcher. We fetch an admin's URL from inside our
// own network, so 169.254.169.254 and private subnets are refused unless
// allowed, which only a laptop with the fixtures beside it wants.
func NewHTTPFetcher(allowPrivate bool) *HTTPFetcher {
	return &HTTPFetcher{c: outbound.Client(10*time.Second, allowPrivate)}
}

// Fetch returns the parsed manifest and the bytes it came from: we act on the
// first and store the second.
func (h *HTTPFetcher) Fetch(ctx context.Context, url string) (*model.Manifest, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, errors.Internal(err)
	}
	req.Header.Set("Accept", "application/json")

	res, err := h.c.Do(req)
	if err != nil {
		return nil, nil, errors.Upstream("manifest_unreachable", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, nil, errors.Upstream("manifest_unreachable",
			fmt.Errorf("manifest: %s returned %d", url, res.StatusCode))
	}

	raw, err := io.ReadAll(io.LimitReader(res.Body, MaxSize+1))
	if err != nil {
		return nil, nil, errors.Upstream("manifest_unreachable", err)
	}
	if int64(len(raw)) > MaxSize {
		return nil, nil, errors.Upstream("manifest_unreachable",
			fmt.Errorf("manifest: %s is larger than %d bytes", url, MaxSize))
	}

	var m model.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, errors.Invalid(map[string]string{"manifest": "not_json"})
	}
	return &m, raw, nil
}

// Revoker ends credentials a game is holding right now. Its own type because
// a player removing a game and an admin suspending one are the same act.
type Revoker struct {
	Tokens    *identity.GameTokenStore
	Whitelist identity.Whitelist
}

// Revoke stops a game's live tokens; an empty player means every player's.
// The whitelist is what makes an already-minted token stop now.
func (r *Revoker) Revoke(ctx context.Context, gameID, playerID string) error {
	if playerID != "" {
		if err := r.Tokens.DeleteForPlayer(ctx, gameID, playerID); err != nil {
			return errors.Internal(err)
		}
	}
	if err := r.Whitelist.RevokeGame(ctx, gameID, playerID); err != nil {
		return errors.Internal(err)
	}
	return nil
}
