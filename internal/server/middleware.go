package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/word-sys/kratel/internal/apperr"
)

const defaultMaxJSONBodyBytes = 4 * 1024 * 1024 // 4 MB (handles item thumbnails & CSV payloads)

type errorEnvelope struct {
	Error *apperr.Error `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(data)
}

func writeError(w http.ResponseWriter, err error) {
	status := apperr.HTTPStatus(err)
	payload := apperr.ToPayload(err)
	writeJSON(w, status, errorEnvelope{Error: payload})
}

func decodeJSON(r *http.Request, dst interface{}) error {
	if r.Body == nil {
		return apperr.Validation("body", "request body cannot be empty")
	}
	defer func() {
		_ = r.Body.Close()
	}()
	limited := io.LimitReader(r.Body, defaultMaxJSONBodyBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return apperr.Validation("body", "failed to read request body")
	}
	if len(raw) > defaultMaxJSONBodyBytes {
		return apperr.Validation("body", "request payload exceeds 4 MB limit")
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return apperr.Validation("body", "request JSON body is empty")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return apperr.Validation("body", fmt.Sprintf("invalid JSON body: %v", err))
	}
	return nil
}

type timingResponseWriter struct {
	http.ResponseWriter
	start       time.Time
	wroteHeader bool
}

func (tw *timingResponseWriter) WriteHeader(code int) {
	if !tw.wroteHeader {
		durMs := float64(time.Since(tw.start).Microseconds()) / 1000.0
		tw.ResponseWriter.Header().Set("X-Kratel-Duration-Ms", fmt.Sprintf("%.2f", durMs))
		tw.wroteHeader = true
	}
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *timingResponseWriter) Write(p []byte) (int, error) {
	if !tw.wroteHeader {
		tw.WriteHeader(http.StatusOK)
	}
	return tw.ResponseWriter.Write(p)
}

func withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &timingResponseWriter{
			ResponseWriter: w,
			start:          time.Now(),
		}

		// Security & offline headers
		tw.Header().Set("X-Content-Type-Options", "nosniff")
		tw.Header().Set("Referrer-Policy", "no-referrer")
		tw.Header().Set("Access-Control-Allow-Origin", "*")
		tw.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		tw.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Kratel-Operator")

		if strings.HasPrefix(r.URL.Path, "/api/") {
			tw.Header().Set("Cache-Control", "no-store")
		}

		if r.Method == http.MethodOptions {
			tw.WriteHeader(http.StatusNoContent)
			return
		}

		defer func() {
			if rec := recover(); rec != nil {
				writeError(tw, apperr.New(apperr.CodeInternal, fmt.Sprintf("unexpected server panic: %v", rec)))
			}
		}()

		next.ServeHTTP(tw, r)
	})
}

func parseIDParam(raw string, field string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.Validation(field, fmt.Sprintf("invalid positive integer %s %q", field, raw))
	}
	return id, nil
}

func parseQueryInt(r *http.Request, key string, defaultVal int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return defaultVal
	}
	return v
}

func parseQueryInt64(r *http.Request, key string) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseQueryBool(r *http.Request, key string) bool {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key)))
	return raw == "1" || raw == "true" || raw == "yes"
}

func requestOperator(r *http.Request, fallback string) string {
	if op := strings.TrimSpace(fallback); op != "" {
		return op
	}
	return strings.TrimSpace(r.Header.Get("X-Kratel-Operator"))
}
