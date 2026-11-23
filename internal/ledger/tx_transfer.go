package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/num"
)

func (s *Service) execTransferTx(ctx context.Context, tx *sql.Tx, item *itemRecord, in MovementInput, now string) (int64, error) {
	qtyMillis, err := num.ParsePositiveMillis("quantity", in.Quantity)
	if err != nil {
		return 0, err
	}

	if (in.FromLocationID == nil || *in.FromLocationID <= 0) && strings.TrimSpace(in.FromLocationCode) == "" {
		return 0, apperr.Validation("from_location_id", "source location is required for TRANSFER")
	}
	if (in.ToLocationID == nil || *in.ToLocationID <= 0) && strings.TrimSpace(in.ToLocationCode) == "" {
		return 0, apperr.Validation("to_location_id", "destination location is required for TRANSFER")
	}

	fromLocID, fromLocCode, err := s.resolveLocationTx(ctx, tx, in.FromLocationID, in.FromLocationCode, nil)
	if err != nil {
		return 0, err
	}
	toLocID, toLocCode, err := s.resolveLocationTx(ctx, tx, in.ToLocationID, in.ToLocationCode, nil)
	if err != nil {
		return 0, err
	}

	if fromLocID == toLocID {
		return 0, apperr.Validation("to_location_id", "source and destination locations must be different")
	}

	fromBalance, err := getStockBalanceTx(ctx, tx, item.ID, fromLocID)
	if err != nil {
		return 0, err
	}
	newFromBalance, err := fromBalance.CheckedSub(qtyMillis)
	if err != nil {
		return 0, err
	}
	if newFromBalance < 0 && !isNegativeStockAllowedTx(ctx, tx, in.AllowNegativeOverride) {
		return 0, apperr.InsufficientStock(item.SKU, fromLocCode, fromBalance.Format(), qtyMillis.Format())
	}

	toBalance, err := getStockBalanceTx(ctx, tx, item.ID, toLocID)
	if err != nil {
		return 0, err
	}
	newToBalance, err := toBalance.CheckedAdd(qtyMillis)
	if err != nil {
		return 0, err
	}

	if err := upsertStockBalanceTx(ctx, tx, item.ID, fromLocID, newFromBalance, now); err != nil {
		return 0, err
	}
	if err := upsertStockBalanceTx(ctx, tx, item.ID, toLocID, newToBalance, now); err != nil {
		return 0, err
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = fmt.Sprintf("Transfer %s -> %s", fromLocCode, toLocCode)
	}

	return insertTransactionRowTx(ctx, tx, txInsertRow{
		ItemID:             item.ID,
		FromLocationID:     &fromLocID,
		ToLocationID:       &toLocID,
		TxType:             TxTransfer,
		QuantityMillis:     qtyMillis,
		DeltaMillis:        0,
		BalanceAfterMillis: newToBalance,
		UnitCostMillis:     item.UnitCostMillis,
		LotNumber:          item.LotNumber,
		ExpiryDate:         item.ExpiryDate,
		BatchID:            in.BatchID,
		Reason:             reason,
		Operator:           in.Operator,
		CreatedAt:          now,
	})
}
