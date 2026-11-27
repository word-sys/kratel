package server

import (
	"net/http"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/ledger"
	"github.com/word-sys/kratel/internal/models"
)

func (s *Server) handleQuickScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, apperr.Validation("method", "POST required"))
		return
	}
	var in ledger.QuickScanInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	in.Operator = requestOperator(r, in.Operator)

	res, err := s.ledger.QuickScan(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTransactionsCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		filter := ledger.TransactionFilter{
			ItemID:        parseQueryInt64(r, "item_id"),
			LocationID:    parseQueryInt64(r, "location_id"),
			TxType:        strings.TrimSpace(r.URL.Query().Get("tx_type")),
			Operator:      strings.TrimSpace(r.URL.Query().Get("operator")),
			BatchID:       strings.TrimSpace(r.URL.Query().Get("batch_id")),
			Query:         strings.TrimSpace(r.URL.Query().Get("q")),
			DateFrom:      strings.TrimSpace(r.URL.Query().Get("date_from")),
			DateTo:        strings.TrimSpace(r.URL.Query().Get("date_to")),
			IncludeUndone: parseQueryBool(r, "include_undone"),
			Limit:         parseQueryInt(r, "limit", 100),
			Offset:        parseQueryInt(r, "offset", 0),
		}
		txs, total, err := s.ledger.ListTransactions(ctx, filter)
		if err != nil {
			writeError(w, err)
			return
		}
		if txs == nil {
			txs = []models.Transaction{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"transactions": txs,
			"total":        total,
		})

	case http.MethodPost:
		var in ledger.MovementInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, err)
			return
		}
		in.Operator = requestOperator(r, in.Operator)

		created, err := s.ledger.RecordMovement(ctx, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)

	default:
		writeError(w, apperr.Validation("method", "method not allowed"))
	}
}

func (s *Server) handleTransactionResource(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/transactions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, apperr.NotFound("transaction", ""))
		return
	}

	// POST /api/v1/transactions/batch-undo
	if parts[0] == "batch-undo" && r.Method == http.MethodPost {
		var body struct {
			BatchID  string `json:"batch_id"`
			Operator string `json:"operator"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, err)
			return
		}
		revs, err := s.ledger.UndoBatch(r.Context(), body.BatchID, requestOperator(r, body.Operator))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"batch_id":  body.BatchID,
			"reversals": revs,
		})
		return
	}

	txID, err := parseIDParam(parts[0], "transaction_id")
	if err != nil {
		writeError(w, err)
		return
	}

	// GET /api/v1/transactions/{id}
	if len(parts) == 1 && r.Method == http.MethodGet {
		txView, err := s.ledger.GetTransactionByID(r.Context(), txID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, txView)
		return
	}

	// POST /api/v1/transactions/{id}/undo
	if len(parts) == 2 && parts[1] == "undo" && r.Method == http.MethodPost {
		var body struct {
			Operator string `json:"operator"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			_ = decodeJSON(r, &body)
		}
		rev, err := s.ledger.UndoTransaction(r.Context(), txID, requestOperator(r, body.Operator))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rev)
		return
	}

	writeError(w, apperr.NotFound("endpoint", r.URL.Path))
}
