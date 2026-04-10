// Package errors defines a structured application error type (AppError) that
// carries a machine-readable code, a human-readable message, and an HTTP status
// code. It also provides a set of pre-defined sentinel errors for the most
// common failure scenarios across the RichPayment platform.
package errors

import (
	"fmt"
	"net/http"
)

// AppError is the standard error type used throughout the application. It
// implements the error interface and supports unwrapping so that callers can
// inspect the original cause via errors.Is / errors.As.
type AppError struct {
	// Code is a machine-readable error identifier (e.g. "NOT_FOUND").
	// Returned in API responses so clients can programmatically handle errors.
	Code string `json:"code"`

	// Message is a human-readable description of the error.
	Message string `json:"message"`

	// HTTPStatus is the HTTP status code associated with this error.
	// It is excluded from JSON serialisation (json:"-") because it is used
	// only to set the response status, not sent in the body.
	HTTPStatus int `json:"-"`

	// Err is the optional underlying error that caused this AppError.
	// When present, it is included in the Error() string and can be retrieved
	// via Unwrap() for errors.Is / errors.As chains.
	Err error `json:"-"`
}

// Error returns a formatted string combining the code, message, and optional
// wrapped error. This satisfies the built-in error interface.
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying error, enabling compatibility with the
// standard library's errors.Is and errors.As functions.
func (e *AppError) Unwrap() error { return e.Err }

// New creates a new AppError without a wrapped cause. Use this for sentinel
// errors or when the root cause does not need to be preserved.
func New(code string, message string, httpStatus int) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: httpStatus}
}

// Wrap creates a new AppError that wraps an existing error. Use this when you
// want to add application-level context (code + message + HTTP status) while
// retaining the original error for debugging and error-chain inspection.
func Wrap(err error, code string, message string, httpStatus int) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: httpStatus, Err: err}
}

// Common sentinel errors used across the platform. Handlers and services
// return these directly or wrap them to provide consistent error codes.
var (
	ErrNotFound          = New("NOT_FOUND", "resource not found", http.StatusNotFound)
	ErrUnauthorized      = New("UNAUTHORIZED", "authentication required", http.StatusUnauthorized)
	ErrForbidden         = New("FORBIDDEN", "permission denied", http.StatusForbidden)
	ErrBadRequest        = New("BAD_REQUEST", "invalid request", http.StatusBadRequest)
	ErrConflict          = New("CONFLICT", "resource conflict", http.StatusConflict)
	ErrInternal          = New("INTERNAL_ERROR", "internal server error", http.StatusInternalServerError)
	ErrRateLimited       = New("RATE_LIMITED", "too many requests", http.StatusTooManyRequests)
	ErrAccountLocked     = New("ACCOUNT_LOCKED", "account is locked", http.StatusLocked)
	ErrInvalidSignature  = New("INVALID_SIGNATURE", "invalid HMAC signature", http.StatusUnauthorized)
	ErrInvalidAPIKey     = New("INVALID_API_KEY", "invalid API key", http.StatusUnauthorized)
	ErrInvalid2FA        = New("INVALID_2FA", "invalid 2FA code", http.StatusUnauthorized)
	ErrEmergencyFreeze   = New("EMERGENCY_FREEZE", "system is frozen", http.StatusServiceUnavailable)
	ErrInsufficientFunds = New("INSUFFICIENT_FUNDS", "insufficient wallet balance", http.StatusBadRequest)
	ErrOrderExpired      = New("ORDER_EXPIRED", "deposit order has expired", http.StatusGone)
	ErrDuplicateSlip     = New("DUPLICATE_SLIP", "this slip has already been used", http.StatusConflict)
)
