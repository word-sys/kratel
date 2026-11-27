package demo

import (
	"context"
	"testing"

	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/ledger"
)

func TestWorkshopDemoSeedIdempotencyAndLedgerBalance(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("db.Open(:memory:) failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	res, err := SeedWorkshopDemo(ctx, store, false)
	if err != nil {
		t.Fatalf("SeedWorkshopDemo failed: %v", err)
	}
	if res.AlreadySeeded || res.ItemsCreated != 18 || res.SuppliersCreated != 3 || res.KitsCreated != 2 || res.LoansCreated != 2 {
		t.Fatalf("unexpected demo seed counts: %+v", res)
	}

	// Second call must be a no-op
	res2, err := SeedWorkshopDemo(ctx, store, false)
	if err != nil {
		t.Fatalf("second SeedWorkshopDemo failed: %v", err)
	}
	if !res2.AlreadySeeded {
		t.Fatalf("expected AlreadySeeded=true on second run, got %+v", res2)
	}

	cat := catalog.NewService(store)
	led := ledger.NewService(store, cat)

	// Verify low-stock queue, expiring items, hierarchical tree, kits, loans, and open audit session
	reorder, err := cat.ListLowStockReorderQueue(ctx)
	if err != nil || len(reorder) < 4 {
		t.Fatalf("expected >=4 low-stock items in demo queue, got %d (err=%v)", len(reorder), err)
	}

	expiring, err := cat.ListExpiringItems(ctx, 30)
	if err != nil || len(expiring) != 2 {
		t.Fatalf("expected 2 expiring/expired demo items, got %d (err=%v)", len(expiring), err)
	}

	tree, err := cat.BuildLocationTree(ctx)
	if err != nil || len(tree) < 2 {
		t.Fatalf("expected multi-root location tree, got %d (err=%v)", len(tree), err)
	}

	kits, err := led.ListKits(ctx)
	if err != nil || len(kits) != 2 {
		t.Fatalf("expected 2 demo kits, got %d (err=%v)", len(kits), err)
	}

	loans, err := led.ListLoans(ctx, "ACTIVE")
	if err != nil || len(loans) != 2 {
		t.Fatalf("expected 2 active demo loans, got %d (err=%v)", len(loans), err)
	}

	// Critical invariant: every demo movement went through the ledger engine, so ReconcileLedger must be 100% balanced
	rep, err := led.ReconcileLedger(ctx, false)
	if err != nil {
		t.Fatalf("ReconcileLedger failed: %v", err)
	}
	if !rep.Balanced || rep.MismatchCount != 0 {
		t.Fatalf("expected demo ledger to be 100%% balanced, got %+v", rep)
	}
}
