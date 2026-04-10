package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/redis/go-redis/v9"
)

// sessionCookieName is the name of the HTTP cookie that carries the session ID
// for dashboard (admin) authentication.
const sessionCookieName = "richpay_session"

// sessionPrefix is the Redis key namespace for session data, matching the
// prefix used by the auth service when storing sessions.
const sessionPrefix = "session:"

// SessionAuth validates dashboard sessions using a cookie backed by Redis.
type SessionAuth struct {
	rdb    *redis.Client
	logger *slog.Logger
}

// NewSessionAuth creates a new SessionAuth middleware.
func NewSessionAuth(rdb *redis.Client, logger *slog.Logger) *SessionAuth {
	return &SessionAuth{rdb: rdb, logger: logger}
}

// Middleware returns the HTTP middleware function.
func (sa *SessionAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeErrorJSON(w, http.StatusUnauthorized, "MISSING_SESSION", "session cookie is required")
			return
		}

		ctx := context.Background()
		sessionData, err := sa.rdb.Get(ctx, sessionPrefix+cookie.Value).Result()
		if err == redis.Nil {
			writeErrorJSON(w, http.StatusUnauthorized, "INVALID_SESSION", "session has expired or is invalid")
			return
		}
		if err != nil {
			sa.logger.Error("session auth redis error", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}

		// Store session data in request context for downstream handlers.
		ctx = context.WithValue(r.Context(), sessionContextKey, sessionData)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// contextKey is an unexported type used as a key in context.WithValue to avoid
// collisions with keys from other packages.
type contextKey string

// sessionContextKey is the context key under which session data is stored.
const sessionContextKey contextKey = "session"

// SessionFromContext retrieves session data stored by the SessionAuth middleware.
func SessionFromContext(ctx context.Context) (string, bool) {
	val, ok := ctx.Value(sessionContextKey).(string)
	return val, ok
}
