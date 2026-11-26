package csvmig

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
)

// ExportReorderListCSV streams the low-stock reorder queue to w as an Excel-compatible UTF-8 CSV.
func (s *Service) ExportReorderListCSV(ctx context.Context, w io.Writer, includeBOM bool) error {
	if includeBOM {
		if _, err := w.Write(utf8BOM); err != nil {
			return fmt.Errorf("write UTF-8 BOM: %w", err)
		}
	}

	queue, err := s.catalog.ListLowStockReorderQueue(ctx)
	if err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"SKU",
		"Barcode",
		"ItemName",
		"Category",
		"Unit",
		"PrimaryLocation",
		"CurrentStock",
		"MinStock",
		"Deficit",
		"SuggestedOrderQty",
		"UnitCost",
		"EstimatedRestockCost",
		"Supplier",
		"SupplierContact",
	}); err != nil {
		return err
	}

	for _, r := range queue {
		if err := cw.Write([]string{
			r.SKU,
			r.Barcode,
			r.Name,
			r.Category,
			r.Unit,
			r.PrimaryLocationCode,
			r.CurrentStockText,
			r.MinStockText,
			r.DeficitText,
			r.SuggestedOrderText,
			r.UnitCostText,
			r.EstimatedRestockText,
			r.SupplierName,
			r.SupplierContact,
		}); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
