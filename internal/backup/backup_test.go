package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
)

func TestSnapshotCreateListDownloadAndDelete(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "kratel.db")

	store, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("db.Open failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	cat := catalog.NewService(store)
	if _, err := cat.CreateItem(ctx, models.ItemInput{
		SKU:             "SNAP-ITEM-1",
		Name:            "Snapshot Test Capacitor",
		OpeningQuantity: "50",
	}); err != nil {
		t.Fatalf("CreateItem failed: %v", err)
	}

	bk := NewService(store, "")
	snap, err := bk.CreateSnapshot(ctx, "")
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}
	if snap.SizeBytes == 0 || len(snap.SHA256) != 64 {
		t.Fatalf("unexpected snapshot info: %+v", snap)
	}

	// Verify downloadable bytes match valid Kratel SQLite snapshot
	dlBytes, dlInfo, err := bk.CreateSnapshotBytes(ctx)
	if err != nil {
		t.Fatalf("CreateSnapshotBytes failed: %v", err)
	}
	sum := sha256.Sum256(dlBytes)
	if hex.EncodeToString(sum[:]) != dlInfo.SHA256 {
		t.Fatal("downloaded snapshot SHA256 mismatch")
	}
	if err := ValidateSQLiteSnapshot(ctx, dlBytes); err != nil {
		t.Fatalf("ValidateSQLiteSnapshot failed on downloaded bytes: %v", err)
	}

	// Verify ListSnapshots finds the saved snapshot
	list, err := bk.ListSnapshots(ctx)
	if err != nil || len(list) != 1 || list[0].Filename != snap.Filename {
		t.Fatalf("ListSnapshots unexpected: len=%d list=%+v err=%v", len(list), list, err)
	}

	// Path traversal rejection on DeleteSnapshot
	if err := bk.DeleteSnapshot(ctx, "../kratel.db"); !apperr.HasCode(err, apperr.CodeValidation) {
		t.Fatalf("expected VALIDATION_ERROR on path traversal filename, got %v", err)
	}

	if err := bk.DeleteSnapshot(ctx, snap.Filename); err != nil {
		t.Fatalf("DeleteSnapshot failed: %v", err)
	}
	afterDel, err := bk.ListSnapshots(ctx)
	if err != nil || len(afterDel) != 0 {
		t.Fatalf("expected 0 snapshots after delete, got %d (err=%v)", len(afterDel), err)
	}
}

func TestRestoreValidationAndLiveHotSwap(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "kratel.db")

	store, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("db.Open failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	cat := catalog.NewService(store)
	bk := NewService(store, "")

	// 1. Seed initial state (V1) and capture snapshot
	if _, err := cat.CreateItem(ctx, models.ItemInput{
		SKU:             "PART-V1",
		Name:            "Original Part V1",
		OpeningQuantity: "10",
	}); err != nil {
		t.Fatalf("CreateItem V1 failed: %v", err)
	}

	snapV1Bytes, _, err := bk.CreateSnapshotBytes(ctx)
	if err != nil {
		t.Fatalf("CreateSnapshotBytes(V1) failed: %v", err)
	}

	// 2. Mutate live database to V2 state
	if _, err := cat.CreateItem(ctx, models.ItemInput{
		SKU:             "PART-V2",
		Name:            "Later Part V2",
		OpeningQuantity: "25",
	}); err != nil {
		t.Fatalf("CreateItem V2 failed: %v", err)
	}

	// 3. Reject invalid/corrupt uploads and non-Kratel SQLite files
	if _, err := bk.RestoreSnapshot(ctx, []byte("not a sqlite file at all")); !errors.Is(err, apperr.ErrCorruptDB) {
		t.Fatalf("expected ErrCorruptDB for non-SQLite bytes, got %v", err)
	}

	foreignDBPath := filepath.Join(tmpDir, "foreign.db")
	foreignConn, err := sql.Open("sqlite", foreignDBPath)
	if err != nil {
		t.Fatalf("open foreign db failed: %v", err)
	}
	_, _ = foreignConn.Exec(`CREATE TABLE random_notes (id INTEGER PRIMARY KEY, body TEXT);`)
	_ = foreignConn.Close()
	foreignBytes, _ := os.ReadFile(foreignDBPath)
	if _, err := bk.RestoreSnapshot(ctx, foreignBytes); !errors.Is(err, apperr.ErrCorruptDB) {
		t.Fatalf("expected ErrCorruptDB for non-Kratel SQLite schema, got %v", err)
	}

	// 4. Perform live restore of V1 snapshot
	res, err := bk.RestoreSnapshot(ctx, snapV1Bytes)
	if err != nil {
		t.Fatalf("RestoreSnapshot(V1) failed: %v", err)
	}
	if !res.Health.Healthy || res.SafetyBackupPath == "" {
		t.Fatalf("unexpected restore result: %+v", res)
	}
	if _, err := os.Stat(res.SafetyBackupPath); err != nil {
		t.Fatalf("expected pre-restore safety backup at %q: %v", res.SafetyBackupPath, err)
	}

	// Verify V1 is restored and V2 is absent
	if _, _, err := cat.ResolveItemByCode(ctx, "PART-V1"); err != nil {
		t.Fatalf("expected PART-V1 to exist after restore: %v", err)
	}
	if _, _, err := cat.ResolveItemByCode(ctx, "PART-V2"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("expected PART-V2 to be absent after V1 restore, got %v", err)
	}
}
