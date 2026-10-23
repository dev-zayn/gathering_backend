package data_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
)

// probe is a database of this package's own with one table to write to.
func probe(t *testing.T) *gorm.DB {
	t.Helper()
	dsn, err := data.ForTest("gathering_data")
	if err != nil {
		t.Fatal(err)
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := data.Open(data.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS probe (id text PRIMARY KEY)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`TRUNCATE probe`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// seen says whether a row is there for anybody outside the transaction.
func seen(t *testing.T, db *gorm.DB, id string) bool {
	t.Helper()
	var n int64
	if err := db.Raw(`SELECT count(*) FROM probe WHERE id = ?`, id).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// What a transaction leaves with AfterCommit runs once it has committed, in
// order, and sees the write; a rollback runs none of it; outside a
// transaction it runs at once.
func TestAfterCommitRunsOnceTheWriteIsThere(t *testing.T) {
	db := probe(t)
	ctx := context.Background()
	tx := data.NewTx(db)

	var ran []string
	err := tx.Do(ctx, func(ctx context.Context) error {
		if err := data.Conn(ctx, db).Exec(`INSERT INTO probe (id) VALUES ('a')`).Error; err != nil {
			return err
		}
		data.AfterCommit(ctx, func() {
			if seen(t, db, "a") {
				ran = append(ran, "first:seen")
			} else {
				ran = append(ran, "first:unseen")
			}
		})
		data.AfterCommit(ctx, func() { ran = append(ran, "second") })
		if len(ran) != 0 {
			t.Errorf("ran inside the transaction: %v", ran)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"first:seen", "second"}) {
		t.Fatalf("after the commit: %v", ran)
	}

	ran = nil
	refused := errors.New("refused")
	err = tx.Do(ctx, func(ctx context.Context) error {
		if err := data.Conn(ctx, db).Exec(`INSERT INTO probe (id) VALUES ('b')`).Error; err != nil {
			return err
		}
		data.AfterCommit(ctx, func() { ran = append(ran, "rolled back") })
		return refused
	})
	if !errors.Is(err, refused) || len(ran) != 0 || seen(t, db, "b") {
		t.Fatalf("a rollback: err %v, ran %v", err, ran)
	}

	data.AfterCommit(ctx, func() { ran = append(ran, "no transaction") })
	if !slices.Equal(ran, []string{"no transaction"}) {
		t.Fatalf("outside a transaction: %v", ran)
	}
}
