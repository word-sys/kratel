package db

import (
	"context"
	"fmt"
	"strings"
)

// HealthReport summarizes SQLite physical file integrity, foreign-key consistency,
// active pragmas, FTS5 status, file sizes, and core table counts.
type HealthReport struct {
	Healthy              bool           `json:"healthy"`
	IntegrityResult      string         `json:"integrity_result"`
	ForeignKeyViolations int            `json:"foreign_key_violations"`
	Pragmas              PragmaStatus   `json:"pragmas"`
	FTS5Enabled          bool           `json:"fts5_enabled"`
	MainFileSizeBytes    int64          `json:"main_file_size_bytes"`
	WALFileSizeBytes     int64          `json:"wal_file_size_bytes"`
	TableCounts          map[string]int `json:"table_counts"`
}

var monitoredTables = []string{
	"items",
	"item_aliases",
	"locations",
	"stock_levels",
	"transactions",
	"suppliers",
	"kits",
	"audit_sessions",
	"loans",
	"operators",
	"webhooks",
}

// CheckHealth runs PRAGMA integrity_check, PRAGMA foreign_key_check, pragma inspection,
// and table count queries to verify database health.
func (s *Store) CheckHealth(ctx context.Context) (HealthReport, error) {
	conn := s.DB()
	mainSize, walSize := s.FileSizeBytes()

	report := HealthReport{
		FTS5Enabled:       s.FTSEnabled(),
		MainFileSizeBytes: mainSize,
		WALFileSizeBytes:  walSize,
		TableCounts:       make(map[string]int, len(monitoredTables)),
	}

	pragmas, err := InspectPragmas(ctx, conn)
	if err != nil {
		return report, err
	}
	report.Pragmas = pragmas

	// 1. Physical B-tree & page integrity check
	rows, err := conn.QueryContext(ctx, "PRAGMA integrity_check(10);")
	if err != nil {
		return report, fmt.Errorf("run integrity_check: %w", err)
	}
	var messages []string
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			_ = rows.Close()
			return report, fmt.Errorf("scan integrity_check: %w", err)
		}
		messages = append(messages, strings.TrimSpace(msg))
	}
	_ = rows.Close()
	report.IntegrityResult = strings.Join(messages, "; ")

	// 2. Referential integrity check across all foreign keys
	fkRows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check;")
	if err != nil {
		return report, fmt.Errorf("run foreign_key_check: %w", err)
	}
	violations := 0
	for fkRows.Next() {
		violations++
	}
	_ = fkRows.Close()
	report.ForeignKeyViolations = violations

	// 3. Collect row counts for core tables
	for _, tbl := range monitoredTables {
		var count int
		query := "SELECT COUNT(*) FROM " + tbl + ";"
		if err := conn.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return report, fmt.Errorf("count rows in %s: %w", tbl, err)
		}
		report.TableCounts[tbl] = count
	}

	report.Healthy = strings.EqualFold(report.IntegrityResult, "ok") &&
		report.ForeignKeyViolations == 0 &&
		report.Pragmas.ForeignKeys

	return report, nil
}
