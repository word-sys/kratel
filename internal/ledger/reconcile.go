package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/word-sys/kratel/internal/num"
)

// ReconciliationMismatch describes a discrepancy between the immutable transactions ledger
// and the materialized stock_levels balance for a specific (item_id, location_id) pair.
type ReconciliationMismatch struct {
	ItemID         int64      `json:"item_id"`
	ItemSKU        string     `json:"item_sku"`
	LocationID     int64      `json:"location_id"`
	LocationCode   string     `json:"location_code"`
	ExpectedMillis num.Millis `json:"expected_millis"`
	ExpectedText   string     `json:"expected_text"`
	ActualMillis   num.Millis `json:"actual_millis"`
	ActualText     string     `json:"actual_text"`
	DiffMillis     num.Millis `json:"diff_millis"`
	DiffText       string     `json:"diff_text"`
}

// ReconciliationReport summarizes the result of replaying the entire immutable ledger
// against materialized stock_levels balances.
type ReconciliationReport struct {
	Balanced             bool                     `json:"balanced"`
	TransactionsReplayed int                      `json:"transactions_replayed"`
	StockLinesChecked    int                      `json:"stock_lines_checked"`
	MismatchCount        int                      `json:"mismatch_count"`
	RepairedCount        int                      `json:"repaired_count"`
	Mismatches           []ReconciliationMismatch `json:"mismatches"`
	DurationMs           float64                  `json:"duration_ms"`
}

type stockKey struct {
	itemID     int64
	locationID int64
}

// ReconcileLedger replays every row in the immutable transactions table from ID #1 and compares
// the derived (item_id, location_id) balances against stock_levels.
// When repair is true, any out-of-sync stock_levels rows are atomically resynchronized to the ledger.
func (s *Service) ReconcileLedger(ctx context.Context, repair bool) (*ReconciliationReport, error) {
	start := time.Now()
	conn := s.store.DB()

	expected := make(map[stockKey]num.Millis)
	txReplayed := 0

	rows, err := conn.QueryContext(
		ctx,
		`SELECT item_id, from_location_id, to_location_id, tx_type, quantity_millis, delta_millis
		 FROM transactions
		 ORDER BY id ASC;`,
	)
	if err != nil {
		return nil, fmt.Errorf("query transactions for reconciliation: %w", err)
	}
	for rows.Next() {
		var (
			itemID      int64
			fromLocID   sql.NullInt64
			toLocID     sql.NullInt64
			txType      string
			qtyMillis   int64
			deltaMillis int64
		)
		if err := rows.Scan(&itemID, &fromLocID, &toLocID, &txType, &qtyMillis, &deltaMillis); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan transaction for reconciliation: %w", err)
		}
		txReplayed++

		qty := num.Millis(qtyMillis)
		delta := num.Millis(deltaMillis)

		switch txType {
		case TxIn:
			if toLocID.Valid {
				k := stockKey{itemID: itemID, locationID: toLocID.Int64}
				expected[k] = expected[k].Add(qty)
			}
		case TxOut:
			if fromLocID.Valid {
				k := stockKey{itemID: itemID, locationID: fromLocID.Int64}
				expected[k] = expected[k].Sub(qty)
			}
		case TxTransfer:
			if fromLocID.Valid {
				kf := stockKey{itemID: itemID, locationID: fromLocID.Int64}
				expected[kf] = expected[kf].Sub(qty)
			}
			if toLocID.Valid {
				kt := stockKey{itemID: itemID, locationID: toLocID.Int64}
				expected[kt] = expected[kt].Add(qty)
			}
		case TxAdjust:
			var locID int64
			if toLocID.Valid {
				locID = toLocID.Int64
			} else if fromLocID.Valid {
				locID = fromLocID.Int64
			}
			if locID > 0 {
				k := stockKey{itemID: itemID, locationID: locID}
				expected[k] = expected[k].Add(delta)
			}
		}
	}
	_ = rows.Close()

	actual := make(map[stockKey]num.Millis)
	slRows, err := conn.QueryContext(ctx, `SELECT item_id, location_id, quantity_millis FROM stock_levels;`)
	if err != nil {
		return nil, fmt.Errorf("query stock_levels for reconciliation: %w", err)
	}
	for slRows.Next() {
		var (
			itemID    int64
			locID     int64
			qtyMillis int64
		)
		if err := slRows.Scan(&itemID, &locID, &qtyMillis); err != nil {
			_ = slRows.Close()
			return nil, fmt.Errorf("scan stock_level for reconciliation: %w", err)
		}
		actual[stockKey{itemID: itemID, locationID: locID}] = num.Millis(qtyMillis)
	}
	_ = slRows.Close()

	allKeys := make(map[stockKey]struct{}, len(expected)+len(actual))
	for k := range expected {
		allKeys[k] = struct{}{}
	}
	for k := range actual {
		allKeys[k] = struct{}{}
	}

	var mismatches []ReconciliationMismatch
	for k := range allKeys {
		exp := expected[k]
		act := actual[k]
		if exp != act {
			diff := act.Sub(exp)
			mismatches = append(mismatches, ReconciliationMismatch{
				ItemID:         k.itemID,
				LocationID:     k.locationID,
				ExpectedMillis: exp,
				ExpectedText:   exp.Format(),
				ActualMillis:   act,
				ActualText:     act.Format(),
				DiffMillis:     diff,
				DiffText:       formatSignedDelta(diff),
			})
		}
	}

	// Enrich any mismatches with SKU and LocationCode for actionable reporting
	if len(mismatches) > 0 {
		for i := range mismatches {
			_ = conn.QueryRowContext(ctx, `SELECT sku FROM items WHERE id = ?;`, mismatches[i].ItemID).Scan(&mismatches[i].ItemSKU)
			_ = conn.QueryRowContext(ctx, `SELECT code FROM locations WHERE id = ?;`, mismatches[i].LocationID).Scan(&mismatches[i].LocationCode)
		}
		sort.Slice(mismatches, func(i, j int) bool {
			if mismatches[i].ItemID != mismatches[j].ItemID {
				return mismatches[i].ItemID < mismatches[j].ItemID
			}
			return mismatches[i].LocationID < mismatches[j].LocationID
		})
	}

	repaired := 0
	if repair && len(mismatches) > 0 {
		now := time.Now().UTC().Format(time.RFC3339)
		if err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
			for _, m := range mismatches {
				if err := upsertStockBalanceTx(ctx, tx, m.ItemID, m.LocationID, m.ExpectedMillis, now); err != nil {
					return err
				}
				repaired++
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("repair stock_levels during reconciliation: %w", err)
		}
	}

	report := &ReconciliationReport{
		Balanced:             len(mismatches) == 0 || (repair && repaired == len(mismatches)),
		TransactionsReplayed: txReplayed,
		StockLinesChecked:    len(allKeys),
		MismatchCount:        len(mismatches),
		RepairedCount:        repaired,
		Mismatches:           mismatches,
		DurationMs:           float64(time.Since(start).Microseconds()) / 1000.0,
	}
	return report, nil
}
