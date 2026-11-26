package csvmig

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"

	"github.com/word-sys/kratel/internal/num"
)

// ExportStockLevelsCSV streams the per-item, per-location stock breakdown matrix to w.
func (s *Service) ExportStockLevelsCSV(ctx context.Context, w io.Writer, includeBOM bool) error {
	if includeBOM {
		if _, err := w.Write(utf8BOM); err != nil {
			return fmt.Errorf("write UTF-8 BOM: %w", err)
		}
	}

	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT
			i.sku, COALESCE(i.barcode, ''), i.name, i.category, i.unit,
			l.code, l.name,
			sl.quantity_millis, i.unit_cost_millis, sl.updated_at
		 FROM stock_levels sl
		 JOIN items i ON i.id = sl.item_id
		 JOIN locations l ON l.id = sl.location_id
		 WHERE i.archived = 0 AND sl.quantity_millis != 0
		 ORDER BY l.code COLLATE NOCASE ASC, i.sku COLLATE NOCASE ASC;`,
	)
	if err != nil {
		return fmt.Errorf("query stock levels for CSV export: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"SKU",
		"Barcode",
		"ItemName",
		"Category",
		"Unit",
		"LocationCode",
		"LocationName",
		"Quantity",
		"UnitCost",
		"LineValuation",
		"UpdatedAt",
	}); err != nil {
		return err
	}

	for rows.Next() {
		var (
			sku, barcode, name, category, unit string
			locCode, locName, updatedAt        string
			qtyMillis, costMillis              int64
		)
		if err := rows.Scan(
			&sku, &barcode, &name, &category, &unit,
			&locCode, &locName, &qtyMillis, &costMillis, &updatedAt,
		); err != nil {
			return err
		}
		qty := num.Millis(qtyMillis)
		cost := num.Millis(costMillis)
		val := num.LineValuationSafe(qty, cost)

		if err := cw.Write([]string{
			sku,
			barcode,
			name,
			category,
			unit,
			locCode,
			locName,
			qty.Format(),
			cost.Format(),
			val.FormatFixed(2),
			updatedAt,
		}); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
