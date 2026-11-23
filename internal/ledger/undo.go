package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

// UndoTransaction atomically reverses a committed ledger transaction by appending
// an immutable compensating transaction (referencing reference_tx_id = txID),
// restoring the exact stock_levels balances, and marking the original transaction undone = 1.
func (s *Service) UndoTransaction(ctx context.Context, txID int64, operator string) (*models.Transaction, error) {
	op := strings.TrimSpace(operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	var reversalID int64
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		revID, undoErr := s.undoSingleTx(ctx, tx, txID, op)
		if undoErr != nil {
			return undoErr
		}
		reversalID = revID
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.getTransactionByIDInternal(ctx, reversalID)
}

// UndoBatch atomically reverses all non-undone transactions belonging to batchID
// (such as a BOM/Repair Kit stock-out or a physical stocktake commit) in reverse chronological order.
func (s *Service) UndoBatch(ctx context.Context, batchID string, operator string) ([]models.Transaction, error) {
	cleanedBatch := strings.TrimSpace(batchID)
	if cleanedBatch == "" {
		return nil, apperr.Validation("batch_id", "batch_id is required")
	}
	op := strings.TrimSpace(operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	var reversalIDs []int64
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		rows, qErr := tx.QueryContext(
			ctx,
			`SELECT id FROM transactions WHERE batch_id = ? AND undone = 0 AND reference_tx_id IS NULL ORDER BY id DESC;`,
			cleanedBatch,
		)
		if qErr != nil {
			return fmt.Errorf("query batch transactions: %w", qErr)
		}
		var txIDs []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			txIDs = append(txIDs, id)
		}
		_ = rows.Close()

		if len(txIDs) == 0 {
			return apperr.NotFound("batch_transactions", cleanedBatch)
		}

		for _, id := range txIDs {
			revID, undoErr := s.undoSingleTx(ctx, tx, id, op)
			if undoErr != nil {
				return undoErr
			}
			reversalIDs = append(reversalIDs, revID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	reversals := make([]models.Transaction, 0, len(reversalIDs))
	for _, id := range reversalIDs {
		rev, err := s.getTransactionByIDInternal(ctx, id)
		if err != nil {
			return nil, err
		}
		reversals = append(reversals, *rev)
	}
	return reversals, nil
}

func (s *Service) undoSingleTx(ctx context.Context, tx *sql.Tx, txID int64, operator string) (int64, error) {
	var (
		itemID      int64
		fromLocID   sql.NullInt64
		toLocID     sql.NullInt64
		txType      string
		qtyMillis   int64
		deltaMillis int64
		costMillis  int64
		lotNumber   string
		expiryDate  string
		batchID     string
		refTxID     sql.NullInt64
		undoneInt   int
	)

	err := tx.QueryRowContext(
		ctx,
		`SELECT
			item_id, from_location_id, to_location_id, tx_type,
			quantity_millis, delta_millis, unit_cost_millis,
			lot_number, expiry_date, batch_id, reference_tx_id, undone
		 FROM transactions WHERE id = ?;`,
		txID,
	).Scan(
		&itemID, &fromLocID, &toLocID, &txType,
		&qtyMillis, &deltaMillis, &costMillis,
		&lotNumber, &expiryDate, &batchID, &refTxID, &undoneInt,
	)
	if err == sql.ErrNoRows {
		return 0, apperr.NotFound("transaction", fmt.Sprintf("%d", txID))
	}
	if err != nil {
		return 0, fmt.Errorf("load transaction %d for undo: %w", txID, err)
	}

	if undoneInt != 0 {
		return 0, apperr.ErrAlreadyUndone.WithDetail("transaction_id", txID)
	}
	if refTxID.Valid {
		return 0, apperr.Validation("transaction_id", "cannot undo a compensating undo transaction directly")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	qty := num.Millis(qtyMillis)
	delta := num.Millis(deltaMillis)

	var (
		revType        string
		revFromLoc     *int64
		revToLoc       *int64
		revDelta       num.Millis
		revBalance     num.Millis
	)

	switch txType {
	case TxIn:
		if !toLocID.Valid {
			return 0, apperr.Validation("transaction_id", "corrupt IN transaction missing to_location_id")
		}
		locID := toLocID.Int64
		curBal, err := getStockBalanceTx(ctx, tx, itemID, locID)
		if err != nil {
			return 0, err
		}
		newBal, err := curBal.CheckedSub(qty)
		if err != nil {
			return 0, err
		}
		if err := upsertStockBalanceTx(ctx, tx, itemID, locID, newBal, now); err != nil {
			return 0, err
		}
		revType = TxOut
		revFromLoc = &locID
		revDelta = qty.Neg()
		revBalance = newBal

	case TxOut:
		if !fromLocID.Valid {
			return 0, apperr.Validation("transaction_id", "corrupt OUT transaction missing from_location_id")
		}
		locID := fromLocID.Int64
		curBal, err := getStockBalanceTx(ctx, tx, itemID, locID)
		if err != nil {
			return 0, err
		}
		newBal, err := curBal.CheckedAdd(qty)
		if err != nil {
			return 0, err
		}
		if err := upsertStockBalanceTx(ctx, tx, itemID, locID, newBal, now); err != nil {
			return 0, err
		}
		revType = TxIn
		revToLoc = &locID
		revDelta = qty
		revBalance = newBal

	case TxTransfer:
		if !fromLocID.Valid || !toLocID.Valid {
			return 0, apperr.Validation("transaction_id", "corrupt TRANSFER transaction missing location IDs")
		}
		origFrom := fromLocID.Int64
		origTo := toLocID.Int64

		curToBal, err := getStockBalanceTx(ctx, tx, itemID, origTo)
		if err != nil {
			return 0, err
		}
		newToBal, err := curToBal.CheckedSub(qty)
		if err != nil {
			return 0, err
		}
		curFromBal, err := getStockBalanceTx(ctx, tx, itemID, origFrom)
		if err != nil {
			return 0, err
		}
		newFromBal, err := curFromBal.CheckedAdd(qty)
		if err != nil {
			return 0, err
		}

		if err := upsertStockBalanceTx(ctx, tx, itemID, origTo, newToBal, now); err != nil {
			return 0, err
		}
		if err := upsertStockBalanceTx(ctx, tx, itemID, origFrom, newFromBal, now); err != nil {
			return 0, err
		}
		revType = TxTransfer
		revFromLoc = &origTo
		revToLoc = &origFrom
		revDelta = 0
		revBalance = newFromBal

	case TxAdjust:
		var locID int64
		if toLocID.Valid {
			locID = toLocID.Int64
		} else if fromLocID.Valid {
			locID = fromLocID.Int64
		} else {
			return 0, apperr.Validation("transaction_id", "corrupt ADJUST transaction missing location ID")
		}
		curBal, err := getStockBalanceTx(ctx, tx, itemID, locID)
		if err != nil {
			return 0, err
		}
		revDelta = delta.Neg()
		newBal, err := curBal.CheckedAdd(revDelta)
		if err != nil {
			return 0, err
		}
		if err := upsertStockBalanceTx(ctx, tx, itemID, locID, newBal, now); err != nil {
			return 0, err
		}
		revType = TxAdjust
		if revDelta < 0 {
			revFromLoc = &locID
		} else {
			revToLoc = &locID
		}
		revBalance = newBal

	default:
		return 0, apperr.Validation("tx_type", fmt.Sprintf("unsupported transaction type %q for undo", txType))
	}

	if _, err := tx.ExecContext(ctx, `UPDATE transactions SET undone = 1 WHERE id = ?;`, txID); err != nil {
		return 0, fmt.Errorf("mark transaction %d undone: %w", txID, err)
	}

	reason := fmt.Sprintf("Undo of %s transaction #%d", txType, txID)
	return insertTransactionRowTx(ctx, tx, txInsertRow{
		ItemID:             itemID,
		FromLocationID:     revFromLoc,
		ToLocationID:       revToLoc,
		TxType:             revType,
		QuantityMillis:     qty,
		DeltaMillis:        revDelta,
		BalanceAfterMillis: revBalance,
		UnitCostMillis:     num.Millis(costMillis),
		LotNumber:          lotNumber,
		ExpiryDate:         expiryDate,
		BatchID:            batchID,
		Reason:             reason,
		Operator:           operator,
		ReferenceTxID:      &txID,
		CreatedAt:          now,
	})
}
