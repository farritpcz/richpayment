package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
)

// APIKeyAuth validates requests using X-API-Key, X-Timestamp, and X-Signature headers.
// The signature is HMAC-SHA256(apiSecret, timestamp+method+path+body).
// For now this is a stub that only checks header presence.
type APIKeyAuth struct {
	logger *slog.Logger
}

// NewAPIKeyAuth creates a new APIKeyAuth middleware.
func NewAPIKeyAuth(logger *slog.Logger) *APIKeyAuth {
	return &APIKeyAuth{logger: logger}
}

// Middleware returns the HTTP middleware function.
func (a *APIKeyAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			writeErrorJSON(w, http.StatusUnauthorized, "MISSING_API_KEY", "X-API-Key header is required")
			return
		}

		timestamp := r.Header.Get("X-Timestamp")
		if timestamp == "" {
			writeErrorJSON(w, http.StatusUnauthorized, "MISSING_TIMESTAMP", "X-Timestamp header is required")
			return
		}

		signature := r.Header.Get("X-Signature")
		if signature == "" {
			writeErrorJSON(w, http.StatusUnauthorized, "MISSING_SIGNATURE", "X-Signature header is required")
			return
		}

		// Validate timestamp is within 5 minutes.
		ts, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			writeErrorJSON(w, http.StatusUnauthorized, "INVALID_TIMESTAMP", "X-Timestamp must be a unix timestamp")
			return
		}
		diff := time.Now().Unix() - ts
		if math.Abs(float64(diff)) > 300 {
			writeErrorJSON(w, http.StatusUnauthorized, "EXPIRED_TIMESTAMP", "request timestamp is too old or too far in the future")
			return
		}

		// Stub: In production, look up the merchant's secret by apiKey from Redis/DB
		// and verify the HMAC signature. For now, accept any valid-looking request.
		_ = signature

		a.logger.Debug("api key auth passed", "api_key", apiKey[:8]+"...")
		next.ServeHTTP(w, r)
	})
}

// VerifyHMAC checks whether the provided signature matches the expected HMAC-SHA256.
// This is a utility for when real secret lookup is implemented.
func VerifyHMAC(secret, message, providedSig string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(providedSig))
}
