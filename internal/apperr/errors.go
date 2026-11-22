package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Code represents a stable, machine-readable domain error identifier used by the REST API and UI i18n layer.
type Code string

const (
	CodeNotFound            Code = "NOT_FOUND"
	CodeDuplicateSKU        Code = "DUPLICATE_SKU"
	CodeDuplicateBarcode    Code = "DUPLICATE_BARCODE"
	CodeDuplicateCode       Code = "DUPLICATE_CODE"
	CodeInsufficientStock   Code = "INSUFFICIENT_STOCK"
	CodeLocationCycle       Code = "LOCATION_CYCLE"
	CodeLocationInUse       Code = "LOCATION_IN_USE"
	CodeItemHasTransactions Code = "ITEM_HAS_TRANSACTIONS"
	CodeAlreadyUndone       Code = "ALREADY_UNDONE"
	CodeValidation          Code = "VALIDATION_ERROR"
	CodeUnauthorizedPIN     Code = "UNAUTHORIZED_PIN"
	CodeCorruptDB           Code = "CORRUPT_DB"
	CodeInternal            Code = "INTERNAL_ERROR"
)

// Sentinel domain errors for errors.Is checks across packages.
var (
	ErrNotFound            = &Error{Code: CodeNotFound, Message: "resource not found"}
	ErrDuplicateSKU        = &Error{Code: CodeDuplicateSKU, Message: "SKU already exists"}
	ErrDuplicateBarcode    = &Error{Code: CodeDuplicateBarcode, Message: "barcode already exists"}
	ErrDuplicateCode       = &Error{Code: CodeDuplicateCode, Message: "code already exists"}
	ErrInsufficientStock   = &Error{Code: CodeInsufficientStock, Message: "insufficient stock available"}
	ErrLocationCycle       = &Error{Code: CodeLocationCycle, Message: "location hierarchy cycle detected"}
	ErrLocationInUse       = &Error{Code: CodeLocationInUse, Message: "location has active stock, child bins, or ledger history"}
	ErrItemHasTransactions = &Error{Code: CodeItemHasTransactions, Message: "item has immutable ledger history and must be archived instead of deleted"}
	ErrAlreadyUndone       = &Error{Code: CodeAlreadyUndone, Message: "transaction has already been undone"}
	ErrValidation          = &Error{Code: CodeValidation, Message: "validation error"}
	ErrUnauthorizedPIN     = &Error{Code: CodeUnauthorizedPIN, Message: "invalid operator PIN"}
	ErrCorruptDB           = &Error{Code: CodeCorruptDB, Message: "invalid or corrupt SQLite database file"}
	ErrInternal            = &Error{Code: CodeInternal, Message: "internal server error"}
)

// Error is a structured domain error carrying a stable Code, human-readable Message,
// optional offending Field, structured Details metadata, and an underlying Cause.
type Error struct {
	Code    Code                   `json:"code"`
	Message string                 `json:"message"`
	Field   string                 `json:"field,omitempty"`
	Details map[string]interface{} `json:"details,omitempty"`
	Cause   error                  `json:"-"`
}

// New creates a new structured domain Error.
func New(code Code, message string) *Error {
	return &Error{
		Code:    code,
		Message: message,
	}
}

// Wrap creates a new structured domain Error wrapping an underlying cause.
func Wrap(code Code, message string, cause error) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// Error implements the standard error interface.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Field != "" {
		return fmt.Sprintf("[%s] %s (field: %s)", e.Code, e.Message, e.Field)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause for errors.Is and errors.As traversal.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is allows errors.Is(err, apperr.ErrNotFound) to match any *Error sharing the same Code.
func (e *Error) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Code != "" && e.Code == t.Code
}

// WithField returns a copy of the Error with Field set.
func (e *Error) WithField(field string) *Error {
	clone := e.clone()
	clone.Field = field
	return clone
}

// WithDetail returns a copy of the Error with a key-value entry added to Details.
func (e *Error) WithDetail(key string, val interface{}) *Error {
	clone := e.clone()
	if clone.Details == nil {
		clone.Details = make(map[string]interface{}, 2)
	}
	clone.Details[key] = val
	return clone
}

func (e *Error) clone() *Error {
	if e == nil {
		return &Error{Code: CodeInternal}
	}
	c := *e
	if e.Details != nil {
		c.Details = make(map[string]interface{}, len(e.Details))
		for k, v := range e.Details {
			c.Details[k] = v
		}
	}
	return &c
}

// NotFound constructs a NOT_FOUND domain error for a specific entity and lookup key.
func NotFound(entity, identifier string) *Error {
	return New(CodeNotFound, fmt.Sprintf("%s %q not found", entity, identifier)).
		WithDetail("entity", entity).
		WithDetail("identifier", identifier)
}

// Validation constructs a VALIDATION_ERROR domain error for a specific input field.
func Validation(field, message string) *Error {
	return New(CodeValidation, message).WithField(field)
}

// InsufficientStock constructs an INSUFFICIENT_STOCK domain error with balance context.
func InsufficientStock(sku, locationCode, available, requested string) *Error {
	msg := fmt.Sprintf("insufficient stock for %s at %s: available %s, requested %s", sku, locationCode, available, requested)
	return New(CodeInsufficientStock, msg).
		WithDetail("sku", sku).
		WithDetail("location_code", locationCode).
		WithDetail("available", available).
		WithDetail("requested", requested)
}

// HTTPStatus maps a domain Error Code to its canonical HTTP status code.
func HTTPStatus(err error) int {
	switch CodeOf(err) {
	case CodeNotFound:
		return http.StatusNotFound
	case CodeDuplicateSKU, CodeDuplicateBarcode, CodeDuplicateCode, CodeLocationInUse, CodeItemHasTransactions, CodeAlreadyUndone:
		return http.StatusConflict
	case CodeInsufficientStock, CodeLocationCycle:
		return http.StatusUnprocessableEntity
	case CodeValidation, CodeCorruptDB:
		return http.StatusBadRequest
	case CodeUnauthorizedPIN:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

// CodeOf extracts the domain Code from err, returning CodeInternal if err is not a domain *Error.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var domErr *Error
	if errors.As(err, &domErr) && domErr != nil && domErr.Code != "" {
		return domErr.Code
	}
	return CodeInternal
}

// HasCode reports whether err or any error in its chain has the given domain Code.
func HasCode(err error, code Code) bool {
	return err != nil && CodeOf(err) == code
}

// ToPayload converts any Go error into a safe, JSON-serializable *Error response payload.
func ToPayload(err error) *Error {
	if err == nil {
		return nil
	}
	var domErr *Error
	if errors.As(err, &domErr) && domErr != nil {
		return domErr.clone()
	}
	return &Error{
		Code:    CodeInternal,
		Message: err.Error(),
	}
}
