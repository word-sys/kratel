package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

// PragmaStatus reports the active runtime SQLite configuration for diagnostics and tests.
type PragmaStatus struct {
	SQLiteVersion string `json:"sqlite_version"`
	JournalMode   string `json:"journal_mode"`
	Synchronous   int    `json:"synchronous"`
	ForeignKeys   bool   `json:"foreign_keys"`
	BusyTimeout   int    `json:"busy_timeout"`
	CacheSize     int    `json:"cache_size"`
}

// DSNWithPragmas constructs a modernc.org/sqlite connection string that applies
// critical per-connection pragmas (WAL, foreign_keys=ON, busy_timeout=5000, 32MB cache)
// to every connection opened by database/sql's pool.
func DSNWithPragmas(dbPath string) string {
	trimmed := strings.TrimSpace(dbPath)
	if trimmed == "" {
		trimmed = "kratel.db"
	}

	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "temp_store(MEMORY)")
	q.Add("_pragma", "cache_size(-32000)")
	if trimmed != ":memory:" && !strings.HasPrefix(trimmed, "file::memory:") {
		q.Add("_pragma", "journal_mode(WAL)")
	}

	sep := "?"
	if strings.Contains(trimmed, "?") {
		sep = "&"
	}
	return trimmed + sep + q.Encode()
}

// ApplyPragmas explicitly executes the required SQLite pragmas on an open connection.
func ApplyPragmas(ctx context.Context, conn *sql.DB, isMemory bool) error {
	statements := []string{
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA temp_store = MEMORY;",
		"PRAGMA cache_size = -32000;",
	}
	if !isMemory {
		statements = append([]string{"PRAGMA journal_mode = WAL;"}, statements...)
	}

	for _, stmt := range statements {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply pragma %q: %w", stmt, err)
		}
	}
	return nil
}

// InspectPragmas queries the live connection to verify active SQLite pragma values.
func InspectPragmas(ctx context.Context, conn *sql.DB) (PragmaStatus, error) {
	var st PragmaStatus

	if err := conn.QueryRowContext(ctx, "SELECT sqlite_version();").Scan(&st.SQLiteVersion); err != nil {
		return st, fmt.Errorf("read sqlite_version: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&st.JournalMode); err != nil {
		return st, fmt.Errorf("read journal_mode: %w", err)
	}
	st.JournalMode = strings.ToLower(strings.TrimSpace(st.JournalMode))

	if err := conn.QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&st.Synchronous); err != nil {
		return st, fmt.Errorf("read synchronous: %w", err)
	}
	var fk int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&fk); err != nil {
		return st, fmt.Errorf("read foreign_keys: %w", err)
	}
	st.ForeignKeys = fk == 1

	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout;").Scan(&st.BusyTimeout); err != nil {
		return st, fmt.Errorf("read busy_timeout: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA cache_size;").Scan(&st.CacheSize); err != nil {
		return st, fmt.Errorf("read cache_size: %w", err)
	}

	return st, nil
}
