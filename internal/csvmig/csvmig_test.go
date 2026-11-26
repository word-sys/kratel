package csvmig

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/ledger"
)

func setupTestCSVMig(t *testing.T) (*db.Store, *catalog.Service, *ledger.Service, *Service) {
	t.Helper()
	store, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("db.Open(:memory:) failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	cat := catalog.NewService(store)
	led := ledger.NewService(store, cat)
	mig := NewService(store, cat)
	return store, cat, led, mig
}

func TestDelimiterDetectionAndTurkishHeaderMapping(t *testing.T) {
	// Turkish Excel export: UTF-8 BOM + semicolon delimiter + comma decimals + extra custom columns
	turkishCSV := append([]byte{0xEF, 0xBB, 0xBF}, []byte(
		"Stok Kodu;Barkod;Ürün Adı;Kategori;Birim;Miktar;Kritik Stok;Birim Fiyat;Konum;Tedarikçi;Voltaj;Paket\n"+
			"RES-10K-0805;8690001112223;10kΩ Direnç;Pasifler;pcs;250;500;0,05 ₺;RACK-A1;Anadolu Elektronik;50V;0805\n"+
			"KABLO-18AWG;8690001112224;Silikon Kablo;Kablolar;m;12,5;20;4,20 TL;RACK-B2;Anadolu Elektronik;600V;18AWG\n",
	)...)

	prev, err := AnalyzeCSV(turkishCSV, 0)
	if err != nil {
		t.Fatalf("AnalyzeCSV failed: %v", err)
	}
	if prev.DetectedDelimiter != ";" {
		t.Fatalf("expected ';' delimiter, got %q", prev.DetectedDelimiter)
	}
	if prev.TotalRows != 2 {
		t.Fatalf("expected 2 data rows, got %d", prev.TotalRows)
	}

	wantMap := map[string]string{
		"Stok Kodu":   FieldSKU,
		"Barkod":      FieldBarcode,
		"Ürün Adı":    FieldName,
		"Kategori":    FieldCategory,
		"Birim":       FieldUnit,
		"Miktar":      FieldOpeningQty,
		"Kritik Stok": FieldMinStock,
		"Birim Fiyat": FieldUnitCost,
		"Konum":       FieldLocationCode,
		"Tedarikçi":   FieldSupplierName,
	}
	for col, want := range wantMap {
		if got := prev.SuggestedMapping[col]; got != want {
			t.Errorf("SuggestedMapping[%q] = %q, want %q", col, got, want)
		}
	}

	// Verify Excel/Sheets Ctrl+V clipboard TSV detection
	tsvData := []byte("Part #\tName\tQty\tBin\nM3-BOLT\tM3x8 Hex Bolt\t100\tBIN-C1\n")
	tsvPrev, err := AnalyzeCSV(tsvData, 0)
	if err != nil {
		t.Fatalf("AnalyzeCSV(TSV) failed: %v", err)
	}
	if tsvPrev.DetectedDelimiter != "\t" {
		t.Fatalf("expected tab delimiter for clipboard TSV, got %q", tsvPrev.DetectedDelimiter)
	}
}

func TestDryRunRollbackCommitDuplicatePoliciesAndBOMExports(t *testing.T) {
	ctx := context.Background()
	_, cat, led, mig := setupTestCSVMig(t)

	csvPayload := append([]byte{0xEF, 0xBB, 0xBF}, []byte(
		"Stok Kodu;Barkod;Supplier Part;Ürün Adı;Kategori;Birim;Miktar;Kritik Stok;Birim Fiyat;Konum;Tedarikçi;Voltaj\n"+
			"MCU-ESP32;8690001001;MOUSER-ESP32;ESP32-S3 Geliştirme Kartı;MCU;pcs;4;10;6,50;LAB-A1;Bosphorus Tech;3.3V\n"+
			"WIRE-RED;8690001002;MOUSER-W18;Silikon Kablo 18AWG;Kablo;m;25,5;10;1,40;LAB-B2;Bosphorus Tech;600V\n",
	)...)

	// 1. Dry-Run must report exact creation counts while mutating ZERO database rows
	dryRes, err := mig.ExecuteImport(ctx, ImportRequest{
		CSVData:                 csvPayload,
		MapUnmappedToAttributes: true,
		DuplicatePolicy:         PolicySkip,
		DryRun:                  true,
		Operator:                "Barin",
	})
	if err != nil {
		t.Fatalf("ExecuteImport(DryRun=true) failed: %v", err)
	}
	if !dryRes.DryRun || dryRes.ItemsCreated != 2 || dryRes.LocationsCreated != 2 || dryRes.SuppliersCreated != 1 || dryRes.OpeningTxCount != 2 {
		t.Fatalf("unexpected dry-run counts: %+v", dryRes)
	}

	itemsAfterDry, totalAfterDry, err := cat.ListItems(ctx, catalog.ItemFilter{})
	if err != nil || totalAfterDry != 0 || len(itemsAfterDry) != 0 {
		t.Fatalf("expected 0 items after DryRun rollback, got %d (err=%v)", totalAfterDry, err)
	}

	// 2. Live Commit imports items, custom JSON attributes, distributor aliases, locations, suppliers, and opening IN txs
	commitRes, err := mig.ExecuteImport(ctx, ImportRequest{
		CSVData:                 csvPayload,
		MapUnmappedToAttributes: true,
		DuplicatePolicy:         PolicySkip,
		DryRun:                  false,
		Operator:                "Barin",
	})
	if err != nil {
		t.Fatalf("ExecuteImport(DryRun=false) failed: %v", err)
	}
	if commitRes.ItemsCreated != 2 || commitRes.AliasesCreated != 2 || commitRes.BatchID == "" {
		t.Fatalf("unexpected commit result: %+v", commitRes)
	}

	// Verify item resolved by Mouser alias and custom attribute Voltaj=3.3V preserved
	esp, matchSrc, err := cat.ResolveItemByCode(ctx, "MOUSER-ESP32")
	if err != nil {
		t.Fatalf("ResolveItemByCode(MOUSER-ESP32) failed: %v", err)
	}
	if matchSrc != "ALIAS:SUPPLIER_PART" || esp.TotalStockText != "4" || esp.Attributes["Voltaj"] != "3.3V" {
		t.Fatalf("unexpected imported item state: src=%s stock=%s attrs=%+v", matchSrc, esp.TotalStockText, esp.Attributes)
	}

	// 3. Re-import with PolicySkip skips both rows
	skipRes, err := mig.ExecuteImport(ctx, ImportRequest{
		CSVData:         csvPayload,
		DuplicatePolicy: PolicySkip,
	})
	if err != nil || skipRes.ItemsSkipped != 2 || skipRes.ItemsCreated != 0 {
		t.Fatalf("expected 2 skipped rows on PolicySkip, got %+v (err=%v)", skipRes, err)
	}

	// 4. Re-import with PolicyAddStock adds +4 to ESP32 (total 8) and +25.5 to WIRE-RED (total 51)
	addRes, err := mig.ExecuteImport(ctx, ImportRequest{
		CSVData:         csvPayload,
		DuplicatePolicy: PolicyAddStock,
	})
	if err != nil || addRes.ItemsUpdated != 2 || addRes.OpeningTxCount != 2 {
		t.Fatalf("expected 2 updated & 2 IN txs on PolicyAddStock, got %+v (err=%v)", addRes, err)
	}

	espAfterAdd, _ := cat.GetItemByID(ctx, esp.ID)
	if espAfterAdd.TotalStockText != "8" {
		t.Fatalf("expected ESP32 stock=8 after PolicyAddStock, got %s", espAfterAdd.TotalStockText)
	}

	// 5. Verify ledger reconciliation is 100% balanced after CSV imports
	rep, err := led.ReconcileLedger(ctx, false)
	if err != nil || !rep.Balanced {
		t.Fatalf("expected balanced reconciliation after CSV imports, got %+v (err=%v)", rep, err)
	}

	// 6. Test all 4 UTF-8 BOM CSV Exporters
	var itemsBuf, stockBuf, txBuf, reorderBuf bytes.Buffer
	if err := mig.ExportItemsCSV(ctx, &itemsBuf, true, false); err != nil {
		t.Fatalf("ExportItemsCSV failed: %v", err)
	}
	if err := mig.ExportStockLevelsCSV(ctx, &stockBuf, true); err != nil {
		t.Fatalf("ExportStockLevelsCSV failed: %v", err)
	}
	if err := mig.ExportTransactionsCSV(ctx, &txBuf, true, 100); err != nil {
		t.Fatalf("ExportTransactionsCSV failed: %v", err)
	}
	if err := mig.ExportReorderListCSV(ctx, &reorderBuf, true); err != nil {
		t.Fatalf("ExportReorderListCSV failed: %v", err)
	}

	for name, buf := range map[string]*bytes.Buffer{
		"items":        &itemsBuf,
		"stock_levels": &stockBuf,
		"transactions": &txBuf,
		"reorder":      &reorderBuf,
	} {
		b := buf.Bytes()
		if !bytes.HasPrefix(b, utf8BOM) {
			t.Errorf("%s CSV missing UTF-8 BOM prefix", name)
		}
		if !strings.Contains(string(b), "MCU-ESP32") {
			t.Errorf("%s CSV missing expected SKU 'MCU-ESP32'", name)
		}
	}
}
