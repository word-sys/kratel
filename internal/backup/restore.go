package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/db"
	_ "modernc.org/sqlite"
)

var sqliteHeaderMagic = []byte("SQLite format 3\x00")

var requiredKratelTables = []string{
	"items",
	"locations",
	"stock_levels",
	"transactions",
	"suppliers",
}

// RestoreResult summarizes a validated live database restoration.
type RestoreResult struct {
	RestoredBytes    int64           `json:"restored_bytes"`
	RestoredSHA256   string          `json:"restored_sha256"`
	SafetyBackupPath string          `json:"safety_backup_path,omitempty"`
	Health           db.HealthReport `json:"health"`
}

// ValidateSQLiteSnapshot verifies that raw bytes form a genuine, uncorrupted SQLite 3 database
// containing Kratel's core inventory schema before replacing the active database.
func ValidateSQLiteSnapshot(ctx context.Context, data []byte) error {
	if len(data) < 100 || !bytes.HasPrefix(data, sqliteHeaderMagic) {
		return apperr.New(apperr.CodeCorruptDB, "uploaded file is not a valid SQLite 3 database")
	}

	tmpFile, err := os.CreateTemp("", "kratel-validate-*.db")
	if err != nil {
		return fmt.Errorf("create temp validation file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp validation file: %w", err)
	}
	_ = tmpFile.Close()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	conn, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		return apperr.Wrap(apperr.CodeCorruptDB, "unable to open uploaded SQLite database", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	var integrity string
	if err := conn.QueryRowContext(ctx, "PRAGMA integrity_check(1);").Scan(&integrity); err != nil {
		return apperr.Wrap(apperr.CodeCorruptDB, "SQLite integrity_check failed on uploaded file", err)
	}
	if !strings.EqualFold(strings.TrimSpace(integrity), "ok") {
		return apperr.New(apperr.CodeCorruptDB, fmt.Sprintf("uploaded database failed integrity check: %s", integrity))
	}

	for _, tbl := range requiredKratelTables {
		var name string
		err := conn.QueryRowContext(
			ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?;`,
			tbl,
		).Scan(&name)
		if err != nil {
			return apperr.New(apperr.CodeCorruptDB, fmt.Sprintf("uploaded database is missing required Kratel table %q", tbl))
		}
	}

	return nil
}

// RestoreSnapshot validates the uploaded .db bytes, creates an automatic pre-restore safety backup
// on disk, atomically swaps the live SQLite database, and verifies post-restore database health.
func (s *Service) RestoreSnapshot(ctx context.Context, data []byte) (*RestoreResult, error) {
	if s.store == nil {
		return nil, apperr.New(apperr.CodeInternal, "database store is not initialized")
	}
	if err := ValidateSQLiteSnapshot(ctx, data); err != nil {
		return nil, err
	}

	var safetyPath string
	if !s.store.IsMemory() {
		if err := os.MkdirAll(s.backupDir, 0o755); err != nil {
			return nil, fmt.Errorf("ensure backup directory: %w", err)
		}
		safetyTarget := s.uniqueSnapshotPath("pre-restore-safety")
		snapInfo, err := s.CreateSnapshot(ctx, safetyTarget)
		if err != nil {
			return nil, fmt.Errorf("create pre-restore safety backup: %w", err)
		}
		safetyPath = snapInfo.Path
	}

	if err := s.store.ReplaceDatabaseFile(ctx, data); err != nil {
		return nil, err
	}

	health, err := s.store.CheckHealth(ctx)
	if err != nil {
		return nil, fmt.Errorf("post-restore health check failed: %w", err)
	}

	sum := sha256.Sum256(data)
	return &RestoreResult{
		RestoredBytes:    int64(len(data)),
		RestoredSHA256:   hex.EncodeToString(sum[:]),
		SafetyBackupPath: safetyPath,
		Health:           health,
	}, nil
}

// RestoreFromLocalSnapshot restores a named snapshot file stored inside BackupDir().
func (s *Service) RestoreFromLocalSnapshot(ctx context.Context, filename string) (*RestoreResult, error) {
	clean := strings.TrimSpace(filename)
	if clean == "" || filepath.Base(clean) != clean || strings.ContainsAny(clean, `/\`) || !strings.HasSuffix(strings.ToLower(clean), ".db") {
		return nil, apperr.Validation("filename", "invalid snapshot filename")
	}
	fullPath := filepath.Join(s.backupDir, clean)
	data, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return nil, apperr.NotFound("backup", clean)
	}
	if err != nil {
		return nil, fmt.Errorf("read snapshot %q: %w", clean, err)
	}
	return s.RestoreSnapshot(ctx, data)
}
