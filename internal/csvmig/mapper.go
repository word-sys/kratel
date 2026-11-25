package csvmig

import (
	"strings"
	"unicode"
)

const (
	FieldIgnore       = ""
	FieldSKU          = "sku"
	FieldBarcode      = "barcode"
	FieldAliasCode    = "alias_code"
	FieldName         = "name"
	FieldDescription  = "description"
	FieldCategory     = "category"
	FieldUnit         = "unit"
	FieldMinStock     = "min_stock"
	FieldUnitCost     = "unit_cost"
	FieldOpeningQty   = "opening_qty"
	FieldLocationCode = "location_code"
	FieldSupplierName = "supplier_name"
	FieldLotNumber    = "lot_number"
	FieldExpiryDate   = "expiry_date"
	FieldAttribute    = "attribute"
)

// FieldOption describes a mappable Kratel catalog/ledger target field for the CSV column mapper UI.
type FieldOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// AvailableTargetFields returns the ordered list of Kratel fields supported by the interactive column mapper.
func AvailableTargetFields() []FieldOption {
	return []FieldOption{
		{Key: FieldIgnore, Label: "Ignore Column"},
		{Key: FieldSKU, Label: "SKU / Part #"},
		{Key: FieldBarcode, Label: "Barcode (EAN / UPC / QR)"},
		{Key: FieldAliasCode, Label: "Alternate Barcode / Supplier Part #"},
		{Key: FieldName, Label: "Item Name"},
		{Key: FieldDescription, Label: "Description"},
		{Key: FieldCategory, Label: "Category"},
		{Key: FieldUnit, Label: "Unit of Measure (pcs, kg, m, L)"},
		{Key: FieldOpeningQty, Label: "Opening Stock Quantity (IN)"},
		{Key: FieldLocationCode, Label: "Storage Location / Bin Code"},
		{Key: FieldMinStock, Label: "Min Stock Alert Threshold"},
		{Key: FieldUnitCost, Label: "Unit Cost"},
		{Key: FieldSupplierName, Label: "Supplier Name"},
		{Key: FieldLotNumber, Label: "Lot / Batch Number"},
		{Key: FieldExpiryDate, Label: "Expiry / Calibration Date (YYYY-MM-DD)"},
		{Key: FieldAttribute, Label: "Custom JSON Attribute (Key = Header)"},
	}
}

// SuggestColumnMapping analyzes spreadsheet column headers in English, Turkish, German, Spanish,
// and common workshop formats, returning a best-effort header -> Kratel target field map.
func SuggestColumnMapping(headers []string) map[string]string {
	mapping := make(map[string]string, len(headers))
	assigned := make(map[string]bool)

	for _, h := range headers {
		norm := normalizeHeaderToken(h)
		target := matchHeaderToField(norm)
		if target != FieldIgnore && target != FieldAttribute {
			// Avoid mapping two columns to the same single-value field automatically
			if assigned[target] {
				mapping[h] = FieldAttribute
				continue
			}
			assigned[target] = true
		}
		mapping[h] = target
	}
	return mapping
}

func matchHeaderToField(norm string) string {
	switch norm {
	case "sku", "part", "partno", "partnumber", "partnum", "itemcode", "productcode", "code", "stokkodu", "urunkodu", "artikelnr", "artikelnummer", "codigo":
		return FieldSKU
	case "barcode", "barkod", "ean", "ean13", "upc", "gtin", "qrcode", "qr":
		return FieldBarcode
	case "alias", "aliascode", "mpn", "mfgpart", "supplierpart", "suppliercode", "mouser", "digikey", "lcsc", "tedarikcikodu":
		return FieldAliasCode
	case "name", "item", "itemname", "product", "productname", "title", "urun", "urunadi", "ad", "isim", "bezeichnung", "nombre":
		return FieldName
	case "description", "desc", "details", "notes", "aciklama", "beschreibung", "descripcion":
		return FieldDescription
	case "category", "cat", "group", "type", "kategori", "grup", "kategorie", "categoria":
		return FieldCategory
	case "unit", "uom", "unitofmeasure", "birim", "einheit", "unidad":
		return FieldUnit
	case "qty", "quantity", "stock", "openingqty", "openingstock", "count", "onhand", "miktar", "adet", "stok", "stokmiktari", "menge", "bestand", "cantidad":
		return FieldOpeningQty
	case "location", "locationcode", "bin", "bincode", "shelf", "rack", "warehouse", "konum", "raf", "depo", "lagerort", "ubicacion":
		return FieldLocationCode
	case "minstock", "min", "minimum", "minqty", "reorderlevel", "reorderpoint", "kritikstok", "asgaristok", "mindestbestand", "stockminimo":
		return FieldMinStock
	case "cost", "unitcost", "price", "unitprice", "buyprice", "fiyat", "birimfiyat", "maliyet", "preis", "kosten", "precio", "costo":
		return FieldUnitCost
	case "supplier", "suppliername", "vendor", "distributor", "tedarikci", "lieferant", "proveedor":
		return FieldSupplierName
	case "lot", "lotnumber", "batch", "batchno", "parti", "partino", "charge", "lote":
		return FieldLotNumber
	case "expiry", "expirydate", "expiration", "expdate", "skt", "sonkullanma", "ablaufdatum", "caducidad":
		return FieldExpiryDate
	}

	// Substring heuristics for composite headers like "Part #", "Opening Qty", "Unit Cost (USD)"
	switch {
	case strings.Contains(norm, "barcode") || strings.Contains(norm, "barkod"):
		return FieldBarcode
	case strings.Contains(norm, "sku") || strings.Contains(norm, "partno") || strings.Contains(norm, "stokkodu"):
		return FieldSKU
	case strings.Contains(norm, "minstock") || strings.Contains(norm, "reorder"):
		return FieldMinStock
	case strings.Contains(norm, "cost") || strings.Contains(norm, "price") || strings.Contains(norm, "fiyat"):
		return FieldUnitCost
	case strings.Contains(norm, "qty") || strings.Contains(norm, "quantity") || strings.Contains(norm, "miktar"):
		return FieldOpeningQty
	case strings.Contains(norm, "location") || strings.Contains(norm, "bin") || strings.Contains(norm, "shelf") || strings.Contains(norm, "konum") || strings.Contains(norm, "raf"):
		return FieldLocationCode
	case strings.Contains(norm, "supplier") || strings.Contains(norm, "vendor") || strings.Contains(norm, "tedarik"):
		return FieldSupplierName
	case strings.Contains(norm, "expir") || strings.Contains(norm, "skt"):
		return FieldExpiryDate
	case strings.Contains(norm, "name") || strings.Contains(norm, "urun"):
		return FieldName
	default:
		return FieldIgnore
	}
}

func normalizeHeaderToken(header string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(header)) {
		switch r {
		case 'ç':
			b.WriteByte('c')
		case 'ğ':
			b.WriteByte('g')
		case 'ı', 'İ':
			b.WriteByte('i')
		case 'ö':
			b.WriteByte('o')
		case 'ş':
			b.WriteByte('s')
		case 'ü':
			b.WriteByte('u')
		case 'ä':
			b.WriteByte('a')
		case 'ß':
			b.WriteString("ss")
		case 'á':
			b.WriteByte('a')
		case 'é':
			b.WriteByte('e')
		case 'í':
			b.WriteByte('i')
		case 'ó':
			b.WriteByte('o')
		case 'ú', 'ñ':
			if r == 'ñ' {
				b.WriteByte('n')
			} else {
				b.WriteByte('u')
			}
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
