package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/models"
)

// AddItemAlias registers an alternate scannable barcode or supplier part number (EAN, Mouser/DigiKey/LCSC, internal QR)
// for an item, verifying that the code does not collide with any existing SKU, primary barcode, or alias.
func (s *Service) AddItemAlias(ctx context.Context, itemID int64, code string, aliasType models.AliasType, notes string) (*models.ItemAlias, error) {
	if _, err := s.GetItemByID(ctx, itemID); err != nil {
		return nil, err
	}

	cleanedCode := strings.TrimSpace(code)
	if cleanedCode == "" {
		return nil, apperr.Validation("code", "alias barcode or part code is required")
	}

	normType := normalizeAliasType(aliasType)
	now := time.Now().UTC().Format(time.RFC3339)

	var created models.ItemAlias
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.ensureCodeAvailableTx(ctx, tx, "code", cleanedCode, 0); err != nil {
			return err
		}

		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO item_aliases (item_id, code, alias_type, notes, created_at) VALUES (?, ?, ?, ?, ?);`,
			itemID,
			cleanedCode,
			string(normType),
			strings.TrimSpace(notes),
			now,
		)
		if execErr != nil {
			return fmt.Errorf("insert item alias: %w", execErr)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("read inserted alias id: %w", err)
		}
		created = models.ItemAlias{
			ID:        id,
			ItemID:    itemID,
			Code:      cleanedCode,
			AliasType: normType,
			Notes:     strings.TrimSpace(notes),
			CreatedAt: now,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// DeleteItemAlias removes an item alias by its ID.
func (s *Service) DeleteItemAlias(ctx context.Context, aliasID int64) error {
	res, err := s.store.DB().ExecContext(ctx, `DELETE FROM item_aliases WHERE id = ?;`, aliasID)
	if err != nil {
		return fmt.Errorf("delete item alias %d: %w", aliasID, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return apperr.NotFound("item_alias", fmt.Sprintf("%d", aliasID))
	}
	return nil
}

// ListItemAliases returns all registered alternate codes for an item.
func (s *Service) ListItemAliases(ctx context.Context, itemID int64) ([]models.ItemAlias, error) {
	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT id, item_id, code, alias_type, notes, created_at
		 FROM item_aliases
		 WHERE item_id = ?
		 ORDER BY id ASC;`,
		itemID,
	)
	if err != nil {
		return nil, fmt.Errorf("list aliases for item %d: %w", itemID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []models.ItemAlias
	for rows.Next() {
		var a models.ItemAlias
		var at string
		if err := rows.Scan(&a.ID, &a.ItemID, &a.Code, &at, &a.Notes, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan item alias: %w", err)
		}
		a.AliasType = models.AliasType(at)
		out = append(out, a)
	}
	return out, nil
}

// ResolveItemByCode resolves a scanned barcode, SKU, or alternate supplier/QR alias in sub-2ms.
// It returns the full Item dossier along with the match source ("BARCODE", "SKU", or "ALIAS:<TYPE>").
func (s *Service) ResolveItemByCode(ctx context.Context, rawCode string) (*models.Item, string, error) {
	code := strings.TrimSpace(rawCode)
	if code == "" {
		return nil, "", apperr.Validation("code", "scan code cannot be empty")
	}

	var (
		itemID      int64
		matchSource string
	)
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT id,
		        CASE
		            WHEN barcode IS NOT NULL AND UPPER(barcode) = UPPER(?) THEN 'BARCODE'
		            ELSE 'SKU'
		        END AS match_source
		 FROM items
		 WHERE (barcode IS NOT NULL AND UPPER(barcode) = UPPER(?)) OR UPPER(sku) = UPPER(?)
		 UNION ALL
		 SELECT item_id, 'ALIAS:' || alias_type AS match_source
		 FROM item_aliases
		 WHERE UPPER(code) = UPPER(?)
		 LIMIT 1;`,
		code, code, code, code,
	).Scan(&itemID, &matchSource)
	if err == sql.ErrNoRows {
		return nil, "", apperr.NotFound("item", code)
	}
	if err != nil {
		return nil, "", fmt.Errorf("resolve item by code %q: %w", code, err)
	}

	item, err := s.GetItemByID(ctx, itemID)
	if err != nil {
		return nil, "", err
	}
	return item, matchSource, nil
}

func normalizeAliasType(t models.AliasType) models.AliasType {
	switch models.AliasType(strings.ToUpper(strings.TrimSpace(string(t)))) {
	case models.AliasEAN:
		return models.AliasEAN
	case models.AliasSupplierPart:
		return models.AliasSupplierPart
	case models.AliasInternalQR:
		return models.AliasInternalQR
	case models.AliasLegacySKU:
		return models.AliasLegacySKU
	case models.AliasOther:
		return models.AliasOther
	default:
		return models.AliasEAN
	}
}
