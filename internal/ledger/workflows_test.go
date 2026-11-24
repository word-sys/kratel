package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/models"
)

func TestBOMKitExecutionQuickScanAndBatchUndo(t *testing.T) {
	ctx := context.Background()
	_, cat, led := setupTestLedger(t)

	esp, _ := cat.CreateItem(ctx, models.ItemInput{
		SKU: "MCU-ESP32", Name: "ESP32-S3 Board", UnitCost: "6.50", OpeningQuantity: "10",
	})
	res, _ := cat.CreateItem(ctx, models.ItemInput{
		SKU: "RES-10K", Name: "10k Resistor", UnitCost: "0.05", OpeningQuantity: "100",
	})
	usb, _ := cat.CreateItem(ctx, models.ItemInput{
		SKU: "CONN-USBC", Name: "USB-C Socket", UnitCost: "1.00", OpeningQuantity: "5",
	})

	kit, err := led.CreateKit(ctx, KitInput{
		Code:        "ESP32-BENCH",
		Name:        "ESP32 Sensor Node BOM",
		Description: "1x ESP32, 4x 10k Resistor, 1x USB-C",
		Category:    "Assemblies",
		Components: []KitComponentInput{
			{ItemID: esp.ID, Quantity: "1"},
			{ItemID: res.ID, Quantity: "4"},
			{ItemID: usb.ID, Quantity: "1"},
		},
	})
	if err != nil {
		t.Fatalf("CreateKit failed: %v", err)
	}
	// Bottleneck is USB-C (5 available / 1 per kit => 5 buildable)
	if kit.MaxBuildable != 5 {
		t.Fatalf("expected MaxBuildable=5, got %d", kit.MaxBuildable)
	}
	// Cost = 6.50 + (4 * 0.05 = 0.20) + 1.00 = 7.70
	if kit.TotalCostText != "7.70" {
		t.Fatalf("expected TotalCostText '7.70', got %q", kit.TotalCostText)
	}

	// Execute 2 kits via QuickScan using the kit barcode ("KIT:ESP32-BENCH")
	scanRes, err := led.QuickScan(ctx, QuickScanInput{
		Code:     kit.Barcode,
		Mode:     ScanModeOut,
		Quantity: "2",
		Operator: "Barin",
	})
	if err != nil {
		t.Fatalf("QuickScan(kit) failed: %v", err)
	}
	if scanRes.Action != ScanActionKitExecuted || scanRes.UndoBatchID == "" {
		t.Fatalf("unexpected kit scan result: %+v", scanRes)
	}

	espAfter, _ := cat.GetItemByID(ctx, esp.ID)
	resAfter, _ := cat.GetItemByID(ctx, res.ID)
	usbAfter, _ := cat.GetItemByID(ctx, usb.ID)
	if espAfter.TotalStockText != "8" || resAfter.TotalStockText != "92" || usbAfter.TotalStockText != "3" {
		t.Fatalf("unexpected balances after 2x kit OUT: esp=%s res=%s usb=%s",
			espAfter.TotalStockText, resAfter.TotalStockText, usbAfter.TotalStockText)
	}

	// One-click Batch Undo restores all 3 components atomically
	reversals, err := led.UndoBatch(ctx, scanRes.UndoBatchID, "Barin")
	if err != nil || len(reversals) != 3 {
		t.Fatalf("UndoBatch failed: len=%d err=%v", len(reversals), err)
	}
	espRestored, _ := cat.GetItemByID(ctx, esp.ID)
	if espRestored.TotalStockText != "10" {
		t.Errorf("expected ESP32 stock restored to 10, got %s", espRestored.TotalStockText)
	}

	rep, err := led.ReconcileLedger(ctx, false)
	if err != nil || !rep.Balanced {
		t.Fatalf("expected balanced reconciliation after kit & undo, got %+v", rep)
	}
}

func TestPhysicalCycleCountStocktakeSession(t *testing.T) {
	ctx := context.Background()
	_, cat, led := setupTestLedger(t)

	bin, _ := cat.CreateLocation(ctx, catalog.LocationInput{Code: "SHELF-1", Name: "Shelf 1"})
	itemA, _ := cat.CreateItem(ctx, models.ItemInput{
		SKU: "PART-A", Barcode: "BAR-A", Name: "Part A", UnitCost: "2.00",
		OpeningQuantity: "10", OpeningLocationID: &bin.ID,
	})
	itemB, _ := cat.CreateItem(ctx, models.ItemInput{
		SKU: "PART-B", Barcode: "BAR-B", Name: "Part B", UnitCost: "5.00",
	})

	sess, err := led.CreateAuditSession(ctx, AuditSessionInput{
		Name:       "Shelf 1 Monthly Audit",
		LocationID: &bin.ID,
		Operator:   "Barin",
	})
	if err != nil {
		t.Fatalf("CreateAuditSession failed: %v", err)
	}
	if sess.TotalLines != 1 || sess.Status != "OPEN" {
		t.Fatalf("expected 1 seeded line and OPEN status, got %+v", sess)
	}

	// Scan PART-A 3 times in increment mode -> counted = 3 (variance = -7)
	for i := 0; i < 3; i++ {
		if _, err := led.RecordAuditCount(ctx, sess.ID, AuditCountInput{
			Code:          "BAR-A",
			Quantity:      "1",
			IncrementMode: true,
		}); err != nil {
			t.Fatalf("RecordAuditCount increment %d failed: %v", i, err)
		}
	}

	// Discover unexpected PART-B in Shelf 1 with direct count = 4 (expected 0 -> variance = +4)
	lineB, err := led.RecordAuditCount(ctx, sess.ID, AuditCountInput{
		ItemID:        itemB.ID,
		Quantity:      "4",
		IncrementMode: false,
		Notes:         "Found in back of shelf",
	})
	if err != nil {
		t.Fatalf("RecordAuditCount unexpected item failed: %v", err)
	}
	if lineB.VarianceText != "+4" {
		t.Errorf("expected variance '+4', got %q", lineB.VarianceText)
	}

	commitRes, err := led.CommitAuditSession(ctx, sess.ID, "Barin")
	if err != nil {
		t.Fatalf("CommitAuditSession failed: %v", err)
	}
	if commitRes.AdjustmentsCount != 2 || commitRes.Session.Status != "COMMITTED" {
		t.Fatalf("expected 2 ADJUST transactions and COMMITTED status, got %+v", commitRes)
	}

	afterA, _ := cat.GetItemByID(ctx, itemA.ID)
	afterB, _ := cat.GetItemByID(ctx, itemB.ID)
	if afterA.TotalStockText != "3" || afterB.TotalStockText != "4" {
		t.Fatalf("expected post-audit balances A=3 B=4, got A=%s B=%s", afterA.TotalStockText, afterB.TotalStockText)
	}

	rep, err := led.ReconcileLedger(ctx, false)
	if err != nil || !rep.Balanced {
		t.Fatalf("expected balanced reconciliation after stocktake commit, got %+v", rep)
	}
}

func TestToolLoanCheckOutOverdueAndReturn(t *testing.T) {
	ctx := context.Background()
	_, cat, led := setupTestLedger(t)

	scope, _ := cat.CreateItem(ctx, models.ItemInput{
		SKU: "TOOL-SCOPE-1", Barcode: "TOOL-001", Name: "Rigol 100MHz Oscilloscope",
		OpeningQuantity: "2",
	})

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	loan, err := led.CheckOutLoan(ctx, LoanCheckOutInput{
		Code:     "TOOL-001",
		Quantity: "1",
		Borrower: "Bench 3 (Ahmet)",
		DueAt:    yesterday,
		Operator: "Barin",
		Notes:    "Debugging power supply ripple",
	})
	if err != nil {
		t.Fatalf("CheckOutLoan failed: %v", err)
	}
	if loan.Status != "ACTIVE" || !loan.IsOverdue {
		t.Fatalf("expected ACTIVE overdue loan, got %+v", loan)
	}

	midScope, _ := cat.GetItemByID(ctx, scope.ID)
	if midScope.TotalStockText != "1" {
		t.Fatalf("expected 1 oscilloscope remaining while checked out, got %s", midScope.TotalStockText)
	}

	returned, err := led.ReturnLoan(ctx, loan.ID, nil, "Barin", "Returned with probes intact")
	if err != nil {
		t.Fatalf("ReturnLoan failed: %v", err)
	}
	if returned.Status != "RETURNED" || returned.IsOverdue || returned.InTxID == nil {
		t.Fatalf("unexpected returned loan state: %+v", returned)
	}

	finalScope, _ := cat.GetItemByID(ctx, scope.ID)
	if finalScope.TotalStockText != "2" {
		t.Fatalf("expected 2 oscilloscopes in stock after return, got %s", finalScope.TotalStockText)
	}

	rep, err := led.ReconcileLedger(ctx, false)
	if err != nil || !rep.Balanced {
		t.Fatalf("expected balanced reconciliation after loan check-out and return, got %+v", rep)
	}
}
