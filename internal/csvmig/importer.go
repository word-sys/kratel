package csvmig

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

// Service orchestrates CSV/TSV spreadsheet import (with dry-run simulation) and Excel-ready BOM exports.
type Service struct {
	store   *db.Store
	catalog *catalog.Service
}

// NewService constructs a CSV migration service backed by store and catalog.
func NewService(store *db.Store, cat *catalog.Service) *Service {
	if cat == nil {
		cat = catalog.NewService(store)
	}
	return &Service{
		store:   store,
		catalog: cat,
	}
}

// ImportRequest specifies parameters for running a CSV/TSV dry-run or atomic batch import.
type ImportRequest struct {
	CSVData                 []byte            `json:"-"`
	ForcedDelimiter         rune              `json:"-"`
	ColumnMapping           map[string]string `json:"column_mapping"`
	MapUnmappedToAttributes bool              `json:"map_unmapped_to_attributes"`
	DuplicatePolicy         DuplicatePolicy   `json:"duplicate_policy"` // "SKIP", "UPDATE", "ADD_STOCK"
	DryRun                  bool              `json:"dry_run"`
	Operator                string            `json:"operator"`
}

// ImportResult reports exact row counts, auto-created entities, issues, and the BatchID for undo.
type ImportResult struct {
	DryRun            bool       `json:"dry_run"`
	DetectedDelimiter string     `json:"detected_delimiter"`
	TotalRows         int        `json:"total_rows"`
	ItemsCreated      int        `json:"items_created"`
	ItemsUpdated      int        `json:"items_updated"`
	ItemsSkipped      int        `json:"items_skipped"`
	LocationsCreated  int        `json:"locations_created"`
	SuppliersCreated  int        `json:"suppliers_created"`
	AliasesCreated    int        `json:"aliases_created"`
	OpeningTxCount    int        `json:"opening_tx_count"`
	ErrorCount        int        `json:"error_count"`
	WarningCount      int        `json:"warning_count"`
	BatchID           string     `json:"batch_id,omitempty"`
	Issues            []RowIssue `json:"issues"`
}

// ExecuteImport validates and imports a CSV/TSV dataset inside a single atomic SQLite transaction.
// When req.DryRun is true, the full import executes against the live database inside a transaction
// that is rolled back before returning—giving an exact zero-mutation preview of all creations,
// updates, skips, and conflicts.
func (s *Service) ExecuteImport(ctx context.Context, req ImportRequest) (*ImportResult, error) {
	headers, rawRows, delim, err := ParseTable(req.CSVData, req.ForcedDelimiter)
	if err != nil {
		return nil, err
	}

	policy := DuplicatePolicy(strings.ToUpper(strings.TrimSpace(string(req.DuplicatePolicy))))
	switch policy {
	case PolicySkip, PolicyUpdate, PolicyAddStock:
	default:
		policy = PolicySkip
	}

	op := strings.TrimSpace(req.Operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	validRows, issues := ValidateRows(headers, rawRows, req.ColumnMapping, req.MapUnmappedToAttributes)

	result := &ImportResult{
		DryRun:            req.DryRun,
		DetectedDelimiter: string(delim),
		TotalRows:         len(rawRows),
		Issues:            issues,
	}

	tx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin CSV import transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := time.Now().UTC().Format(time.RFC3339)
	batchID := fmt.Sprintf("CSV-%d", time.Now().UTC().UnixNano())
	if !req.DryRun {
		result.BatchID = batchID
	}

	// Find default MAIN location ID for rows without a location column
	var defaultMainLocID int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT id FROM locations ORDER BY CASE WHEN code = 'MAIN' THEN 0 ELSE 1 END, id ASC LIMIT 1;`,
	).Scan(&defaultMainLocID); err != nil {
		return nil, fmt.Errorf("resolve default MAIN location: %w", err)
	}

	locCache := make(map[string]int64)
	supCache := make(map[string]int64)

	for _, row := range validRows {
		// 1. Ensure supplier if present
		var supplierID *int64
		if row.SupplierName != "" {
			supKey := strings.ToLower(row.SupplierName)
			if cachedID, ok := supCache[supKey]; ok {
				id := cachedID
				supplierID = &id
			} else {
				id, created, supErr := ensureSupplierTx(ctx, tx, row.SupplierName, now)
				if supErr != nil {
					result.Issues = append(result.Issues, RowIssue{
						RowNumber: row.RowNumber,
						Severity:  "ERROR",
						Field:     FieldSupplierName,
						Message:   supErr.Error(),
					})
					continue
				}
				supCache[supKey] = id
				if created {
					result.SuppliersCreated++
				}
				supplierID = &id
			}
		}

		// 2. Ensure location if present
		locID := defaultMainLocID
		var defaultLocPtr *int64
		if row.LocationCode != "" {
			locKey := strings.ToUpper(row.LocationCode)
			if cachedID, ok := locCache[locKey]; ok {
				locID = cachedID
				id := cachedID
				defaultLocPtr = &id
			} else {
				id, created, locErr := ensureLocationTx(ctx, tx, row.LocationCode, now)
				if locErr != nil {
					result.Issues = append(result.Issues, RowIssue{
						RowNumber: row.RowNumber,
						Severity:  "ERROR",
						Field:     FieldLocationCode,
						Message:   locErr.Error(),
					})
					continue
				}
				locCache[locKey] = id
				locID = id
				if created {
					result.LocationsCreated++
				}
				defaultLocPtr = &id
			}
		}

		// 3. Check if item already exists by SKU or Barcode
		existingID, found, err := findExistingItemTx(ctx, tx, row.SKU, row.Barcode)
		if err != nil {
			return nil, err
		}

		attrsJSON, _, attrErr := catalog.NormalizeAttributes("", row.Attributes)
		if attrErr != nil {
			result.Issues = append(result.Issues, RowIssue{
				RowNumber: row.RowNumber,
				Severity:  "ERROR",
				Field:     FieldAttribute,
				Message:   attrErr.Error(),
			})
			continue
		}

		if found {
			switch policy {
			case PolicySkip:
				result.ItemsSkipped++
				continue

			case PolicyUpdate:
				if _, execErr := tx.ExecContext(
					ctx,
					`UPDATE items SET
						name = ?,
						description = CASE WHEN ? != '' THEN ? ELSE description END,
						category = CASE WHEN ? != '' THEN ? ELSE category END,
						unit = CASE WHEN ? != '' THEN ? ELSE unit END,
						min_stock = CASE WHEN ? > 0 THEN ? ELSE min_stock END,
						min_stock_millis = CASE WHEN ? > 0 THEN ? ELSE min_stock_millis END,
						unit_cost = CASE WHEN ? > 0 THEN ? ELSE unit_cost END,
						unit_cost_millis = CASE WHEN ? > 0 THEN ? ELSE unit_cost_millis END,
						supplier_id = COALESCE(?, supplier_id),
						default_location_id = COALESCE(?, default_location_id),
						lot_number = CASE WHEN ? != '' THEN ? ELSE lot_number END,
						expiry_date = CASE WHEN ? != '' THEN ? ELSE expiry_date END,
						attributes_json = CASE WHEN ? != '{}' THEN ? ELSE attributes_json END,
						updated_at = ?
					 WHERE id = ?;`,
					row.Name,
					row.Description, row.Description,
					row.Category, row.Category,
					row.Unit, row.Unit,
					row.MinStockMillis.Int64(), row.MinStockMillis.ToFloat(),
					row.MinStockMillis.Int64(), row.MinStockMillis.Int64(),
					row.UnitCostMillis.Int64(), row.UnitCostMillis.ToFloat(),
					row.UnitCostMillis.Int64(), row.UnitCostMillis.Int64(),
					supplierID,
					defaultLocPtr,
					row.LotNumber, row.LotNumber,
					row.ExpiryDate, row.ExpiryDate,
					attrsJSON, attrsJSON,
					now,
					existingID,
				); execErr != nil {
					result.Issues = append(result.Issues, RowIssue{
						RowNumber: row.RowNumber,
						Severity:  "ERROR",
						Field:     FieldSKU,
						Message:   fmt.Sprintf("update item failed: %v", execErr),
					})
					continue
				}
				result.ItemsUpdated++

			case PolicyAddStock:
				result.ItemsUpdated++
			}

			// Optional alias registration for existing item
			if row.AliasCode != "" {
				if added := tryInsertAliasTx(ctx, tx, existingID, row.AliasCode, now); added {
					result.AliasesCreated++
				}
			}

			// In ADD_STOCK (or UPDATE when opening qty > 0), record an IN ledger entry
			if row.OpeningQtyMillis > 0 && policy == PolicyAddStock {
				if err := recordImportStockInTx(ctx, tx, existingID, locID, row, batchID, op, now); err != nil {
					result.Issues = append(result.Issues, RowIssue{
						RowNumber: row.RowNumber,
						Severity:  "ERROR",
						Field:     FieldOpeningQty,
						Message:   err.Error(),
					})
					continue
				}
				result.OpeningTxCount++
			}
			continue
		}

		// 4. Create new Item
		sku := row.SKU
		if sku == "" {
			generated, genErr := catalog.GenerateSuggestedSKU(ctx, tx, row.Category, row.Name)
			if genErr != nil {
				return nil, genErr
			}
			sku = generated
		}

		var barcodeParam interface{}
		if row.Barcode != "" {
			barcodeParam = row.Barcode
		}

		res, insErr := tx.ExecContext(
			ctx,
			`INSERT INTO items (
				sku, barcode, name, description, category, unit,
				min_stock, min_stock_millis, unit_cost, unit_cost_millis,
				supplier_id, default_location_id, lot_number, expiry_date,
				attributes_json, photo_data, archived, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', 0, ?, ?);`,
			sku,
			barcodeParam,
			row.Name,
			row.Description,
			row.Category,
			row.Unit,
			row.MinStockMillis.ToFloat(),
			row.MinStockMillis.Int64(),
			row.UnitCostMillis.ToFloat(),
			row.UnitCostMillis.Int64(),
			supplierID,
			defaultLocPtr,
			row.LotNumber,
			row.ExpiryDate,
			attrsJSON,
			now,
			now,
		)
		if insErr != nil {
			result.Issues = append(result.Issues, RowIssue{
				RowNumber: row.RowNumber,
				Severity:  "ERROR",
				Field:     FieldSKU,
				Message:   fmt.Sprintf("insert item %q failed: %v", sku, insErr),
			})
			continue
		}

		itemID, _ := res.LastInsertId()
		result.ItemsCreated++

		if row.AliasCode != "" {
			if added := tryInsertAliasTx(ctx, tx, itemID, row.AliasCode, now); added {
				result.AliasesCreated++
			}
		}

		if row.OpeningQtyMillis > 0 {
			if err := recordImportStockInTx(ctx, tx, itemID, locID, row, batchID, op, now); err != nil {
				result.Issues = append(result.Issues, RowIssue{
					RowNumber: row.RowNumber,
					Severity:  "ERROR",
					Field:     FieldOpeningQty,
					Message:   err.Error(),
				})
				continue
			}
			result.OpeningTxCount++
		}
	}

	for _, iss := range result.Issues {
		if iss.Severity == "ERROR" {
			result.ErrorCount++
		} else {
			result.WarningCount++
		}
	}

	if req.DryRun {
		// Dry-run always rolls back so zero database state is mutated
		_ = tx.Rollback()
		return result, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit CSV import transaction: %w", err)
	}
	return result, nil
}

func ensureSupplierTx(ctx context.Context, tx *sql.Tx, name, now string) (int64, bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM suppliers WHERE LOWER(name) = LOWER(?) LIMIT 1;`, name).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO suppliers (name, created_at, updated_at) VALUES (?, ?, ?);`,
		name, now, now,
	)
	if err != nil {
		return 0, false, err
	}
	newID, _ := res.LastInsertId()
	return newID, true, nil
}

func ensureLocationTx(ctx context.Context, tx *sql.Tx, code, now string) (int64, bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM locations WHERE UPPER(code) = UPPER(?) LIMIT 1;`, code).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO locations (code, name, parent_id, notes, created_at) VALUES (?, ?, NULL, 'Created via CSV import', ?);`,
		code, code, now,
	)
	if err != nil {
		return 0, false, err
	}
	newID, _ := res.LastInsertId()
	return newID, true, nil
}

func findExistingItemTx(ctx context.Context, tx *sql.Tx, sku, barcode string) (int64, bool, error) {
	var id int64
	if sku != "" {
		err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE UPPER(sku) = UPPER(?) LIMIT 1;`, sku).Scan(&id)
		if err == nil {
			return id, true, nil
		}
		if err != sql.ErrNoRows {
			return 0, false, err
		}
	}
	if barcode != "" {
		err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE barcode IS NOT NULL AND UPPER(barcode) = UPPER(?) LIMIT 1;`, barcode).Scan(&id)
		if err == nil {
			return id, true, nil
		}
		if err != sql.ErrNoRows {
			return 0, false, err
		}
	}
	return 0, false, nil
}

func tryInsertAliasTx(ctx context.Context, tx *sql.Tx, itemID int64, code, now string) bool {
	res, err := tx.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO item_aliases (item_id, code, alias_type, notes, created_at) VALUES (?, ?, ?, 'CSV import', ?);`,
		itemID, code, string(models.AliasSupplierPart), now,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func recordImportStockInTx(ctx context.Context, tx *sql.Tx, itemID, locID int64, row MappedRow, batchID, operator, now string) error {
	var curMillis int64
	err := tx.QueryRowContext(
		ctx,
		`SELECT quantity_millis FROM stock_levels WHERE item_id = ? AND location_id = ?;`,
		itemID, locID,
	).Scan(&curMillis)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	newBal, err := num.Millis(curMillis).CheckedAdd(row.OpeningQtyMillis)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO stock_levels (item_id, location_id, quantity, quantity_millis, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(item_id, location_id) DO UPDATE SET
		     quantity = excluded.quantity,
		     quantity_millis = excluded.quantity_millis,
		     updated_at = excluded.updated_at;`,
		itemID, locID, newBal.ToFloat(), newBal.Int64(), now,
	); err != nil {
		return err
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO transactions (
			item_id, from_location_id, to_location_id, tx_type,
			quantity, quantity_millis, delta_millis, balance_after_millis,
			unit_cost_millis, lot_number, expiry_date, batch_id,
			reason, operator, created_at
		) VALUES (?, NULL, ?, 'IN', ?, ?, ?, ?, ?, ?, ?, ?, 'CSV Import Opening Stock', ?, ?);`,
		itemID,
		locID,
		row.OpeningQtyMillis.ToFloat(),
		row.OpeningQtyMillis.Int64(),
		row.OpeningQtyMillis.Int64(),
		newBal.Int64(),
		row.UnitCostMillis.Int64(),
		row.LotNumber,
		row.ExpiryDate,
		batchID,
		operator,
		now,
	)
	return err
}
