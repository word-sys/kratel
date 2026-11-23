package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenWALPragmasHealthAndIdempotentSeed(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, "test_kratel.db")

	store, err := Open(ctx, dbFile)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbFile, err)
	}
	defer func() {
		_ = store.Close()
	}()

	if store.IsMemory() {
		t.Fatal("expected file-backed store not to be marked IsMemory")
	}

	report, err := store.CheckHealth(ctx)
	if err != nil {
		t.Fatalf("CheckHealth failed: %v", err)
	}
	if !report.Healthy {
		t.Fatalf("expected Healthy=true, got report: %+v", report)
	}
	if report.Pragmas.JournalMode != "wal" {
		t.Errorf("expected journal_mode 'wal', got %q", report.Pragmas.JournalMode)
	}
	if !report.Pragmas.ForeignKeys {
		t.Error("expected foreign_keys=true")
	}
	if report.Pragmas.BusyTimeout != 5000 {
		t.Errorf("expected busy_timeout=5000, got %d", report.Pragmas.BusyTimeout)
	}
	if report.TableCounts["locations"] != 1 {
		t.Errorf("expected 1 default location ('MAIN'), got %d", report.TableCounts["locations"])
	}
	if report.TableCounts["operators"] != 1 {
		t.Errorf("expected 1 default operator ('Admin'), got %d", report.TableCounts["operators"])
	}

	// Re-running Migrate on the same database must be 100% idempotent
	if _, err := Migrate(ctx, store.DB()); err != nil {
		t.Fatalf("second Migrate failed: %v", err)
	}
	report2, err := store.CheckHealth(ctx)
	if err != nil {
		t.Fatalf("second CheckHealth failed: %v", err)
	}
	if report2.TableCounts["locations"] != 1 || report2.TableCounts["operators"] != 1 {
		t.Errorf("expected idempotent seed counts (1, 1), got (%d, %d)",
			report2.TableCounts["locations"], report2.TableCounts["operators"])
	}
}

func TestForeignKeyEnforcementAndTransactions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	// Attempting to insert a stock_level for non-existent item_id=9999 must fail due to foreign_keys=ON
	_, err = store.DB().ExecContext(
		ctx,
		`INSERT INTO stock_levels (item_id, location_id, quantity, quantity_millis) VALUES (9999, 1, 5, 5000);`,
	)
	if err == nil {
		t.Fatal("expected foreign key constraint error when inserting stock_levels for missing item_id")
	}

	// Verify WithTx rollback on error
	intentionalErr := errors.New("abort transaction")
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO items (sku, name, unit, created_at, updated_at) VALUES ('ROLLBACK-1', 'Rollback Item', 'pcs', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`,
		)
		if execErr != nil {
			return execErr
		}
		return intentionalErr
	})
	if !errors.Is(err, intentionalErr) {
		t.Fatalf("expected intentionalErr, got %v", err)
	}

	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE sku = 'ROLLBACK-1';`).Scan(&count); err != nil {
		t.Fatalf("query items failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected rolled-back item count to be 0, got %d", count)
	}

	// Verify WithTx commit on success
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO items (sku, name, unit, created_at, updated_at) VALUES ('COMMIT-1', 'Committed Item', 'pcs', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`,
		)
		return execErr
	})
	if err != nil {
		t.Fatalf("WithTx commit failed: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE sku = 'COMMIT-1';`).Scan(&count); err != nil {
		t.Fatalf("query items failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected committed item count to be 1, got %d", count)
	}
}

func TestReplaceDatabaseFileHotSwap(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	dbA := filepath.Join(tmpDir, "active.db")
	dbB := filepath.Join(tmpDir, "snapshot.db")

	storeA, err := Open(ctx, dbA)
	if err != nil {
		t.Fatalf("Open(dbA) failed: %v", err)
	}
	defer func() {
		_ = storeA.Close()
	}()

	// Create a distinct snapshot database dbB with an item
	storeB, err := Open(ctx, dbB)
	if err != nil {
		t.Fatalf("Open(dbB) failed: %v", err)
	}
	_, err = storeB.DB().ExecContext(
		ctx,
		`INSERT INTO items (sku, name, unit, created_at, updated_at) VALUES ('SNAP-SKU-1', 'Restored Part', 'pcs', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`,
	)
	if err != nil {
		t.Fatalf("insert into dbB failed: %v", err)
	}
	if err := storeB.Close(); err != nil {
		t.Fatalf("close dbB failed: %v", err)
	}

	snapBytes, err := os.ReadFile(dbB)
	if err != nil {
		t.Fatalf("ReadFile(dbB) failed: %v", err)
	}

	if err := storeA.ReplaceDatabaseFile(ctx, snapBytes); err != nil {
		t.Fatalf("ReplaceDatabaseFile failed: %v", err)
	}

	var name string
	if err := storeA.DB().QueryRowContext(ctx, `SELECT name FROM items WHERE sku = 'SNAP-SKU-1';`).Scan(&name); err != nil {
		t.Fatalf("query restored item failed: %v", err)
	}
	if name != "Restored Part" {
		t.Errorf("expected 'Restored Part', got %q", name)
	}
}
