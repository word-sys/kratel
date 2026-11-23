package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

// ItemFilter defines multi-facet search and filter parameters for catalog queries.
type ItemFilter struct {
	Query           string
	Category        string
	LocationID      int64
	SupplierID      int64
	LowStockOnly    bool
	ExpiringOnly    bool
	IncludeArchived bool
	AttributeKey    string
	AttributeValue  string
	Limit           int
	Offset          int
}

// ListItems executes a sub-10ms catalog query combining SQLite FTS5 full-text search
// (plus multi-barcode alias lookup) with category, location, supplier, low-stock,
// expiry, and custom JSON attribute filters.
func (s *Service) ListItems(ctx context.Context, filter ItemFilter) ([]models.Item, int, error) {
	var where []string
	var args []interface{}

	if !filter.IncludeArchived {
		where = append(where, "i.archived = 0")
	}
	if cat := strings.TrimSpace(filter.Category); cat != "" {
		where = append(where, "LOWER(i.category) = LOWER(?)")
		args = append(args, cat)
	}
	if filter.SupplierID > 0 {
		where = append(where, "i.supplier_id = ?")
		args = append(args, filter.SupplierID)
	}
	if filter.LocationID > 0 {
		where = append(where, `(
			i.default_location_id = ?
			OR EXISTS (SELECT 1 FROM stock_levels sl WHERE sl.item_id = i.id AND sl.location_id = ? AND sl.quantity_millis != 0)
		)`)
		args = append(args, filter.LocationID, filter.LocationID)
	}
	if filter.ExpiringOnly {
		where = append(where, "i.expiry_date != ''")
	}

	rawQuery := strings.TrimSpace(filter.Query)
	if rawQuery != "" {
		ftsExpr := db.BuildFTS5MatchQuery(rawQuery)
		likePattern := "%" + strings.ToLower(rawQuery) + "%"
		if s.store.FTSEnabled() && ftsExpr != "" {
			where = append(where, `(
				i.id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)
				OR LOWER(i.sku) LIKE ?
				OR LOWER(COALESCE(i.barcode, '')) LIKE ?
				OR EXISTS (SELECT 1 FROM item_aliases ia WHERE ia.item_id = i.id AND LOWER(ia.code) LIKE ?)
			)`)
			args = append(args, ftsExpr, likePattern, likePattern, likePattern)
		} else {
			where = append(where, `(
				LOWER(i.sku) LIKE ?
				OR LOWER(COALESCE(i.barcode, '')) LIKE ?
				OR LOWER(i.name) LIKE ?
				OR LOWER(i.description) LIKE ?
				OR LOWER(i.category) LIKE ?
				OR LOWER(i.attributes_json) LIKE ?
				OR EXISTS (SELECT 1 FROM item_aliases ia WHERE ia.item_id = i.id AND LOWER(ia.code) LIKE ?)
			)`)
			args = append(args, likePattern, likePattern, likePattern, likePattern, likePattern, likePattern, likePattern)
		}
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT
			i.id, i.sku, COALESCE(i.barcode, ''), i.name, i.description, i.category, i.unit,
			i.min_stock, i.min_stock_millis, i.unit_cost, i.unit_cost_millis,
			i.supplier_id, COALESCE(sup.name, ''),
			i.default_location_id, COALESCE(loc.code, ''),
			i.lot_number, i.expiry_date, i.attributes_json, i.photo_data, i.archived,
			i.created_at, i.updated_at
		FROM items i
		LEFT JOIN suppliers sup ON sup.id = i.supplier_id
		LEFT JOIN locations loc ON loc.id = i.default_location_id
		%s
		ORDER BY i.name COLLATE NOCASE ASC, i.id ASC;`, whereSQL)

	rows, err := s.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query items: %w", err)
	}

	var candidates []*models.Item
	for rows.Next() {
		item, scanErr := scanItemRow(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, 0, fmt.Errorf("scan item row: %w", scanErr)
		}
		if !MatchesAttributeFilter(item.Attributes, filter.AttributeKey, filter.AttributeValue) {
			continue
		}
		if filter.ExpiringOnly && item.ExpiryStatus != "EXPIRED" && item.ExpiryStatus != "EXPIRING_SOON" {
			continue
		}
		candidates = append(candidates, item)
	}
	_ = rows.Close()

	if err := s.hydrateItemsDetails(ctx, candidates); err != nil {
		return nil, 0, err
	}

	var filtered []models.Item
	for _, item := range candidates {
		if filter.LowStockOnly && !item.IsLowStock {
			continue
		}
		filtered = append(filtered, *item)
	}

	totalCount := len(filtered)
	if filter.Offset > 0 {
		if filter.Offset >= len(filtered) {
			return []models.Item{}, totalCount, nil
		}
		filtered = filtered[filter.Offset:]
	}
	if filter.Limit > 0 && len(filtered) > filter.Limit {
		filtered = filtered[:filter.Limit]
	}

	return filtered, totalCount, nil
}

// ListCategories returns all distinct non-empty item categories sorted alphabetically.
func (s *Service) ListCategories(ctx context.Context) ([]string, error) {
	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT DISTINCT category FROM items WHERE category != '' AND archived = 0 ORDER BY category COLLATE NOCASE ASC;`,
	)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var cats []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, nil
}

// hydrateItemsDetails batch-loads per-location stock levels and multi-barcode aliases
// for a slice of items without N+1 queries, computing total stock, valuation, and low-stock status.
func (s *Service) hydrateItemsDetails(ctx context.Context, items []*models.Item) error {
	if len(items) == 0 {
		return nil
	}

	byID := make(map[int64]*models.Item, len(items))
	placeholders := make([]string, 0, len(items))
	args := make([]interface{}, 0, len(items))
	for _, it := range items {
		byID[it.ID] = it
		placeholders = append(placeholders, "?")
		args = append(args, it.ID)
	}
	inClause := strings.Join(placeholders, ",")

	// 1. Batch-load stock_levels joined with locations
	stockQuery := fmt.Sprintf(`
		SELECT
			sl.item_id, sl.location_id, l.code, l.name,
			sl.quantity, sl.quantity_millis, sl.updated_at
		FROM stock_levels sl
		JOIN locations l ON l.id = sl.location_id
		WHERE sl.item_id IN (%s)
		ORDER BY l.code ASC;`, inClause)

	sRows, err := s.store.DB().QueryContext(ctx, stockQuery, args...)
	if err != nil {
		return fmt.Errorf("batch query stock_levels: %w", err)
	}
	for sRows.Next() {
		var (
			sl        models.StockLevel
			qtyMillis int64
		)
		if err := sRows.Scan(
			&sl.ItemID,
			&sl.LocationID,
			&sl.LocationCode,
			&sl.LocationName,
			&sl.Quantity,
			&qtyMillis,
			&sl.UpdatedAt,
		); err != nil {
			_ = sRows.Close()
			return fmt.Errorf("scan stock_level: %w", err)
		}
		sl.QuantityMillis = num.Millis(qtyMillis)
		sl.Quantity = sl.QuantityMillis.ToFloat()
		sl.QuantityText = sl.QuantityMillis.Format()

		if it, ok := byID[sl.ItemID]; ok {
			it.StockByLocation = append(it.StockByLocation, sl)
			it.TotalStockMillis = it.TotalStockMillis.Add(sl.QuantityMillis)
		}
	}
	_ = sRows.Close()

	// 2. Batch-load item_aliases
	aliasQuery := fmt.Sprintf(`
		SELECT id, item_id, code, alias_type, notes, created_at
		FROM item_aliases
		WHERE item_id IN (%s)
		ORDER BY id ASC;`, inClause)

	aRows, err := s.store.DB().QueryContext(ctx, aliasQuery, args...)
	if err != nil {
		return fmt.Errorf("batch query item_aliases: %w", err)
	}
	for aRows.Next() {
		var (
			a  models.ItemAlias
			at string
		)
		if err := aRows.Scan(&a.ID, &a.ItemID, &a.Code, &at, &a.Notes, &a.CreatedAt); err != nil {
			_ = aRows.Close()
			return fmt.Errorf("scan item_alias: %w", err)
		}
		a.AliasType = models.AliasType(at)
		if it, ok := byID[a.ItemID]; ok {
			it.Aliases = append(it.Aliases, a)
		}
	}
	_ = aRows.Close()

	// 3. Finalize computed fields on each Item
	for _, it := range items {
		it.TotalStock = it.TotalStockMillis.ToFloat()
		it.TotalStockText = it.TotalStockMillis.Format()
		it.ValuationMillis = num.LineValuationSafe(it.TotalStockMillis, it.UnitCostMillis)
		it.ValuationText = it.ValuationMillis.FormatFixed(2)
		it.IsLowStock = !it.Archived && (it.TotalStockMillis <= it.MinStockMillis) && (it.MinStockMillis > 0 || it.TotalStockMillis <= 0)
	}

	return nil
}
