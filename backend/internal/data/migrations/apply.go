package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Up applies everything pending, safely from every replica at once: one takes an
// advisory lock and the others wait, then find the work already done.
func Up(ctx context.Context, db *sql.DB) error {
	// Session-level, so a process killed mid-migration releases it rather than
	// wedging every later boot.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}

	// Per database, so only copies of this binary ever wait on each other.
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files,
		goose.WithSessionLocker(locker),
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		return err
	}

	_, err = provider.Up(ctx)
	return err
}
