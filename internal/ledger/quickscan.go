package ledger

import (
	"context"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
)

const (
	ScanModeIn       = "IN"
	ScanModeOut      = "OUT"
	ScanModeLookup   = "LOOKUP"
	ScanModeTransfer = "TRANSFER"
	ScanModeAdjust   = "ADJUST"

	ScanActionMovementRecorded = "MOVEMENT_RECORDED"
	ScanActionItemLookup       = "ITEM_LOOKUP"
	ScanActionLocationSwitched = "LOCATION_SWITCHED"
	ScanActionKitExecuted      = "KIT_EXECUTED"
	ScanActionUnknownBarcode   = "UNKNOWN_BARCODE"
)

// QuickScanInput represents a single barcode or SKU scan from the Persistent Global Quick-Scan Bar.
type QuickScanInput struct {
	Code           string `json:"code"`
	Mode           string `json:"mode"` // "IN", "OUT", "LOOKUP", "TRANSFER", "ADJUST"
	Quantity       string `json:"quantity"`
	LocationID     *int64 `json:"location_id,omitempty"`
	LocationCode   string `json:"location_code,omitempty"`
	ToLocationID   *int64 `json:"to_location_id,omitempty"`
	ToLocationCode string `json:"to_location_code,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Operator       string `json:"operator,omitempty"`
}

// QuickScanResult returns the immediate outcome of a Quick-Scan Bar operation.
type QuickScanResult struct {
	Action             string               `json:"action"`
	Mode               string               `json:"mode"`
	ScannedCode        string               `json:"scanned_code"`
	MatchSource        string               `json:"match_source,omitempty"`
	Item               *models.Item         `json:"item,omitempty"`
	Transaction        *models.Transaction  `json:"transaction,omitempty"`
	UndoTxID           int64                `json:"undo_tx_id,omitempty"`
	UndoBatchID        string               `json:"undo_batch_id,omitempty"`
	RecentTransactions []models.Transaction `json:"recent_transactions,omitempty"`
	SwitchedLocation   *models.Location     `json:"switched_location,omitempty"`
	ExecutedKit        *models.Kit          `json:"executed_kit,omitempty"`
	UnknownBarcode     bool                 `json:"unknown_barcode"`
	SuggestedSKU       string               `json:"suggested_sku,omitempty"`
}

// kitScanHook is registered by kits.go (Step 020) to handle scanning KIT:<CODE> barcodes.
var kitScanHook func(s *Service, ctx context.Context, kitCode string, in QuickScanInput) (*QuickScanResult, bool, error)

// QuickScan processes a barcode or SKU scan from the Persistent Global Quick-Scan Bar in sub-10ms.
// 1. If the code is a location label ("LOC:A-01-B3"), it returns LOCATION_SWITCHED so the UI
//    hands-free switches the active bin.
// 2. If the code matches a BOM/Repair Kit ("KIT:..." or kit barcode), it executes the kit movement.
// 3. If the code matches an item (by Barcode, SKU, or ItemAlias), it executes the requested
//    IN / OUT / LOOKUP / TRANSFER / ADJUST operation and returns a one-click Undo handle.
// 4. If the code is unknown, it returns UNKNOWN_BARCODE with SuggestedSKU so the UI opens
//    "Unknown barcode — Create new item?" pre-filled.
func (s *Service) QuickScan(ctx context.Context, in QuickScanInput) (*QuickScanResult, error) {
	code := strings.TrimSpace(in.Code)
	if code == "" {
		return nil, apperr.Validation("code", "scanned barcode or SKU cannot be empty")
	}

	mode := strings.ToUpper(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = ScanModeOut
	}
	switch mode {
	case ScanModeIn, ScanModeOut, ScanModeLookup, ScanModeTransfer, ScanModeAdjust:
	default:
		return nil, apperr.Validation("mode", "scan mode must be IN, OUT, LOOKUP, TRANSFER, or ADJUST")
	}

	qty := strings.TrimSpace(in.Quantity)
	if qty == "" {
		qty = "1"
	}
	op := strings.TrimSpace(in.Operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	// 1. Check if the user scanned a storage location bin label ("LOC:A-01-B3")
	if locCode, isLoc := catalog.ParseLocationBarcode(code); isLoc {
		loc, err := s.catalog.GetLocationByCode(ctx, locCode)
		if err != nil {
			return nil, err
		}
		return &QuickScanResult{
			Action:           ScanActionLocationSwitched,
			Mode:             mode,
			ScannedCode:      code,
			MatchSource:      "LOCATION_BARCODE",
			SwitchedLocation: loc,
		}, nil
	}

	// 2. Check if the user scanned a BOM / Repair Kit barcode ("KIT:...")
	if kitScanHook != nil {
		if kitRes, matched, err := kitScanHook(s, ctx, code, in); err != nil {
			return nil, err
		} else if matched {
			return kitRes, nil
		}
	}

	// 3. Resolve catalog item by primary Barcode, SKU, or ItemAlias
	item, matchSource, err := s.catalog.ResolveItemByCode(ctx, code)
	if err != nil {
		if apperr.HasCode(err, apperr.CodeNotFound) {
			suggestedSKU, _ := catalog.GenerateSuggestedSKU(ctx, s.store.DB(), "", "")
			return &QuickScanResult{
				Action:         ScanActionUnknownBarcode,
				Mode:           mode,
				ScannedCode:    code,
				UnknownBarcode: true,
				SuggestedSKU:   suggestedSKU,
			}, nil
		}
		return nil, err
	}

	// 4. In LOOKUP mode, return the full item dossier and its last 10 ledger movements
	if mode == ScanModeLookup {
		recent, _, _ := s.ListTransactions(ctx, TransactionFilter{
			ItemID: item.ID,
			Limit:  10,
		})
		return &QuickScanResult{
			Action:             ScanActionItemLookup,
			Mode:               mode,
			ScannedCode:        code,
			MatchSource:        matchSource,
			Item:               item,
			RecentTransactions: recent,
		}, nil
	}

	// 5. Execute the stock movement (IN, OUT, TRANSFER, or ADJUST)
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "Quick-Scan " + mode
	}

	mov := MovementInput{
		ItemID:   item.ID,
		TxType:   mode,
		Quantity: qty,
		Reason:   reason,
		Operator: op,
	}

	switch mode {
	case ScanModeIn:
		mov.ToLocationID = in.LocationID
		mov.ToLocationCode = in.LocationCode
	case ScanModeOut:
		mov.FromLocationID = in.LocationID
		mov.FromLocationCode = in.LocationCode
	case ScanModeTransfer:
		mov.FromLocationID = in.LocationID
		mov.FromLocationCode = in.LocationCode
		mov.ToLocationID = in.ToLocationID
		mov.ToLocationCode = in.ToLocationCode
	case ScanModeAdjust:
		mov.ToLocationID = in.LocationID
		mov.ToLocationCode = in.LocationCode
		mov.AdjustMode = AdjustModeTarget
	}

	txView, err := s.RecordMovement(ctx, mov)
	if err != nil {
		return nil, err
	}

	updatedItem, err := s.catalog.GetItemByID(ctx, item.ID)
	if err != nil {
		return nil, err
	}

	return &QuickScanResult{
		Action:      ScanActionMovementRecorded,
		Mode:        mode,
		ScannedCode: code,
		MatchSource: matchSource,
		Item:        updatedItem,
		Transaction: txView,
		UndoTxID:    txView.ID,
	}, nil
}
