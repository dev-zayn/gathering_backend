package biz

import (
	"context"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/identity"
)

// Maintenance is what the clock tidies: rows that stopped meaning anything.
// Rooms nobody is in join it with their milestone.
type Maintenance struct {
	// Devices are the phones pushed to; one whose session ended goes.
	Devices *identity.Devices

	// Codes are the one-time launch codes, kept for CodeRetention after they
	// die. Not zero: one that failed a minute ago is worth having while
	// somebody reads the logs about it.
	Codes         *identity.CodeStore
	CodeRetention time.Duration
}

// Swept says what one round removed.
type Swept struct {
	Devices int64
	Codes   int64
}

// Any says whether the round removed anything at all, so the clock's log
// line is written only when there is something to say.
func (s Swept) Any() bool { return s.Devices > 0 || s.Codes > 0 }

func (m *Maintenance) Sweep(ctx context.Context) (Swept, error) {
	var out Swept
	now := time.Now()
	if m.Devices != nil {
		n, err := m.Devices.Sweep(ctx, now)
		if err != nil {
			return out, err
		}
		out.Devices = n
	}
	if m.Codes != nil {
		n, err := m.Codes.Sweep(ctx, now.Add(-m.CodeRetention))
		if err != nil {
			return out, err
		}
		out.Codes = n
	}
	return out, nil
}
