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

// LocationInput carries fields for creating or updating a hierarchical storage Location.
type LocationInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
	Notes    string `json:"notes"`
}

// CreateLocation validates and inserts a new storage Location (bin, shelf, rack, or room).
func (s *Service) CreateLocation(ctx context.Context, in LocationInput) (*models.Location, error) {
	code := strings.TrimSpace(in.Code)
	if code == "" {
		return nil, apperr.Validation("code", "location code is required")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = code
	}

	var createdID int64
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := ensureLocationCodeAvailableTx(ctx, tx, code, 0); err != nil {
			return err
		}
		if in.ParentID != nil && *in.ParentID > 0 {
			if err := ensureLocationExistsTx(ctx, tx, *in.ParentID); err != nil {
				return err
			}
		} else {
			in.ParentID = nil
		}

		now := time.Now().UTC().Format(time.RFC3339)
		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO locations (code, name, parent_id, notes, created_at) VALUES (?, ?, ?, ?, ?);`,
			code,
			name,
			in.ParentID,
			strings.TrimSpace(in.Notes),
			now,
		)
		if execErr != nil {
			return fmt.Errorf("insert location: %w", execErr)
		}
		id, idErr := res.LastInsertId()
		if idErr != nil {
			return fmt.Errorf("read inserted location id: %w", idErr)
		}
		createdID = id
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.GetLocationByID(ctx, createdID)
}

// UpdateLocation validates and updates an existing Location, enforcing ancestor cycle detection
// so a location can never be parented under itself or one of its own descendants.
func (s *Service) UpdateLocation(ctx context.Context, id int64, in LocationInput) (*models.Location, error) {
	existing, err := s.GetLocationByID(ctx, id)
	if err != nil {
		return nil, err
	}

	code := strings.TrimSpace(in.Code)
	if code == "" {
		code = existing.Code
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = existing.Name
	}

	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := ensureLocationCodeAvailableTx(ctx, tx, code, id); err != nil {
			return err
		}
		if in.ParentID != nil && *in.ParentID > 0 {
			if err := detectLocationCycleTx(ctx, tx, id, *in.ParentID); err != nil {
				return err
			}
		} else {
			in.ParentID = nil
		}

		_, execErr := tx.ExecContext(
			ctx,
			`UPDATE locations SET code = ?, name = ?, parent_id = ?, notes = ? WHERE id = ?;`,
			code,
			name,
			in.ParentID,
			strings.TrimSpace(in.Notes),
			id,
		)
		return execErr
	})
	if err != nil {
		return nil, err
	}

	return s.GetLocationByID(ctx, id)
}

// DeleteLocation removes a Location only if it has no child locations, no non-zero stock levels,
// and no historical ledger transactions.
func (s *Service) DeleteLocation(ctx context.Context, id int64) error {
	if _, err := s.GetLocationByID(ctx, id); err != nil {
		return err
	}

	var childCount, stockCount, txCount int
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT
			(SELECT COUNT(*) FROM locations WHERE parent_id = ?),
			(SELECT COUNT(*) FROM stock_levels WHERE location_id = ? AND quantity_millis != 0),
			(SELECT COUNT(*) FROM transactions WHERE from_location_id = ? OR to_location_id = ?);`,
		id, id, id, id,
	).Scan(&childCount, &stockCount, &txCount)
	if err != nil {
		return fmt.Errorf("check location dependencies: %w", err)
	}

	if childCount > 0 || stockCount > 0 || txCount > 0 {
		return apperr.ErrLocationInUse.
			WithDetail("location_id", id).
			WithDetail("child_locations", childCount).
			WithDetail("active_stock_items", stockCount).
			WithDetail("transactions", txCount)
	}

	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM stock_levels WHERE location_id = ?;`, id); err != nil {
			return fmt.Errorf("clean zero stock_levels for location %d: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM locations WHERE id = ?;`, id); err != nil {
			return fmt.Errorf("delete location %d: %w", id, err)
		}
		return nil
	})
}

// GetLocationByID retrieves a Location by ID with computed breadcrumb and stock summary.
func (s *Service) GetLocationByID(ctx context.Context, id int64) (*models.Location, error) {
	all, err := s.ListLocations(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, apperr.NotFound("location", fmt.Sprintf("%d", id))
}

// GetLocationByCode retrieves a Location by its code (case-insensitive) or LOC:<CODE> barcode.
func (s *Service) GetLocationByCode(ctx context.Context, rawCode string) (*models.Location, error) {
	code := strings.TrimSpace(rawCode)
	if parsed, ok := ParseLocationBarcode(code); ok {
		code = parsed
	}
	if code == "" {
		return nil, apperr.Validation("code", "location code cannot be empty")
	}

	all, err := s.ListLocations(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if strings.EqualFold(all[i].Code, code) {
			return &all[i], nil
		}
	}
	return nil, apperr.NotFound("location", code)
}

// EnsureLocationByCode returns an existing location matching code (case-insensitive) or creates it.
func (s *Service) EnsureLocationByCode(ctx context.Context, code, name string) (*models.Location, error) {
	cleaned := strings.TrimSpace(code)
	if cleaned == "" {
		cleaned = "MAIN"
	}
	loc, err := s.GetLocationByCode(ctx, cleaned)
	if err == nil {
		return loc, nil
	}
	if !apperr.HasCode(err, apperr.CodeNotFound) {
		return nil, err
	}
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = cleaned
	}
	return s.CreateLocation(ctx, LocationInput{
		Code: cleaned,
		Name: displayName,
	})
}

func ensureLocationCodeAvailableTx(ctx context.Context, tx *sql.Tx, code string, excludeID int64) error {
	var existingID int64
	err := tx.QueryRowContext(
		ctx,
		`SELECT id FROM locations WHERE UPPER(code) = UPPER(?) AND id != ? LIMIT 1;`,
		code, excludeID,
	).Scan(&existingID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check location code uniqueness: %w", err)
	}
	return apperr.New(apperr.CodeDuplicateCode, fmt.Sprintf("location code %q already exists", code)).
		WithField("code").
		WithDetail("conflict_location_id", existingID)
}

func ensureLocationExistsTx(ctx context.Context, tx *sql.Tx, id int64) error {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM locations WHERE id = ?;`, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return apperr.NotFound("parent_location", fmt.Sprintf("%d", id)).WithField("parent_id")
	}
	return err
}

func detectLocationCycleTx(ctx context.Context, tx *sql.Tx, locationID, newParentID int64) error {
	if locationID > 0 && locationID == newParentID {
		return apperr.New(apperr.CodeLocationCycle, "a location cannot be its own parent").WithField("parent_id")
	}
	if err := ensureLocationExistsTx(ctx, tx, newParentID); err != nil {
		return err
	}

	visited := make(map[int64]bool)
	if locationID > 0 {
		visited[locationID] = true
	}

	cur := newParentID
	for cur > 0 {
		if visited[cur] {
			return apperr.New(apperr.CodeLocationCycle, "setting this parent would create a cycle in the location hierarchy").
				WithField("parent_id")
		}
		visited[cur] = true

		var nextParent sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT parent_id FROM locations WHERE id = ?;`, cur).Scan(&nextParent)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return fmt.Errorf("traverse location ancestors: %w", err)
		}
		if !nextParent.Valid {
			break
		}
		cur = nextParent.Int64
	}
	return nil
}
