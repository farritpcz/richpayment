// Package middleware provides reusable HTTP middleware components shared across
// RichPayment services. This file contains the panic-recovery middleware that
// prevents unhandled panics from crashing the server process.
package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery returns an HTTP middleware that catches panics raised by downstream
// handlers, logs the error with a full stack trace, and returns a generic 500
// Internal Server Error response to the client. This ensures that a single
// malformed request cannot bring down the entire service.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered",
						"error", err,
						"stack", string(debug.Stack()),
						"path", r.URL.Path,
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
