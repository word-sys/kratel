package settings

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
)

// Operator represents a shop staff member or technician profile for shared counter accountability.
type Operator struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	HasPIN    bool   `json:"has_pin"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}

// OperatorInput carries fields for creating or updating an Operator profile.
type OperatorInput struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	PIN      string `json:"pin,omitempty"`
	ClearPIN bool   `json:"clear_pin,omitempty"`
	Active   *bool  `json:"active,omitempty"`
}

// ListOperators returns all operator profiles ordered with active operators first.
func (s *Service) ListOperators(ctx context.Context) ([]Operator, error) {
	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT id, name, role, pin_hash, active, created_at
		 FROM operators
		 ORDER BY active DESC, id ASC;`,
	)
	if err != nil {
		return nil, fmt.Errorf("list operators: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []Operator
	for rows.Next() {
		var (
			op        Operator
			pinHash   string
			activeInt int
		)
		if err := rows.Scan(&op.ID, &op.Name, &op.Role, &pinHash, &activeInt, &op.CreatedAt); err != nil {
			return nil, err
		}
		op.HasPIN = strings.TrimSpace(pinHash) != ""
		op.Active = activeInt != 0
		out = append(out, op)
	}
	return out, nil
}

// GetOperatorByID retrieves a single Operator profile without exposing its PIN hash.
func (s *Service) GetOperatorByID(ctx context.Context, id int64) (*Operator, error) {
	var (
		op        Operator
		pinHash   string
		activeInt int
	)
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT id, name, role, pin_hash, active, created_at FROM operators WHERE id = ?;`,
		id,
	).Scan(&op.ID, &op.Name, &op.Role, &pinHash, &activeInt, &op.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("operator", fmt.Sprintf("%d", id))
	}
	if err != nil {
		return nil, fmt.Errorf("query operator %d: %w", id, err)
	}
	op.HasPIN = strings.TrimSpace(pinHash) != ""
	op.Active = activeInt != 0
	return &op, nil
}

// CreateOperator registers a new shop operator with an optional 4–12 character PIN.
func (s *Service) CreateOperator(ctx context.Context, in OperatorInput) (*Operator, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "operator name is required")
	}
	role := strings.TrimSpace(in.Role)
	if role == "" {
		role = "Technician"
	}

	pinHash := ""
	if pin := strings.TrimSpace(in.PIN); pin != "" {
		if len(pin) < 4 || len(pin) > 16 {
			return nil, apperr.Validation("pin", "operator PIN must be between 4 and 16 characters")
		}
		var err error
		pinHash, err = hashPIN(pin)
		if err != nil {
			return nil, err
		}
	}

	activeInt := 1
	if in.Active != nil && !*in.Active {
		activeInt = 0
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.store.DB().ExecContext(
		ctx,
		`INSERT INTO operators (name, role, pin_hash, active, created_at) VALUES (?, ?, ?, ?, ?);`,
		name, role, pinHash, activeInt, now,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, apperr.New(apperr.CodeDuplicateCode, fmt.Sprintf("operator %q already exists", name)).WithField("name")
		}
		return nil, fmt.Errorf("insert operator: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetOperatorByID(ctx, id)
}

// UpdateOperator updates an operator's name, role, active state, or optional PIN.
func (s *Service) UpdateOperator(ctx context.Context, id int64, in OperatorInput) (*Operator, error) {
	if _, err := s.GetOperatorByID(ctx, id); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("name", "operator name is required")
	}
	role := strings.TrimSpace(in.Role)
	if role == "" {
		role = "Technician"
	}

	var curPinHash string
	var curActive int
	if err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT pin_hash, active FROM operators WHERE id = ?;`,
		id,
	).Scan(&curPinHash, &curActive); err != nil {
		return nil, err
	}

	nextPinHash := curPinHash
	if in.ClearPIN {
		nextPinHash = ""
	} else if pin := strings.TrimSpace(in.PIN); pin != "" {
		if len(pin) < 4 || len(pin) > 16 {
			return nil, apperr.Validation("pin", "operator PIN must be between 4 and 16 characters")
		}
		var err error
		nextPinHash, err = hashPIN(pin)
		if err != nil {
			return nil, err
		}
	}

	nextActive := curActive
	if in.Active != nil {
		if *in.Active {
			nextActive = 1
		} else {
			nextActive = 0
		}
	}

	if _, err := s.store.DB().ExecContext(
		ctx,
		`UPDATE operators SET name = ?, role = ?, pin_hash = ?, active = ? WHERE id = ?;`,
		name, role, nextPinHash, nextActive, id,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, apperr.New(apperr.CodeDuplicateCode, fmt.Sprintf("operator %q already exists", name)).WithField("name")
		}
		return nil, fmt.Errorf("update operator %d: %w", id, err)
	}
	return s.GetOperatorByID(ctx, id)
}

// DeleteOperator deletes an operator profile while guaranteeing at least one active operator remains.
func (s *Service) DeleteOperator(ctx context.Context, id int64) error {
	op, err := s.GetOperatorByID(ctx, id)
	if err != nil {
		return err
	}
	if op.Active {
		var activeCount int
		if err := s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM operators WHERE active = 1;`).Scan(&activeCount); err != nil {
			return err
		}
		if activeCount <= 1 {
			return apperr.Validation("operator_id", "cannot delete the last active operator")
		}
	}
	if _, err := s.store.DB().ExecContext(ctx, `DELETE FROM operators WHERE id = ?;`, id); err != nil {
		return fmt.Errorf("delete operator %d: %w", id, err)
	}
	return nil
}

// VerifyOperatorPIN checks a counter operator's PIN (or succeeds immediately if the operator has no PIN set).
func (s *Service) VerifyOperatorPIN(ctx context.Context, id int64, pin string) (*Operator, error) {
	var (
		op        Operator
		pinHash   string
		activeInt int
	)
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT id, name, role, pin_hash, active, created_at FROM operators WHERE id = ?;`,
		id,
	).Scan(&op.ID, &op.Name, &op.Role, &pinHash, &activeInt, &op.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("operator", fmt.Sprintf("%d", id))
	}
	if err != nil {
		return nil, err
	}
	op.HasPIN = strings.TrimSpace(pinHash) != ""
	op.Active = activeInt != 0

	if !op.Active {
		return nil, apperr.Validation("operator_id", "operator profile is inactive")
	}
	if !op.HasPIN {
		return &op, nil
	}
	if !verifyPINHash(strings.TrimSpace(pin), pinHash) {
		return nil, apperr.ErrUnauthorizedPIN.WithDetail("operator_id", id)
	}
	return &op, nil
}

func hashPIN(pin string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate PIN salt: %w", err)
	}
	saltHex := hex.EncodeToString(salt)
	sum := sha256.Sum256([]byte(saltHex + ":" + pin))
	return saltHex + ":" + hex.EncodeToString(sum[:]), nil
}

func verifyPINHash(pin, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	expectedBytes, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	actual := sha256.Sum256([]byte(parts[0] + ":" + pin))
	return subtle.ConstantTimeCompare(actual[:], expectedBytes) == 1
}
