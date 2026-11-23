package ledger

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/num"
)

func (s *Service) execInTx(ctx context.Context, tx *sql.Tx, item *itemRecord, in MovementInput, now string) (int64, error) {
	qtyMillis, err := num.ParsePositiveMillis("quantity", in.Quantity)
	if err != nil {
		return 0, err
	}

	toLocID, _, err := s.resolveLocationTx(ctx, tx, in.ToLocationID, in.ToLocationCode, item.DefaultLocationID)
	if err != nil {
		return 0, err
	}

	currentBalance, err := getStockBalanceTx(ctx, tx, item.ID, toLocID)
	if err != nil {
		return 0, err
	}

	newBalance, err := currentBalance.CheckedAdd(qtyMillis)
	if err != nil {
		return 0, err
	}

	unitCostMillis := item.UnitCostMillis
	if strings.TrimSpace(in.UnitCost) != "" {
		parsedCost, costErr := num.ParseNonNegativeMillis("unit_cost", in.UnitCost)
		if costErr != nil {
			return 0, costErr
		}
		unitCostMillis = parsedCost
	}

	lotNumber := strings.TrimSpace(in.LotNumber)
	if lotNumber == "" {
		lotNumber = item.LotNumber
	}

	expiryDate := strings.TrimSpace(in.ExpiryDate)
	if expiryDate != "" {
		if _, parseErr := time.Parse("2006-01-02", expiryDate); parseErr != nil {
			return 0, apperr.Validation("expiry_date", "expiry_date must be in YYYY-MM-DD format")
		}
	} else {
		expiryDate = item.ExpiryDate
	}

	if err := upsertStockBalanceTx(ctx, tx, item.ID, toLocID, newBalance, now); err != nil {
		return 0, err
	}

	// If the IN restock carries an updated unit cost, lot number, or expiry date, sync it to items
	if unitCostMillis != item.UnitCostMillis || lotNumber != item.LotNumber || expiryDate != item.ExpiryDate {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE items SET unit_cost = ?, unit_cost_millis = ?, lot_number = ?, expiry_date = ?, updated_at = ? WHERE id = ?;`,
			unitCostMillis.ToFloat(),
			unitCostMillis.Int64(),
			lotNumber,
			expiryDate,
			now,
			item.ID,
		); err != nil {
			return 0, err
		}
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "Stock In"
	}

	return insertTransactionRowTx(ctx, tx, txInsertRow{
		ItemID:             item.ID,
		FromLocationID:     nil,
		ToLocationID:       &toLocID,
		TxType:             TxIn,
		QuantityMillis:     qtyMillis,
		DeltaMillis:        qtyMillis,
		BalanceAfterMillis: newBalance,
		UnitCostMillis:     unitCostMillis,
		LotNumber:          lotNumber,
		ExpiryDate:         expiryDate,
		BatchID:            in.BatchID,
		Reason:             reason,
		Operator:           in.Operator,
		CreatedAt:          now,
	})
}
