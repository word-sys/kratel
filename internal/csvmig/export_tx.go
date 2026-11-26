package csvmig

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"

	"github.com/word-sys/kratel/internal/ledger"
)

// ExportTransactionsCSV streams the immutable ledger history to w as UTF-8 CSV
// with an optional UTF-8 BOM (\xEF\xBB\xBF) for Microsoft Excel compatibility.
func (s *Service) ExportTransactionsCSV(ctx context.Context, w io.Writer, includeBOM bool, limit int) error {
	if includeBOM {
		if _, err := w.Write(utf8BOM); err != nil {
			return fmt.Errorf("write UTF-8 BOM: %w", err)
		}
	}

	if limit <= 0 {
		limit = 100000
	}

	led := ledger.NewService(s.store, s.catalog)
	txs, _, err := led.ListTransactions(ctx, ledger.TransactionFilter{
		Limit: limit,
	})
	if err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"TxID",
		"Timestamp",
		"Type",
		"SKU",
		"Barcode",
		"ItemName",
		"Unit",
		"FromLocation",
		"ToLocation",
		"Quantity",
		"Delta",
		"BalanceAfter",
		"UnitCost",
		"LotNumber",
		"ExpiryDate",
		"BatchID",
		"Reason",
		"Operator",
		"Undone",
		"ReferenceTxID",
	}); err != nil {
		return err
	}

	for _, t := range txs {
		undoneStr := "false"
		if t.Undone {
			undoneStr = "true"
		}
		refStr := ""
		if t.ReferenceTxID != nil {
			refStr = strconv.FormatInt(*t.ReferenceTxID, 10)
		}
		if err := cw.Write([]string{
			strconv.FormatInt(t.ID, 10),
			t.CreatedAt,
			t.TxType,
			t.ItemSKU,
			t.ItemBarcode,
			t.ItemName,
			t.ItemUnit,
			t.FromLocationCode,
			t.ToLocationCode,
			t.QuantityText,
			t.DeltaText,
			t.BalanceAfterText,
			t.UnitCostMillis.Format(),
			t.LotNumber,
			t.ExpiryDate,
			t.BatchID,
			t.Reason,
			t.Operator,
			undoneStr,
			refStr,
		}); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
