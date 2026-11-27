package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/db"
)

// SnapshotInfo describes an on-disk or downloadable SQLite database snapshot.
type SnapshotInfo struct {
	Filename  string `json:"filename"`
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	CreatedAt string `json:"created_at"`
}

// Service manages hot online SQLite snapshot backups and validated live restores.
type Service struct {
	store     *db.Store
	backupDir string
}

// NewService constructs a backup service. If backupDir is empty, it defaults to a "backups"
// directory next to the active SQLite database file (or os.TempDir() for :memory: stores).
func NewService(store *db.Store, backupDir string) *Service {
	dir := strings.TrimSpace(backupDir)
	if dir == "" && store != nil {
		if store.IsMemory() {
			dir = filepath.Join(os.TempDir(), "kratel-backups")
		} else {
			dir = filepath.Join(filepath.Dir(store.Path()), "backups")
		}
	}
	return &Service{
		store:     store,
		backupDir: dir,
	}
}

// BackupDir returns the resolved directory path where local snapshots are stored.
func (s *Service) BackupDir() string {
	return s.backupDir
}

// CreateSnapshot produces a consistent, self-contained SQLite database snapshot at destPath
// (or inside BackupDir() with a timestamped filename when destPath is empty) without stopping the server.
func (s *Service) CreateSnapshot(ctx context.Context, destPath string) (*SnapshotInfo, error) {
	if s.store == nil {
		return nil, apperr.New(apperr.CodeInternal, "database store is not initialized")
	}

	target := strings.TrimSpace(destPath)
	if target == "" {
		if err := os.MkdirAll(s.backupDir, 0o755); err != nil {
			return nil, fmt.Errorf("create backup directory %q: %w", s.backupDir, err)
		}
		target = s.uniqueSnapshotPath("kratel-backup")
	} else {
		parent := filepath.Dir(target)
		if parent != "" && parent != "." {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return nil, fmt.Errorf("create backup destination directory %q: %w", parent, err)
			}
		}
		// VACUUM INTO refuses to overwrite an existing file
		_ = os.Remove(target)
	}

	absTarget, err := filepath.Abs(target)
	if err == nil {
		target = absTarget
	}

	if err := s.store.Checkpoint(ctx); err != nil {
		return nil, err
	}

	// VACUUM INTO creates a clean, defragmented, standalone SQLite database file online
	escapedPath := strings.ReplaceAll(target, "'", "''")
	if _, vacuumErr := s.store.DB().ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s';", escapedPath)); vacuumErr != nil {
		// Fallback for file-backed stores: copy checkpointed .db file atomically
		if s.store.IsMemory() {
			return nil, fmt.Errorf("vacuum snapshot failed: %w", vacuumErr)
		}
		if copyErr := copyFileAtomic(s.store.Path(), target); copyErr != nil {
			return nil, fmt.Errorf("snapshot fallback copy failed (vacuum err: %v): %w", vacuumErr, copyErr)
		}
	}

	return inspectSnapshotFile(target)
}

// CreateSnapshotBytes creates a temporary clean online snapshot, reads its bytes and metadata,
// and cleans up the temporary file (ideal for HTTP browser downloads).
func (s *Service) CreateSnapshotBytes(ctx context.Context) ([]byte, *SnapshotInfo, error) {
	tmpFile, err := os.CreateTemp("", "kratel-snap-*.db")
	if err != nil {
		return nil, nil, fmt.Errorf("create temp snapshot file: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpPath)
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	info, err := s.CreateSnapshot(ctx, tmpPath)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read snapshot bytes: %w", err)
	}
	info.Filename = fmt.Sprintf("kratel-backup-%s.db", time.Now().UTC().Format("20060102-150405"))
	info.Path = ""
	return data, info, nil
}

// ListSnapshots lists all .db snapshot backups inside BackupDir(), newest first.
func (s *Service) ListSnapshots(ctx context.Context) ([]SnapshotInfo, error) {
	entries, err := os.ReadDir(s.backupDir)
	if os.IsNotExist(err) {
		return []SnapshotInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read backup directory %q: %w", s.backupDir, err)
	}

	out := make([]SnapshotInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".db") {
			continue
		}
		fullPath := filepath.Join(s.backupDir, entry.Name())
		info, err := inspectSnapshotFile(fullPath)
		if err != nil {
			continue
		}
		out = append(out, *info)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].Filename > out[j].Filename
	})
	return out, nil
}

// DeleteSnapshot removes a named snapshot file inside BackupDir(), rejecting path traversal.
func (s *Service) DeleteSnapshot(ctx context.Context, filename string) error {
	clean := strings.TrimSpace(filename)
	if clean == "" || filepath.Base(clean) != clean || strings.ContainsAny(clean, `/\`) || !strings.HasSuffix(strings.ToLower(clean), ".db") {
		return apperr.Validation("filename", "invalid snapshot filename")
	}

	target := filepath.Join(s.backupDir, clean)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return apperr.NotFound("backup", clean)
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("remove snapshot %q: %w", clean, err)
	}
	return nil
}

func (s *Service) uniqueSnapshotPath(prefix string) string {
	stamp := time.Now().UTC().Format("20060102-150405")
	base := filepath.Join(s.backupDir, fmt.Sprintf("%s-%s.db", prefix, stamp))
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	for i := 1; i < 1000; i++ {
		cand := filepath.Join(s.backupDir, fmt.Sprintf("%s-%s-%02d.db", prefix, stamp, i))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return filepath.Join(s.backupDir, fmt.Sprintf("%s-%s-%d.db", prefix, stamp, time.Now().UTC().UnixNano()))
}

func inspectSnapshotFile(path string) (*SnapshotInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat snapshot %q: %w", path, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open snapshot %q: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("hash snapshot %q: %w", path, err)
	}

	return &SnapshotInfo{
		Filename:  filepath.Base(path),
		Path:      path,
		SizeBytes: st.Size(),
		SHA256:    hex.EncodeToString(h.Sum(nil)),
		CreatedAt: st.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

func copyFileAtomic(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = src.Close()
	}()

	tmp := dstPath + ".tmp"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dstPath)
}
