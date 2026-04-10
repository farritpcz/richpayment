package errors

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// =============================================================================
// TestAppError_Error verifies the Error() string formatting for AppError.
//
// AppError.Error() has two formats:
//   - Without a wrapped error: "CODE: message"
//   - With a wrapped error:    "CODE: message: wrapped error text"
//
// Correct formatting is essential because error messages appear in logs,
// monitoring dashboards, and API responses. A malformed message makes
// debugging much harder.
// =============================================================================
func TestAppError_Error(t *testing.T) {
	// Subtest: error without a wrapped inner error.
	// Expected format: "CODE: message"
	t.Run("without wrapped error", func(t *testing.T) {
		appErr := &AppError{
			Code:       "NOT_FOUND",
			Message:    "resource not found",
			HTTPStatus: http.StatusNotFound,
			Err:        nil,
		}

		expected := "NOT_FOUND: resource not found"
		if got := appErr.Error(); got != expected {
			t.Errorf("AppError.Error() = %q, want %q", got, expected)
		}
	})

	// Subtest: error wrapping another error.
	// Expected format: "CODE: message: inner error text"
	// This is the common case when a database or network error is wrapped
	// with business context before being returned to the caller.
	t.Run("with wrapped error", func(t *testing.T) {
		innerErr := fmt.Errorf("connection refused")
		appErr := &AppError{
			Code:       "INTERNAL_ERROR",
			Message:    "database failure",
			HTTPStatus: http.StatusInternalServerError,
			Err:        innerErr,
		}

		expected := "INTERNAL_ERROR: database failure: connection refused"
		if got := appErr.Error(); got != expected {
			t.Errorf("AppError.Error() = %q, want %q", got, expected)
		}
	})
}

// =============================================================================
// TestAppError_Unwrap verifies that Unwrap() returns the inner error.
//
// This is required for compatibility with Go's errors.Is() and errors.As()
// functions, which rely on the Unwrap() method to traverse the error chain.
// Without correct Unwrap(), callers cannot use errors.Is(err, ErrNotFound)
// to check for specific error types.
// =============================================================================
func TestAppError_Unwrap(t *testing.T) {
	// Subtest: Unwrap returns the inner error when present.
	t.Run("with inner error", func(t *testing.T) {
		inner := fmt.Errorf("inner error")
		appErr := &AppError{
			Code:       "TEST",
			Message:    "test message",
			HTTPStatus: http.StatusBadRequest,
			Err:        inner,
		}

		unwrapped := appErr.Unwrap()
		if unwrapped != inner {
			t.Errorf("Unwrap() = %v, want %v", unwrapped, inner)
		}
	})

	// Subtest: Unwrap returns nil when there is no inner error.
	t.Run("without inner error", func(t *testing.T) {
		appErr := &AppError{
			Code:       "TEST",
			Message:    "no inner",
			HTTPStatus: http.StatusBadRequest,
			Err:        nil,
		}

		if unwrapped := appErr.Unwrap(); unwrapped != nil {
			t.Errorf("Unwrap() = %v, want nil", unwrapped)
		}
	})

	// Subtest: errors.Is can traverse the chain through Unwrap.
	// This is the real-world use case: checking if an AppError wraps a
	// specific sentinel error.
	t.Run("errors.Is traverses chain", func(t *testing.T) {
		sentinel := fmt.Errorf("sentinel")
		appErr := Wrap(sentinel, "WRAPPED", "wrapped sentinel", http.StatusInternalServerError)

		if !errors.Is(appErr, sentinel) {
			t.Error("errors.Is(appErr, sentinel) = false, want true; Unwrap chain is broken")
		}
	})
}

// =============================================================================
// TestNew verifies the New() constructor for AppError.
//
// New() creates an AppError without wrapping an inner error. It is used for
// defining sentinel errors like ErrNotFound and ErrUnauthorized.
// We verify that all fields are correctly populated and that the inner
// error (Err) is nil.
// =============================================================================
func TestNew(t *testing.T) {
	appErr := New("BAD_REQUEST", "invalid input", http.StatusBadRequest)

	if appErr.Code != "BAD_REQUEST" {
		t.Errorf("Code = %q, want %q", appErr.Code, "BAD_REQUEST")
	}
	if appErr.Message != "invalid input" {
		t.Errorf("Message = %q, want %q", appErr.Message, "invalid input")
	}
	if appErr.HTTPStatus != http.StatusBadRequest {
		t.Errorf("HTTPStatus = %d, want %d", appErr.HTTPStatus, http.StatusBadRequest)
	}
	if appErr.Err != nil {
		t.Errorf("Err = %v, want nil", appErr.Err)
	}
}

// =============================================================================
// TestWrap verifies the Wrap() constructor for AppError.
//
// Wrap() creates an AppError that wraps an existing error with additional
// context (code, message, HTTP status). This is the primary way to add
// business-level context to low-level errors (e.g. wrapping a pgx error
// with "INTERNAL_ERROR").
// =============================================================================
func TestWrap(t *testing.T) {
	inner := fmt.Errorf("pg: connection timeout")
	appErr := Wrap(inner, "INTERNAL_ERROR", "database unavailable", http.StatusInternalServerError)

	// Verify all fields are correctly populated.
	if appErr.Code != "INTERNAL_ERROR" {
		t.Errorf("Code = %q, want %q", appErr.Code, "INTERNAL_ERROR")
	}
	if appErr.Message != "database unavailable" {
		t.Errorf("Message = %q, want %q", appErr.Message, "database unavailable")
	}
	if appErr.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("HTTPStatus = %d, want %d", appErr.HTTPStatus, http.StatusInternalServerError)
	}
	if appErr.Err != inner {
		t.Errorf("Err = %v, want %v", appErr.Err, inner)
	}

	// Verify the Error() string includes both the AppError context and the inner error.
	errorStr := appErr.Error()
	if errorStr != "INTERNAL_ERROR: database unavailable: pg: connection timeout" {
		t.Errorf("Error() = %q, want %q", errorStr, "INTERNAL_ERROR: database unavailable: pg: connection timeout")
	}
}

// =============================================================================
// TestCommonErrors verifies that the pre-defined sentinel errors have the
// correct codes and HTTP status codes.
//
// These sentinel errors are used throughout the application for consistent
// error responses. If any of them have the wrong HTTP status, clients will
// receive incorrect status codes (e.g. a 500 instead of a 404).
// =============================================================================
func TestCommonErrors(t *testing.T) {
	// Table-driven test: each row checks one sentinel error's code and HTTP status.
	tests := []struct {
		name       string
		err        *AppError
		wantCode   string
		wantStatus int
	}{
		{"ErrNotFound", ErrNotFound, "NOT_FOUND", http.StatusNotFound},
		{"ErrUnauthorized", ErrUnauthorized, "UNAUTHORIZED", http.StatusUnauthorized},
		{"ErrForbidden", ErrForbidden, "FORBIDDEN", http.StatusForbidden},
		{"ErrBadRequest", ErrBadRequest, "BAD_REQUEST", http.StatusBadRequest},
		{"ErrConflict", ErrConflict, "CONFLICT", http.StatusConflict},
		{"ErrInternal", ErrInternal, "INTERNAL_ERROR", http.StatusInternalServerError},
		{"ErrRateLimited", ErrRateLimited, "RATE_LIMITED", http.StatusTooManyRequests},
		{"ErrInsufficientFunds", ErrInsufficientFunds, "INSUFFICIENT_FUNDS", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", tt.err.Code, tt.wantCode)
			}
			if tt.err.HTTPStatus != tt.wantStatus {
				t.Errorf("HTTPStatus = %d, want %d", tt.err.HTTPStatus, tt.wantStatus)
			}
		})
	}
}
