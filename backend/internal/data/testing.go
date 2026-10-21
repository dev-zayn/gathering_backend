package data

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// ForTest is one test package's own database, created here rather than expected.
// It returns "" when TEST_DATABASE_URL is unset, which is the signal to skip.
func ForTest(name string) (string, error) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		return "", nil
	}

	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	db := name + "_test"
	if err := createDB(base, db); err != nil {
		return "", err
	}
	u.Path = "/" + db
	return u.String(), nil
}

// createDB works around two things Postgres will not do: there is no IF NOT
// EXISTS for a database, and the name cannot be a parameter.
func createDB(admin, name string) error {
	if strings.ContainsAny(name, `"\`) {
		return fmt.Errorf("data: %q is not a database name", name)
	}

	conn, err := sql.Open("pgx", admin)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.Exec(`CREATE DATABASE "` + name + `"`)

	var pge *pgconn.PgError
	if errors.As(err, &pge) && pge.Code == "42P04" { // duplicate_database
		return nil
	}
	return err
}
