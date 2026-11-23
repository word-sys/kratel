package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
)

func setupTestCatalog(t *testing.T) (*db.Store, *Service) {
	t.Helper()
	store, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("db.Open(:memory:) failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store, NewService(store)
}

func TestItemCRUDOpeningStockAliasesAndAttributes(t *testing.T) {
	ctx := context.Background()
	store, svc := setupTestCatalog(t)

	// 1. Create a supplier and a shelf location
	sup, err := svc.CreateSupplier(ctx, SupplierInput{
		Code:    "SUP-MOUSER",
		Name:    "Mouser Electronics",
		Contact: "Desk A",
		Email:   "orders@mouser.example",
		Phone:   "+1-800-555-0100",
	})
	if err != nil {
		t.Fatalf("CreateSupplier failed: %v", err)
	}

	loc, err := svc.CreateLocation(ctx, LocationInput{
		Code:  "A-01-B2",
		Name:  "Bin A-01-B2",
		Notes: "SMD Passives",
	})
	if err != nil {
		t.Fatalf("CreateLocation failed: %v", err)
	}

	// 2. Create item with opening stock (12.5 meters), custom attributes, and near-term expiry date
	soonDate := time.Now().UTC().AddDate(0, 0, 10).Format("2006-01-02")
	item, err := svc.CreateItem(ctx, models.ItemInput{
		SKU:               "WIRE-SIL-18AWG",
		Barcode:           "8691234560001",
		Name:              "Silicone Hookup Wire 18AWG Red",
		Description:       "High-flex tinned copper silicone wire",
		Category:          "Cables & Wire",
		Unit:              "m",
		MinStock:          "20",
		UnitCost:          "1.40",
		SupplierID:        &sup.ID,
		DefaultLocationID: &loc.ID,
		LotNumber:         "LOT-W18",
		ExpiryDate:        soonDate,
		Attributes: map[string]string{
			"gauge":   "18AWG",
			"color":   "Red",
			"tempMax": "200C",
		},
		OpeningQuantity:   "12.5",
		OpeningLocationID: &loc.ID,
		Operator:          "Barin",
	})
	if err != nil {
		t.Fatalf("CreateItem failed: %v", err)
	}

	if item.TotalStockMillis != 12500 || item.TotalStockText != "12.5" {
		t.Fatalf("expected opening stock 12.5 (12500 Millis), got %s (%d)", item.TotalStockText, item.TotalStockMillis)
	}
	// 12.5m * 1.40 = 17.50 (17500 Millis)
	if item.ValuationMillis != 17500 || item.ValuationText != "17.50" {
		t.Fatalf("expected valuation 17.50 (17500 Millis), got %s (%d)", item.ValuationText, item.ValuationMillis)
	}
	if !item.IsLowStock {
		t.Fatal("expected IsLowStock=true since 12.5 <= min_stock 20")
	}
	if item.ExpiryStatus != "EXPIRING_SOON" {
		t.Fatalf("expected ExpiryStatus 'EXPIRING_SOON', got %q", item.ExpiryStatus)
	}
	if item.Attributes["gauge"] != "18AWG" {
		t.Fatalf("expected custom attribute gauge=18AWG, got %+v", item.Attributes)
	}

	// 3. Duplicate SKU or Barcode must be rejected with conflict codes
	_, err = svc.CreateItem(ctx, models.ItemInput{
		SKU:  "wire-sil-18awg", // case-insensitive collision
		Name: "Duplicate SKU Test",
	})
	if !errors.Is(err, apperr.ErrDuplicateSKU) {
		t.Fatalf("expected ErrDuplicateSKU, got %v", err)
	}

	_, err = svc.CreateItem(ctx, models.ItemInput{
		SKU:     "UNIQUE-SKU-2",
		Barcode: "8691234560001",
		Name:    "Duplicate Barcode Test",
	})
	if !errors.Is(err, apperr.ErrDuplicateBarcode) {
		t.Fatalf("expected ErrDuplicateBarcode, got %v", err)
	}

	// 4. Register distributor alias (Mouser part number) and resolve by SKU, Barcode, and Alias
	alias, err := svc.AddItemAlias(ctx, item.ID, "534-18AWG-RED", models.AliasSupplierPart, "Mouser reel code")
	if err != nil {
		t.Fatalf("AddItemAlias failed: %v", err)
	}
	if alias.Code != "534-18AWG-RED" {
		t.Fatalf("unexpected alias code: %+v", alias)
	}

	for _, lookup := range []struct {
		code       string
		wantSource string
	}{
		{code: "WIRE-SIL-18AWG", wantSource: "SKU"},
		{code: "8691234560001", wantSource: "BARCODE"},
		{code: "534-18AWG-RED", wantSource: "ALIAS:SUPPLIER_PART"},
	} {
		resolved, src, err := svc.ResolveItemByCode(ctx, lookup.code)
		if err != nil {
			t.Fatalf("ResolveItemByCode(%q) failed: %v", lookup.code, err)
		}
		if resolved.ID != item.ID || src != lookup.wantSource {
			t.Errorf("ResolveItemByCode(%q) = (id=%d, src=%q), want (id=%d, src=%q)",
				lookup.code, resolved.ID, src, item.ID, lookup.wantSource)
		}
	}

	// 5. AssignInternalBarcode on an item that already has a primary barcode adds an INTERNAL_QR alias
	withInternal, err := svc.AssignInternalBarcode(ctx, item.ID)
	if err != nil {
		t.Fatalf("AssignInternalBarcode failed: %v", err)
	}
	if len(withInternal.Aliases) != 2 {
		t.Fatalf("expected 2 aliases after AssignInternalBarcode, got %d", len(withInternal.Aliases))
	}

	// 6. Test FTS5 search, custom attribute filter, low-stock reorder queue, and expiring items
	results, total, err := svc.ListItems(ctx, ItemFilter{
		Query:          "silicone copper",
		AttributeKey:   "gauge",
		AttributeValue: "18awg",
		LowStockOnly:   true,
	})
	if err != nil || total != 1 || len(results) != 1 {
		t.Fatalf("ListItems FTS5 + attribute filter failed: total=%d len=%d err=%v", total, len(results), err)
	}

	facets, err := DiscoverAttributeKeys(ctx, store.DB())
	if err != nil || len(facets) != 3 {
		t.Fatalf("expected 3 discovered attribute facets, got %d (err=%v)", len(facets), err)
	}

	reorderGroups, err := svc.BuildSupplierReorderGroups(ctx)
	if err != nil || len(reorderGroups) != 1 {
		t.Fatalf("expected 1 supplier reorder group, got %d (err=%v)", len(reorderGroups), err)
	}
	// min_stock=20, current=12.5 -> deficit=7.5, suggested=2*20-12.5=27.5m @ 1.40 = 38.50
	rg := reorderGroups[0]
	if rg.SupplierName != "Mouser Electronics" || len(rg.Items) != 1 {
		t.Fatalf("unexpected reorder group: %+v", rg)
	}
	if rg.Items[0].DeficitText != "7.5" || rg.Items[0].SuggestedOrderText != "27.5" || rg.EstimatedCostText != "38.50" {
		t.Errorf("unexpected reorder math: deficit=%s suggested=%s cost=%s",
			rg.Items[0].DeficitText, rg.Items[0].SuggestedOrderText, rg.EstimatedCostText)
	}

	expiring, err := svc.ListExpiringItems(ctx, 30)
	if err != nil || len(expiring) != 1 {
		t.Fatalf("expected 1 expiring item, got %d (err=%v)", len(expiring), err)
	}

	// 7. DeleteItem must refuse to hard-delete an item with opening ledger history
	if err := svc.DeleteItem(ctx, item.ID); !errors.Is(err, apperr.ErrItemHasTransactions) {
		t.Fatalf("expected ErrItemHasTransactions when deleting item with ledger history, got %v", err)
	}

	// Create a second item with zero transactions and verify it can be assigned a primary barcode and deleted
	tempItem, err := svc.CreateItem(ctx, models.ItemInput{
		Name:     "Loose M3 Washer",
		Category: "Fasteners",
	})
	if err != nil {
		t.Fatalf("CreateItem(Loose M3 Washer) failed: %v", err)
	}
	if !strings.HasPrefix(tempItem.SKU, "FAS-") {
		t.Errorf("expected auto-generated SKU prefix 'FAS-', got %q", tempItem.SKU)
	}
	tempItem, err = svc.AssignInternalBarcode(ctx, tempItem.ID)
	if err != nil || !strings.HasPrefix(tempItem.Barcode, "KRT-") {
		t.Fatalf("expected primary KRT- barcode assigned, got %q (err=%v)", tempItem.Barcode, err)
	}
	if err := svc.DeleteItem(ctx, tempItem.ID); err != nil {
		t.Fatalf("expected clean DeleteItem on item with zero transactions, got %v", err)
	}
}

func TestHierarchicalLocationsCycleDetectionAndTreeRollup(t *testing.T) {
	ctx := context.Background()
	_, svc := setupTestCatalog(t)

	room, err := svc.CreateLocation(ctx, LocationInput{Code: "LAB-1", Name: "Electronics Lab 1"})
	if err != nil {
		t.Fatalf("CreateLocation(LAB-1) failed: %v", err)
	}
	rack, err := svc.CreateLocation(ctx, LocationInput{Code: "RACK-A", Name: "Rack A", ParentID: &room.ID})
	if err != nil {
		t.Fatalf("CreateLocation(RACK-A) failed: %v", err)
	}
	bin, err := svc.CreateLocation(ctx, LocationInput{Code: "A-01", Name: "Bin A-01", ParentID: &rack.ID})
	if err != nil {
		t.Fatalf("CreateLocation(A-01) failed: %v", err)
	}

	if bin.Breadcrumb != "Electronics Lab 1 ▸ Rack A ▸ Bin A-01" {
		t.Errorf("unexpected breadcrumb: %q", bin.Breadcrumb)
	}
	if bin.Depth != 2 {
		t.Errorf("expected depth 2, got %d", bin.Depth)
	}

	// Verify LOC:A-01 barcode lookup
	byScan, err := svc.GetLocationByCode(ctx, "LOC:a-01")
	if err != nil || byScan.ID != bin.ID {
		t.Fatalf("GetLocationByCode(LOC:a-01) failed: %v", err)
	}

	// Cycle test 1: cannot set room's parent to itself
	_, err = svc.UpdateLocation(ctx, room.ID, LocationInput{Code: room.Code, Name: room.Name, ParentID: &room.ID})
	if !errors.Is(err, apperr.ErrLocationCycle) {
		t.Fatalf("expected ErrLocationCycle on self-parent, got %v", err)
	}

	// Cycle test 2: cannot set room's parent to its grand-child bin
	_, err = svc.UpdateLocation(ctx, room.ID, LocationInput{Code: room.Code, Name: room.Name, ParentID: &bin.ID})
	if !errors.Is(err, apperr.ErrLocationCycle) {
		t.Fatalf("expected ErrLocationCycle on descendant cycle, got %v", err)
	}

	// Stock an item in bin and verify BuildLocationTree rolls up stock to rack and room
	_, err = svc.CreateItem(ctx, models.ItemInput{
		SKU:               "CAP-100NF",
		Name:              "100nF Ceramic Capacitor",
		OpeningQuantity:   "250",
		OpeningLocationID: &bin.ID,
	})
	if err != nil {
		t.Fatalf("CreateItem in bin failed: %v", err)
	}

	tree, err := svc.BuildLocationTree(ctx)
	if err != nil {
		t.Fatalf("BuildLocationTree failed: %v", err)
	}
	var labNode *models.LocationTreeNode
	for _, root := range tree {
		if root.ID == room.ID {
			labNode = root
			break
		}
	}
	if labNode == nil {
		t.Fatal("failed to find LAB-1 root node in tree")
	}
	if labNode.SubtreeItems != 1 || labNode.SubtreeStockText != "250" {
		t.Errorf("expected LAB-1 subtree rollup (1 item, 250 units), got (%d items, %s units)",
			labNode.SubtreeItems, labNode.SubtreeStockText)
	}

	// DeleteLocation must refuse to delete room while it has child locations
	if err := svc.DeleteLocation(ctx, room.ID); !errors.Is(err, apperr.ErrLocationInUse) {
		t.Fatalf("expected ErrLocationInUse when deleting parent location, got %v", err)
	}
}
