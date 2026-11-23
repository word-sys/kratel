package ledger

import (
	"context"
	"database/sql"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/num"
)

func (s *Service) execOutTx(ctx context.Context, tx *sql.Tx, item *itemRecord, in MovementInput, now string) (int64, error) {
	qtyMillis, err := num.ParsePositiveMillis("quantity", in.Quantity)
	if err != nil {
		return 0, err
	}

	fromLocID, fromLocCode, err := s.resolveOutSourceLocationTx(ctx, tx, item, in.FromLocationID, in.FromLocationCode)
	if err != nil {
		return 0, err
	}

	currentBalance, err := getStockBalanceTx(ctx, tx, item.ID, fromLocID)
	if err != nil {
		return 0, err
	}

	newBalance, err := currentBalance.CheckedSub(qtyMillis)
	if err != nil {
		return 0, err
	}

	if newBalance < 0 && !isNegativeStockAllowedTx(ctx, tx, in.AllowNegativeOverride) {
		return 0, apperr.InsufficientStock(item.SKU, fromLocCode, currentBalance.Format(), qtyMillis.Format())
	}

	if err := upsertStockBalanceTx(ctx, tx, item.ID, fromLocID, newBalance, now); err != nil {
		return 0, err
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "Stock Out"
	}

	return insertTransactionRowTx(ctx, tx, txInsertRow{
		ItemID:             item.ID,
		FromLocationID:     &fromLocID,
		ToLocationID:       nil,
		TxType:             TxOut,
		QuantityMillis:     qtyMillis,
		DeltaMillis:        qtyMillis.Neg(),
		BalanceAfterMillis: newBalance,
		UnitCostMillis:     item.UnitCostMillis,
		LotNumber:          item.LotNumber,
		ExpiryDate:         item.ExpiryDate,
		BatchID:            in.BatchID,
		Reason:             reason,
		Operator:           in.Operator,
		CreatedAt:          now,
	})
}

// resolveOutSourceLocationTx determines the source bin for an OUT deduction.
// If the user explicitly provided FromLocationID or FromLocationCode, that bin is used.
// Otherwise, during fast barcode scanning, it automatically picks the bin currently holding
// the highest positive stock (preferring the item's default location if stocked).
func (s *Service) resolveOutSourceLocationTx(ctx context.Context, tx *sql.Tx, item *itemRecord, explicitID *int64, explicitCode string) (int64, string, error) {
	if (explicitID != nil && *explicitID > 0) || strings.TrimSpace(explicitCode) != "" {
		return s.resolveLocationTx(ctx, tx, explicitID, explicitCode, item.DefaultLocationID)
	}

	var bestLocID int64
	var bestLocCode string
	defaultID := int64(0)
	if item.DefaultLocationID != nil {
		defaultID = *item.DefaultLocationID
	}

	err := tx.QueryRowContext(
		ctx,
		`SELECT sl.location_id, l.code
		 FROM stock_levels sl
		 JOIN locations l ON l.id = sl.location_id
		 WHERE sl.item_id = ? AND sl.quantity_millis > 0
		 ORDER BY CASE WHEN sl.location_id = ? THEN 0 ELSE 1 END,
		          sl.quantity_millis DESC,
		          sl.location_id ASC
		 LIMIT 1;`,
		item.ID, defaultID,
	).Scan(&bestLocID, &bestLocCode)
	if err == nil {
		return bestLocID, bestLocCode, nil
	}

	return s.resolveLocationTx(ctx, tx, nil, "", item.DefaultLocationID)
}
