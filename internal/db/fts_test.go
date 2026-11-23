package db

import (
	"context"
	"testing"
)

func TestBuildFTS5MatchQuerySanitization(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: "ESP32-S3", want: `"esp32"* "s3"*`},
		{input: "M3-8*0.5 (Hex)", want: `"m3"* "8"* "0"* "5"* "hex"*`},
		{input: `  "10kΩ" / 0805  `, want: `"10kω"* "0805"*`},
		{input: "   --- *** ()  ", want: ""},
	}

	for _, tc := range cases {
		if got := BuildFTS5MatchQuery(tc.input); got != tc.want {
			t.Errorf("BuildFTS5MatchQuery(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFTS5TriggerSynchronizationAndDiacriticSearch(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	if !store.FTSEnabled() {
		t.Fatal("expected FTSEnabled() == true with modernc.org/sqlite")
	}

	// 1. Insert item with Turkish diacritics and custom JSON attributes
	_, err = store.DB().ExecContext(
		ctx,
		`INSERT INTO items (sku, barcode, name, description, category, lot_number, attributes_json, created_at, updated_at)
		 VALUES ('KRT-ESP32', '8690000111222', 'ESP32-S3 Geliştirme Kartı', 'Çift çekirdekli Wi-Fi & BLE modülü', 'Mikrodenetleyici', 'LOT-2026-A', '{"voltage":"3.3V","flash":"16MB"}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`,
	)
	if err != nil {
		t.Fatalf("insert item failed: %v", err)
	}

	// Search by ASCII-folded Turkish query ("gelistirme modulu"), JSON attribute ("16MB"), and lot number ("LOT-2026")
	for _, query := range []string{"gelistirme modulu", "16MB", "3.3V", "8690000111", "LOT-2026"} {
		matchExpr := BuildFTS5MatchQuery(query)
		var matchedID int64
		err := store.DB().QueryRowContext(
			ctx,
			`SELECT rowid FROM items_fts WHERE items_fts MATCH ?;`,
			matchExpr,
		).Scan(&matchedID)
		if err != nil || matchedID != 1 {
			t.Fatalf("expected FTS5 search for %q (match=%q) to return rowid=1, got id=%d err=%v",
				query, matchExpr, matchedID, err)
		}
	}

	// 2. Update item SKU and attributes; verify old SKU no longer matches and new SKU matches
	_, err = store.DB().ExecContext(
		ctx,
		`UPDATE items SET sku = 'KRT-STM32H7', name = 'STM32H743 ARM Board', attributes_json = '{"clock":"480MHz"}' WHERE id = 1;`,
	)
	if err != nil {
		t.Fatalf("update item failed: %v", err)
	}

	var oldCount int
	if err := store.DB().QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM items_fts WHERE items_fts MATCH ?;`,
		BuildFTS5MatchQuery("ESP32"),
	).Scan(&oldCount); err != nil {
		t.Fatalf("query old FTS token failed: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("expected 0 matches for old SKU after UPDATE trigger, got %d", oldCount)
	}

	var newCount int
	if err := store.DB().QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM items_fts WHERE items_fts MATCH ?;`,
		BuildFTS5MatchQuery("STM32H7 480MHz"),
	).Scan(&newCount); err != nil {
		t.Fatalf("query new FTS token failed: %v", err)
	}
	if newCount != 1 {
		t.Fatalf("expected 1 match for updated item in FTS5, got %d", newCount)
	}

	// 3. Verify RebuildFTS5 works cleanly
	if err := RebuildFTS5(ctx, store.DB()); err != nil {
		t.Fatalf("RebuildFTS5 failed: %v", err)
	}

	// 4. Delete item and verify FTS5 row is removed by AFTER DELETE trigger
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM items WHERE id = 1;`); err != nil {
		t.Fatalf("delete item failed: %v", err)
	}
	if err := store.DB().QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM items_fts WHERE items_fts MATCH ?;`,
		BuildFTS5MatchQuery("STM32H7"),
	).Scan(&newCount); err != nil {
		t.Fatalf("query deleted FTS token failed: %v", err)
	}
	if newCount != 0 {
		t.Fatalf("expected 0 matches in FTS5 after DELETE trigger, got %d", newCount)
	}
}
