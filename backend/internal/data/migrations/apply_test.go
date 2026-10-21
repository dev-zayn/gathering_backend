package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/jamaa/gathering/backend/internal/data/migrations"
)

// Two connections racing on the same tables, which no fake reproduces. Point
// MIGRATE_TEST_DATABASE_URL at a database nobody minds losing; it skips in CI.
func TestReplicasBootingTogetherDoNotRaceEachOther(t *testing.T) {
	url := os.Getenv("MIGRATE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("no MIGRATE_TEST_DATABASE_URL")
	}

	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = admin.Close() }()

	// Its own database, so the goose bookkeeping starts empty — which is the
	// moment the race happens.
	const name = "migrate_race_test"
	for _, stmt := range []string{
		`drop database if exists ` + name,
		`create database ` + name,
	} {
		if _, err := admin.ExecContext(context.Background(), stmt); err != nil {
			t.Skipf("cannot prepare a database to race in: %v", err)
		}
	}
	defer func() { _, _ = admin.ExecContext(context.Background(), `drop database if exists `+name) }()

	target := replaceDB(url, name)

	// The real schema, not a stand-in one. Up reads what the package embeds now,
	// which means this races the migrations the binary actually applies.
	const replicas = 5
	var wg sync.WaitGroup
	errs := make([]error, replicas)

	for i := range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := sql.Open("pgx", target)
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = db.Close() }()
			errs[i] = migrations.Up(context.Background(), db)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			// Without the lock this is where it lands: goose_db_version does not
			// exist yet and every runner tries to create it.
			t.Errorf("replica %d: %v", i, err)
		}
	}
}

// replaceDB swaps the database name on a postgres URL, keeping everything else.
func replaceDB(url, name string) string {
	slash := len(url)
	for i := len("postgres://"); i < len(url); i++ {
		if url[i] == '/' {
			slash = i
			break
		}
	}
	rest := ""
	for i := slash; i < len(url); i++ {
		if url[i] == '?' {
			rest = url[i:]
			break
		}
	}
	return url[:slash] + "/" + name + rest
}
