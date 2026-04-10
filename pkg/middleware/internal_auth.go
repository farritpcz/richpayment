// Package middleware provides reusable HTTP middleware components shared across
// RichPayment services. This file implements internal service-to-service
// authentication using HMAC-SHA256 signatures to prevent unauthorized calls
// between microservices.
//
// # Problem Statement
//
// Without internal authentication, any service that is compromised (e.g.,
// telegram-service) can directly call sensitive endpoints on other services
// (e.g., wallet-service /wallet/credit) to credit unlimited funds. This
// middleware ensures that only services possessing the shared secret can
// make authenticated requests.
//
// # Authentication Protocol
//
// Every internal request must carry three headers:
//
//   - X-Internal-Service:   the name of the calling service (e.g., "order-service")
//   - X-Internal-Timestamp: a Unix timestamp (seconds since epoch), must be
//     within 30 seconds of the server's clock to prevent replay attacks
//   - X-Internal-Signature: HMAC-SHA256 of the string "timestamp.service_name.request_path"
//     using the shared secret from the INTERNAL_API_SECRET environment variable
//
// If any header is missing or invalid, the middleware returns HTTP 401 Unauthorized
// and logs the rejected attempt with caller information for security auditing.
package middleware

import (
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/farritpcz/richpayment/pkg/crypto"
)

// internalAuthMaxClockSkew defines the maximum allowed difference (in seconds)
// between the request timestamp and the server's current time. Requests outside
// this window are rejected to prevent replay attacks. 30 seconds is generous
// enough to handle minor clock drift between containers but tight enough to
// limit the replay window.
const internalAuthMaxClockSkew = 30

// InternalAuth is an HTTP middleware that validates service-to-service
// authentication headers on incoming requests. It verifies three things:
//
//  1. The caller has identified itself via the X-Internal-Service header.
//  2. The request is fresh (X-Internal-Timestamp within 30 seconds).
//  3. The HMAC-SHA256 signature (X-Internal-Signature) is valid, proving
//     the caller possesses the shared secret.
//
// This middleware should be applied to all internal-facing HTTP endpoints
// that are not meant to be called by external clients.
type InternalAuth struct {
	// logger is the structured logger used to record rejected authentication
	// attempts. All log entries include the caller's service name, IP address,
	// request path, and the reason for rejection.
	logger *slog.Logger

	// secret is the shared HMAC signing key loaded from the INTERNAL_API_SECRET
	// environment variable. All services in the cluster must use the same secret.
	secret string
}

// NewInternalAuth creates a new InternalAuth middleware instance.
//
// Parameters:
//   - logger: structured logger for recording authentication events. All rejected
//     attempts are logged at the Warn level with full context (service name, IP,
//     path, reason) to support security auditing and incident investigation.
//
// The shared secret is loaded from the INTERNAL_API_SECRET environment variable.
// If the variable is not set, the middleware will reject ALL requests because
// an empty secret cannot produce valid signatures. This is a fail-closed design
// that prevents accidental misconfiguration from opening the system.
//
// Returns a ready-to-use *InternalAuth instance.
func NewInternalAuth(logger *slog.Logger) *InternalAuth {
	// Load the shared secret from the environment. In production, this should
	// be injected via Kubernetes secrets or Docker secrets. If missing, every
	// request will fail authentication — this is intentional (fail-closed).
	secret := os.Getenv("INTERNAL_API_SECRET")
	if secret == "" {
		logger.Warn("INTERNAL_API_SECRET is not set — all internal auth will be rejected (fail-closed)")
	}

	return &InternalAuth{
		logger: logger,
		secret: secret,
	}
}

// Middleware returns an http.Handler middleware function that can be used with
// standard Go HTTP routers and middleware chains. It wraps the next handler
// and only forwards the request if authentication succeeds.
//
// Usage with http.ServeMux:
//
//	internalAuth := middleware.NewInternalAuth(logger)
//	handler := internalAuth.Middleware(mux)
//
// Usage in a middleware chain:
//
//	srv := &http.Server{
//	    Handler: middleware.Recovery(log)(
//	        internalAuth.Middleware(mux),
//	    ),
//	}
//
// On authentication failure, the middleware:
//   - Returns HTTP 401 Unauthorized with a JSON error body.
//   - Logs the rejection at Warn level with: service name, remote address,
//     request path, and the specific reason (missing header, expired timestamp,
//     invalid signature).
//   - Does NOT forward the request to the next handler.
func (ia *InternalAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ---------------------------------------------------------------
		// Step 1: Extract and validate the X-Internal-Service header.
		// This identifies which service is making the request.
		// ---------------------------------------------------------------
		serviceName := r.Header.Get("X-Internal-Service")
		if serviceName == "" {
			ia.logRejection(r, serviceName, "missing X-Internal-Service header")
			writeInternalAuthError(w, http.StatusUnauthorized, "MISSING_SERVICE_HEADER",
				"X-Internal-Service header is required for internal calls")
			return
		}

		// ---------------------------------------------------------------
		// Step 2: Extract and validate the X-Internal-Timestamp header.
		// The timestamp must be a valid Unix epoch integer and must be
		// within the allowed clock skew window (±30 seconds).
		// ---------------------------------------------------------------
		timestampStr := r.Header.Get("X-Internal-Timestamp")
		if timestampStr == "" {
			ia.logRejection(r, serviceName, "missing X-Internal-Timestamp header")
			writeInternalAuthError(w, http.StatusUnauthorized, "MISSING_TIMESTAMP_HEADER",
				"X-Internal-Timestamp header is required for internal calls")
			return
		}

		// Parse the timestamp string to an int64 Unix epoch value.
		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			ia.logRejection(r, serviceName, "invalid timestamp format: "+timestampStr)
			writeInternalAuthError(w, http.StatusUnauthorized, "INVALID_TIMESTAMP",
				"X-Internal-Timestamp must be a valid Unix timestamp")
			return
		}

		// Calculate the absolute difference between request time and server time.
		// If greater than 30 seconds, reject to prevent replay attacks.
		clockDiff := math.Abs(float64(time.Now().Unix() - timestamp))
		if clockDiff > internalAuthMaxClockSkew {
			ia.logRejection(r, serviceName, fmt.Sprintf(
				"timestamp expired: diff=%.0fs, max=%ds", clockDiff, internalAuthMaxClockSkew))
			writeInternalAuthError(w, http.StatusUnauthorized, "EXPIRED_TIMESTAMP",
				"request timestamp is too old or too far in the future (max 30s skew)")
			return
		}

		// ---------------------------------------------------------------
		// Step 3: Extract and validate the X-Internal-Signature header.
		// The signature is HMAC-SHA256(secret, "timestamp.service_name.request_path").
		// ---------------------------------------------------------------
		signature := r.Header.Get("X-Internal-Signature")
		if signature == "" {
			ia.logRejection(r, serviceName, "missing X-Internal-Signature header")
			writeInternalAuthError(w, http.StatusUnauthorized, "MISSING_SIGNATURE_HEADER",
				"X-Internal-Signature header is required for internal calls")
			return
		}

		// Build the message that should have been signed by the caller:
		//   "timestamp.service_name.request_path"
		// Example: "1712700000.order-service./wallet/credit"
		//
		// Including the timestamp prevents signature reuse (replay).
		// Including the service name binds the signature to the caller.
		// Including the path prevents a signature for one endpoint from
		// being used against a different endpoint.
		message := fmt.Sprintf("%s.%s.%s", timestampStr, serviceName, r.URL.Path)

		// Verify the HMAC signature using constant-time comparison (via
		// crypto.HMACVerify) to prevent timing side-channel attacks.
		if !crypto.HMACVerify([]byte(message), []byte(ia.secret), signature) {
			ia.logRejection(r, serviceName, "invalid HMAC signature")
			writeInternalAuthError(w, http.StatusUnauthorized, "INVALID_SIGNATURE",
				"X-Internal-Signature does not match expected HMAC-SHA256")
			return
		}

		// ---------------------------------------------------------------
		// Authentication passed. Log success at Debug level and forward
		// the request to the next handler in the chain.
		// ---------------------------------------------------------------
		ia.logger.Debug("internal auth passed",
			"caller_service", serviceName,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)

		next.ServeHTTP(w, r)
	})
}

// logRejection logs a rejected authentication attempt at the Warn level with
// full context for security auditing. Every rejected request is logged so that
// operations teams can detect brute-force attempts, misconfigured services,
// or potential intrusion attempts.
//
// Parameters:
//   - r: the HTTP request, used to extract the remote address and path.
//   - serviceName: the value from X-Internal-Service (may be empty if missing).
//   - reason: a human-readable explanation of why the request was rejected.
func (ia *InternalAuth) logRejection(r *http.Request, serviceName string, reason string) {
	ia.logger.Warn("internal auth rejected",
		"caller_service", serviceName,
		"remote_addr", r.RemoteAddr,
		"method", r.Method,
		"path", r.URL.Path,
		"reason", reason,
	)
}

// writeInternalAuthError writes a JSON error response for internal auth failures.
// This is a convenience wrapper around the standard JSON error encoding pattern
// used throughout the RichPayment middleware layer.
//
// Parameters:
//   - w: the HTTP response writer.
//   - status: the HTTP status code (typically 401 or 403).
//   - code: a machine-readable error code (e.g., "MISSING_SERVICE_HEADER").
//   - message: a human-readable error description.
func writeInternalAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// Write a structured JSON error body matching the standard RichPayment
	// error envelope format: { "success": false, "error": "...", "code": "..." }
	fmt.Fprintf(w, `{"success":false,"error":%q,"code":%q}`, message, code)
}
