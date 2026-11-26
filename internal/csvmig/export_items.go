package csvmig

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/word-sys/kratel/internal/catalog"
)

// ExportItemsCSV streams the full active item catalog to w as clean UTF-8 CSV.
// When includeBOM is true, a UTF-8 BOM (\xEF\xBB\xBF) is written first for seamless Microsoft Excel compatibility.
func (s *Service) ExportItemsCSV(ctx context.Context, w io.Writer, includeBOM bool, includeArchived bool) error {
	if includeBOM {
		if _, err := w.Write(utf8BOM); err != nil {
			return fmt.Errorf("write UTF-8 BOM: %w", err)
		}
	}

	items, _, err := s.catalog.ListItems(ctx, catalog.ItemFilter{
		IncludeArchived: includeArchived,
	})
	if err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	header := []string{
		"SKU",
		"Barcode",
		"Name",
		"Description",
		"Category",
		"Unit",
		"TotalStock",
		"MinStock",
		"UnitCost",
		"Valuation",
		"PrimaryLocation",
		"Supplier",
		"LotNumber",
		"ExpiryDate",
		"Aliases",
		"AttributesJSON",
		"Archived",
		"CreatedAt",
		"UpdatedAt",
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, it := range items {
		primaryLoc := it.DefaultLocationCode
		if primaryLoc == "" && len(it.StockByLocation) > 0 {
			primaryLoc = it.StockByLocation[0].LocationCode
		}
		aliasCodes := make([]string, 0, len(it.Aliases))
		for _, a := range it.Aliases {
			aliasCodes = append(aliasCodes, a.Code)
		}
		archivedStr := "false"
		if it.Archived {
			archivedStr = "true"
		}

		rec := []string{
			it.SKU,
			it.Barcode,
			it.Name,
			it.Description,
			it.Category,
			it.Unit,
			it.TotalStockText,
			it.MinStockText,
			it.UnitCostText,
			it.ValuationText,
			primaryLoc,
			it.SupplierName,
			it.LotNumber,
			it.ExpiryDate,
			strings.Join(aliasCodes, " | "),
			it.AttributesJSON,
			archivedStr,
			it.CreatedAt,
			it.UpdatedAt,
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
