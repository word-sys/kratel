package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/num"
)

func init() {
	adjustTxHook = (*Service).execAdjustTx
}

func (s *Service) execAdjustTx(ctx context.Context, tx *sql.Tx, item *itemRecord, in MovementInput, now string) (int64, error) {
	rawQty := strings.TrimSpace(in.Quantity)
	if rawQty == "" {
		return 0, apperr.Validation("quantity", "quantity is required for ADJUST")
	}

	parsedMillis, err := num.ParseMillis(rawQty)
	if err != nil {
		return 0, err
	}

	locIDPtr := in.ToLocationID
	locCodeStr := in.ToLocationCode
	if (locIDPtr == nil || *locIDPtr <= 0) && strings.TrimSpace(locCodeStr) == "" {
		locIDPtr = in.FromLocationID
		locCodeStr = in.FromLocationCode
	}

	locID, locCode, err := s.resolveLocationTx(ctx, tx, locIDPtr, locCodeStr, item.DefaultLocationID)
	if err != nil {
		return 0, err
	}

	currentBalance, err := getStockBalanceTx(ctx, tx, item.ID, locID)
	if err != nil {
		return 0, err
	}

	mode := strings.ToUpper(strings.TrimSpace(in.AdjustMode))
	if mode == "" {
		mode = AdjustModeTarget
	}

	var (
		newBalance num.Millis
		delta      num.Millis
	)

	switch mode {
	case AdjustModeTarget:
		newBalance = parsedMillis
		delta, err = newBalance.CheckedSub(currentBalance)
		if err != nil {
			return 0, err
		}
	case AdjustModeDelta:
		delta = parsedMillis
		newBalance, err = currentBalance.CheckedAdd(delta)
		if err != nil {
			return 0, err
		}
	default:
		return 0, apperr.Validation("adjust_mode", "adjust_mode must be TARGET or DELTA")
	}

	if newBalance < 0 && !isNegativeStockAllowedTx(ctx, tx, in.AllowNegativeOverride) {
		reqDeduct := delta.Abs()
		return 0, apperr.InsufficientStock(item.SKU, locCode, currentBalance.Format(), reqDeduct.Format())
	}

	if err := upsertStockBalanceTx(ctx, tx, item.ID, locID, newBalance, now); err != nil {
		return 0, err
	}

	var (
		fromLocPtr *int64
		toLocPtr   *int64
	)
	if delta < 0 {
		fromLocPtr = &locID
	} else {
		toLocPtr = &locID
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = fmt.Sprintf("Stock count adjust (%s -> %s)", currentBalance.Format(), newBalance.Format())
	}

	return insertTransactionRowTx(ctx, tx, txInsertRow{
		ItemID:             item.ID,
		FromLocationID:     fromLocPtr,
		ToLocationID:       toLocPtr,
		TxType:             TxAdjust,
		QuantityMillis:     delta.Abs(),
		DeltaMillis:        delta,
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
