package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"

	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Check answers whether one thing the api needs is reachable now.
type Check func(ctx context.Context) error

// readyTimeout is how long each check may take before it counts as down.
const readyTimeout = 2 * time.Second

// ready is /health's stricter twin: /health says the process is up, /ready
// that it can also reach what it needs. The body never names what failed,
// because anybody may ask; the log does.
func ready(api huma.API, checks map[string]Check) {
	type out struct {
		Body struct {
			Status string `json:"status"`
		}
	}
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)

	huma.Register(api, huma.Operation{
		OperationID: "ready",
		Method:      http.MethodGet,
		Path:        "/ready",
		Summary:     "Readiness",
		Description: "200 when Postgres and Redis both answer, 503 not_ready when either does not.",
		Tags:        []string{"internal"},
	}, func(ctx context.Context, _ *struct{}) (*out, error) {
		var failed []error
		for _, name := range names {
			cctx, cancel := context.WithTimeout(ctx, readyTimeout)
			err := checks[name](cctx)
			cancel()
			if err != nil {
				failed = append(failed, fmt.Errorf("%s: %w", name, err))
			}
		}
		if len(failed) > 0 {
			return nil, apperr.New(http.StatusServiceUnavailable, "not_ready").Wrap(errors.Join(failed...))
		}
		o := &out{}
		o.Body.Status = "ok"
		return o, nil
	})
}
