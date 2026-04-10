// Package middleware provides reusable HTTP middleware for the gateway-api
// service, including API key authentication, rate limiting, IP whitelisting,
// emergency freeze checking, and session-based authentication.
package middleware

import (
	"encoding/json"
	"net/http"
)

// errorResponse is the standard error envelope used by middleware to return
// JSON error responses before the request reaches a handler.
type errorResponse struct {
	// Success is always false for error responses.
	Success bool `json:"success"`
	// Error is a human-readable error message.
	Error string `json:"error"`
	// Code is a machine-readable error code for programmatic handling.
	Code string `json:"code"`
}

// writeErrorJSON writes a JSON error response from middleware.
func writeErrorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Success: false,
		Error:   message,
		Code:    code,
	})
}
