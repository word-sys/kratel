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

// SupplierInput carries fields for creating or updating a Supplier record.
type SupplierInput struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	Notes   string `json:"notes"`
}

// CreateSupplier validates and inserts a new Supplier into the directory.
func (s *Service) CreateSupplier(ctx context.Context, in SupplierInput) (*models.Supplier, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "supplier name is required")
	}

	code := strings.TrimSpace(in.Code)
	var codeParam interface{}
	if code != "" {
		codeParam = code
	}

	var createdID int64
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if code != "" {
			if err := ensureSupplierCodeAvailableTx(ctx, tx, code, 0); err != nil {
				return err
			}
		}
		now := time.Now().UTC().Format(time.RFC3339)
		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO suppliers (code, name, contact, email, phone, notes, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
			codeParam,
			name,
			strings.TrimSpace(in.Contact),
			strings.TrimSpace(in.Email),
			strings.TrimSpace(in.Phone),
			strings.TrimSpace(in.Notes),
			now,
			now,
		)
		if execErr != nil {
			return fmt.Errorf("insert supplier: %w", execErr)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("read inserted supplier id: %w", err)
		}
		createdID = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetSupplierByID(ctx, createdID)
}

// UpdateSupplier updates an existing Supplier record.
func (s *Service) UpdateSupplier(ctx context.Context, id int64, in SupplierInput) (*models.Supplier, error) {
	if _, err := s.GetSupplierByID(ctx, id); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "supplier name is required")
	}

	code := strings.TrimSpace(in.Code)
	var codeParam interface{}
	if code != "" {
		codeParam = code
	}

	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if code != "" {
			if err := ensureSupplierCodeAvailableTx(ctx, tx, code, id); err != nil {
				return err
			}
		}
		now := time.Now().UTC().Format(time.RFC3339)
		_, execErr := tx.ExecContext(
			ctx,
			`UPDATE suppliers
			 SET code = ?, name = ?, contact = ?, email = ?, phone = ?, notes = ?, updated_at = ?
			 WHERE id = ?;`,
			codeParam,
			name,
			strings.TrimSpace(in.Contact),
			strings.TrimSpace(in.Email),
			strings.TrimSpace(in.Phone),
			strings.TrimSpace(in.Notes),
			now,
			id,
		)
		return execErr
	})
	if err != nil {
		return nil, err
	}
	return s.GetSupplierByID(ctx, id)
}

// DeleteSupplier removes a Supplier from the directory (linked items have supplier_id set to NULL).
func (s *Service) DeleteSupplier(ctx context.Context, id int64) error {
	if _, err := s.GetSupplierByID(ctx, id); err != nil {
		return err
	}
	if _, err := s.store.DB().ExecContext(ctx, `DELETE FROM suppliers WHERE id = ?;`, id); err != nil {
		return fmt.Errorf("delete supplier %d: %w", id, err)
	}
	return nil
}

// GetSupplierByID retrieves a Supplier by its ID along with its active item count.
func (s *Service) GetSupplierByID(ctx context.Context, id int64) (*models.Supplier, error) {
	var sup models.Supplier
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT
			s.id, COALESCE(s.code, ''), s.name, s.contact, s.email, s.phone, s.notes,
			s.created_at, s.updated_at,
			(SELECT COUNT(*) FROM items i WHERE i.supplier_id = s.id AND i.archived = 0) AS item_count
		 FROM suppliers s
		 WHERE s.id = ?;`,
		id,
	).Scan(
		&sup.ID,
		&sup.Code,
		&sup.Name,
		&sup.Contact,
		&sup.Email,
		&sup.Phone,
		&sup.Notes,
		&sup.CreatedAt,
		&sup.UpdatedAt,
		&sup.ItemCount,
	)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("supplier", fmt.Sprintf("%d", id))
	}
	if err != nil {
		return nil, fmt.Errorf("query supplier %d: %w", id, err)
	}
	return &sup, nil
}

// ListSuppliers returns all Suppliers ordered by name along with their linked item counts.
func (s *Service) ListSuppliers(ctx context.Context) ([]models.Supplier, error) {
	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT
			s.id, COALESCE(s.code, ''), s.name, s.contact, s.email, s.phone, s.notes,
			s.created_at, s.updated_at,
			COUNT(CASE WHEN i.archived = 0 THEN i.id END) AS item_count
		 FROM suppliers s
		 LEFT JOIN items i ON i.supplier_id = s.id
		 GROUP BY s.id
		 ORDER BY s.name COLLATE NOCASE ASC, s.id ASC;`,
	)
	if err != nil {
		return nil, fmt.Errorf("list suppliers: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []models.Supplier
	for rows.Next() {
		var sup models.Supplier
		if err := rows.Scan(
			&sup.ID,
			&sup.Code,
			&sup.Name,
			&sup.Contact,
			&sup.Email,
			&sup.Phone,
			&sup.Notes,
			&sup.CreatedAt,
			&sup.UpdatedAt,
			&sup.ItemCount,
		); err != nil {
			return nil, fmt.Errorf("scan supplier row: %w", err)
		}
		out = append(out, sup)
	}
	return out, nil
}

// EnsureSupplierByName finds a supplier by case-insensitive name or creates it.
func (s *Service) EnsureSupplierByName(ctx context.Context, rawName string) (*models.Supplier, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return nil, nil
	}
	var existingID int64
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT id FROM suppliers WHERE LOWER(name) = LOWER(?) LIMIT 1;`,
		name,
	).Scan(&existingID)
	if err == nil {
		return s.GetSupplierByID(ctx, existingID)
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("lookup supplier by name %q: %w", name, err)
	}
	return s.CreateSupplier(ctx, SupplierInput{Name: name})
}

func ensureSupplierCodeAvailableTx(ctx context.Context, tx *sql.Tx, code string, excludeID int64) error {
	var existingID int64
	err := tx.QueryRowContext(
		ctx,
		`SELECT id FROM suppliers WHERE code IS NOT NULL AND UPPER(code) = UPPER(?) AND id != ? LIMIT 1;`,
		code, excludeID,
	).Scan(&existingID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check supplier code uniqueness: %w", err)
	}
	return apperr.New(apperr.CodeDuplicateCode, fmt.Sprintf("supplier code %q already exists", code)).
		WithField("code")
}
