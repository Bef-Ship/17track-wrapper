// Package migrate applies embedded tracking DDL on process start
// (same ownership model as GoTrue for auth: service owns its schema).
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SolaTyolo/17track-wrapper/migrations"
	blog "github.com/SolaTyolo/17track-wrapper/pkg/log"
)

// Ledger lives in tracking schema — not the platform migrations.schema_migrations.
const ensureLedgerSQL = `
CREATE SCHEMA IF NOT EXISTS tracking;

CREATE TABLE IF NOT EXISTS tracking.schema_migrations (
  version     text PRIMARY KEY,
  name        text NOT NULL DEFAULT '',
  checksum    text NOT NULL DEFAULT '',
  applied_at  timestamptz NOT NULL DEFAULT now()
);
`

// Distinct from backend migrate advisory lock (7_420_611_002).
const lockKey = int64(7_420_611_102)

type file struct {
	Version  string
	Name     string
	SQL      string
	Checksum string
}

// Apply runs pending embedded DDL files against pool (TRACKING_DATABASE_URL).
// Concurrent replicas serialize via pg_advisory_lock.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("migrate: pool is nil")
	}

	files, err := loadEmbedded()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("migrate: no embedded migrations/*.sql")
	}

	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate lock conn: %w", err)
	}
	defer lockConn.Release()
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("migrate lock: %w", err)
	}
	defer func() {
		_, _ = lockConn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockKey)
	}()

	if _, err := pool.Exec(ctx, ensureLedgerSQL); err != nil {
		return fmt.Errorf("ensure tracking.schema_migrations: %w", err)
	}

	for _, f := range files {
		var applied string
		err := pool.QueryRow(ctx,
			`SELECT checksum FROM tracking.schema_migrations WHERE version = $1`, f.Version,
		).Scan(&applied)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			if applied != "" && applied != f.Checksum {
				blog.Svc("migrate").Warn("migration file changed since applied; edits are NOT re-applied",
					"version", f.Version, "ledger", shortSum(applied), "file", shortSum(f.Checksum))
			}
			continue
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, f.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", f.Version, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO tracking.schema_migrations (version, name, checksum)
			VALUES ($1, $2, $3)`,
			f.Version, f.Name, f.Checksum,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("ledger %s: %w", f.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		blog.Svc("migrate").Info("migration applied", "version", f.Version)
	}
	return nil
}

func loadEmbedded() ([]file, error) {
	entries, err := fs.ReadDir(migrations.DDL, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	out := make([]file, 0, len(names))
	for _, base := range names {
		body, err := migrations.DDL.ReadFile(base)
		if err != nil {
			return nil, err
		}
		version := strings.TrimSuffix(base, ".sql")
		sum := sha256.Sum256(body)
		out = append(out, file{
			Version:  version,
			Name:     versionName(version),
			SQL:      string(body),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	return out, nil
}

func shortSum(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func versionName(v string) string {
	if i := strings.IndexByte(v, '_'); i >= 0 && i+1 < len(v) {
		return v[i+1:]
	}
	return v
}
