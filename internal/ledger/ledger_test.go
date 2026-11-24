package ledger

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
)

func setupTestLedger(t *testing.T) (*db.Store, *catalog.Service, *Service) {
	t.Helper()
	store, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("db.Open(:memory:) failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	cat := catalog.NewService(store)
	led := NewService(store, cat)
	return store, cat, led
}

func TestMovementsAdjustUndoAndReconciliation(t *testing.T) {
	ctx := context.Background()
	store, cat, led := setupTestLedger(t)

	binA, err := cat.CreateLocation(ctx, catalog.LocationInput{Code: "A-01", Name: "Bin A-01"})
	if err != nil {
		t.Fatalf("CreateLocation A-01 failed: %v", err)
	}
	binB, err := cat.CreateLocation(ctx, catalog.LocationInput{Code: "B-02", Name: "Bin B-02"})
	if err != nil {
		t.Fatalf("CreateLocation B-02 failed: %v", err)
	}

	item, err := cat.CreateItem(ctx, models.ItemInput{
		SKU:               "SOLDER-PB-FREE",
		Barcode:           "8690001000101",
		Name:              "Lead-Free Solder Wire 0.8mm",
		Unit:              "kg",
		MinStock:          "1.0",
		UnitCost:          "24.50",
		DefaultLocationID: &binA.ID,
	})
	if err != nil {
		t.Fatalf("CreateItem failed: %v", err)
	}

	// 1. IN 2.500 kg into Bin A-01
	txIn, err := led.RecordMovement(ctx, MovementInput{
		ItemID:       item.ID,
		TxType:       TxIn,
		Quantity:     "2.5",
		ToLocationID: &binA.ID,
		Reason:       "Supplier delivery",
		Operator:     "Barin",
	})
	if err != nil {
		t.Fatalf("RecordMovement IN failed: %v", err)
	}
	if txIn.BalanceAfterText != "2.5" || txIn.DeltaText != "+2.5" {
		t.Fatalf("unexpected IN transaction values: balance=%s delta=%s", txIn.BalanceAfterText, txIn.DeltaText)
	}

	// 2. OUT 0.750 kg (auto-resolves Bin A-01) -> 1.750 kg remaining
	txOut, err := led.RecordMovement(ctx, MovementInput{
		ItemID:   item.ID,
		TxType:   TxOut,
		Quantity: "0.75",
		Reason:   "Bench 2 assembly",
		Operator: "Barin",
	})
	if err != nil {
		t.Fatalf("RecordMovement OUT failed: %v", err)
	}
	if txOut.BalanceAfterText != "1.75" || txOut.DeltaText != "-0.75" {
		t.Fatalf("unexpected OUT transaction values: balance=%s delta=%s", txOut.BalanceAfterText, txOut.DeltaText)
	}

	// 3. Insufficient stock guard: attempting OUT 5.0 kg must return ErrInsufficientStock
	_, err = led.RecordMovement(ctx, MovementInput{
		ItemID:         item.ID,
		TxType:         TxOut,
		Quantity:       "5.0",
		FromLocationID: &binA.ID,
	})
	if !errors.Is(err, apperr.ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	// 4. TRANSFER 0.500 kg from A-01 to B-02 -> A-01 has 1.250 kg, B-02 has 0.500 kg
	txXfer, err := led.RecordMovement(ctx, MovementInput{
		ItemID:         item.ID,
		TxType:         TxTransfer,
		Quantity:       "0.5",
		FromLocationID: &binA.ID,
		ToLocationID:   &binB.ID,
		Operator:       "Barin",
	})
	if err != nil {
		t.Fatalf("RecordMovement TRANSFER failed: %v", err)
	}
	if txXfer.BalanceAfterText != "0.5" {
		t.Fatalf("expected B-02 balance 0.5 after transfer, got %s", txXfer.BalanceAfterText)
	}

	// 5. ADJUST (TARGET mode): physical count at A-01 is 1.100 kg (delta = -0.150 kg)
	txAdj, err := led.RecordMovement(ctx, MovementInput{
		ItemID:       item.ID,
		TxType:       TxAdjust,
		AdjustMode:   AdjustModeTarget,
		Quantity:     "1.1",
		ToLocationID: &binA.ID,
		Reason:       "Weekly scale audit",
		Operator:     "Barin",
	})
	if err != nil {
		t.Fatalf("RecordMovement ADJUST failed: %v", err)
	}
	if txAdj.DeltaText != "-0.15" || txAdj.BalanceAfterText != "1.1" {
		t.Fatalf("unexpected ADJUST values: delta=%s balance=%s", txAdj.DeltaText, txAdj.BalanceAfterText)
	}

	// 6. Undo the ADJUST transaction -> A-01 must return to 1.250 kg
	revAdj, err := led.UndoTransaction(ctx, txAdj.ID, "Barin")
	if err != nil {
		t.Fatalf("UndoTransaction(ADJUST) failed: %v", err)
	}
	if revAdj.BalanceAfterText != "1.25" || revAdj.ReferenceTxID == nil || *revAdj.ReferenceTxID != txAdj.ID {
		t.Fatalf("unexpected ADJUST undo result: %+v", revAdj)
	}

	// Double-undo must be rejected with ErrAlreadyUndone
	if _, err := led.UndoTransaction(ctx, txAdj.ID, "Barin"); !errors.Is(err, apperr.ErrAlreadyUndone) {
		t.Fatalf("expected ErrAlreadyUndone on double undo, got %v", err)
	}

	// 7. Undo the TRANSFER transaction -> A-01 returns to 1.750 kg, B-02 returns to 0 kg
	if _, err := led.UndoTransaction(ctx, txXfer.ID, "Barin"); err != nil {
		t.Fatalf("UndoTransaction(TRANSFER) failed: %v", err)
	}

	// Verify final total stock is 1.75 kg
	finalItem, err := cat.GetItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItemByID failed: %v", err)
	}
	if finalItem.TotalStockText != "1.75" {
		t.Fatalf("expected final total stock 1.75 kg, got %s", finalItem.TotalStockText)
	}

	// 8. ReconcileLedger must report Balanced == true with 0 mismatches
	rep, err := led.ReconcileLedger(ctx, false)
	if err != nil {
		t.Fatalf("ReconcileLedger failed: %v", err)
	}
	if !rep.Balanced || rep.MismatchCount != 0 {
		t.Fatalf("expected Balanced=true with 0 mismatches, got %+v", rep)
	}

	// 9. Simulate an external corruption in stock_levels and verify ReconcileLedger detects & repairs it
	if _, err := store.DB().ExecContext(
		ctx,
		`UPDATE stock_levels SET quantity = 99, quantity_millis = 99000 WHERE item_id = ? AND location_id = ?;`,
		item.ID, binA.ID,
	); err != nil {
		t.Fatalf("corrupt stock_levels failed: %v", err)
	}

	detectRep, err := led.ReconcileLedger(ctx, false)
	if err != nil {
		t.Fatalf("ReconcileLedger(detect) failed: %v", err)
	}
	if detectRep.Balanced || detectRep.MismatchCount != 1 {
		t.Fatalf("expected 1 detected mismatch, got %+v", detectRep)
	}
	if detectRep.Mismatches[0].ExpectedText != "1.75" || detectRep.Mismatches[0].ActualText != "99" {
		t.Errorf("unexpected mismatch details: %+v", detectRep.Mismatches[0])
	}

	repairRep, err := led.ReconcileLedger(ctx, true)
	if err != nil {
		t.Fatalf("ReconcileLedger(repair) failed: %v", err)
	}
	if !repairRep.Balanced || repairRep.RepairedCount != 1 {
		t.Fatalf("expected repaired report Balanced=true RepairedCount=1, got %+v", repairRep)
	}

	healedItem, _ := cat.GetItemByID(ctx, item.ID)
	if healedItem.TotalStockText != "1.75" {
		t.Errorf("expected healed total stock 1.75, got %s", healedItem.TotalStockText)
	}
}

func TestQuickScanWorkflowsAndBatchUndo(t *testing.T) {
	ctx := context.Background()
	_, cat, led := setupTestLedger(t)

	binA, _ := cat.CreateLocation(ctx, catalog.LocationInput{Code: "RACK-1", Name: "Rack 1"})
	item, err := cat.CreateItem(ctx, models.ItemInput{
		SKU:               "ESP32-S3-DEV",
		Barcode:           "8690009990001",
		Name:              "ESP32-S3 DevKitC",
		Unit:              "pcs",
		DefaultLocationID: &binA.ID,
	})
	if err != nil {
		t.Fatalf("CreateItem failed: %v", err)
	}
	if _, err := cat.AddItemAlias(ctx, item.ID, "MOUSER-ESP32S3", models.AliasSupplierPart, "Mouser label"); err != nil {
		t.Fatalf("AddItemAlias failed: %v", err)
	}

	// 1. Scanning a location sticker switches the active location hands-free
	locRes, err := led.QuickScan(ctx, QuickScanInput{Code: "LOC:RACK-1", Mode: ScanModeIn})
	if err != nil {
		t.Fatalf("QuickScan(LOC:RACK-1) failed: %v", err)
	}
	if locRes.Action != ScanActionLocationSwitched || locRes.SwitchedLocation == nil || locRes.SwitchedLocation.ID != binA.ID {
		t.Fatalf("expected LOCATION_SWITCHED to RACK-1, got %+v", locRes)
	}

	// 2. Scanning distributor alias in IN mode with preset batch quantity 10
	inRes, err := led.QuickScan(ctx, QuickScanInput{
		Code:       "MOUSER-ESP32S3",
		Mode:       ScanModeIn,
		Quantity:   "10",
		LocationID: &binA.ID,
		Operator:   "Tech-1",
	})
	if err != nil {
		t.Fatalf("QuickScan IN failed: %v", err)
	}
	if inRes.Action != ScanActionMovementRecorded || inRes.MatchSource != "ALIAS:SUPPLIER_PART" || inRes.Item.TotalStockText != "10" {
		t.Fatalf("unexpected QuickScan IN result: %+v", inRes)
	}

	// 3. Scanning primary barcode in LOOKUP mode
	lookupRes, err := led.QuickScan(ctx, QuickScanInput{Code: "8690009990001", Mode: ScanModeLookup})
	if err != nil {
		t.Fatalf("QuickScan LOOKUP failed: %v", err)
	}
	if lookupRes.Action != ScanActionItemLookup || len(lookupRes.RecentTransactions) != 1 {
		t.Fatalf("unexpected LOOKUP result: %+v", lookupRes)
	}

	// 4. Scanning unknown barcode returns UNKNOWN_BARCODE with SuggestedSKU
	unkRes, err := led.QuickScan(ctx, QuickScanInput{Code: "9998887776665", Mode: ScanModeIn})
	if err != nil {
		t.Fatalf("QuickScan UNKNOWN failed: %v", err)
	}
	if unkRes.Action != ScanActionUnknownBarcode || !unkRes.UnknownBarcode || unkRes.SuggestedSKU == "" {
		t.Fatalf("expected UNKNOWN_BARCODE with SuggestedSKU, got %+v", unkRes)
	}

	// 5. Batch movement + UndoBatch
	_, err = led.RecordMovement(ctx, MovementInput{
		ItemID:   item.ID,
		TxType:   TxOut,
		Quantity: "2",
		BatchID:  "BATCH-REPAIR-101",
		Reason:   "Job #101 part 1",
	})
	if err != nil {
		t.Fatalf("batch tx 1 failed: %v", err)
	}
	_, err = led.RecordMovement(ctx, MovementInput{
		ItemID:   item.ID,
		TxType:   TxOut,
		Quantity: "3",
		BatchID:  "BATCH-REPAIR-101",
		Reason:   "Job #101 part 2",
	})
	if err != nil {
		t.Fatalf("batch tx 2 failed: %v", err)
	}

	reversals, err := led.UndoBatch(ctx, "BATCH-REPAIR-101", "Tech-1")
	if err != nil || len(reversals) != 2 {
		t.Fatalf("expected 2 batch reversals, got len=%d err=%v", len(reversals), err)
	}

	afterBatchUndo, _ := cat.GetItemByID(ctx, item.ID)
	if afterBatchUndo.TotalStockText != "10" {
		t.Errorf("expected stock restored to 10 after UndoBatch, got %s", afterBatchUndo.TotalStockText)
	}
}

func TestConcurrentStockMovementsPreserveLedgerInvariant(t *testing.T) {
	ctx := context.Background()
	_, cat, led := setupTestLedger(t)

	item, err := cat.CreateItem(ctx, models.ItemInput{
		SKU:             "BOLT-M3X8",
		Name:            "M3x8 Hex Bolt",
		OpeningQuantity: "100",
	})
	if err != nil {
		t.Fatalf("CreateItem failed: %v", err)
	}

	const workers = 20
	var wg sync.WaitGroup
	errCh := make(chan error, workers*2)

	// 20 concurrent IN (+2) and 20 concurrent OUT (-1) -> net +20 -> final stock must be exactly 120
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := led.RecordMovement(ctx, MovementInput{ItemID: item.ID, TxType: TxIn, Quantity: "2"}); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := led.RecordMovement(ctx, MovementInput{ItemID: item.ID, TxType: TxOut, Quantity: "1"}); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent movement error: %v", err)
	}

	updated, err := cat.GetItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItemByID failed: %v", err)
	}
	if updated.TotalStockText != "120" {
		t.Fatalf("expected final stock 120 after concurrent ops, got %s", updated.TotalStockText)
	}

	rep, err := led.ReconcileLedger(ctx, false)
	if err != nil || !rep.Balanced {
		t.Fatalf("expected balanced reconciliation after concurrent ops, got %+v (err=%v)", rep, err)
	}
}
