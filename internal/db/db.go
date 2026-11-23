package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Store manages the SQLite database connection lifecycle, WAL checkpoints,
// atomic transactions, and FTS5 capability state.
type Store struct {
	mu         sync.RWMutex
	conn       *sql.DB
	path       string
	isMemory   bool
	ftsEnabled bool
}

// Open initializes or opens a Kratel SQLite database at dbPath, applies WAL and
// foreign-key pragmas, and runs idempotent schema migrations and default seeding.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	cleaned := strings.TrimSpace(dbPath)
	if cleaned == "" {
		cleaned = "kratel.db"
	}

	isMem := cleaned == ":memory:" || strings.HasPrefix(cleaned, "file::memory:")
	if !isMem && !strings.HasPrefix(cleaned, "file:") {
		absPath, err := filepath.Abs(cleaned)
		if err == nil {
			cleaned = absPath
		}
		dir := filepath.Dir(cleaned)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create database directory %q: %w", dir, err)
			}
		}
	}

	conn, ftsEnabled, err := openAndMigrate(ctx, cleaned, isMem)
	if err != nil {
		return nil, err
	}

	return &Store{
		conn:       conn,
		path:       cleaned,
		isMemory:   isMem,
		ftsEnabled: ftsEnabled,
	}, nil
}

func openAndMigrate(ctx context.Context, cleanedPath string, isMem bool) (*sql.DB, bool, error) {
	dsn := DSNWithPragmas(cleanedPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, false, fmt.Errorf("open sqlite connection: %w", err)
	}

	// Single writer pool prevents SQLITE_BUSY transaction-upgrade contention in embedded mode
	// while keeping foreign_keys=ON and WAL state deterministic across all operations.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	if err := ApplyPragmas(ctx, conn, isMem); err != nil {
		_ = conn.Close()
		return nil, false, err
	}

	ftsEnabled, err := Migrate(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return nil, false, err
	}

	return conn, ftsEnabled, nil
}

// DB returns the underlying *sql.DB connection handle.
func (s *Store) DB() *sql.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conn
}

// Path returns the resolved SQLite database path.
func (s *Store) Path() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.path
}

// IsMemory reports whether the store is backed by an in-memory database.
func (s *Store) IsMemory() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isMemory
}

// FTSEnabled reports whether SQLite FTS5 full-text search is active.
func (s *Store) FTSEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ftsEnabled
}

// WithTx executes fn inside an atomic SQLite transaction, rolling back automatically
// if fn returns an error or panics.
func (s *Store) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	conn := s.DB()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Checkpoint flushes and truncates the SQLite WAL file into the main database file.
func (s *Store) Checkpoint(ctx context.Context) error {
	if s.IsMemory() {
		return nil
	}
	conn := s.DB()
	if _, err := conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	return nil
}

// ReplaceDatabaseFile atomically closes the active SQLite connection, replaces the on-disk
// database file with the provided clean SQLite snapshot bytes, and reopens/migrates the pool.
func (s *Store) ReplaceDatabaseFile(ctx context.Context, newDBBytes []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isMemory {
		return fmt.Errorf("cannot replace file on an in-memory database")
	}

	if s.conn != nil {
		_, _ = s.conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
		_ = s.conn.Close()
		s.conn = nil
	}

	// Remove stale WAL and SHM sidecar files before writing the restored snapshot.
	_ = os.Remove(s.path + "-wal")
	_ = os.Remove(s.path + "-shm")

	tmpPath := s.path + ".restore.tmp"
	if err := os.WriteFile(tmpPath, newDBBytes, 0o644); err != nil {
		// Reopen original DB before returning error
		conn, fts, _ := openAndMigrate(ctx, s.path, false)
		s.conn, s.ftsEnabled = conn, fts
		return fmt.Errorf("write temporary restore file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		conn, fts, _ := openAndMigrate(ctx, s.path, false)
		s.conn, s.ftsEnabled = conn, fts
		return fmt.Errorf("replace database file: %w", err)
	}

	conn, ftsEnabled, err := openAndMigrate(ctx, s.path, false)
	if err != nil {
		return fmt.Errorf("reopen restored database: %w", err)
	}
	s.conn = conn
	s.ftsEnabled = ftsEnabled
	return nil
}

// FileSizeBytes returns the on-disk byte sizes of the main .db file and any active -wal file.
func (s *Store) FileSizeBytes() (mainBytes int64, walBytes int64) {
	if s.IsMemory() {
		return 0, 0
	}
	path := s.Path()
	if info, err := os.Stat(path); err == nil {
		mainBytes = info.Size()
	}
	if info, err := os.Stat(path + "-wal"); err == nil {
		walBytes = info.Size()
	}
	return mainBytes, walBytes
}

// Close checkpoints the WAL file (if file-backed) and closes the database connection.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	if !s.isMemory {
		_, _ = s.conn.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}
