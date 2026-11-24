package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/models"
	"github.com/word-sys/kratel/internal/num"
)

func init() {
	kitScanHook = (*Service).handleKitQuickScan
}

// KitComponentInput defines one required item and quantity inside a BOM/Repair Kit.
type KitComponentInput struct {
	ItemID   int64  `json:"item_id"`
	Quantity string `json:"quantity"`
	Notes    string `json:"notes"`
}

// KitInput defines fields for creating or updating a BOM/Repair Kit.
type KitInput struct {
	Code        string              `json:"code"`
	Barcode     string              `json:"barcode"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Category    string              `json:"category"`
	Components  []KitComponentInput `json:"components"`
}

// KitExecuteInput specifies how to execute a BOM/Repair Kit against inventory.
type KitExecuteInput struct {
	KitID      int64  `json:"kit_id"`
	Mode       string `json:"mode"`       // "OUT" (consume/issue kit) or "IN" (restock/disassemble kit)
	Multiplier int64  `json:"multiplier"` // Number of kit bundles (default 1)
	LocationID *int64 `json:"location_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Operator   string `json:"operator,omitempty"`
}

// KitExecuteResult summarizes an atomic multi-item kit execution with its BatchID for one-click Batch Undo.
type KitExecuteResult struct {
	Kit          *models.Kit          `json:"kit"`
	Mode         string               `json:"mode"`
	Multiplier   int64                `json:"multiplier"`
	BatchID      string               `json:"batch_id"`
	Transactions []models.Transaction `json:"transactions"`
}

// CreateKit creates a new BOM / Repair Kit with its required component items.
func (s *Service) CreateKit(ctx context.Context, in KitInput) (*models.Kit, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "kit name is required")
	}
	if len(in.Components) == 0 {
		return nil, apperr.Validation("components", "kit must contain at least one component item")
	}

	var createdID int64
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		code := strings.TrimSpace(in.Code)
		if code == "" {
			var maxID int64
			_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM kits;`).Scan(&maxID)
			code = fmt.Sprintf("KIT-%04d", maxID+1)
		}
		barcode := strings.TrimSpace(in.Barcode)
		if barcode == "" {
			barcode = catalog.KitBarcodePrefix + strings.ToUpper(code)
		}

		now := time.Now().UTC().Format(time.RFC3339)
		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO kits (code, barcode, name, description, category, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?);`,
			code,
			barcode,
			name,
			strings.TrimSpace(in.Description),
			strings.TrimSpace(in.Category),
			now,
			now,
		)
		if execErr != nil {
			if strings.Contains(strings.ToLower(execErr.Error()), "unique") {
				return apperr.New(apperr.CodeDuplicateCode, fmt.Sprintf("kit code %q or barcode %q already exists", code, barcode))
			}
			return fmt.Errorf("insert kit: %w", execErr)
		}
		kitID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		createdID = kitID

		return replaceKitComponentsTx(ctx, tx, kitID, in.Components)
	})
	if err != nil {
		return nil, err
	}
	return s.GetKitByID(ctx, createdID)
}

// UpdateKit updates a kit's metadata and replaces its component list atomically.
func (s *Service) UpdateKit(ctx context.Context, id int64, in KitInput) (*models.Kit, error) {
	existing, err := s.GetKitByID(ctx, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "kit name is required")
	}
	if len(in.Components) == 0 {
		return nil, apperr.Validation("components", "kit must contain at least one component item")
	}

	code := strings.TrimSpace(in.Code)
	if code == "" {
		code = existing.Code
	}
	barcode := strings.TrimSpace(in.Barcode)
	if barcode == "" {
		barcode = existing.Barcode
	}

	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339)
		if _, execErr := tx.ExecContext(
			ctx,
			`UPDATE kits SET code = ?, barcode = ?, name = ?, description = ?, category = ?, updated_at = ? WHERE id = ?;`,
			code, barcode, name, strings.TrimSpace(in.Description), strings.TrimSpace(in.Category), now, id,
		); execErr != nil {
			if strings.Contains(strings.ToLower(execErr.Error()), "unique") {
				return apperr.New(apperr.CodeDuplicateCode, fmt.Sprintf("kit code %q or barcode %q already exists", code, barcode))
			}
			return fmt.Errorf("update kit: %w", execErr)
		}
		return replaceKitComponentsTx(ctx, tx, id, in.Components)
	})
	if err != nil {
		return nil, err
	}
	return s.GetKitByID(ctx, id)
}

// DeleteKit deletes a BOM/Repair Kit definition (historical ledger transactions remain intact).
func (s *Service) DeleteKit(ctx context.Context, id int64) error {
	res, err := s.store.DB().ExecContext(ctx, `DELETE FROM kits WHERE id = ?;`, id)
	if err != nil {
		return fmt.Errorf("delete kit %d: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return apperr.NotFound("kit", fmt.Sprintf("%d", id))
	}
	return nil
}

// GetKitByID retrieves a Kit with live component availability, MaxBuildable count, and total kit cost.
func (s *Service) GetKitByID(ctx context.Context, id int64) (*models.Kit, error) {
	var k models.Kit
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT id, code, COALESCE(barcode, ''), name, description, category, created_at, updated_at
		 FROM kits WHERE id = ?;`,
		id,
	).Scan(&k.ID, &k.Code, &k.Barcode, &k.Name, &k.Description, &k.Category, &k.CreatedAt, &k.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("kit", fmt.Sprintf("%d", id))
	}
	if err != nil {
		return nil, fmt.Errorf("query kit %d: %w", id, err)
	}

	if err := s.hydrateKitComponents(ctx, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// ListKits returns all defined BOM / Repair Kits enriched with live buildability metrics.
func (s *Service) ListKits(ctx context.Context) ([]models.Kit, error) {
	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT id, code, COALESCE(barcode, ''), name, description, category, created_at, updated_at
		 FROM kits ORDER BY name COLLATE NOCASE ASC, id ASC;`,
	)
	if err != nil {
		return nil, fmt.Errorf("list kits: %w", err)
	}

	var kits []models.Kit
	for rows.Next() {
		var k models.Kit
		if err := rows.Scan(&k.ID, &k.Code, &k.Barcode, &k.Name, &k.Description, &k.Category, &k.CreatedAt, &k.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		kits = append(kits, k)
	}
	_ = rows.Close()

	for i := range kits {
		if err := s.hydrateKitComponents(ctx, &kits[i]); err != nil {
			return nil, err
		}
	}
	return kits, nil
}

// ExecuteKit atomically deducts (OUT) or restocks (IN) every component of a BOM/Repair Kit
// under a shared batch_id so the entire operation can be undone in one click via UndoBatch.
func (s *Service) ExecuteKit(ctx context.Context, in KitExecuteInput) (*KitExecuteResult, error) {
	kit, err := s.GetKitByID(ctx, in.KitID)
	if err != nil {
		return nil, err
	}
	if len(kit.Components) == 0 {
		return nil, apperr.Validation("kit_id", "kit has no components to execute")
	}

	mode := strings.ToUpper(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = TxOut
	}
	if mode != TxOut && mode != TxIn {
		return nil, apperr.Validation("mode", "kit execution mode must be OUT or IN")
	}

	mult := in.Multiplier
	if mult <= 0 {
		mult = 1
	}

	op := strings.TrimSpace(in.Operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	batchID := fmt.Sprintf("KIT-%d-%d", kit.ID, time.Now().UTC().UnixNano())
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = fmt.Sprintf("Kit %s (%s x%d)", mode, kit.Code, mult)
	}

	var createdTxIDs []int64
	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339)
		for _, comp := range kit.Components {
			itemRec, err := s.resolveItemTx(ctx, tx, comp.ItemID, "")
			if err != nil {
				return err
			}
			totalQty := num.Millis(comp.QuantityMillis.Int64() * mult)
			mov := MovementInput{
				ItemID:   comp.ItemID,
				TxType:   mode,
				Quantity: totalQty.Format(),
				BatchID:  batchID,
				Reason:   reason,
				Operator: op,
			}
			var txID int64
			if mode == TxOut {
				mov.FromLocationID = in.LocationID
				txID, err = s.execOutTx(ctx, tx, itemRec, mov, now)
			} else {
				mov.ToLocationID = in.LocationID
				txID, err = s.execInTx(ctx, tx, itemRec, mov, now)
			}
			if err != nil {
				return err
			}
			createdTxIDs = append(createdTxIDs, txID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	txViews := make([]models.Transaction, 0, len(createdTxIDs))
	for _, id := range createdTxIDs {
		v, err := s.getTransactionByIDInternal(ctx, id)
		if err != nil {
			return nil, err
		}
		txViews = append(txViews, *v)
	}

	updatedKit, err := s.GetKitByID(ctx, kit.ID)
	if err != nil {
		return nil, err
	}

	return &KitExecuteResult{
		Kit:          updatedKit,
		Mode:         mode,
		Multiplier:   mult,
		BatchID:      batchID,
		Transactions: txViews,
	}, nil
}

func (s *Service) handleKitQuickScan(ctx context.Context, code string, in QuickScanInput) (*QuickScanResult, bool, error) {
	var kitID int64
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT id FROM kits WHERE UPPER(code) = UPPER(?) OR (barcode IS NOT NULL AND UPPER(barcode) = UPPER(?)) LIMIT 1;`,
		code, code,
	).Scan(&kitID)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	mode := strings.ToUpper(strings.TrimSpace(in.Mode))
	if mode == ScanModeLookup {
		kit, err := s.GetKitByID(ctx, kitID)
		if err != nil {
			return nil, true, err
		}
		return &QuickScanResult{
			Action:      ScanActionKitExecuted,
			Mode:        mode,
			ScannedCode: code,
			MatchSource: "KIT_BARCODE",
			ExecutedKit: kit,
		}, true, nil
	}

	execMode := TxOut
	if mode == ScanModeIn {
		execMode = TxIn
	}
	mult := int64(1)
	if parsed, err := num.ParsePositiveMillis("quantity", in.Quantity); err == nil && parsed.WholeUnits() > 0 {
		mult = parsed.WholeUnits()
	}

	res, err := s.ExecuteKit(ctx, KitExecuteInput{
		KitID:      kitID,
		Mode:       execMode,
		Multiplier: mult,
		LocationID: in.LocationID,
		Reason:     in.Reason,
		Operator:   in.Operator,
	})
	if err != nil {
		return nil, true, err
	}

	return &QuickScanResult{
		Action:      ScanActionKitExecuted,
		Mode:        execMode,
		ScannedCode: code,
		MatchSource: "KIT_BARCODE",
		ExecutedKit: res.Kit,
		UndoBatchID: res.BatchID,
	}, true, nil
}

func replaceKitComponentsTx(ctx context.Context, tx *sql.Tx, kitID int64, components []KitComponentInput) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM kit_items WHERE kit_id = ?;`, kitID); err != nil {
		return fmt.Errorf("clear kit components: %w", err)
	}
	seen := make(map[int64]bool, len(components))
	for _, c := range components {
		if c.ItemID <= 0 {
			return apperr.Validation("components", "component item_id is required")
		}
		if seen[c.ItemID] {
			return apperr.Validation("components", fmt.Sprintf("duplicate component item_id %d in kit", c.ItemID))
		}
		seen[c.ItemID] = true

		qtyMillis, err := num.ParsePositiveMillis("quantity", c.Quantity)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO kit_items (kit_id, item_id, quantity, quantity_millis, notes) VALUES (?, ?, ?, ?, ?);`,
			kitID, c.ItemID, qtyMillis.ToFloat(), qtyMillis.Int64(), strings.TrimSpace(c.Notes),
		); err != nil {
			return fmt.Errorf("insert kit component item %d: %w", c.ItemID, err)
		}
	}
	return nil
}

func (s *Service) hydrateKitComponents(ctx context.Context, k *models.Kit) error {
	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT
			ki.id, ki.kit_id, ki.item_id, i.sku, i.name, i.unit,
			ki.quantity, ki.quantity_millis, ki.notes,
			i.unit_cost_millis,
			COALESCE((SELECT SUM(sl.quantity_millis) FROM stock_levels sl WHERE sl.item_id = ki.item_id), 0) AS avail_millis
		 FROM kit_items ki
		 JOIN items i ON i.id = ki.item_id
		 WHERE ki.kit_id = ?
		 ORDER BY ki.id ASC;`,
		k.ID,
	)
	if err != nil {
		return fmt.Errorf("query kit components: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	k.Components = []models.KitComponent{}
	var (
		totalCost num.Millis
		minBuild  int64 = -1
	)

	for rows.Next() {
		var (
			c          models.KitComponent
			qtyMillis  int64
			costMillis int64
			avail      int64
		)
		if err := rows.Scan(
			&c.ID, &c.KitID, &c.ItemID, &c.ItemSKU, &c.ItemName, &c.ItemUnit,
			&c.Quantity, &qtyMillis, &c.Notes, &costMillis, &avail,
		); err != nil {
			return err
		}
		c.QuantityMillis = num.Millis(qtyMillis)
		c.Quantity = c.QuantityMillis.ToFloat()
		c.QuantityText = c.QuantityMillis.Format()
		c.AvailableMillis = num.Millis(avail)
		c.AvailableText = c.AvailableMillis.Format()
		c.Sufficient = c.AvailableMillis >= c.QuantityMillis

		lineCost := num.LineValuationSafe(c.QuantityMillis, num.Millis(costMillis))
		totalCost = totalCost.Add(lineCost)

		var buildable int64
		if c.QuantityMillis > 0 && c.AvailableMillis > 0 {
			buildable = c.AvailableMillis.Int64() / c.QuantityMillis.Int64()
		}
		if minBuild == -1 || buildable < minBuild {
			minBuild = buildable
		}
		k.Components = append(k.Components, c)
	}

	if minBuild < 0 {
		minBuild = 0
	}
	k.MaxBuildable = minBuild
	k.TotalCostMillis = totalCost
	k.TotalCostText = totalCost.FormatFixed(2)
	return nil
}
