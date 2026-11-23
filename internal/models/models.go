package models

import "github.com/word-sys/kratel/internal/num"

// AliasType categorizes alternate item barcodes or part numbers.
type AliasType string

const (
	AliasEAN          AliasType = "EAN"
	AliasSupplierPart AliasType = "SUPPLIER_PART"
	AliasInternalQR   AliasType = "INTERNAL_QR"
	AliasLegacySKU    AliasType = "LEGACY_SKU"
	AliasOther        AliasType = "OTHER"
)

// ItemAlias represents an additional scannable barcode or distributor part code mapped to an Item.
type ItemAlias struct {
	ID        int64     `json:"id"`
	ItemID    int64     `json:"item_id"`
	Code      string    `json:"code"`
	AliasType AliasType `json:"alias_type"`
	Notes     string    `json:"notes"`
	CreatedAt string    `json:"created_at"`
}

// StockLevel represents the materialized stock balance of an Item in a specific Location.
type StockLevel struct {
	ItemID         int64      `json:"item_id"`
	LocationID     int64      `json:"location_id"`
	LocationCode   string     `json:"location_code"`
	LocationName   string     `json:"location_name"`
	Breadcrumb     string     `json:"breadcrumb,omitempty"`
	Quantity       float64    `json:"quantity"`
	QuantityMillis num.Millis `json:"quantity_millis"`
	QuantityText   string     `json:"quantity_text"`
	UpdatedAt      string     `json:"updated_at"`
}

// Item represents a trackable catalog item with aggregated stock, valuation, aliases, and custom attributes.
type Item struct {
	ID                 int64             `json:"id"`
	SKU                string            `json:"sku"`
	Barcode            string            `json:"barcode"`
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	Category           string            `json:"category"`
	Unit               string            `json:"unit"`
	MinStock           float64           `json:"min_stock"`
	MinStockMillis     num.Millis        `json:"min_stock_millis"`
	MinStockText       string            `json:"min_stock_text"`
	UnitCost           float64           `json:"unit_cost"`
	UnitCostMillis     num.Millis        `json:"unit_cost_millis"`
	UnitCostText       string            `json:"unit_cost_text"`
	SupplierID         *int64            `json:"supplier_id"`
	SupplierName       string            `json:"supplier_name,omitempty"`
	DefaultLocationID  *int64            `json:"default_location_id"`
	DefaultLocationCode string           `json:"default_location_code,omitempty"`
	LotNumber          string            `json:"lot_number"`
	ExpiryDate         string            `json:"expiry_date"`
	ExpiryStatus       string            `json:"expiry_status,omitempty"` // "", "OK", "EXPIRING_SOON", "EXPIRED"
	AttributesJSON     string            `json:"attributes_json"`
	Attributes         map[string]string `json:"attributes"`
	PhotoData          string            `json:"photo_data,omitempty"`
	Archived           bool              `json:"archived"`
	TotalStock         float64           `json:"total_stock"`
	TotalStockMillis   num.Millis        `json:"total_stock_millis"`
	TotalStockText     string            `json:"total_stock_text"`
	ValuationMillis    num.Millis        `json:"valuation_millis"`
	ValuationText      string            `json:"valuation_text"`
	IsLowStock         bool              `json:"is_low_stock"`
	Aliases            []ItemAlias       `json:"aliases,omitempty"`
	StockByLocation    []StockLevel      `json:"stock_by_location,omitempty"`
	CreatedAt          string            `json:"created_at"`
	UpdatedAt          string            `json:"updated_at"`
}

// ItemInput carries validated fields for creating or updating a catalog Item.
type ItemInput struct {
	SKU               string            `json:"sku"`
	Barcode           string            `json:"barcode"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	Category          string            `json:"category"`
	Unit              string            `json:"unit"`
	MinStock          string            `json:"min_stock"`
	UnitCost          string            `json:"unit_cost"`
	SupplierID        *int64            `json:"supplier_id"`
	DefaultLocationID *int64            `json:"default_location_id"`
	LotNumber         string            `json:"lot_number"`
	ExpiryDate        string            `json:"expiry_date"`
	AttributesJSON    string            `json:"attributes_json"`
	Attributes        map[string]string `json:"attributes"`
	PhotoData         string            `json:"photo_data"`
	Archived          bool              `json:"archived"`
	OpeningQuantity   string            `json:"opening_quantity,omitempty"`
	OpeningLocationID *int64            `json:"opening_location_id,omitempty"`
	Operator          string            `json:"operator,omitempty"`
}

// Location represents a hierarchical storage bin, shelf, rack, or room.
type Location struct {
	ID                int64      `json:"id"`
	Code              string     `json:"code"`
	Name              string     `json:"name"`
	ParentID          *int64     `json:"parent_id"`
	ParentCode        string     `json:"parent_code,omitempty"`
	Breadcrumb        string     `json:"breadcrumb"`
	Depth             int        `json:"depth"`
	Notes             string     `json:"notes"`
	ScanBarcode       string     `json:"scan_barcode"`
	DistinctItems     int        `json:"distinct_items"`
	DirectStockMillis num.Millis `json:"direct_stock_millis"`
	DirectStockText   string     `json:"direct_stock_text"`
	CreatedAt         string     `json:"created_at"`
}

// LocationTreeNode represents a Location node within a hierarchical storage tree with rolled-up subtree totals.
type LocationTreeNode struct {
	Location
	SubtreeItems       int                 `json:"subtree_items"`
	SubtreeStockMillis num.Millis          `json:"subtree_stock_millis"`
	SubtreeStockText   string              `json:"subtree_stock_text"`
	Children           []*LocationTreeNode `json:"children"`
}

// Supplier represents a vendor or parts distributor in the supplier directory.
type Supplier struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Contact   string `json:"contact"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Notes     string `json:"notes"`
	ItemCount int    `json:"item_count"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ReorderItem represents a low-stock item requiring replenishment.
type ReorderItem struct {
	ItemID                 int64      `json:"item_id"`
	SKU                    string     `json:"sku"`
	Barcode                string     `json:"barcode"`
	Name                   string     `json:"name"`
	Category               string     `json:"category"`
	Unit                   string     `json:"unit"`
	PrimaryLocationCode    string     `json:"primary_location_code"`
	SupplierID             *int64     `json:"supplier_id"`
	SupplierName           string     `json:"supplier_name"`
	SupplierContact        string     `json:"supplier_contact"`
	CurrentStockMillis     num.Millis `json:"current_stock_millis"`
	CurrentStockText       string     `json:"current_stock_text"`
	MinStockMillis         num.Millis `json:"min_stock_millis"`
	MinStockText           string     `json:"min_stock_text"`
	DeficitMillis          num.Millis `json:"deficit_millis"`
	DeficitText            string     `json:"deficit_text"`
	SuggestedOrderMillis   num.Millis `json:"suggested_order_millis"`
	SuggestedOrderText     string     `json:"suggested_order_text"`
	UnitCostMillis         num.Millis `json:"unit_cost_millis"`
	UnitCostText           string     `json:"unit_cost_text"`
	EstimatedRestockMillis num.Millis `json:"estimated_restock_millis"`
	EstimatedRestockText   string     `json:"estimated_restock_text"`
}

// SupplierReorderGroup groups low-stock reorder items by supplier for purchase checklists.
type SupplierReorderGroup struct {
	SupplierID         int64         `json:"supplier_id"`
	SupplierName       string        `json:"supplier_name"`
	SupplierContact    string        `json:"supplier_contact"`
	SupplierEmail      string        `json:"supplier_email"`
	SupplierPhone      string        `json:"supplier_phone"`
	TotalItems         int           `json:"total_items"`
	EstimatedCostMillis num.Millis   `json:"estimated_cost_millis"`
	EstimatedCostText  string        `json:"estimated_cost_text"`
	Items              []ReorderItem `json:"items"`
}

// Transaction represents an immutable stock movement record in the ledger.
type Transaction struct {
	ID                 int64      `json:"id"`
	ItemID             int64      `json:"item_id"`
	ItemSKU            string     `json:"item_sku"`
	ItemBarcode        string     `json:"item_barcode"`
	ItemName           string     `json:"item_name"`
	ItemUnit           string     `json:"item_unit"`
	FromLocationID     *int64     `json:"from_location_id"`
	FromLocationCode   string     `json:"from_location_code,omitempty"`
	FromLocationName   string     `json:"from_location_name,omitempty"`
	ToLocationID       *int64     `json:"to_location_id"`
	ToLocationCode     string     `json:"to_location_code,omitempty"`
	ToLocationName     string     `json:"to_location_name,omitempty"`
	TxType             string     `json:"tx_type"`
	Quantity           float64    `json:"quantity"`
	QuantityMillis     num.Millis `json:"quantity_millis"`
	QuantityText       string     `json:"quantity_text"`
	DeltaMillis        num.Millis `json:"delta_millis"`
	DeltaText          string     `json:"delta_text"`
	BalanceAfterMillis num.Millis `json:"balance_after_millis"`
	BalanceAfterText   string     `json:"balance_after_text"`
	UnitCostMillis     num.Millis `json:"unit_cost_millis"`
	LotNumber          string     `json:"lot_number,omitempty"`
	ExpiryDate         string     `json:"expiry_date,omitempty"`
	BatchID            string     `json:"batch_id,omitempty"`
	Reason             string     `json:"reason"`
	Operator           string     `json:"operator"`
	ReferenceTxID      *int64     `json:"reference_tx_id,omitempty"`
	Undone             bool       `json:"undone"`
	CanUndo            bool       `json:"can_undo"`
	CreatedAt          string     `json:"created_at"`
}

// Kit represents a Bill of Materials (BOM) or repair parts bundle.
type Kit struct {
	ID          int64          `json:"id"`
	Code        string         `json:"code"`
	Barcode     string         `json:"barcode"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	MaxBuildable int64         `json:"max_buildable"`
	TotalCostMillis num.Millis `json:"total_cost_millis"`
	TotalCostText string       `json:"total_cost_text"`
	Components  []KitComponent `json:"components"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

// KitComponent represents a single required item and quantity inside a Kit.
type KitComponent struct {
	ID               int64      `json:"id"`
	KitID            int64      `json:"kit_id"`
	ItemID           int64      `json:"item_id"`
	ItemSKU          string     `json:"item_sku"`
	ItemName         string     `json:"item_name"`
	ItemUnit         string     `json:"item_unit"`
	Quantity         float64    `json:"quantity"`
	QuantityMillis   num.Millis `json:"quantity_millis"`
	QuantityText     string     `json:"quantity_text"`
	AvailableMillis  num.Millis `json:"available_millis"`
	AvailableText    string     `json:"available_text"`
	Sufficient       bool       `json:"sufficient"`
	Notes            string     `json:"notes"`
}

// AuditSession represents a physical stocktake / cycle-count session.
type AuditSession struct {
	ID           int64       `json:"id"`
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	LocationID   *int64      `json:"location_id"`
	LocationCode string      `json:"location_code,omitempty"`
	Status       string      `json:"status"` // "OPEN", "COMMITTED", "CANCELLED"
	Operator     string      `json:"operator"`
	Notes        string      `json:"notes"`
	BatchID      string      `json:"batch_id,omitempty"`
	TotalLines   int         `json:"total_lines"`
	CountedLines int         `json:"counted_lines"`
	VarianceLines int        `json:"variance_lines"`
	Lines        []AuditLine `json:"lines,omitempty"`
	CreatedAt    string      `json:"created_at"`
	CommittedAt  string      `json:"committed_at,omitempty"`
}

// AuditLine represents the expected vs. counted balance for an item at a location during an AuditSession.
type AuditLine struct {
	ID                    int64      `json:"id"`
	SessionID             int64      `json:"session_id"`
	ItemID                int64      `json:"item_id"`
	ItemSKU               string     `json:"item_sku"`
	ItemBarcode           string     `json:"item_barcode"`
	ItemName              string     `json:"item_name"`
	ItemUnit              string     `json:"item_unit"`
	LocationID            int64      `json:"location_id"`
	LocationCode          string     `json:"location_code"`
	ExpectedMillis        num.Millis `json:"expected_millis"`
	ExpectedText          string     `json:"expected_text"`
	CountedMillis         num.Millis `json:"counted_millis"`
	CountedText           string     `json:"counted_text"`
	VarianceMillis        num.Millis `json:"variance_millis"`
	VarianceText          string     `json:"variance_text"`
	ValuationImpactMillis num.Millis `json:"valuation_impact_millis"`
	ValuationImpactText   string     `json:"valuation_impact_text"`
	Counted               bool       `json:"counted"`
	Notes                 string     `json:"notes"`
	UpdatedAt             string     `json:"updated_at"`
}

// Loan represents a returnable workshop tool, meter, or test board checked out to a borrower.
type Loan struct {
	ID             int64      `json:"id"`
	ItemID         int64      `json:"item_id"`
	ItemSKU        string     `json:"item_sku"`
	ItemBarcode    string     `json:"item_barcode"`
	ItemName       string     `json:"item_name"`
	ItemUnit       string     `json:"item_unit"`
	LocationID     int64      `json:"location_id"`
	LocationCode   string     `json:"location_code"`
	Quantity       float64    `json:"quantity"`
	QuantityMillis num.Millis `json:"quantity_millis"`
	QuantityText   string     `json:"quantity_text"`
	Borrower       string     `json:"borrower"`
	Operator       string     `json:"operator"`
	Notes          string     `json:"notes"`
	Status         string     `json:"status"` // "ACTIVE", "RETURNED"
	IsOverdue      bool       `json:"is_overdue"`
	OutTxID        *int64     `json:"out_tx_id,omitempty"`
	InTxID         *int64     `json:"in_tx_id,omitempty"`
	CheckedOutAt   string     `json:"checked_out_at"`
	DueAt          string     `json:"due_at,omitempty"`
	ReturnedAt     string     `json:"returned_at,omitempty"`
}

// DashboardSummary aggregates all actionable workshop metrics for the home view.
type DashboardSummary struct {
	TotalActiveItems      int           `json:"total_active_items"`
	TotalArchivedItems    int           `json:"total_archived_items"`
	TotalLocations        int           `json:"total_locations"`
	TotalSuppliers        int           `json:"total_suppliers"`
	TotalStockUnitsMillis num.Millis    `json:"total_stock_units_millis"`
	TotalStockUnitsText   string        `json:"total_stock_units_text"`
	TotalValuationMillis  num.Millis    `json:"total_valuation_millis"`
	TotalValuationText    string        `json:"total_valuation_text"`
	LowStockCount         int           `json:"low_stock_count"`
	ExpiringSoonCount     int           `json:"expiring_soon_count"`
	ActiveLoanCount       int           `json:"active_loan_count"`
	TodayMovementCount    int           `json:"today_movement_count"`
	LowStockQueue         []ReorderItem `json:"low_stock_queue"`
	ExpiringItems         []Item        `json:"expiring_items"`
	ActiveLoans           []Loan        `json:"active_loans"`
	RecentTransactions    []Transaction `json:"recent_transactions"`
}
