// Package apperr defines machine-readable service errors shared by the
// CLI and the REST API. Both adapters render the same code for the same
// failure; HTTP status mapping lives here so handlers stay thin.
package apperr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// Codes returned in the JSON error envelope.
const (
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeValidation   = "validation"
	CodeFrozen       = "frozen"
	CodeUnsupported  = "unsupported"
	CodeInvalidState = "invalid_state"
	CodeInternal     = "internal"
)

// Error is a coded service failure.
type Error struct {
	Code    string
	Message string
	Status  int
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds a coded error with the conventional HTTP status.
func New(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Status: statusFor(code)}
}

func statusFor(code string) int {
	switch code {
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeValidation, CodeInvalidState, CodeUnsupported:
		return http.StatusUnprocessableEntity
	case CodeFrozen:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// AsCode extracts the machine code from err, defaulting to internal.
func AsCode(err error) string {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return CodeInternal
}

// FromDB maps PostgreSQL errors to coded errors. Messages name the
// operation, never database internals: no constraint, table or column
// names reach the client. P0001 carries our own trigger text, which is
// written for operators and stays intact.
func FromDB(err error, what string) error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return err
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return New(CodeInternal, "%s failed", what)
	}
	switch pgErr.Code {
	case "23505":
		return New(CodeConflict, "%s conflicts with an existing record", what)
	case "23503":
		return New(CodeValidation, "%s references a missing record", what)
	case "23502":
		return New(CodeValidation, "%s omits a required value", what)
	case "23514":
		return New(CodeValidation, "%s violates a field rule", what)
	case "23P01":
		return New(CodeConflict, "%s overlaps an existing effective-dated row", what)
	case "P0001":
		return New(CodeFrozen, "%s", pgErr.Message)
	default:
		return New(CodeInternal, "%s failed", what)
	}
}
