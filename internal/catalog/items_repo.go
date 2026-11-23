package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

const MaxPhotoDataBytes = 512 * 1024 // 512 KB cap for inline base64 thumbnail attachments

// Service manages catalog items, multi-barcode aliases, hierarchical locations,
// suppliers, custom JSON attributes, and low-stock reorder lists.
type Service struct {
	store *db.Store
}

// NewService creates a new catalog domain service backed by store.
func NewService(store *db.Store) *Service {
	return &Service{store: store}
}

// CreateItem validates and inserts a new catalog Item. If in.OpeningQuantity > 0,
// it atomically records an opening IN ledger transaction and materializes stock_levels.
func (s *Service) CreateItem(ctx context.Context, in models.ItemInput) (*models.Item, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "item name is required")
	}

	category := strings.TrimSpace(in.Category)
	unit := strings.TrimSpace(in.Unit)
	if unit == "" {
		unit = "pcs"
	}

	minStockMillis, err := num.ParseNonNegativeMillis("min_stock", in.MinStock)
	if err != nil {
		return nil, err
	}
	unitCostMillis, err := num.ParseNonNegativeMillis("unit_cost", in.UnitCost)
	if err != nil {
		return nil, err
	}
	openingMillis, err := num.ParseNonNegativeMillis("opening_quantity", in.OpeningQuantity)
	if err != nil {
		return nil, err
	}

	expiryDate, err := normalizeOptionalISODate("expiry_date", in.ExpiryDate)
	if err != nil {
		return nil, err
	}

	attrsJSON, _, err := NormalizeAttributes(in.AttributesJSON, in.Attributes)
	if err != nil {
		return nil, err
	}

	photoData := strings.TrimSpace(in.PhotoData)
	if len(photoData) > MaxPhotoDataBytes {
		return nil, apperr.Validation("photo_data", "photo thumbnail exceeds 512 KB maximum size")
	}

	var createdID int64
	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		sku := strings.TrimSpace(in.SKU)
		if sku == "" {
			generatedSKU, genErr := GenerateSuggestedSKU(ctx, tx, category, name)
			if genErr != nil {
				return genErr
			}
			sku = generatedSKU
		}

		if err := s.ensureCodeAvailableTx(ctx, tx, "sku", sku, 0); err != nil {
			return err
		}

		barcode := strings.TrimSpace(in.Barcode)
		var barcodeParam interface{}
		if barcode != "" {
			if strings.EqualFold(barcode, sku) {
				// Allowed when an item's barcode equals its own SKU
			} else if err := s.ensureCodeAvailableTx(ctx, tx, "barcode", barcode, 0); err != nil {
				return err
			}
			barcodeParam = barcode
		}

		now := time.Now().UTC().Format(time.RFC3339)
		archivedInt := 0
		if in.Archived {
			archivedInt = 1
		}

		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO items (
				sku, barcode, name, description, category, unit,
				min_stock, min_stock_millis, unit_cost, unit_cost_millis,
				supplier_id, default_location_id, lot_number, expiry_date,
				attributes_json, photo_data, archived, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			sku,
			barcodeParam,
			name,
			strings.TrimSpace(in.Description),
			category,
			unit,
			minStockMillis.ToFloat(),
			minStockMillis.Int64(),
			unitCostMillis.ToFloat(),
			unitCostMillis.Int64(),
			in.SupplierID,
			in.DefaultLocationID,
			strings.TrimSpace(in.LotNumber),
			expiryDate,
			attrsJSON,
			photoData,
			archivedInt,
			now,
			now,
		)
		if execErr != nil {
			return fmt.Errorf("insert item: %w", execErr)
		}

		itemID, idErr := res.LastInsertId()
		if idErr != nil {
			return fmt.Errorf("read inserted item id: %w", idErr)
		}
		createdID = itemID

		if openingMillis > 0 {
			locID, locErr := resolveOpeningLocationTx(ctx, tx, in.OpeningLocationID, in.DefaultLocationID)
			if locErr != nil {
				return locErr
			}
			operator := strings.TrimSpace(in.Operator)
			if operator == "" {
				operator = db.DefaultOperatorName
			}

			if _, execErr := tx.ExecContext(
				ctx,
				`INSERT INTO stock_levels (item_id, location_id, quantity, quantity_millis, updated_at)
				 VALUES (?, ?, ?, ?, ?);`,
				itemID,
				locID,
				openingMillis.ToFloat(),
				openingMillis.Int64(),
				now,
			); execErr != nil {
				return fmt.Errorf("insert opening stock_level: %w", execErr)
			}

			if _, execErr := tx.ExecContext(
				ctx,
				`INSERT INTO transactions (
					item_id, from_location_id, to_location_id, tx_type,
					quantity, quantity_millis, delta_millis, balance_after_millis,
					unit_cost_millis, lot_number, expiry_date, reason, operator, created_at
				) VALUES (?, NULL, ?, 'IN', ?, ?, ?, ?, ?, ?, ?, 'Opening stock', ?, ?);`,
				itemID,
				locID,
				openingMillis.ToFloat(),
				openingMillis.Int64(),
				openingMillis.Int64(),
				openingMillis.Int64(),
				unitCostMillis.Int64(),
				strings.TrimSpace(in.LotNumber),
				expiryDate,
				operator,
				now,
			); execErr != nil {
				return fmt.Errorf("insert opening IN transaction: %w", execErr)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.GetItemByID(ctx, createdID)
}

// UpdateItem validates and updates an existing catalog Item's metadata and attributes.
func (s *Service) UpdateItem(ctx context.Context, id int64, in models.ItemInput) (*models.Item, error) {
	existing, err := s.GetItemByID(ctx, id)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "item name is required")
	}
	sku := strings.TrimSpace(in.SKU)
	if sku == "" {
		sku = existing.SKU
	}
	unit := strings.TrimSpace(in.Unit)
	if unit == "" {
		unit = existing.Unit
	}

	minStockMillis, err := num.ParseNonNegativeMillis("min_stock", in.MinStock)
	if err != nil {
		return nil, err
	}
	unitCostMillis, err := num.ParseNonNegativeMillis("unit_cost", in.UnitCost)
	if err != nil {
		return nil, err
	}
	expiryDate, err := normalizeOptionalISODate("expiry_date", in.ExpiryDate)
	if err != nil {
		return nil, err
	}
	attrsJSON, _, err := NormalizeAttributes(in.AttributesJSON, in.Attributes)
	if err != nil {
		return nil, err
	}
	photoData := strings.TrimSpace(in.PhotoData)
	if len(photoData) > MaxPhotoDataBytes {
		return nil, apperr.Validation("photo_data", "photo thumbnail exceeds 512 KB maximum size")
	}

	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.ensureCodeAvailableTx(ctx, tx, "sku", sku, id); err != nil {
			return err
		}

		barcode := strings.TrimSpace(in.Barcode)
		var barcodeParam interface{}
		if barcode != "" {
			if !strings.EqualFold(barcode, sku) {
				if err := s.ensureCodeAvailableTx(ctx, tx, "barcode", barcode, id); err != nil {
					return err
				}
			}
			barcodeParam = barcode
		}

		now := time.Now().UTC().Format(time.RFC3339)
		archivedInt := 0
		if in.Archived {
			archivedInt = 1
		}

		_, execErr := tx.ExecContext(
			ctx,
			`UPDATE items SET
				sku = ?, barcode = ?, name = ?, description = ?, category = ?, unit = ?,
				min_stock = ?, min_stock_millis = ?, unit_cost = ?, unit_cost_millis = ?,
				supplier_id = ?, default_location_id = ?, lot_number = ?, expiry_date = ?,
				attributes_json = ?, photo_data = ?, archived = ?, updated_at = ?
			 WHERE id = ?;`,
			sku,
			barcodeParam,
			name,
			strings.TrimSpace(in.Description),
			strings.TrimSpace(in.Category),
			unit,
			minStockMillis.ToFloat(),
			minStockMillis.Int64(),
			unitCostMillis.ToFloat(),
			unitCostMillis.Int64(),
			in.SupplierID,
			in.DefaultLocationID,
			strings.TrimSpace(in.LotNumber),
			expiryDate,
			attrsJSON,
			photoData,
			archivedInt,
			now,
			id,
		)
		return execErr
	})
	if err != nil {
		return nil, err
	}

	return s.GetItemByID(ctx, id)
}

// AssignInternalBarcode generates a unique KRT-000001 barcode and assigns it to the item
// (either as its primary barcode if blank, or as an INTERNAL_QR alias if a primary barcode already exists).
func (s *Service) AssignInternalBarcode(ctx context.Context, itemID int64) (*models.Item, error) {
	item, err := s.GetItemByID(ctx, itemID)
	if err != nil {
		return nil, err
	}

	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		code, genErr := GenerateNextItemBarcode(ctx, tx)
		if genErr != nil {
			return genErr
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if strings.TrimSpace(item.Barcode) == "" {
			_, execErr := tx.ExecContext(
				ctx,
				`UPDATE items SET barcode = ?, updated_at = ? WHERE id = ?;`,
				code, now, itemID,
			)
			return execErr
		}
		_, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO item_aliases (item_id, code, alias_type, notes, created_at) VALUES (?, ?, ?, 'Auto-generated internal barcode', ?);`,
			itemID, code, string(models.AliasInternalQR), now,
		)
		return execErr
	})
	if err != nil {
		return nil, err
	}
	return s.GetItemByID(ctx, itemID)
}

// ArchiveItem toggles an item's soft-archived state.
func (s *Service) ArchiveItem(ctx context.Context, id int64, archived bool) (*models.Item, error) {
	if _, err := s.GetItemByID(ctx, id); err != nil {
		return nil, err
	}
	archivedInt := 0
	if archived {
		archivedInt = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.store.DB().ExecContext(
		ctx,
		`UPDATE items SET archived = ?, updated_at = ? WHERE id = ?;`,
		archivedInt, now, id,
	); err != nil {
		return nil, fmt.Errorf("archive item: %w", err)
	}
	return s.GetItemByID(ctx, id)
}

// DeleteItem permanently deletes an item only if it has zero ledger transactions, kit links, or loans.
// Items with ledger history return apperr.ErrItemHasTransactions so the audit trail remains immutable.
func (s *Service) DeleteItem(ctx context.Context, id int64) error {
	if _, err := s.GetItemByID(ctx, id); err != nil {
		return err
	}

	var txCount int
	if err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT
			(SELECT COUNT(*) FROM transactions WHERE item_id = ?) +
			(SELECT COUNT(*) FROM kit_items WHERE item_id = ?) +
			(SELECT COUNT(*) FROM loans WHERE item_id = ?);`,
		id, id, id,
	).Scan(&txCount); err != nil {
		return fmt.Errorf("check item dependencies: %w", err)
	}
	if txCount > 0 {
		return apperr.ErrItemHasTransactions.WithDetail("item_id", id).WithDetail("references", txCount)
	}

	if _, err := s.store.DB().ExecContext(ctx, `DELETE FROM items WHERE id = ?;`, id); err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	return nil
}

// GetItemByID retrieves a complete Item dossier by its primary key ID.
func (s *Service) GetItemByID(ctx context.Context, id int64) (*models.Item, error) {
	row := s.store.DB().QueryRowContext(
		ctx,
		`SELECT
			i.id, i.sku, COALESCE(i.barcode, ''), i.name, i.description, i.category, i.unit,
			i.min_stock, i.min_stock_millis, i.unit_cost, i.unit_cost_millis,
			i.supplier_id, COALESCE(sup.name, ''),
			i.default_location_id, COALESCE(loc.code, ''),
			i.lot_number, i.expiry_date, i.attributes_json, i.photo_data, i.archived,
			i.created_at, i.updated_at
		 FROM items i
		 LEFT JOIN suppliers sup ON sup.id = i.supplier_id
		 LEFT JOIN locations loc ON loc.id = i.default_location_id
		 WHERE i.id = ?;`,
		id,
	)

	item, err := scanItemRow(row)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("item", fmt.Sprintf("%d", id))
	}
	if err != nil {
		return nil, fmt.Errorf("query item %d: %w", id, err)
	}

	if err := s.hydrateItemsDetails(ctx, []*models.Item{item}); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ensureCodeAvailableTx(ctx context.Context, tx *sql.Tx, field, code string, excludeItemID int64) error {
	var conflictItemID int64
	var conflictSource string
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, 'SKU' FROM items WHERE UPPER(sku) = UPPER(?) AND id != ?
		 UNION ALL
		 SELECT id, 'BARCODE' FROM items WHERE barcode IS NOT NULL AND UPPER(barcode) = UPPER(?) AND id != ?
		 UNION ALL
		 SELECT item_id, 'ALIAS' FROM item_aliases WHERE UPPER(code) = UPPER(?) AND item_id != ?
		 LIMIT 1;`,
		code, excludeItemID,
		code, excludeItemID,
		code, excludeItemID,
	).Scan(&conflictItemID, &conflictSource)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check code collision for %q: %w", code, err)
	}

	if field == "sku" {
		return apperr.New(apperr.CodeDuplicateSKU, fmt.Sprintf("SKU %q is already assigned (conflict: %s on item #%d)", code, conflictSource, conflictItemID)).
			WithField("sku").
			WithDetail("conflict_item_id", conflictItemID)
	}
	return apperr.New(apperr.CodeDuplicateBarcode, fmt.Sprintf("barcode %q is already assigned (conflict: %s on item #%d)", code, conflictSource, conflictItemID)).
		WithField(field).
		WithDetail("conflict_item_id", conflictItemID)
}

func resolveOpeningLocationTx(ctx context.Context, tx *sql.Tx, openingLocID, defaultLocID *int64) (int64, error) {
	if openingLocID != nil && *openingLocID > 0 {
		return *openingLocID, nil
	}
	if defaultLocID != nil && *defaultLocID > 0 {
		return *defaultLocID, nil
	}
	var mainID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM locations ORDER BY CASE WHEN code = 'MAIN' THEN 0 ELSE 1 END, id ASC LIMIT 1;`).Scan(&mainID)
	if err != nil {
		return 0, apperr.Validation("opening_location_id", "no storage location exists for opening stock")
	}
	return mainID, nil
}

func normalizeOptionalISODate(field, raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", apperr.Validation(field, fmt.Sprintf("%s must be in YYYY-MM-DD format", field))
	}
	return parsed.Format("2006-01-02"), nil
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanItemRow(r rowScanner) (*models.Item, error) {
	var (
		item          models.Item
		minStockInt   int64
		unitCostInt   int64
		supplierID    sql.NullInt64
		defaultLocID  sql.NullInt64
		archivedInt   int
	)

	err := r.Scan(
		&item.ID,
		&item.SKU,
		&item.Barcode,
		&item.Name,
		&item.Description,
		&item.Category,
		&item.Unit,
		&item.MinStock,
		&minStockInt,
		&item.UnitCost,
		&unitCostInt,
		&supplierID,
		&item.SupplierName,
		&defaultLocID,
		&item.DefaultLocationCode,
		&item.LotNumber,
		&item.ExpiryDate,
		&item.AttributesJSON,
		&item.PhotoData,
		&archivedInt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	item.MinStockMillis = num.Millis(minStockInt)
	item.MinStock = item.MinStockMillis.ToFloat()
	item.MinStockText = item.MinStockMillis.Format()

	item.UnitCostMillis = num.Millis(unitCostInt)
	item.UnitCost = item.UnitCostMillis.ToFloat()
	item.UnitCostText = item.UnitCostMillis.Format()

	if supplierID.Valid {
		v := supplierID.Int64
		item.SupplierID = &v
	}
	if defaultLocID.Valid {
		v := defaultLocID.Int64
		item.DefaultLocationID = &v
	}
	item.Archived = archivedInt != 0
	item.Attributes = ParseAttributesJSON(item.AttributesJSON)
	item.ExpiryStatus = evaluateExpiryStatus(item.ExpiryDate, 30)
	return &item, nil
}

func evaluateExpiryStatus(expiryDate string, warningDays int) string {
	if strings.TrimSpace(expiryDate) == "" {
		return ""
	}
	exp, err := time.Parse("2006-01-02", expiryDate)
	if err != nil {
		return ""
	}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	if exp.Before(now) {
		return "EXPIRED"
	}
	warnHorizon := now.AddDate(0, 0, warningDays)
	if !exp.After(warnHorizon) {
		return "EXPIRING_SOON"
	}
	return "OK"
}
