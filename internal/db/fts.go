package db

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
)

// sqlExecutor abstracts *sql.DB and *sql.Tx for migration and trigger execution.
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

const fts5DDL = `
CREATE VIRTUAL TABLE IF NOT EXISTS items_fts USING fts5(
    sku,
    barcode,
    name,
    description,
    category,
    lot_number,
    attributes_json,
    content='items',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS trg_items_fts_ai AFTER INSERT ON items BEGIN
    INSERT INTO items_fts(rowid, sku, barcode, name, description, category, lot_number, attributes_json)
    VALUES (new.id, new.sku, COALESCE(new.barcode, ''), new.name, new.description, new.category, new.lot_number, new.attributes_json);
END;

CREATE TRIGGER IF NOT EXISTS trg_items_fts_ad AFTER DELETE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, sku, barcode, name, description, category, lot_number, attributes_json)
    VALUES ('delete', old.id, old.sku, COALESCE(old.barcode, ''), old.name, old.description, old.category, old.lot_number, old.attributes_json);
END;

CREATE TRIGGER IF NOT EXISTS trg_items_fts_au AFTER UPDATE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, sku, barcode, name, description, category, lot_number, attributes_json)
    VALUES ('delete', old.id, old.sku, COALESCE(old.barcode, ''), old.name, old.description, old.category, old.lot_number, old.attributes_json);
    INSERT INTO items_fts(rowid, sku, barcode, name, description, category, lot_number, attributes_json)
    VALUES (new.id, new.sku, COALESCE(new.barcode, ''), new.name, new.description, new.category, new.lot_number, new.attributes_json);
END;
`

// InitFTS5 initializes the SQLite FTS5 full-text search virtual table and synchronization triggers.
// It returns true if FTS5 is active, or false if the underlying SQLite build lacks FTS5 support.
func InitFTS5(ctx context.Context, exec sqlExecutor) (bool, error) {
	if _, err := exec.ExecContext(ctx, fts5DDL); err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "no such module: fts5") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RebuildFTS5 repopulates the items_fts index from the items table.
func RebuildFTS5(ctx context.Context, exec sqlExecutor) error {
	_, err := exec.ExecContext(ctx, "INSERT INTO items_fts(items_fts) VALUES('rebuild');")
	return err
}

// BuildFTS5MatchQuery sanitizes an arbitrary user search string (including part numbers with
// hyphens, slashes, asterisks, parentheses, or quotes like "M3-8*0.5" or "ESP32-S3 (Rev.B)")
// into a safe FTS5 prefix MATCH expression (e.g. `"m3"* "8"* "0"* "5"*`).
// Returns an empty string if the input contains no searchable alphanumeric or script tokens.
func BuildFTS5MatchQuery(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	var tokens []string
	var cur strings.Builder

	flush := func() {
		if cur.Len() > 0 {
			tok := cur.String()
			cur.Reset()
			tokens = append(tokens, `"`+tok+`"*`)
		}
	}

	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()

	return strings.Join(tokens, " ")
}
