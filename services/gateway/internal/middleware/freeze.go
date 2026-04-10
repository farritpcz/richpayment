package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/redis/go-redis/v9"
)

const emergencyFreezeKey = "system:emergency_freeze"

// FreezeCheck checks whether the system is in emergency freeze mode.
// When frozen, all mutating API requests are rejected.
type FreezeCheck struct {
	rdb    *redis.Client
	logger *slog.Logger
}

// NewFreezeCheck creates a new FreezeCheck middleware.
func NewFreezeCheck(rdb *redis.Client, logger *slog.Logger) *FreezeCheck {
	return &FreezeCheck{rdb: rdb, logger: logger}
}

// Middleware returns the HTTP middleware function.
func (fc *FreezeCheck) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only block mutating methods.
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.Background()
		val, err := fc.rdb.Get(ctx, emergencyFreezeKey).Result()
		if err != nil && err != redis.Nil {
			fc.logger.Error("freeze check redis error", "error", err)
			// On Redis failure, allow the request through to avoid blocking all traffic.
			next.ServeHTTP(w, r)
			return
		}

		if val == "1" || val == "true" {
			fc.logger.Warn("request blocked by emergency freeze", "method", r.Method, "path", r.URL.Path)
			writeErrorJSON(w, http.StatusServiceUnavailable, "SYSTEM_FROZEN", "system is temporarily frozen for maintenance")
			return
		}

		next.ServeHTTP(w, r)
	})
}
