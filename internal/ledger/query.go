package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

// TransactionFilter specifies multi-facet filter parameters for ledger audit trail queries.
type TransactionFilter struct {
	ItemID        int64
	LocationID    int64
	TxType        string
	Operator      string
	BatchID       string
	Query         string
	DateFrom      string
	DateTo        string
	IncludeUndone bool
	Limit         int
	Offset        int
}

// GetTransactionByID retrieves a single ledger Transaction by its ID.
func (s *Service) GetTransactionByID(ctx context.Context, id int64) (*models.Transaction, error) {
	return s.getTransactionByIDInternal(ctx, id)
}

// ListTransactions returns a paginated slice of immutable ledger transactions matching filter,
// ordered from newest to oldest (created_at DESC, id DESC), along with the total matching count.
func (s *Service) ListTransactions(ctx context.Context, filter TransactionFilter) ([]models.Transaction, int, error) {
	var where []string
	var args []interface{}

	if filter.ItemID > 0 {
		where = append(where, "t.item_id = ?")
		args = append(args, filter.ItemID)
	}
	if filter.LocationID > 0 {
		where = append(where, "(t.from_location_id = ? OR t.to_location_id = ?)")
		args = append(args, filter.LocationID, filter.LocationID)
	}
	if txType := strings.ToUpper(strings.TrimSpace(filter.TxType)); txType != "" {
		where = append(where, "t.tx_type = ?")
		args = append(args, txType)
	}
	if op := strings.TrimSpace(filter.Operator); op != "" {
		where = append(where, "LOWER(t.operator) = LOWER(?)")
		args = append(args, op)
	}
	if batch := strings.TrimSpace(filter.BatchID); batch != "" {
		where = append(where, "t.batch_id = ?")
		args = append(args, batch)
	}
	if df := strings.TrimSpace(filter.DateFrom); df != "" {
		where = append(where, "t.created_at >= ?")
		args = append(args, df)
	}
	if dt := strings.TrimSpace(filter.DateTo); dt != "" {
		if len(dt) == 10 {
			dt = dt + "T23:59:59Z"
		}
		where = append(where, "t.created_at <= ?")
		args = append(args, dt)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, `(
			LOWER(i.sku) LIKE ?
			OR LOWER(COALESCE(i.barcode, '')) LIKE ?
			OR LOWER(i.name) LIKE ?
			OR LOWER(t.reason) LIKE ?
			OR LOWER(t.operator) LIKE ?
			OR LOWER(t.lot_number) LIKE ?
			OR LOWER(COALESCE(fl.code, '')) LIKE ?
			OR LOWER(COALESCE(tl.code, '')) LIKE ?
		)`)
		args = append(args, like, like, like, like, like, like, like, like)
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM transactions t
		JOIN items i ON i.id = t.item_id
		LEFT JOIN locations fl ON fl.id = t.from_location_id
		LEFT JOIN locations tl ON tl.id = t.to_location_id
		%s;`, whereSQL)

	var totalCount int
	if err := s.store.DB().QueryRowContext(ctx, countQuery, args...).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("count transactions: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	listQuery := fmt.Sprintf(`
		SELECT
			t.id, t.item_id, i.sku, COALESCE(i.barcode, ''), i.name, i.unit,
			t.from_location_id, COALESCE(fl.code, ''), COALESCE(fl.name, ''),
			t.to_location_id, COALESCE(tl.code, ''), COALESCE(tl.name, ''),
			t.tx_type, t.quantity, t.quantity_millis, t.delta_millis, t.balance_after_millis,
			t.unit_cost_millis, t.lot_number, t.expiry_date, t.batch_id,
			t.reason, t.operator, t.reference_tx_id, t.undone, t.created_at
		FROM transactions t
		JOIN items i ON i.id = t.item_id
		LEFT JOIN locations fl ON fl.id = t.from_location_id
		LEFT JOIN locations tl ON tl.id = t.to_location_id
		%s
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT ? OFFSET ?;`, whereSQL)

	listArgs := append(append([]interface{}{}, args...), limit, offset)
	rows, err := s.store.DB().QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query transactions: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []models.Transaction
	for rows.Next() {
		var (
			t           models.Transaction
			fromLocID   sql.NullInt64
			toLocID     sql.NullInt64
			refTxID     sql.NullInt64
			qtyMillis   int64
			deltaMillis int64
			balMillis   int64
			costMillis  int64
			undoneInt   int
		)
		if err := rows.Scan(
			&t.ID, &t.ItemID, &t.ItemSKU, &t.ItemBarcode, &t.ItemName, &t.ItemUnit,
			&fromLocID, &t.FromLocationCode, &t.FromLocationName,
			&toLocID, &t.ToLocationCode, &t.ToLocationName,
			&t.TxType, &t.Quantity, &qtyMillis, &deltaMillis, &balMillis,
			&costMillis, &t.LotNumber, &t.ExpiryDate, &t.BatchID,
			&t.Reason, &t.Operator, &refTxID, &undoneInt, &t.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan transaction row: %w", err)
		}

		if fromLocID.Valid {
			v := fromLocID.Int64
			t.FromLocationID = &v
		}
		if toLocID.Valid {
			v := toLocID.Int64
			t.ToLocationID = &v
		}
		if refTxID.Valid {
			v := refTxID.Int64
			t.ReferenceTxID = &v
		}

		t.QuantityMillis = num.Millis(qtyMillis)
		t.Quantity = t.QuantityMillis.ToFloat()
		t.QuantityText = t.QuantityMillis.Format()

		t.DeltaMillis = num.Millis(deltaMillis)
		t.DeltaText = formatSignedDelta(t.DeltaMillis)

		t.BalanceAfterMillis = num.Millis(balMillis)
		t.BalanceAfterText = t.BalanceAfterMillis.Format()
		t.UnitCostMillis = num.Millis(costMillis)

		t.Undone = undoneInt != 0
		t.CanUndo = !t.Undone && t.ReferenceTxID == nil
		out = append(out, t)
	}

	return out, totalCount, nil
}
