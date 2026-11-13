package main

import (
	"context"

	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var migrationName = regexp.MustCompile(`^(\d+)_[a-z0-9_]+\.sql$`)

// migrate applies the Up section of each pending db/migrations file in version order
// before the repository is composed (packages/go/repository/README.md). It records
// versions in goose's goose_db_version table, so the goose CLI in docs/storage.md
// sees the same state and can run Down. An advisory lock prevents two API processes
// from migrating at once. Each migration runs in its own transaction.
func migrate(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.New("migrations directory unavailable")
	}
	type migration struct {
		version int64
		up      string
	}
	var pending []migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		version, _ := strconv.ParseInt(m[1], 10, 64)
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return errors.New("migration unreadable")
		}
		up, ok := gooseUp(string(data))
		if !ok {
			return fmt.Errorf("migration %d has no goose Up section", version)
		}
		pending = append(pending, migration{version, up})
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].version < pending[j].version })

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer conn.Release()
	const lock = 4330_6_13
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lock); err != nil {
		return errors.New("migration bookkeeping failed")
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lock) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS goose_db_version (
		id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL, tstamp timestamp DEFAULT now())`); err != nil {
		return errors.New("migration bookkeeping failed")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied)
		SELECT 0, true WHERE NOT EXISTS (SELECT 1 FROM goose_db_version)`); err != nil {
		return errors.New("migration bookkeeping failed")
	}
	var current int64
	if err := conn.QueryRow(ctx, `SELECT coalesce((SELECT version_id FROM goose_db_version ORDER BY id DESC LIMIT 1), 0)`).Scan(&current); err != nil {
		return errors.New("migration bookkeeping failed")
	}
	for _, m := range pending {
		if m.version <= current {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return errors.New("migration bookkeeping failed")
		}
		// No arguments: pgx sends the multi-statement script with the simple protocol.
		if _, err := tx.Exec(ctx, m.up); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %d failed", m.version)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, m.version); err != nil {
			_ = tx.Rollback(ctx)
			return errors.New("migration bookkeeping failed")
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("migration bookkeeping failed")
		}
	}
	return nil
}

// gooseUp extracts the SQL between "-- +goose Up" and "-- +goose Down", dropping the
// StatementBegin/End markers, which only matter to goose's own statement splitter.
func gooseUp(script string) (string, bool) {
	start := strings.Index(script, "-- +goose Up")
	if start < 0 {
		return "", false
	}
	body := script[start+len("-- +goose Up"):]
	if end := strings.Index(body, "-- +goose Down"); end >= 0 {
		body = body[:end]
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "-- +goose StatementBegin" || t == "-- +goose StatementEnd" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), true
}
