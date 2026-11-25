package csvmig

import (
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/num"
)

// DuplicatePolicy controls how the CSV importer handles rows whose SKU or Barcode already exists in Kratel.
type DuplicatePolicy string

const (
	PolicySkip     DuplicatePolicy = "SKIP"
	PolicyUpdate   DuplicatePolicy = "UPDATE"
	PolicyAddStock DuplicatePolicy = "ADD_STOCK"
)

// ValidatedRow holds the normalized, parsed values and any validation errors/warnings for a single CSV row.
type ValidatedRow struct {
	RowNumber        int               `json:"row_number"` // 1-based spreadsheet row (header = row 1, first data row = row 2)
	SKU              string            `json:"sku"`
	Barcode          string            `json:"barcode"`
	Alias            string            `json:"alias"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Category         string            `json:"category"`
	Unit             string            `json:"unit"`
	MinStockMillis   num.Millis        `json:"min_stock_millis"`
	UnitCostMillis   num.Millis        `json:"unit_cost_millis"`
	OpeningQtyMillis num.Millis        `json:"opening_qty_millis"`
	LocationCode     string            `json:"location_code"`
	SupplierName     string            `json:"supplier_name"`
	LotNumber        string            `json:"lot_number"`
	ExpiryDate       string            `json:"expiry_date"`
	Attributes       map[string]string `json:"attributes"`
	AttributesJSON   string            `json:"attributes_json"`
	Warnings         []string          `json:"warnings,omitempty"`
	Errors           []string          `json:"errors,omitempty"`
}

// HasErrors reports whether this CSV row has one or more blocking validation errors.
func (r ValidatedRow) HasErrors() bool {
	return len(r.Errors) > 0
}

// ValidateRows applies user-confirmed column mappings across all parsed CSV data rows,
// parsing fixed-point Millis quantities/costs, validating optional dates, routing extra columns
// into attributes_json, and flagging row-level errors or within-file duplicates.
func ValidateRows(headers []string, rows [][]string, mappings []ColumnMapping) ([]ValidatedRow, int) {
	out := make([]ValidatedRow, 0, len(rows))
	seenSKUs := make(map[string]int, len(rows))
	seenBarcodes := make(map[string]int, len(rows))
	errorRows := 0

	for i, rawRow := range rows {
		vr := ValidatedRow{
			RowNumber:  i + 2,
			Unit:       "pcs",
			Attributes: make(map[string]string),
		}

		for _, m := range mappings {
			if m.TargetField == FieldIgnore || m.TargetField == "" {
				continue
			}
			if m.ColumnIndex < 0 || m.ColumnIndex >= len(rawRow) {
				continue
			}
			val := strings.TrimSpace(rawRow[m.ColumnIndex])
			if val == "" {
				continue
			}

			switch m.TargetField {
			case FieldSKU:
				vr.SKU = val
			case FieldBarcode:
				vr.Barcode = val
			case FieldAlias:
				vr.Alias = val
			case FieldName:
				vr.Name = val
			case FieldDescription:
				vr.Description = val
			case FieldCategory:
				vr.Category = val
			case FieldUnit:
				vr.Unit = val
			case FieldLocationCode:
				vr.LocationCode = val
			case FieldSupplierName:
				vr.SupplierName = val
			case FieldLotNumber:
				vr.LotNumber = val
			case FieldExpiryDate:
				if _, err := time.Parse("2006-01-02", val); err != nil {
					vr.Errors = append(vr.Errors, fmt.Sprintf("invalid expiry_date %q (expected YYYY-MM-DD)", val))
				} else {
					vr.ExpiryDate = val
				}
			case FieldMinStock:
				parsed, err := num.ParseNonNegativeMillis("min_stock", val)
				if err != nil {
					vr.Errors = append(vr.Errors, fmt.Sprintf("invalid min_stock %q", val))
				} else {
					vr.MinStockMillis = parsed
				}
			case FieldUnitCost:
				parsed, err := num.ParseNonNegativeMillis("unit_cost", val)
				if err != nil {
					vr.Errors = append(vr.Errors, fmt.Sprintf("invalid unit_cost %q", val))
				} else {
					vr.UnitCostMillis = parsed
				}
			case FieldOpeningQty:
				parsed, err := num.ParseNonNegativeMillis("opening_qty", val)
				if err != nil {
					vr.Errors = append(vr.Errors, fmt.Sprintf("invalid opening_qty %q", val))
				} else {
					vr.OpeningQtyMillis = parsed
				}
			case FieldAttribute:
				key := strings.TrimSpace(m.AttributeKey)
				if key == "" && m.ColumnIndex < len(headers) {
					key = strings.TrimSpace(headers[m.ColumnIndex])
				}
				if key != "" {
					vr.Attributes[key] = val
				}
			}
		}

		// Require Name (or fall back to SKU if Name is missing and SKU is present, emitting a warning)
		if vr.Name == "" {
			if vr.SKU != "" {
				vr.Name = vr.SKU
				vr.Warnings = append(vr.Warnings, "missing item name; defaulted to SKU")
			} else {
				vr.Errors = append(vr.Errors, "item name is required")
			}
		}

		if vr.SKU != "" {
			key := strings.ToUpper(vr.SKU)
			if prevRow, dup := seenSKUs[key]; dup {
				vr.Warnings = append(vr.Warnings, fmt.Sprintf("duplicate SKU %q also appears in row %d", vr.SKU, prevRow))
			} else {
				seenSKUs[key] = vr.RowNumber
			}
		}

		if vr.Barcode != "" {
			key := strings.ToUpper(vr.Barcode)
			if prevRow, dup := seenBarcodes[key]; dup {
				vr.Errors = append(vr.Errors, fmt.Sprintf("duplicate barcode %q already appears in row %d", vr.Barcode, prevRow))
			} else {
				seenBarcodes[key] = vr.RowNumber
			}
		}

		attrsJSON, cleanedAttrs, attrErr := catalog.NormalizeAttributes("", vr.Attributes)
		if attrErr != nil {
			vr.Errors = append(vr.Errors, attrErr.Error())
		} else {
			vr.AttributesJSON = attrsJSON
			vr.Attributes = cleanedAttrs
		}

		if vr.HasErrors() {
			errorRows++
		}
		out = append(out, vr)
	}

	return out, errorRows
}
