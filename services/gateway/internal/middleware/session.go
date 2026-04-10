// Package middleware provides reusable HTTP middleware for the gateway-api
// service, including API key authentication, rate limiting, IP whitelisting,
// emergency freeze checking, and session-based authentication.
//
// This file implements the SessionAuth middleware which validates dashboard
// sessions using a cookie backed by Redis. It enforces secure cookie
// attributes (Secure, HttpOnly, SameSite=Strict, Path=/, Max-Age) to
// protect against session hijacking, XSS, and CSRF attacks.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// sessionCookieName is the name of the HTTP cookie that carries the session ID
// for dashboard (admin) authentication. This name is used both when reading the
// cookie from incoming requests and when setting it in responses.
const sessionCookieName = "richpay_session"

// sessionPrefix is the Redis key namespace for session data, matching the
// prefix used by the auth service when storing sessions.
const sessionPrefix = "session:"

// defaultSessionTTL is the default session time-to-live used for the cookie
// Max-Age attribute. This should match the session TTL configured in the
// auth service (24 hours) to ensure the cookie expires at the same time as
// the Redis session entry.
const defaultSessionTTL = 24 * time.Hour

// SessionAuth validates dashboard sessions using a cookie backed by Redis.
// It reads the session cookie from incoming requests, looks up the session
// in Redis, and stores the session data in the request context for downstream
// handlers. When setting cookies (e.g. on login redirect), it applies all
// recommended security attributes.
type SessionAuth struct {
	// rdb is the Redis client used to look up session data by session ID.
	rdb *redis.Client
	// logger is used for error and security event logging.
	logger *slog.Logger
	// sessionTTL is the session time-to-live used for cookie Max-Age.
	// Defaults to defaultSessionTTL (24h) if not explicitly set.
	sessionTTL time.Duration
}

// NewSessionAuth creates a new SessionAuth middleware with the default
// session TTL (24 hours). The Redis client is used to look up session data,
// and the logger records authentication errors and security events.
func NewSessionAuth(rdb *redis.Client, logger *slog.Logger) *SessionAuth {
	return &SessionAuth{
		rdb:        rdb,
		logger:     logger,
		sessionTTL: defaultSessionTTL,
	}
}

// NewSessionAuthWithTTL creates a new SessionAuth middleware with a custom
// session TTL. This is useful when the auth service is configured with a
// non-default session duration and the cookie Max-Age needs to match.
func NewSessionAuthWithTTL(rdb *redis.Client, logger *slog.Logger, ttl time.Duration) *SessionAuth {
	return &SessionAuth{
		rdb:        rdb,
		logger:     logger,
		sessionTTL: ttl,
	}
}

// Middleware returns the HTTP middleware function that validates session
// cookies on every request. The middleware:
//  1. Extracts the session cookie from the request.
//  2. Looks up the session ID in Redis.
//  3. If valid, stores the session data in the request context.
//  4. If invalid/missing, returns HTTP 401 with a JSON error.
//
// The middleware also refreshes the cookie on every successful validation,
// extending the Max-Age so that active sessions do not expire while the
// user is actively using the dashboard.
func (sa *SessionAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// -----------------------------------------------------------
		// Step 1: Extract the session cookie from the request.
		// -----------------------------------------------------------
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeErrorJSON(w, http.StatusUnauthorized, "MISSING_SESSION", "session cookie is required")
			return
		}

		// -----------------------------------------------------------
		// Step 2: Look up the session in Redis.
		// -----------------------------------------------------------
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

		// -----------------------------------------------------------
		// Step 3: Refresh the secure cookie on every successful
		// validation. This extends the cookie lifetime so active
		// users are not unexpectedly logged out.
		// -----------------------------------------------------------
		sa.SetSecureCookie(w, cookie.Value)

		// -----------------------------------------------------------
		// Step 4: Store session data in request context for downstream
		// handlers so they can access the session without another
		// Redis lookup.
		// -----------------------------------------------------------
		ctx = context.WithValue(r.Context(), sessionContextKey, sessionData)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SetSecureCookie writes the session cookie to the response with all
// recommended security attributes:
//
//   - Secure: the cookie is only sent over HTTPS connections, preventing
//     interception over unencrypted HTTP.
//   - HttpOnly: the cookie is inaccessible to JavaScript (document.cookie),
//     mitigating XSS-based session theft.
//   - SameSite=Strict: the cookie is never sent on cross-site requests,
//     preventing CSRF attacks.
//   - Path=/: the cookie is available to all routes on the domain.
//   - Max-Age: set to the session TTL in seconds so the cookie expires
//     at the same time as the Redis session entry. This ensures the
//     browser automatically clears the cookie when the session expires.
//
// This method is public so that login handlers can also set the cookie
// with consistent security attributes after a successful authentication.
func (sa *SessionAuth) SetSecureCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   int(sa.sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie writes a cookie with Max-Age=-1 to instruct the
// browser to delete the session cookie. This should be called on logout
// to ensure the cookie is removed even if the Redis session is already
// deleted.
func (sa *SessionAuth) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// contextKey is an unexported type used as a key in context.WithValue to avoid
// collisions with keys from other packages.
type contextKey string

// sessionContextKey is the context key under which session data is stored.
const sessionContextKey contextKey = "session"

// SessionFromContext retrieves session data stored by the SessionAuth middleware.
// Returns the session data string and true if present, or empty string and
// false if the middleware did not run or the session was not found.
func SessionFromContext(ctx context.Context) (string, bool) {
	val, ok := ctx.Value(sessionContextKey).(string)
	return val, ok
}
