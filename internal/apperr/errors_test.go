package apperr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestErrorsIsAndAs(t *testing.T) {
	baseCause := errors.New("sqlite constraint failed: items.sku")
	err := Wrap(CodeDuplicateSKU, "SKU 'ESP32-S3' is already registered", baseCause).
		WithField("sku").
		WithDetail("sku", "ESP32-S3")

	if !errors.Is(err, ErrDuplicateSKU) {
		t.Fatal("expected errors.Is(err, ErrDuplicateSKU) to be true")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("expected errors.Is(err, ErrNotFound) to be false")
	}
	if !errors.Is(err, baseCause) {
		t.Fatal("expected errors.Is(err, baseCause) to unwrap underlying cause")
	}
	if !HasCode(err, CodeDuplicateSKU) {
		t.Fatal("expected HasCode(err, CodeDuplicateSKU) to be true")
	}

	wrappedFmt := fmt.Errorf("create item failed: %w", err)
	if CodeOf(wrappedFmt) != CodeDuplicateSKU {
		t.Fatalf("expected CodeOf(wrappedFmt) == %q, got %q", CodeDuplicateSKU, CodeOf(wrappedFmt))
	}
	if HTTPStatus(wrappedFmt) != http.StatusConflict {
		t.Fatalf("expected HTTP 409 Conflict, got %d", HTTPStatus(wrappedFmt))
	}
}

func TestHelperConstructorsAndHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantCode   Code
		wantStatus int
	}{
		{
			name:       "NotFound",
			err:        NotFound("item", "KRT-0042"),
			wantCode:   CodeNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "Validation",
			err:        Validation("quantity", "quantity must be greater than zero"),
			wantCode:   CodeValidation,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "InsufficientStock",
			err:        InsufficientStock("RES-10K", "A-01-B2", "4", "10"),
			wantCode:   CodeInsufficientStock,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "LocationCycle",
			err:        New(CodeLocationCycle, "cannot move location inside its own child"),
			wantCode:   CodeLocationCycle,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "AlreadyUndone",
			err:        New(CodeAlreadyUndone, "transaction #19 was already reversed"),
			wantCode:   CodeAlreadyUndone,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "UnauthorizedPIN",
			err:        New(CodeUnauthorizedPIN, "operator PIN is incorrect"),
			wantCode:   CodeUnauthorizedPIN,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "CorruptDB",
			err:        New(CodeCorruptDB, "uploaded file is not a valid SQLite 3 database"),
			wantCode:   CodeCorruptDB,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "GenericStdError",
			err:        errors.New("disk I/O failure"),
			wantCode:   CodeInternal,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodeOf(tc.err); got != tc.wantCode {
				t.Errorf("CodeOf() = %q, want %q", got, tc.wantCode)
			}
			if got := HTTPStatus(tc.err); got != tc.wantStatus {
				t.Errorf("HTTPStatus() = %d, want %d", got, tc.wantStatus)
			}
			payload := ToPayload(tc.err)
			if payload == nil || payload.Code != tc.wantCode {
				t.Fatalf("ToPayload() returned %+v, want code %q", payload, tc.wantCode)
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("json.Marshal(payload) failed: %v", err)
			}
			if len(raw) == 0 {
				t.Fatal("expected non-empty JSON payload")
			}
		})
	}
}

func TestCloneImmutability(t *testing.T) {
	orig := New(CodeValidation, "invalid attribute").WithDetail("key", "voltage")
	modified := orig.WithField("attributes_json").WithDetail("extra", true)

	if orig.Field != "" {
		t.Errorf("expected original Field to remain empty, got %q", orig.Field)
	}
	if _, exists := orig.Details["extra"]; exists {
		t.Error("expected original Details map not to be mutated by WithDetail")
	}
	if modified.Field != "attributes_json" {
		t.Errorf("expected modified Field 'attributes_json', got %q", modified.Field)
	}
}
