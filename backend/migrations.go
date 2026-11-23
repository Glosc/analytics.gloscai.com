package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// The migration numbers and history table are shared with previous releases.
func migratePostgres(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(4812205231)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migration_history (version bigint NOT NULL, applied_at timestamp, description varchar(1024)); CREATE UNIQUE INDEX IF NOT EXISTS idx_version ON migration_history(version)`); err != nil {
		return err
	}
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, file := range files {
		name := strings.TrimSuffix(file.Name(), ".sql")
		prefix, description, _ := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return err
		}
		var done bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM migration_history WHERE version=$1)`, version).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + file.Name())
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO migration_history(version,applied_at,description) VALUES($1,now(),$2)`, version, description); err != nil {
			return err
		}
	}
	return tx.Commit()
}
