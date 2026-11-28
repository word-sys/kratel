package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
	"github.com/word-sys/kratel/internal/csvmig"
	"github.com/word-sys/kratel/internal/labels"
)

const maxCSVUploadBytes = 16 * 1024 * 1024 // 16 MB spreadsheet upload cap

func (s *Server) handleCSVPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, apperr.Validation("method", "POST required"))
		return
	}

	rawBytes, forcedDelim, err := readCSVPayloadFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}

	preview, err := csvmig.AnalyzeCSV(rawBytes, forcedDelim)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleCSVImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, apperr.Validation("method", "POST required"))
		return
	}

	var body struct {
		CSVText                 string                 `json:"csv_text"`
		Delimiter               string                 `json:"delimiter"`
		ColumnMapping           map[string]string      `json:"column_mapping"`
		MapUnmappedToAttributes bool                   `json:"map_unmapped_to_attributes"`
		DuplicatePolicy         csvmig.DuplicatePolicy `json:"duplicate_policy"`
		DryRun                  bool                   `json:"dry_run"`
		Operator                string                 `json:"operator"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(body.CSVText) == "" {
		writeError(w, apperr.Validation("csv_text", "CSV content cannot be empty"))
		return
	}

	res, err := s.csvmig.ExecuteImport(r.Context(), csvmig.ImportRequest{
		CSVData:                 []byte(body.CSVText),
		ForcedDelimiter:         parseDelimiterHint(body.Delimiter),
		ColumnMapping:           body.ColumnMapping,
		MapUnmappedToAttributes: body.MapUnmappedToAttributes,
		DuplicatePolicy:         body.DuplicatePolicy,
		DryRun:                  body.DryRun,
		Operator:                requestOperator(r, body.Operator),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCSVExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, apperr.Validation("method", "GET required"))
		return
	}

	exportType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if exportType == "" {
		exportType = "items"
	}

	includeBOM := true
	if rawBOM := strings.TrimSpace(r.URL.Query().Get("bom")); rawBOM != "" {
		includeBOM = parseQueryBool(r, "bom")
	}
	includeArchived := parseQueryBool(r, "include_archived")
	limit := parseQueryInt(r, "limit", 0)

	var buf bytes.Buffer
	ctx := r.Context()

	switch exportType {
	case "items":
		if err := s.csvmig.ExportItemsCSV(ctx, &buf, includeBOM, includeArchived); err != nil {
			writeError(w, err)
			return
		}
	case "stock_levels":
		if err := s.csvmig.ExportStockLevelsCSV(ctx, &buf, includeBOM); err != nil {
			writeError(w, err)
			return
		}
	case "transactions":
		if err := s.csvmig.ExportTransactionsCSV(ctx, &buf, includeBOM, limit); err != nil {
			writeError(w, err)
			return
		}
	case "reorder":
		if err := s.csvmig.ExportReorderListCSV(ctx, &buf, includeBOM); err != nil {
			writeError(w, err)
			return
		}
	default:
		writeError(w, apperr.Validation("type", "export type must be items, stock_levels, transactions, or reorder"))
		return
	}

	stamp := time.Now().UTC().Format("20060102")
	filename := fmt.Sprintf("kratel-%s-%s.csv", exportType, stamp)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleLabelLayouts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, apperr.Validation("method", "GET required"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"layouts": labels.SupportedLayouts(),
	})
}

func (s *Server) handleLabelSVG(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, apperr.Validation("method", "GET required"))
		return
	}

	q := r.URL.Query()
	req := labels.LabelEntryRequest{
		TargetType: labels.LabelTargetType(strings.TrimSpace(q.Get("target_type"))),
		ID:         parseQueryInt64(r, "id"),
		Code:       strings.TrimSpace(q.Get("code")),
		Title:      strings.TrimSpace(q.Get("title")),
		Subtitle:   strings.TrimSpace(q.Get("subtitle")),
		Badge:      strings.TrimSpace(q.Get("badge")),
	}
	style := labels.LabelStyle(strings.TrimSpace(q.Get("style")))

	svg, lbl, err := s.labels.RenderSingleSVG(r.Context(), req, style)
	if err != nil {
		writeError(w, err)
		return
	}

	if strings.EqualFold(q.Get("format"), "raw") {
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(svg))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"svg":   svg,
		"label": lbl,
	})
}

func (s *Server) handleLabelPDF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, apperr.Validation("method", "POST required"))
		return
	}

	var req labels.PDFBatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	pdfBytes, err := s.labels.GenerateBatchPDF(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}

	layoutName := strings.TrimSpace(string(req.Layout))
	if layoutName == "" {
		layoutName = string(labels.LayoutThermal50x25)
	}
	filename := fmt.Sprintf("kratel-labels-%s.pdf", layoutName)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

func readCSVPayloadFromRequest(r *http.Request) ([]byte, rune, error) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	delimHint := parseDelimiterHint(r.URL.Query().Get("delimiter"))

	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxCSVUploadBytes); err != nil {
			return nil, 0, apperr.Validation("file", "failed to parse multipart CSV upload")
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, 0, apperr.Validation("file", "multipart form must include a 'file' field")
		}
		defer func() {
			_ = file.Close()
		}()
		raw, err := io.ReadAll(io.LimitReader(file, maxCSVUploadBytes))
		if err != nil {
			return nil, 0, apperr.Validation("file", "failed to read uploaded CSV file")
		}
		if d := r.FormValue("delimiter"); d != "" {
			delimHint = parseDelimiterHint(d)
		}
		return raw, delimHint, nil
	}

	if strings.Contains(ct, "application/json") || ct == "" {
		var body struct {
			CSVText   string `json:"csv_text"`
			Delimiter string `json:"delimiter"`
		}
		if err := decodeJSON(r, &body); err != nil {
			return nil, 0, err
		}
		if body.Delimiter != "" {
			delimHint = parseDelimiterHint(body.Delimiter)
		}
		return []byte(body.CSVText), delimHint, nil
	}

	defer func() {
		_ = r.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxCSVUploadBytes))
	if err != nil {
		return nil, 0, apperr.Validation("body", "failed to read CSV payload")
	}
	return raw, delimHint, nil
}

func parseDelimiterHint(hint string) rune {
	switch strings.ToLower(strings.TrimSpace(hint)) {
	case ",", "comma":
		return ','
	case ";", "semicolon":
		return ';'
	case `\t`, "\t", "tab", "tsv":
		return '\t'
	default:
		return 0
	}
}
