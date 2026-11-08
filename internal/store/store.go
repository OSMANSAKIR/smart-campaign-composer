// Package store owns the Postgres connection pool and the embedded schema migrations.
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationLockID is the advisory lock key that serializes concurrent `migrate` runs.
const migrationLockID = 7_261_108_001

// Open creates a pool and pings the database once.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// OpenWithRetry retries Open until ctx ends, so services survive starting before Postgres is ready.
func OpenWithRetry(ctx context.Context, url string) (*pgxpool.Pool, error) {
	delay := 250 * time.Millisecond
	for {
		pool, err := Open(ctx, url)
		if err == nil {
			return pool, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("database not reachable: %w", err)
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}

func migrationNames() ([]string, error) {
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	slices.Sort(names) // timestamp prefixes make lexical order the apply order
	return names, nil
}

// Migrate applies every embedded migration that is not yet recorded, each in its own transaction.
// An advisory lock makes concurrent runs (e.g. two `migrate` containers) safe.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (applied []string, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return nil, err
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID) //nolint:errcheck // released on disconnect anyway

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	names, err := migrationNames()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		version := versionOf(name)
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&exists); err != nil {
			return applied, err
		}
		if exists {
			continue
		}
		sql, err := migrationFS.ReadFile(name)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			// Exec without arguments uses the simple protocol, which allows multiple statements per file.
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migration %s: %w", version, err)
		}
		applied = append(applied, version)
	}
	return applied, nil
}

// MigrationsApplied reports whether every embedded migration is recorded; /readyz uses it.
func MigrationsApplied(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	names, err := migrationNames()
	if err != nil {
		return false, err
	}
	versions := make([]string, len(names))
	for i, n := range names {
		versions[i] = versionOf(n)
	}
	var n int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version = ANY($1)", versions).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == len(versions), nil
}

func versionOf(name string) string {
	return strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
}
