package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter provides Redis-based rate limiting.
type RateLimiter struct {
	rdb    *redis.Client
	limit  int
	window time.Duration
	logger *slog.Logger
}

// NewRateLimiter creates a new RateLimiter.
// limit is the maximum number of requests per window duration.
func NewRateLimiter(rdb *redis.Client, limit int, window time.Duration, logger *slog.Logger) *RateLimiter {
	return &RateLimiter{
		rdb:    rdb,
		limit:  limit,
		window: window,
		logger: logger,
	}
}

// Middleware returns the HTTP middleware function.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use API key as rate limit key; fall back to IP.
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = r.RemoteAddr
		}
		redisKey := fmt.Sprintf("ratelimit:%s", key)

		ctx := context.Background()
		count, err := rl.rdb.Incr(ctx, redisKey).Result()
		if err != nil {
			rl.logger.Error("rate limiter redis error", "error", err)
			// On Redis failure, allow the request through.
			next.ServeHTTP(w, r)
			return
		}

		// Set TTL on first increment.
		if count == 1 {
			rl.rdb.Expire(ctx, redisKey, rl.window)
		}

		if count > int64(rl.limit) {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rl.window.Seconds())))
			writeErrorJSON(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, please try again later")
			return
		}

		next.ServeHTTP(w, r)
	})
}
