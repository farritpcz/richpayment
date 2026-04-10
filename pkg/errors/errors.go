package errors

import (
	"fmt"
	"net/http"
)

type AppError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
	Err        error  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.Err }

func New(code string, message string, httpStatus int) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: httpStatus}
}

func Wrap(err error, code string, message string, httpStatus int) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: httpStatus, Err: err}
}

// Common errors
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
