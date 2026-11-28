package server

import (
	"net/http"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/ledger"
	"github.com/word-sys/kratel/internal/models"
)

func (s *Server) handleKitsCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		kits, err := s.ledger.ListKits(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		if kits == nil {
			kits = []models.Kit{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"kits": kits,
		})

	case http.MethodPost:
		var in ledger.KitInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		created, err := s.ledger.CreateKit(ctx, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleKitResource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/kits/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, apperr.NotFound("kit", ""))
		return
	}

	kitID, err := parseIDParam(parts[0], "kit_id")
	if err != nil {
		writeError(w, err)
		return
	}

	if len(parts) == 2 && parts[1] == "execute" && r.Method == http.MethodPost {
		var in ledger.KitExecuteInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		in.KitID = kitID
		in.Operator = requestOperator(r, in.Operator)
		res, err := s.ledger.ExecuteKit(ctx, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	if len(parts) != 1 {
		writeError(w, apperr.NotFound("endpoint", r.URL.Path))
		return
	}

	switch r.Method {
	case http.MethodGet:
		kit, err := s.ledger.GetKitByID(ctx, kitID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, kit)

	case http.MethodPut:
		var in ledger.KitInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		updated, err := s.ledger.UpdateKit(ctx, kitID, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if err := s.ledger.DeleteKit(ctx, kitID); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleAuditsCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		sessions, err := s.ledger.ListAuditSessions(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		if sessions == nil {
			sessions = []models.AuditSession{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"audits": sessions,
		})

	case http.MethodPost:
		var in ledger.AuditSessionInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		in.Operator = requestOperator(r, in.Operator)
		created, err := s.ledger.CreateAuditSession(ctx, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleAuditResource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/audits/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, apperr.NotFound("audit_session", ""))
		return
	}

	sessionID, err := parseIDParam(parts[0], "session_id")
	if err != nil {
		writeError(w, err)
		return
	}

	if len(parts) == 1 && r.Method == http.MethodGet {
		sess, err := s.ledger.GetAuditSession(ctx, sessionID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sess)
		return
	}

	if len(parts) == 2 && r.Method == http.MethodPost {
		switch parts[1] {
		case "count":
			var in ledger.AuditCountInput
			if err := decodeJSON(r, &in); err != nil {
				writeError(w, err)
				return
			}
			line, err := s.ledger.RecordAuditCount(ctx, sessionID, in)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, line)
			return

		case "commit":
			var body struct {
				Operator string `json:"operator"`
			}
			if r.Body != nil && r.ContentLength != 0 {
				_ = decodeJSON(r, &body)
			}
			res, err := s.ledger.CommitAuditSession(ctx, sessionID, requestOperator(r, body.Operator))
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, res)
			return

		case "cancel":
			if err := s.ledger.CancelAuditSession(ctx, sessionID); err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
			return
		}
	}

	writeError(w, apperr.NotFound("endpoint", r.URL.Path))
}

func (s *Server) handleLoansCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		loans, err := s.ledger.ListLoans(ctx, status)
		if err != nil {
			writeError(w, err)
			return
		}
		if loans == nil {
			loans = []models.Loan{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"loans": loans,
		})

	case http.MethodPost:
		var in ledger.LoanCheckOutInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		in.Operator = requestOperator(r, in.Operator)
		created, err := s.ledger.CheckOutLoan(ctx, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleLoanResource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/loans/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, apperr.NotFound("loan", ""))
		return
	}

	loanID, err := parseIDParam(parts[0], "loan_id")
	if err != nil {
		writeError(w, err)
		return
	}

	if len(parts) == 1 && r.Method == http.MethodGet {
		loan, err := s.ledger.GetLoanByID(ctx, loanID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, loan)
		return
	}

	if len(parts) == 2 && parts[1] == "return" && r.Method == http.MethodPost {
		var body struct {
			ReturnLocationID *int64 `json:"return_location_id,omitempty"`
			Operator         string `json:"operator"`
			Notes            string `json:"notes"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			_ = decodeJSON(r, &body)
		}
		loan, err := s.ledger.ReturnLoan(ctx, loanID, body.ReturnLocationID, requestOperator(r, body.Operator), body.Notes)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, loan)
		return
	}

	writeError(w, apperr.NotFound("endpoint", r.URL.Path))
}
