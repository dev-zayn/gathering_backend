package main

import (
	"context"
	"time"

	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Job is one piece of housekeeping the clock runs every tick: a sweep, a
// re-read. Run says what it did in a word or two, or nothing when there was
// nothing to say.
type Job struct {
	Name string
	Run  func(ctx context.Context) (string, error)
}

// clock is the api's own timer. Every job is safe to run twice, so a second
// copy of the api doing the same round costs a few queries and nothing else.
func clock(ctx context.Context, every time.Duration, jobs []Job, log *applog.Logger) {
	log.Info("clock started", "every", every, "jobs", len(jobs))

	// One round at boot, so a fresh deployment does not wait to do its first
	// useful thing.
	round(ctx, jobs, log)

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			round(ctx, jobs, log)
		}
	}
}

// round logs only when there was something to say. A failure is not retried
// here: the next round is the retry.
func round(ctx context.Context, jobs []Job, log *applog.Logger) {
	for _, j := range jobs {
		did, err := j.Run(ctx)
		if err != nil {
			log.Warn(j.Name+" failed", "err", err)
			continue
		}
		if did != "" {
			log.Info(j.Name, "did", did)
		}
	}
}
