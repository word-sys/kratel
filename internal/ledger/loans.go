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

// LoanCheckOutInput describes checking out a returnable workshop tool, meter, or loaner board.
type LoanCheckOutInput struct {
	ItemID     int64  `json:"item_id,omitempty"`
	Code       string `json:"code,omitempty"`
	LocationID *int64 `json:"location_id,omitempty"`
	Quantity   string `json:"quantity"`
	Borrower   string `json:"borrower"`
	DueAt      string `json:"due_at,omitempty"` // Optional ISO YYYY-MM-DD
	Operator   string `json:"operator,omitempty"`
	Notes      string `json:"notes,omitempty"`
}

// CheckOutLoan atomically deducts the item from stock (recording an immutable OUT transaction)
// and creates an ACTIVE loan record assigned to the borrower.
func (s *Service) CheckOutLoan(ctx context.Context, in LoanCheckOutInput) (*models.Loan, error) {
	borrower := strings.TrimSpace(in.Borrower)
	if borrower == "" {
		return nil, apperr.Validation("borrower", "borrower name or bench identifier is required")
	}
	qtyStr := strings.TrimSpace(in.Quantity)
	if qtyStr == "" {
		qtyStr = "1"
	}
	qtyMillis, err := num.ParsePositiveMillis("quantity", qtyStr)
	if err != nil {
		return nil, err
	}

	dueAt := strings.TrimSpace(in.DueAt)
	if dueAt != "" {
		if _, parseErr := time.Parse("2006-01-02", dueAt); parseErr != nil {
			return nil, apperr.Validation("due_at", "due_at must be in YYYY-MM-DD format")
		}
	}

	op := strings.TrimSpace(in.Operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	var loanID int64
	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		item, err := s.resolveItemTx(ctx, tx, in.ItemID, in.Code)
		if err != nil {
			return err
		}
		locID, _, err := s.resolveOutSourceLocationTx(ctx, tx, item, in.LocationID, "")
		if err != nil {
			return err
		}

		now := time.Now().UTC().Format(time.RFC3339)
		reason := fmt.Sprintf("Tool check-out to %s", borrower)
		if strings.TrimSpace(in.Notes) != "" {
			reason += " (" + strings.TrimSpace(in.Notes) + ")"
		}

		outTxID, err := s.execOutTx(ctx, tx, item, MovementInput{
			ItemID:         item.ID,
			TxType:         TxOut,
			Quantity:       qtyMillis.Format(),
			FromLocationID: &locID,
			Reason:         reason,
			Operator:       op,
		}, now)
		if err != nil {
			return err
		}

		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO loans (
				item_id, location_id, quantity, quantity_millis,
				borrower, operator, notes, status, out_tx_id, checked_out_at, due_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'ACTIVE', ?, ?, ?);`,
			item.ID, locID, qtyMillis.ToFloat(), qtyMillis.Int64(),
			borrower, op, strings.TrimSpace(in.Notes), outTxID, now, dueAt,
		)
		if execErr != nil {
			return fmt.Errorf("insert loan: %w", execErr)
		}
		loanID, _ = res.LastInsertId()
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.GetLoanByID(ctx, loanID)
}

// ReturnLoan atomically returns a checked-out tool or item back into stock (recording an immutable IN transaction)
// and marks the loan RETURNED.
func (s *Service) ReturnLoan(ctx context.Context, loanID int64, returnLocationID *int64, operator, notes string) (*models.Loan, error) {
	op := strings.TrimSpace(operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var (
			itemID    int64
			origLocID int64
			qtyMillis int64
			borrower  string
			status    string
		)
		err := tx.QueryRowContext(
			ctx,
			`SELECT item_id, location_id, quantity_millis, borrower, status FROM loans WHERE id = ?;`,
			loanID,
		).Scan(&itemID, &origLocID, &qtyMillis, &borrower, &status)
		if err == sql.ErrNoRows {
			return apperr.NotFound("loan", fmt.Sprintf("%d", loanID))
		}
		if err != nil {
			return err
		}
		if status != "ACTIVE" {
			return apperr.Validation("status", fmt.Sprintf("loan #%d is already returned", loanID))
		}

		destLocID := origLocID
		if returnLocationID != nil && *returnLocationID > 0 {
			destLocID = *returnLocationID
		}

		item, err := s.resolveItemTx(ctx, tx, itemID, "")
		if err != nil {
			return err
		}

		now := time.Now().UTC().Format(time.RFC3339)
		reason := fmt.Sprintf("Tool returned by %s", borrower)
		if strings.TrimSpace(notes) != "" {
			reason += " (" + strings.TrimSpace(notes) + ")"
		}

		inTxID, err := s.execInTx(ctx, tx, item, MovementInput{
			ItemID:       item.ID,
			TxType:       TxIn,
			Quantity:     num.Millis(qtyMillis).Format(),
			ToLocationID: &destLocID,
			Reason:       reason,
			Operator:     op,
		}, now)
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(
			ctx,
			`UPDATE loans SET status = 'RETURNED', in_tx_id = ?, returned_at = ? WHERE id = ?;`,
			inTxID, now, loanID,
		); err != nil {
			return fmt.Errorf("mark loan returned: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetLoanByID(ctx, loanID)
}

// GetLoanByID retrieves a single Loan record by ID.
func (s *Service) GetLoanByID(ctx context.Context, id int64) (*models.Loan, error) {
	loans, err := s.queryLoans(ctx, "WHERE ln.id = ?", []interface{}{id})
	if err != nil {
		return nil, err
	}
	if len(loans) == 0 {
		return nil, apperr.NotFound("loan", fmt.Sprintf("%d", id))
	}
	return &loans[0], nil
}

// ListLoans lists tool check-outs filtered optionally by status ("ACTIVE", "RETURNED", or "" for all).
func (s *Service) ListLoans(ctx context.Context, statusFilter string) ([]models.Loan, error) {
	sf := strings.ToUpper(strings.TrimSpace(statusFilter))
	if sf == "ACTIVE" || sf == "RETURNED" {
		return s.queryLoans(ctx, "WHERE ln.status = ?", []interface{}{sf})
	}
	return s.queryLoans(ctx, "", nil)
}

func (s *Service) queryLoans(ctx context.Context, whereSQL string, args []interface{}) ([]models.Loan, error) {
	query := fmt.Sprintf(`
		SELECT
			ln.id, ln.item_id, i.sku, COALESCE(i.barcode, ''), i.name, i.unit,
			ln.location_id, l.code,
			ln.quantity, ln.quantity_millis,
			ln.borrower, ln.operator, ln.notes, ln.status,
			ln.out_tx_id, ln.in_tx_id,
			ln.checked_out_at, ln.due_at, ln.returned_at
		FROM loans ln
		JOIN items i ON i.id = ln.item_id
		JOIN locations l ON l.id = ln.location_id
		%s
		ORDER BY CASE WHEN ln.status = 'ACTIVE' THEN 0 ELSE 1 END, ln.id DESC;`, whereSQL)

	rows, err := s.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query loans: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	today := time.Now().UTC().Format("2006-01-02")
	var out []models.Loan
	for rows.Next() {
		var (
			ln        models.Loan
			qtyMillis int64
			outTxID   sql.NullInt64
			inTxID    sql.NullInt64
		)
		if err := rows.Scan(
			&ln.ID, &ln.ItemID, &ln.ItemSKU, &ln.ItemBarcode, &ln.ItemName, &ln.ItemUnit,
			&ln.LocationID, &ln.LocationCode,
			&ln.Quantity, &qtyMillis,
			&ln.Borrower, &ln.Operator, &ln.Notes, &ln.Status,
			&outTxID, &inTxID,
			&ln.CheckedOutAt, &ln.DueAt, &ln.ReturnedAt,
		); err != nil {
			return nil, err
		}
		ln.QuantityMillis = num.Millis(qtyMillis)
		ln.Quantity = ln.QuantityMillis.ToFloat()
		ln.QuantityText = ln.QuantityMillis.Format()
		if outTxID.Valid {
			v := outTxID.Int64
			ln.OutTxID = &v
		}
		if inTxID.Valid {
			v := inTxID.Int64
			ln.InTxID = &v
		}
		ln.IsOverdue = ln.Status == "ACTIVE" && ln.DueAt != "" && ln.DueAt < today
		out = append(out, ln)
	}
	return out, nil
}
