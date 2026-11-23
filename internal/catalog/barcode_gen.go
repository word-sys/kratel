package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"
)

// sqlQueryer abstracts *sql.DB and *sql.Tx for uniqueness queries during code generation.
type sqlQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

const (
	// LocationBarcodePrefix is the canonical barcode prefix for storage bin labels ("LOC:A-01-B3").
	LocationBarcodePrefix = "LOC:"
	// KitBarcodePrefix is the canonical barcode prefix for BOM/repair kit labels ("KIT:ESP32-BUNDLE").
	KitBarcodePrefix = "KIT:"
)

// GenerateNextItemBarcode produces a collision-free internal Code128 barcode in the format "KRT-000001".
// It verifies that the generated code does not collide with any existing item SKU, item barcode, or item alias.
func GenerateNextItemBarcode(ctx context.Context, q sqlQueryer) (string, error) {
	var maxID int64
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM items;").Scan(&maxID); err != nil {
		return "", fmt.Errorf("read max item id: %w", err)
	}

	seq := maxID + 1
	for attempt := 0; attempt < 10000; attempt++ {
		candidate := fmt.Sprintf("KRT-%06d", seq+int64(attempt))
		taken, err := isCodeTaken(ctx, q, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to allocate unique internal barcode after 10000 attempts")
}

// GenerateSuggestedSKU creates a clean, collision-free SKU using an alphanumeric prefix derived
// from category or name (e.g. "MIC-0001" or "KRT-0001").
func GenerateSuggestedSKU(ctx context.Context, q sqlQueryer, category, name string) (string, error) {
	prefix := extractSKUPrefix(category)
	if len(prefix) < 2 {
		prefix = extractSKUPrefix(name)
	}
	if len(prefix) < 2 {
		prefix = "KRT"
	}

	var maxID int64
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM items;").Scan(&maxID); err != nil {
		return "", fmt.Errorf("read max item id: %w", err)
	}

	seq := maxID + 1
	for attempt := 0; attempt < 10000; attempt++ {
		candidate := fmt.Sprintf("%s-%04d", prefix, seq+int64(attempt))
		taken, err := isCodeTaken(ctx, q, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to allocate unique SKU for prefix %q", prefix)
}

func extractSKUPrefix(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			if b.Len() == 3 {
				break
			}
		} else if unicode.IsLetter(r) {
			// Fold common non-ASCII letters for clean ASCII Code128 SKU prefixes
			switch r {
			case 'Ç':
				b.WriteByte('C')
			case 'Ğ':
				b.WriteByte('G')
			case 'İ', 'I':
				b.WriteByte('I')
			case 'Ö':
				b.WriteByte('O')
			case 'Ş':
				b.WriteByte('S')
			case 'Ü':
				b.WriteByte('U')
			}
			if b.Len() == 3 {
				break
			}
		}
	}
	return b.String()
}

func isCodeTaken(ctx context.Context, q sqlQueryer, code string) (bool, error) {
	var exists int
	err := q.QueryRowContext(
		ctx,
		`SELECT 1 FROM items WHERE UPPER(sku) = UPPER(?) OR UPPER(COALESCE(barcode, '')) = UPPER(?)
		 UNION ALL
		 SELECT 1 FROM item_aliases WHERE UPPER(code) = UPPER(?)
		 LIMIT 1;`,
		code, code, code,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check code uniqueness for %q: %w", code, err)
	}
	return true, nil
}

// FormatLocationBarcode formats a scannable location barcode string (e.g. "LOC:A-01-B3").
func FormatLocationBarcode(locationCode string) string {
	return LocationBarcodePrefix + strings.ToUpper(strings.TrimSpace(locationCode))
}

// ParseLocationBarcode checks if a scanned barcode represents a storage location bin ("LOC:A-01-B3")
// and returns the extracted location code.
func ParseLocationBarcode(scanned string) (string, bool) {
	trimmed := strings.TrimSpace(scanned)
	if len(trimmed) > len(LocationBarcodePrefix) && strings.EqualFold(trimmed[:len(LocationBarcodePrefix)], LocationBarcodePrefix) {
		code := strings.TrimSpace(trimmed[len(LocationBarcodePrefix):])
		if code != "" {
			return code, true
		}
	}
	return "", false
}
