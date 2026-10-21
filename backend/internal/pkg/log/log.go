// Package log is the single logger the api uses, so one LOG_LEVEL and one
// output format apply everywhere.
package log

import (
	"context"
	"os"

	"github.com/charmbracelet/log"
)

type Logger = log.Logger

// New reads LOG_LEVEL and LOG_FORMAT from the environment. Anything other than
// LOG_FORMAT=text gives JSON, which is what CloudWatch wants.
func New(service string) *Logger {
	l := log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		Prefix:          service,
	})

	if lvl, err := log.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil {
		l.SetLevel(lvl)
	} else {
		l.SetLevel(log.InfoLevel)
	}

	if os.Getenv("LOG_FORMAT") != "text" {
		l.SetFormatter(log.JSONFormatter)
	}
	return l
}

type ctxKey struct{}

// Into puts a logger on the context so a handler deep in a call can log with
// the request's fields already attached.
func Into(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From returns the request's logger, or a plain one if the context carries none.
func From(ctx context.Context) *Logger {
	if l, ok := ctx.Value(ctxKey{}).(*Logger); ok {
		return l
	}
	return log.Default()
}
