// Package repositorytest gives other packages' integration tests a disposable,
// migrated repository, using the same convention as this package's own suite: set
// SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL to a database whose name ends in _test.
// Each test gets a random schema that is dropped afterwards. Without the variable
// the test is skipped.
package repositorytest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// Database is one isolated schema.
type Database struct {
	Repo   *repository.Repository
	Config *pgxpool.Config // reopen to simulate a restart
}

// Open returns a repository on a fresh schema with every db/migrations Up applied.
func Open(t testing.TB) Database {
	t.Helper()
	dsn := os.Getenv("SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL to a disposable *_test database for Postgres integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("refusing integration tests: database name must end in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("test database unavailable")
	}
	schema := "repository_test_" + strings.ReplaceAll(repository.NewID(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal("cannot create disposable test schema")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	entries, err := os.ReadDir(MigrationsDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(MigrationsDir(), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(string(b), "-- +goose Down")[0]
		if _, err = pool.Exec(ctx, up); err != nil {
			t.Fatalf("migration %s: %v", e.Name(), err)
		}
	}
	return Database{Repo: repository.New(pool), Config: cfg}
}

// Reopen returns a new repository on the same schema, as a restarted process would.
func (d Database) Reopen(t testing.TB) *repository.Repository {
	t.Helper()
	pool, err := pgxpool.NewWithConfig(context.Background(), d.Config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return repository.New(pool)
}

// MigrationsDir is the repository's db/migrations directory.
func MigrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "db", "migrations")
}
