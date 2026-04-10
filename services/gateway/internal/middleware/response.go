package middleware

import (
	"encoding/json"
	"net/http"
)

// errorResponse is the standard error envelope used by middleware.
type errorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
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
