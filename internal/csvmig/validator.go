package csvmig

import (
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/num"
)

// DuplicatePolicy controls how the CSV importer handles rows whose SKU or Barcode already exists.
type DuplicatePolicy string

const (
	PolicySkip     DuplicatePolicy = "SKIP"
	PolicyUpdate   DuplicatePolicy = "UPDATE"
	PolicyAddStock DuplicatePolicy = "ADD_STOCK"
)

// MappedRow holds validated, typed fields extracted from one spreadsheet row.
type MappedRow struct {
	RowNumber        int               `json:"row_number"`
	SKU              string            `json:"sku"`
	Barcode          string            `json:"barcode"`
	AliasCode        string            `json:"alias_code"`
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
}

// RowIssue describes a row-level validation error or warning during CSV dry-run or commit.
type RowIssue struct {
	RowNumber int    `json:"row_number"`
	Severity  string `json:"severity"` // "ERROR" or "WARNING"
	Field     string `json:"field"`
	Message   string `json:"message"`
}

// ValidateRows maps every raw CSV row according to columnMapping, parsing fixed-point quantities/costs,
// routing custom columns into Attributes, and returning all valid rows alongside structured issues.
func ValidateRows(headers []string, rows [][]string, columnMapping map[string]string, mapUnmappedToAttributes bool) ([]MappedRow, []RowIssue) {
	if len(columnMapping) == 0 {
		columnMapping = SuggestColumnMapping(headers)
	}

	var (
		validRows []MappedRow
		issues    []RowIssue
	)

	seenSKUs := make(map[string]int, len(rows))
	seenBarcodes := make(map[string]int, len(rows))

	for idx, rawRow := range rows {
		rowNum := idx + 2 // 1-indexed spreadsheet line number (header is line 1)
		mr := MappedRow{
			RowNumber:  rowNum,
			Unit:       "pcs",
			Attributes: make(map[string]string),
		}

		rowHasError := false

		for colIdx, header := range headers {
			if colIdx >= len(rawRow) {
				continue
			}
			val := strings.TrimSpace(rawRow[colIdx])
			if val == "" {
				continue
			}

			target, mapped := columnMapping[header]
			if !mapped && mapUnmappedToAttributes {
				target = FieldAttribute
			}

			switch target {
			case FieldIgnore:
				if mapUnmappedToAttributes {
					mr.Attributes[header] = val
				}
			case FieldSKU:
				mr.SKU = val
			case FieldBarcode:
				mr.Barcode = val
			case FieldAliasCode:
				mr.AliasCode = val
			case FieldName:
				mr.Name = val
			case FieldDescription:
				mr.Description = val
			case FieldCategory:
				mr.Category = val
			case FieldUnit:
				mr.Unit = val
			case FieldLocationCode:
				mr.LocationCode = val
			case FieldSupplierName:
				mr.SupplierName = val
			case FieldLotNumber:
				mr.LotNumber = val
			case FieldExpiryDate:
				parsedDate, err := normalizeCSVDate(val)
				if err != nil {
					issues = append(issues, RowIssue{
						RowNumber: rowNum,
						Severity:  "ERROR",
						Field:     FieldExpiryDate,
						Message:   fmt.Sprintf("invalid expiry date %q (expected YYYY-MM-DD)", val),
					})
					rowHasError = true
				} else {
					mr.ExpiryDate = parsedDate
				}
			case FieldMinStock:
				m, err := num.ParseNonNegativeMillis(FieldMinStock, val)
				if err != nil {
					issues = append(issues, RowIssue{
						RowNumber: rowNum,
						Severity:  "ERROR",
						Field:     FieldMinStock,
						Message:   fmt.Sprintf("invalid min_stock %q: %v", val, err),
					})
					rowHasError = true
				} else {
					mr.MinStockMillis = m
				}
			case FieldUnitCost:
				cleanedCost := stripCommonCurrencySymbols(val)
				m, err := num.ParseNonNegativeMillis(FieldUnitCost, cleanedCost)
				if err != nil {
					issues = append(issues, RowIssue{
						RowNumber: rowNum,
						Severity:  "ERROR",
						Field:     FieldUnitCost,
						Message:   fmt.Sprintf("invalid unit_cost %q: %v", val, err),
					})
					rowHasError = true
				} else {
					mr.UnitCostMillis = m
				}
			case FieldOpeningQty:
				m, err := num.ParseNonNegativeMillis(FieldOpeningQty, val)
				if err != nil {
					issues = append(issues, RowIssue{
						RowNumber: rowNum,
						Severity:  "ERROR",
						Field:     FieldOpeningQty,
						Message:   fmt.Sprintf("invalid opening_qty %q: %v", val, err),
					})
					rowHasError = true
				} else {
					mr.OpeningQtyMillis = m
				}
			case FieldAttribute:
				mr.Attributes[header] = val
			}
		}

		if mr.Name == "" && mr.SKU != "" {
			mr.Name = mr.SKU
			issues = append(issues, RowIssue{
				RowNumber: rowNum,
				Severity:  "WARNING",
				Field:     FieldName,
				Message:   "missing item name; defaulted to SKU",
			})
		}

		if mr.Name == "" && mr.SKU == "" {
			issues = append(issues, RowIssue{
				RowNumber: rowNum,
				Severity:  "ERROR",
				Field:     FieldName,
				Message:   "row must contain at least an item name or SKU",
			})
			rowHasError = true
		}

		if mr.SKU != "" {
			skuKey := strings.ToUpper(mr.SKU)
			if prevRow, dup := seenSKUs[skuKey]; dup {
				issues = append(issues, RowIssue{
					RowNumber: rowNum,
					Severity:  "WARNING",
					Field:     FieldSKU,
					Message:   fmt.Sprintf("duplicate SKU %q also appears on row %d", mr.SKU, prevRow),
				})
			} else {
				seenSKUs[skuKey] = rowNum
			}
		}

		if mr.Barcode != "" {
			bcKey := strings.ToUpper(mr.Barcode)
			if prevRow, dup := seenBarcodes[bcKey]; dup {
				issues = append(issues, RowIssue{
					RowNumber: rowNum,
					Severity:  "WARNING",
					Field:     FieldBarcode,
					Message:   fmt.Sprintf("duplicate barcode %q also appears on row %d", mr.Barcode, prevRow),
				})
			} else {
				seenBarcodes[bcKey] = rowNum
			}
		}

		if !rowHasError {
			validRows = append(validRows, mr)
		}
	}

	return validRows, issues
}

func stripCommonCurrencySymbols(raw string) string {
	s := strings.TrimSpace(raw)
	for _, sym := range []string{"$", "€", "£", "₺", "¥", "USD", "EUR", "TRY", "GBP", "TL"} {
		s = strings.ReplaceAll(s, sym, "")
	}
	return strings.TrimSpace(s)
}

func normalizeCSVDate(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", "2006/01/02", "02.01.2006", "02/01/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("unrecognized date format")
}
