package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

const (
	TxIn       = "IN"
	TxOut      = "OUT"
	TxTransfer = "TRANSFER"
	TxAdjust   = "ADJUST"

	AdjustModeTarget = "TARGET"
	AdjustModeDelta  = "DELTA"
)

// EventHookFunc is invoked asynchronously after a ledger movement commits
// so webhooks can dispatch transaction.created and low_stock alerts.
type EventHookFunc func(tx *models.Transaction, item *models.Item, crossedLowStock bool)

// Service implements Kratel's immutable append-only stock movement ledger,
// compensating undo engine, Quick-Scan bar processor, BOM kits, stocktake audits,
// tool loans, and full ledger-to-stock reconciliation.
type Service struct {
	store   *db.Store
	catalog *catalog.Service
	hookFn  EventHookFunc
}

// NewService creates a new ledger domain service.
func NewService(store *db.Store, cat *catalog.Service) *Service {
	if cat == nil {
		cat = catalog.NewService(store)
	}
	return &Service{
		store:   store,
		catalog: cat,
	}
}

// SetEventHook registers an optional callback for post-transaction webhook notifications.
func (s *Service) SetEventHook(fn EventHookFunc) {
	s.hookFn = fn
}

// MovementInput describes an atomic stock movement request (IN, OUT, TRANSFER, or ADJUST).
type MovementInput struct {
	ItemID                int64  `json:"item_id"`
	Code                  string `json:"code"` // Optional barcode, SKU, or alias if ItemID == 0
	TxType                string `json:"tx_type"`
	Quantity              string `json:"quantity"`
	AdjustMode            string `json:"adjust_mode,omitempty"` // "TARGET" (exact count) or "DELTA" (signed change)
	FromLocationID        *int64 `json:"from_location_id,omitempty"`
	FromLocationCode      string `json:"from_location_code,omitempty"`
	ToLocationID          *int64 `json:"to_location_id,omitempty"`
	ToLocationCode        string `json:"to_location_code,omitempty"`
	UnitCost              string `json:"unit_cost,omitempty"`
	LotNumber             string `json:"lot_number,omitempty"`
	ExpiryDate            string `json:"expiry_date,omitempty"`
	BatchID               string `json:"batch_id,omitempty"`
	Reason                string `json:"reason"`
	Operator              string `json:"operator"`
	AllowNegativeOverride *bool  `json:"allow_negative_override,omitempty"`
}

type itemRecord struct {
	ID                int64
	SKU               string
	Barcode           string
	Name              string
	Unit              string
	MinStockMillis    num.Millis
	UnitCostMillis    num.Millis
	DefaultLocationID *int64
	LotNumber         string
	ExpiryDate        string
}

// adjustHandler Hook registered by tx_adjust.go (Step 018)
var adjustTxHook func(s *Service, ctx context.Context, tx *sql.Tx, item *itemRecord, in MovementInput, now string) (int64, error)

// RecordMovement executes an atomic stock movement (IN, OUT, TRANSFER, or ADJUST),
// updating stock_levels and appending an immutable row to transactions.
func (s *Service) RecordMovement(ctx context.Context, in MovementInput) (*models.Transaction, error) {
	txType := strings.ToUpper(strings.TrimSpace(in.TxType))
	switch txType {
	case TxIn, TxOut, TxTransfer, TxAdjust:
	default:
		return nil, apperr.Validation("tx_type", "tx_type must be one of IN, OUT, TRANSFER, ADJUST")
	}
	in.TxType = txType

	if strings.TrimSpace(in.Operator) == "" {
		in.Operator = db.DefaultOperatorName
	}

	var (
		createdTxID     int64
		targetItemID    int64
		wasAboveMin     bool
		minStockMillis  num.Millis
	)

	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		item, err := s.resolveItemTx(ctx, tx, in.ItemID, in.Code)
		if err != nil {
			return err
		}
		targetItemID = item.ID
		minStockMillis = item.MinStockMillis

		prevTotal, err := getTotalItemStockTx(ctx, tx, item.ID)
		if err != nil {
			return err
		}
		wasAboveMin = prevTotal > item.MinStockMillis

		now := time.Now().UTC().Format(time.RFC3339)

		switch txType {
		case TxIn:
			createdTxID, err = s.execInTx(ctx, tx, item, in, now)
		case TxOut:
			createdTxID, err = s.execOutTx(ctx, tx, item, in, now)
		case TxTransfer:
			createdTxID, err = s.execTransferTx(ctx, tx, item, in, now)
		case TxAdjust:
			if adjustTxHook == nil {
				return apperr.Validation("tx_type", "ADJUST handler not initialized")
			}
			createdTxID, err = adjustTxHook(s, ctx, tx, item, in, now)
		}
		return err
	})
	if err != nil {
		return nil, err
	}

	txView, err := s.getTransactionByIDInternal(ctx, createdTxID)
	if err != nil {
		return nil, err
	}

	if s.hookFn != nil && targetItemID > 0 {
		if itemView, itemErr := s.catalog.GetItemByID(ctx, targetItemID); itemErr == nil {
			crossedLow := itemView.IsLowStock && (wasAboveMin || minStockMillis > 0)
			s.hookFn(txView, itemView, crossedLow)
		}
	}

	return txView, nil
}

func (s *Service) resolveItemTx(ctx context.Context, tx *sql.Tx, itemID int64, rawCode string) (*itemRecord, error) {
	if itemID <= 0 {
		code := strings.TrimSpace(rawCode)
		if code == "" {
			return nil, apperr.Validation("item_id", "either item_id or barcode/SKU code is required")
		}
		err := tx.QueryRowContext(
			ctx,
			`SELECT id FROM items WHERE (barcode IS NOT NULL AND UPPER(barcode) = UPPER(?)) OR UPPER(sku) = UPPER(?)
			 UNION ALL
			 SELECT item_id FROM item_aliases WHERE UPPER(code) = UPPER(?)
			 LIMIT 1;`,
			code, code, code,
		).Scan(&itemID)
		if err == sql.ErrNoRows {
			return nil, apperr.NotFound("item", code)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve item code %q: %w", code, err)
		}
	}

	var (
		rec          itemRecord
		minMillis    int64
		costMillis   int64
		defaultLocID sql.NullInt64
	)
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, sku, COALESCE(barcode, ''), name, unit,
		        min_stock_millis, unit_cost_millis, default_location_id,
		        lot_number, expiry_date
		 FROM items WHERE id = ?;`,
		itemID,
	).Scan(
		&rec.ID,
		&rec.SKU,
		&rec.Barcode,
		&rec.Name,
		&rec.Unit,
		&minMillis,
		&costMillis,
		&defaultLocID,
		&rec.LotNumber,
		&rec.ExpiryDate,
	)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("item", fmt.Sprintf("%d", itemID))
	}
	if err != nil {
		return nil, fmt.Errorf("load item %d: %w", itemID, err)
	}
	rec.MinStockMillis = num.Millis(minMillis)
	rec.UnitCostMillis = num.Millis(costMillis)
	if defaultLocID.Valid {
		v := defaultLocID.Int64
		rec.DefaultLocationID = &v
	}
	return &rec, nil
}

func (s *Service) resolveLocationTx(ctx context.Context, tx *sql.Tx, locID *int64, locCode string, fallbackLocID *int64) (int64, string, error) {
	if locID != nil && *locID > 0 {
		var code string
		err := tx.QueryRowContext(ctx, `SELECT code FROM locations WHERE id = ?;`, *locID).Scan(&code)
		if err == sql.ErrNoRows {
			return 0, "", apperr.NotFound("location", fmt.Sprintf("%d", *locID))
		}
		if err != nil {
			return 0, "", err
		}
		return *locID, code, nil
	}

	cleanedCode := strings.TrimSpace(locCode)
	if parsed, ok := catalog.ParseLocationBarcode(cleanedCode); ok {
		cleanedCode = parsed
	}
	if cleanedCode != "" {
		var id int64
		var code string
		err := tx.QueryRowContext(ctx, `SELECT id, code FROM locations WHERE UPPER(code) = UPPER(?) LIMIT 1;`, cleanedCode).Scan(&id, &code)
		if err == sql.ErrNoRows {
			return 0, "", apperr.NotFound("location", cleanedCode)
		}
		if err != nil {
			return 0, "", err
		}
		return id, code, nil
	}

	if fallbackLocID != nil && *fallbackLocID > 0 {
		var code string
		if err := tx.QueryRowContext(ctx, `SELECT code FROM locations WHERE id = ?;`, *fallbackLocID).Scan(&code); err == nil {
			return *fallbackLocID, code, nil
		}
	}

	var mainID int64
	var mainCode string
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, code FROM locations ORDER BY CASE WHEN code = 'MAIN' THEN 0 ELSE 1 END, id ASC LIMIT 1;`,
	).Scan(&mainID, &mainCode)
	if err != nil {
		return 0, "", apperr.Validation("location_id", "no storage location available")
	}
	return mainID, mainCode, nil
}

func getStockBalanceTx(ctx context.Context, tx *sql.Tx, itemID, locationID int64) (num.Millis, error) {
	var millis int64
	err := tx.QueryRowContext(
		ctx,
		`SELECT quantity_millis FROM stock_levels WHERE item_id = ? AND location_id = ?;`,
		itemID, locationID,
	).Scan(&millis)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read stock_level (item=%d, loc=%d): %w", itemID, locationID, err)
	}
	return num.Millis(millis), nil
}

func getTotalItemStockTx(ctx context.Context, tx *sql.Tx, itemID int64) (num.Millis, error) {
	var total int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(SUM(quantity_millis), 0) FROM stock_levels WHERE item_id = ?;`,
		itemID,
	).Scan(&total); err != nil {
		return 0, err
	}
	return num.Millis(total), nil
}

func upsertStockBalanceTx(ctx context.Context, tx *sql.Tx, itemID, locationID int64, newBalance num.Millis, now string) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO stock_levels (item_id, location_id, quantity, quantity_millis, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(item_id, location_id) DO UPDATE SET
		     quantity = excluded.quantity,
		     quantity_millis = excluded.quantity_millis,
		     updated_at = excluded.updated_at;`,
		itemID,
		locationID,
		newBalance.ToFloat(),
		newBalance.Int64(),
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert stock_level (item=%d, loc=%d): %w", itemID, locationID, err)
	}
	return nil
}

func isNegativeStockAllowedTx(ctx context.Context, tx *sql.Tx, override *bool) bool {
	if override != nil {
		return *override
	}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = 'allow_negative_stock';`).Scan(&raw)
	if err != nil {
		return false
	}
	var allowed bool
	if err := json.Unmarshal([]byte(raw), &allowed); err != nil {
		return strings.EqualFold(strings.TrimSpace(raw), "true")
	}
	return allowed
}

type txInsertRow struct {
	ItemID             int64
	FromLocationID     *int64
	ToLocationID       *int64
	TxType             string
	QuantityMillis     num.Millis
	DeltaMillis        num.Millis
	BalanceAfterMillis num.Millis
	UnitCostMillis     num.Millis
	LotNumber          string
	ExpiryDate         string
	BatchID            string
	Reason             string
	Operator           string
	ReferenceTxID      *int64
	CreatedAt          string
}

func insertTransactionRowTx(ctx context.Context, tx *sql.Tx, row txInsertRow) (int64, error) {
	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO transactions (
			item_id, from_location_id, to_location_id, tx_type,
			quantity, quantity_millis, delta_millis, balance_after_millis,
			unit_cost_millis, lot_number, expiry_date, batch_id,
			reason, operator, reference_tx_id, undone, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?);`,
		row.ItemID,
		row.FromLocationID,
		row.ToLocationID,
		row.TxType,
		row.QuantityMillis.ToFloat(),
		row.QuantityMillis.Int64(),
		row.DeltaMillis.Int64(),
		row.BalanceAfterMillis.Int64(),
		row.UnitCostMillis.Int64(),
		strings.TrimSpace(row.LotNumber),
		strings.TrimSpace(row.ExpiryDate),
		strings.TrimSpace(row.BatchID),
		strings.TrimSpace(row.Reason),
		strings.TrimSpace(row.Operator),
		row.ReferenceTxID,
		row.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("insert ledger transaction: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read inserted transaction id: %w", err)
	}
	return id, nil
}

func (s *Service) getTransactionByIDInternal(ctx context.Context, id int64) (*models.Transaction, error) {
	var (
		t            models.Transaction
		fromLocID    sql.NullInt64
		toLocID      sql.NullInt64
		refTxID      sql.NullInt64
		qtyMillis    int64
		deltaMillis  int64
		balMillis    int64
		costMillis   int64
		undoneInt    int
	)

	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT
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
		 WHERE t.id = ?;`,
		id,
	).Scan(
		&t.ID, &t.ItemID, &t.ItemSKU, &t.ItemBarcode, &t.ItemName, &t.ItemUnit,
		&fromLocID, &t.FromLocationCode, &t.FromLocationName,
		&toLocID, &t.ToLocationCode, &t.ToLocationName,
		&t.TxType, &t.Quantity, &qtyMillis, &deltaMillis, &balMillis,
		&costMillis, &t.LotNumber, &t.ExpiryDate, &t.BatchID,
		&t.Reason, &t.Operator, &refTxID, &undoneInt, &t.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("transaction", fmt.Sprintf("%d", id))
	}
	if err != nil {
		return nil, fmt.Errorf("query transaction %d: %w", id, err)
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
	return &t, nil
}

func formatSignedDelta(d num.Millis) string {
	if d > 0 {
		return "+" + d.Format()
	}
	return d.Format()
}
