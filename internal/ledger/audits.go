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

// AuditSessionInput defines parameters to open a physical cycle-count / stocktake session.
type AuditSessionInput struct {
	Name       string `json:"name"`
	LocationID *int64 `json:"location_id,omitempty"`
	Operator   string `json:"operator"`
	Notes      string `json:"notes"`
}

// AuditCountInput records a scanned or manually entered physical count inside an open AuditSession.
type AuditCountInput struct {
	ItemID        int64  `json:"item_id,omitempty"`
	Code          string `json:"code,omitempty"`
	LocationID    *int64 `json:"location_id,omitempty"`
	Quantity      string `json:"quantity"`
	IncrementMode bool   `json:"increment_mode"` // true: add Quantity to counted_millis (barcode tally); false: set exact count
	Notes         string `json:"notes,omitempty"`
}

// AuditCommitResult reports the ADJUST transactions posted when committing a physical stocktake.
type AuditCommitResult struct {
	Session          *models.AuditSession `json:"session"`
	BatchID          string               `json:"batch_id"`
	AdjustmentsCount int                  `json:"adjustments_count"`
	Transactions     []models.Transaction `json:"transactions"`
}

// CreateAuditSession opens a new physical cycle-count session and snapshots current expected
// stock_levels for the target location (or entire shop if LocationID is nil).
func (s *Service) CreateAuditSession(ctx context.Context, in AuditSessionInput) (*models.AuditSession, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Cycle Count " + time.Now().UTC().Format("2006-01-02")
	}
	op := strings.TrimSpace(in.Operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	var sessionID int64
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var maxID int64
		_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM audit_sessions;`).Scan(&maxID)
		code := fmt.Sprintf("AUD-%04d", maxID+1)
		now := time.Now().UTC().Format(time.RFC3339)

		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO audit_sessions (code, name, location_id, status, operator, notes, created_at)
			 VALUES (?, ?, ?, 'OPEN', ?, ?, ?);`,
			code, name, in.LocationID, op, strings.TrimSpace(in.Notes), now,
		)
		if execErr != nil {
			return fmt.Errorf("insert audit_session: %w", execErr)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		sessionID = id

		// Pre-populate audit_lines from current stock_levels in scope
		var seedQuery string
		var seedArgs []interface{}
		if in.LocationID != nil && *in.LocationID > 0 {
			seedQuery = `
				INSERT INTO audit_lines (session_id, item_id, location_id, expected_millis, counted_millis, counted, notes, updated_at)
				SELECT ?, sl.item_id, sl.location_id, sl.quantity_millis, 0, 0, '', ?
				FROM stock_levels sl
				JOIN items i ON i.id = sl.item_id
				WHERE i.archived = 0 AND sl.location_id = ? AND sl.quantity_millis != 0;`
			seedArgs = []interface{}{sessionID, now, *in.LocationID}
		} else {
			seedQuery = `
				INSERT INTO audit_lines (session_id, item_id, location_id, expected_millis, counted_millis, counted, notes, updated_at)
				SELECT ?, sl.item_id, sl.location_id, sl.quantity_millis, 0, 0, '', ?
				FROM stock_levels sl
				JOIN items i ON i.id = sl.item_id
				WHERE i.archived = 0 AND sl.quantity_millis != 0;`
			seedArgs = []interface{}{sessionID, now}
		}

		if _, execErr := tx.ExecContext(ctx, seedQuery, seedArgs...); execErr != nil {
			return fmt.Errorf("seed audit_lines: %w", execErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetAuditSession(ctx, sessionID)
}

// RecordAuditCount tallies or sets a physical item count inside an OPEN stocktake session.
// If an unexpected item is scanned in a bin where it had 0 expected stock, a new audit line is automatically created.
func (s *Service) RecordAuditCount(ctx context.Context, sessionID int64, in AuditCountInput) (*models.AuditLine, error) {
	qtyStr := strings.TrimSpace(in.Quantity)
	if qtyStr == "" {
		qtyStr = "1"
	}
	qtyMillis, err := num.ParseNonNegativeMillis("quantity", qtyStr)
	if err != nil {
		return nil, err
	}

	var lineID int64
	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var status string
		var sessionLocID sql.NullInt64
		if err := tx.QueryRowContext(
			ctx,
			`SELECT status, location_id FROM audit_sessions WHERE id = ?;`,
			sessionID,
		).Scan(&status, &sessionLocID); err == sql.ErrNoRows {
			return apperr.NotFound("audit_session", fmt.Sprintf("%d", sessionID))
		} else if err != nil {
			return err
		}

		if status != "OPEN" {
			return apperr.Validation("status", fmt.Sprintf("audit session #%d is %s and cannot be modified", sessionID, status))
		}

		item, err := s.resolveItemTx(ctx, tx, in.ItemID, in.Code)
		if err != nil {
			return err
		}

		targetLocPtr := in.LocationID
		if (targetLocPtr == nil || *targetLocPtr <= 0) && sessionLocID.Valid {
			v := sessionLocID.Int64
			targetLocPtr = &v
		}
		locID, _, err := s.resolveLocationTx(ctx, tx, targetLocPtr, "", item.DefaultLocationID)
		if err != nil {
			return err
		}

		now := time.Now().UTC().Format(time.RFC3339)

		var existingLineID int64
		var prevCounted int64
		err = tx.QueryRowContext(
			ctx,
			`SELECT id, counted_millis FROM audit_lines WHERE session_id = ? AND item_id = ? AND location_id = ?;`,
			sessionID, item.ID, locID,
		).Scan(&existingLineID, &prevCounted)

		if err == sql.ErrNoRows {
			expectedBal, balErr := getStockBalanceTx(ctx, tx, item.ID, locID)
			if balErr != nil {
				return balErr
			}
			res, insErr := tx.ExecContext(
				ctx,
				`INSERT INTO audit_lines (session_id, item_id, location_id, expected_millis, counted_millis, counted, notes, updated_at)
				 VALUES (?, ?, ?, ?, ?, 1, ?, ?);`,
				sessionID, item.ID, locID, expectedBal.Int64(), qtyMillis.Int64(), strings.TrimSpace(in.Notes), now,
			)
			if insErr != nil {
				return fmt.Errorf("insert unexpected audit line: %w", insErr)
			}
			lineID, _ = res.LastInsertId()
			return nil
		}
		if err != nil {
			return err
		}

		newCounted := qtyMillis
		if in.IncrementMode {
			newCounted, err = num.Millis(prevCounted).CheckedAdd(qtyMillis)
			if err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(
			ctx,
			`UPDATE audit_lines SET counted_millis = ?, counted = 1, notes = CASE WHEN ? != '' THEN ? ELSE notes END, updated_at = ?
			 WHERE id = ?;`,
			newCounted.Int64(), strings.TrimSpace(in.Notes), strings.TrimSpace(in.Notes), now, existingLineID,
		); err != nil {
			return fmt.Errorf("update audit line: %w", err)
		}
		lineID = existingLineID
		return nil
	})
	if err != nil {
		return nil, err
	}

	session, err := s.GetAuditSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for i := range session.Lines {
		if session.Lines[i].ID == lineID {
			return &session.Lines[i], nil
		}
	}
	return nil, apperr.NotFound("audit_line", fmt.Sprintf("%d", lineID))
}

// CommitAuditSession atomically posts immutable ADJUST ledger transactions for every counted
// audit line whose counted_millis differs from expected_millis, grouping them under a shared batch_id.
func (s *Service) CommitAuditSession(ctx context.Context, sessionID int64, operator string) (*AuditCommitResult, error) {
	op := strings.TrimSpace(operator)
	if op == "" {
		op = db.DefaultOperatorName
	}

	var (
		batchID      string
		createdTxIDs []int64
	)

	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var code, status string
		if err := tx.QueryRowContext(
			ctx,
			`SELECT code, status FROM audit_sessions WHERE id = ?;`,
			sessionID,
		).Scan(&code, &status); err == sql.ErrNoRows {
			return apperr.NotFound("audit_session", fmt.Sprintf("%d", sessionID))
		} else if err != nil {
			return err
		}
		if status != "OPEN" {
			return apperr.Validation("status", fmt.Sprintf("audit session %s is already %s", code, status))
		}

		now := time.Now().UTC().Format(time.RFC3339)
		batchID = fmt.Sprintf("%s-%d", code, time.Now().UTC().Unix())

		rows, err := tx.QueryContext(
			ctx,
			`SELECT item_id, location_id, expected_millis, counted_millis, notes
			 FROM audit_lines
			 WHERE session_id = ? AND counted = 1
			 ORDER BY id ASC;`,
			sessionID,
		)
		if err != nil {
			return fmt.Errorf("query counted audit lines: %w", err)
		}

		type pendingAdj struct {
			itemID   int64
			locID    int64
			expected num.Millis
			counted  num.Millis
			notes    string
		}
		var pending []pendingAdj
		for rows.Next() {
			var p pendingAdj
			var exp, cnt int64
			if err := rows.Scan(&p.itemID, &p.locID, &exp, &cnt, &p.notes); err != nil {
				_ = rows.Close()
				return err
			}
			p.expected = num.Millis(exp)
			p.counted = num.Millis(cnt)
			if p.counted != p.expected {
				pending = append(pending, p)
			}
		}
		_ = rows.Close()

		for _, p := range pending {
			itemRec, err := s.resolveItemTx(ctx, tx, p.itemID, "")
			if err != nil {
				return err
			}
			locID := p.locID
			reason := fmt.Sprintf("Stocktake %s (%s -> %s)", code, p.expected.Format(), p.counted.Format())
			if p.notes != "" {
				reason += ": " + p.notes
			}
			txID, err := s.execAdjustTx(ctx, tx, itemRec, MovementInput{
				ItemID:       p.itemID,
				TxType:       TxAdjust,
				AdjustMode:   AdjustModeTarget,
				Quantity:     p.counted.Format(),
				ToLocationID: &locID,
				BatchID:      batchID,
				Reason:       reason,
				Operator:     op,
			}, now)
			if err != nil {
				return err
			}
			createdTxIDs = append(createdTxIDs, txID)
		}

		if _, err := tx.ExecContext(
			ctx,
			`UPDATE audit_sessions SET status = 'COMMITTED', batch_id = ?, committed_at = ? WHERE id = ?;`,
			batchID, now, sessionID,
		); err != nil {
			return fmt.Errorf("mark audit session committed: %w", err)
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

	session, err := s.GetAuditSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	return &AuditCommitResult{
		Session:          session,
		BatchID:          batchID,
		AdjustmentsCount: len(txViews),
		Transactions:     txViews,
	}, nil
}

// CancelAuditSession marks an OPEN audit session as CANCELLED without posting any stock adjustments.
func (s *Service) CancelAuditSession(ctx context.Context, sessionID int64) error {
	res, err := s.store.DB().ExecContext(
		ctx,
		`UPDATE audit_sessions SET status = 'CANCELLED' WHERE id = ? AND status = 'OPEN';`,
		sessionID,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return apperr.Validation("status", "only OPEN audit sessions can be cancelled")
	}
	return nil
}

// GetAuditSession retrieves an AuditSession with all its variance lines.
func (s *Service) GetAuditSession(ctx context.Context, sessionID int64) (*models.AuditSession, error) {
	var (
		sess  models.AuditSession
		locID sql.NullInt64
	)
	err := s.store.DB().QueryRowContext(
		ctx,
		`SELECT a.id, a.code, a.name, a.location_id, COALESCE(l.code, ''),
		        a.status, a.operator, a.notes, a.batch_id, a.created_at, a.committed_at
		 FROM audit_sessions a
		 LEFT JOIN locations l ON l.id = a.location_id
		 WHERE a.id = ?;`,
		sessionID,
	).Scan(
		&sess.ID, &sess.Code, &sess.Name, &locID, &sess.LocationCode,
		&sess.Status, &sess.Operator, &sess.Notes, &sess.BatchID, &sess.CreatedAt, &sess.CommittedAt,
	)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound("audit_session", fmt.Sprintf("%d", sessionID))
	}
	if err != nil {
		return nil, err
	}
	if locID.Valid {
		v := locID.Int64
		sess.LocationID = &v
	}

	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT
			al.id, al.session_id, al.item_id, i.sku, COALESCE(i.barcode, ''), i.name, i.unit,
			al.location_id, l.code, al.expected_millis, al.counted_millis, al.counted,
			al.notes, al.updated_at, i.unit_cost_millis
		 FROM audit_lines al
		 JOIN items i ON i.id = al.item_id
		 JOIN locations l ON l.id = al.location_id
		 WHERE al.session_id = ?
		 ORDER BY l.code ASC, i.sku ASC;`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("query audit lines: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	sess.Lines = []models.AuditLine{}
	for rows.Next() {
		var (
			ln         models.AuditLine
			expInt     int64
			cntInt     int64
			countedInt int
			costInt    int64
		)
		if err := rows.Scan(
			&ln.ID, &ln.SessionID, &ln.ItemID, &ln.ItemSKU, &ln.ItemBarcode, &ln.ItemName, &ln.ItemUnit,
			&ln.LocationID, &ln.LocationCode, &expInt, &cntInt, &countedInt,
			&ln.Notes, &ln.UpdatedAt, &costInt,
		); err != nil {
			return nil, err
		}
		ln.ExpectedMillis = num.Millis(expInt)
		ln.ExpectedText = ln.ExpectedMillis.Format()
		ln.CountedMillis = num.Millis(cntInt)
		ln.CountedText = ln.CountedMillis.Format()
		ln.Counted = countedInt != 0

		if ln.Counted {
			ln.VarianceMillis = ln.CountedMillis.Sub(ln.ExpectedMillis)
			ln.VarianceText = formatSignedDelta(ln.VarianceMillis)
			impact, _ := ln.VarianceMillis.MulMillis(num.Millis(costInt))
			ln.ValuationImpactMillis = impact
			ln.ValuationImpactText = impact.FormatFixed(2)
			sess.CountedLines++
			if ln.VarianceMillis != 0 {
				sess.VarianceLines++
			}
		} else {
			ln.VarianceText = "—"
			ln.ValuationImpactText = "0.00"
		}

		sess.TotalLines++
		sess.Lines = append(sess.Lines, ln)
	}

	return &sess, nil
}

// ListAuditSessions returns all stocktake sessions ordered newest first.
func (s *Service) ListAuditSessions(ctx context.Context) ([]models.AuditSession, error) {
	rows, err := s.store.DB().QueryContext(
		ctx,
		`SELECT a.id, a.code, a.name, a.location_id, COALESCE(l.code, ''),
		        a.status, a.operator, a.notes, a.batch_id, a.created_at, a.committed_at,
		        (SELECT COUNT(*) FROM audit_lines al WHERE al.session_id = a.id) AS total_lines,
		        (SELECT COUNT(*) FROM audit_lines al WHERE al.session_id = a.id AND al.counted = 1) AS counted_lines,
		        (SELECT COUNT(*) FROM audit_lines al WHERE al.session_id = a.id AND al.counted = 1 AND al.counted_millis != al.expected_millis) AS variance_lines
		 FROM audit_sessions a
		 LEFT JOIN locations l ON l.id = a.location_id
		 ORDER BY a.id DESC;`,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit sessions: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []models.AuditSession
	for rows.Next() {
		var (
			sess  models.AuditSession
			locID sql.NullInt64
		)
		if err := rows.Scan(
			&sess.ID, &sess.Code, &sess.Name, &locID, &sess.LocationCode,
			&sess.Status, &sess.Operator, &sess.Notes, &sess.BatchID, &sess.CreatedAt, &sess.CommittedAt,
			&sess.TotalLines, &sess.CountedLines, &sess.VarianceLines,
		); err != nil {
			return nil, err
		}
		if locID.Valid {
			v := locID.Int64
			sess.LocationID = &v
		}
		out = append(out, sess)
	}
	return out, nil
}
