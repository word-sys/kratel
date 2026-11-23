package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"
)

//go:embed schema.sql
var coreSchemaSQL string

//go:embed indexes.sql
var coreIndexesSQL string

// SchemaVersion is the current schema migration version.
const SchemaVersion = 1

// Migrate applies the embedded core DDL, B-tree indexes, FTS5 virtual table/triggers,
// and first-boot default records inside a transaction.
// It returns whether FTS5 full-text indexing is active on the connection.
func Migrate(ctx context.Context, conn *sql.DB) (bool, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, coreSchemaSQL); err != nil {
		return false, fmt.Errorf("execute core schema.sql: %w", err)
	}

	if _, err := tx.ExecContext(ctx, coreIndexesSQL); err != nil {
		return false, fmt.Errorf("execute core indexes.sql: %w", err)
	}

	ftsEnabled, err := InitFTS5(ctx, tx)
	if err != nil {
		return false, fmt.Errorf("initialize FTS5 index: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?);`,
		SchemaVersion,
		"001_initial_kratel_schema",
		now,
	); err != nil {
		return false, fmt.Errorf("record schema migration version: %w", err)
	}

	if err := SeedDefaults(ctx, tx); err != nil {
		return false, fmt.Errorf("seed default records: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit migration transaction: %w", err)
	}

	return ftsEnabled, nil
}
