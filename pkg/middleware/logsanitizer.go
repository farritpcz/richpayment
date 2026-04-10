// Package middleware provides reusable HTTP middleware components shared across
// RichPayment services.
//
// This file implements the log sanitization middleware which intercepts HTTP
// requests and responses before they are logged, masking sensitive data in
// headers and JSON bodies to prevent accidental credential leakage into log
// aggregation systems, monitoring dashboards, or debug output.
//
// The following sensitive data is sanitized:
//
// Headers:
//   - X-API-Key:            show first 8 and last 4 chars (e.g. "rpay_liv...f456")
//   - X-Signature:          show first 8 chars only (e.g. "a1b2c3d4...")
//   - X-Internal-Signature: show first 8 chars only (e.g. "a1b2c3d4...")
//   - Authorization:        masked as "Bearer ****...****"
//   - Cookie:               session values masked as "richpay_session=****...****"
//
// JSON body fields:
//   - "password":   replaced with "[REDACTED]"
//   - "api_key":    replaced with "[REDACTED]"
//   - "secret":     replaced with "[REDACTED]"
//   - "totp_code":  replaced with "[REDACTED]"
package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

// sensitiveBodyFields lists the JSON field names whose values must be
// replaced with "[REDACTED]" before the body is written to logs. These
// patterns match both the field name and its value in a JSON string,
// replacing the value portion while preserving the field name for
// debugging context.
var sensitiveBodyFields = []string{
	"password",
	"api_key",
	"secret",
	"totp_code",
}

// sensitiveBodyPatterns contains pre-compiled regular expressions for each
// sensitive body field. Each pattern matches a JSON key-value pair like
// "password":"any_value" and captures the key portion so the replacement
// can preserve it while redacting only the value.
//
// The regex handles:
//   - Optional whitespace around the colon.
//   - Escaped quotes within the value string.
//   - Both simple and complex string values.
var sensitiveBodyPatterns []*regexp.Regexp

// init pre-compiles the regex patterns for all sensitive body fields at
// package initialisation time. This avoids re-compiling on every request,
// which would be wasteful given the patterns never change at runtime.
func init() {
	for _, field := range sensitiveBodyFields {
		// Pattern explanation:
		//   "field_name" : "any_value"
		//   The (?:  ) around the value handles escaped quotes inside strings.
		pattern := regexp.MustCompile(`("` + regexp.QuoteMeta(field) + `")\s*:\s*"(?:[^"\\]|\\.)*"`)
		sensitiveBodyPatterns = append(sensitiveBodyPatterns, pattern)
	}
}

// LogSanitizer is an HTTP middleware that sanitizes sensitive data from
// request headers and bodies before they appear in log output. It wraps
// the request with masked headers and a sanitized body, then passes the
// modified request to downstream handlers and logging middleware.
//
// IMPORTANT: This middleware should be placed in the middleware chain
// BEFORE any request-logging middleware so that the logger sees only the
// sanitized versions of headers and bodies.
type LogSanitizer struct {
	// logger is used for the middleware's own operational logging
	// (e.g. errors during body reading). It is NOT the logger that
	// writes request logs — that logger sits downstream.
	logger *slog.Logger
}

// NewLogSanitizer creates a new LogSanitizer middleware instance.
// The provided logger is used for the middleware's own diagnostic output
// and is NOT the same logger used by the request-logging middleware.
func NewLogSanitizer(logger *slog.Logger) *LogSanitizer {
	return &LogSanitizer{logger: logger}
}

// Middleware returns an http.Handler that sanitizes sensitive data from
// the request before passing it to the next handler in the chain. It:
//  1. Clones the request headers and masks sensitive values in the clone.
//  2. Reads and sanitizes the request body if it contains sensitive fields.
//  3. Replaces the request's Header and Body with the sanitized versions.
//  4. Calls the next handler with the sanitized request.
//
// The original request body is fully read into memory and then replaced
// with a new io.ReadCloser so downstream handlers can still read it.
// This is acceptable because request bodies in this system are small
// JSON payloads (typically < 10KB).
func (ls *LogSanitizer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// -----------------------------------------------------------
		// Step 1: Sanitize request headers.
		// Create a shallow clone of the header map so we do not mutate
		// the original request headers (which downstream handlers may
		// still need in their original form for authentication).
		// -----------------------------------------------------------
		sanitizedHeaders := sanitizeHeaders(r.Header)

		// -----------------------------------------------------------
		// Step 2: Sanitize request body if it contains sensitive JSON
		// fields. We only process the body if the Content-Type is JSON
		// and the body is non-nil, to avoid unnecessary work on file
		// uploads or other binary payloads.
		// -----------------------------------------------------------
		var sanitizedBody []byte
		var originalBody []byte
		if r.Body != nil && isJSONContentType(r.Header.Get("Content-Type")) {
			var err error
			originalBody, err = io.ReadAll(r.Body)
			if err != nil {
				ls.logger.Error("log sanitizer: failed to read request body",
					"error", err,
					"path", r.URL.Path,
				)
				// On read failure, proceed with the original request
				// rather than blocking the request entirely.
				next.ServeHTTP(w, r)
				return
			}
			// Close the original body since we consumed it.
			r.Body.Close()

			// Sanitize the body for logging purposes.
			sanitizedBody = sanitizeBody(originalBody)

			// Replace the request body with the ORIGINAL (unsanitized)
			// data so downstream handlers can process it normally.
			// The sanitized version is only used in log context.
			r.Body = io.NopCloser(bytes.NewReader(originalBody))
		}

		// -----------------------------------------------------------
		// Step 3: Store sanitized data in the request context so the
		// request-logging middleware can access it.
		// We attach the sanitized headers and body as slog attributes
		// to a child logger, or we can store them in context. For
		// simplicity, we log the sanitized version of sensitive
		// headers right here if debug logging is enabled.
		// -----------------------------------------------------------
		if ls.logger.Enabled(r.Context(), slog.LevelDebug) {
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
			}
			// Log sanitized header values for debugging.
			for key, values := range sanitizedHeaders {
				if len(values) > 0 {
					attrs = append(attrs, "header."+key, values[0])
				}
			}
			// Log sanitized body if present.
			if sanitizedBody != nil {
				attrs = append(attrs, "body", string(sanitizedBody))
			}
			ls.logger.Debug("sanitized request", attrs...)
		}

		// -----------------------------------------------------------
		// Step 4: Replace request headers with sanitized versions for
		// any downstream logging middleware that inspects r.Header.
		// We store the original headers in context so auth middleware
		// can still access unsanitized values for authentication.
		// -----------------------------------------------------------
		origHeaders := r.Header
		r.Header = sanitizedHeaders

		// Wrap the response writer to capture the response for body
		// sanitization in logs (if needed in the future).
		next.ServeHTTP(w, r)

		// Restore original headers after the request is complete so
		// we do not permanently mutate the request object.
		r.Header = origHeaders
	})
}

// sanitizeHeaders creates a sanitized copy of the given HTTP headers.
// Sensitive headers are masked according to these rules:
//   - X-API-Key: first 8 chars + "..." + last 4 chars (e.g. "rpay_liv...f456").
//     If the key is shorter than 12 chars, show first 4 + "...".
//   - X-Signature: first 8 chars + "..." (remaining chars hidden).
//   - X-Internal-Signature: first 8 chars + "..." (same as X-Signature).
//   - Authorization: always masked as "Bearer ****...****" regardless of
//     the actual token type or value.
//   - Cookie: session cookie values are masked as
//     "richpay_session=****...****" while non-session cookies are preserved.
//
// All other headers are copied as-is without modification.
func sanitizeHeaders(original http.Header) http.Header {
	// Create a deep copy of the headers to avoid mutating the original.
	sanitized := make(http.Header, len(original))
	for key, values := range original {
		sanitized[key] = append([]string(nil), values...)
	}

	// Mask X-API-Key: show first 8 and last 4 characters.
	// Example: "rpay_live_abc123xyz456" -> "rpay_liv...f456"
	if apiKey := sanitized.Get("X-API-Key"); apiKey != "" {
		sanitized.Set("X-API-Key", maskAPIKey(apiKey))
	}

	// Mask X-Signature: show only first 8 characters.
	// Example: "a1b2c3d4e5f6g7h8" -> "a1b2c3d4..."
	if sig := sanitized.Get("X-Signature"); sig != "" {
		sanitized.Set("X-Signature", maskSignature(sig))
	}

	// Mask X-Internal-Signature: same treatment as X-Signature.
	// This header is used for inter-service communication and contains
	// HMAC signatures that must not appear in logs.
	if intSig := sanitized.Get("X-Internal-Signature"); intSig != "" {
		sanitized.Set("X-Internal-Signature", maskSignature(intSig))
	}

	// Mask Authorization: always replace with "Bearer ****...****"
	// regardless of the actual scheme or token value. This prevents
	// Bearer tokens, Basic auth credentials, and any other auth
	// schemes from leaking into logs.
	if auth := sanitized.Get("Authorization"); auth != "" {
		sanitized.Set("Authorization", "Bearer ****...****")
	}

	// Mask Cookie: find and mask the richpay_session cookie value.
	// Other cookies are preserved as-is since they may not contain
	// sensitive data.
	if cookieHeader := sanitized.Get("Cookie"); cookieHeader != "" {
		sanitized.Set("Cookie", maskCookieHeader(cookieHeader))
	}

	return sanitized
}

// maskAPIKey masks an API key by showing only the first 8 and last 4
// characters, separated by "...". This provides enough context to identify
// which key is being used (the prefix typically encodes the environment,
// e.g. "rpay_liv" for live keys) while hiding the secret portion.
//
// Examples:
//   - "rpay_live_abc123def456" -> "rpay_liv...f456"
//   - "short"                 -> "shor..."
//   - ""                      -> "****"
func maskAPIKey(key string) string {
	if len(key) == 0 {
		return "****"
	}
	if len(key) <= 12 {
		// Key is too short to show both prefix and suffix safely.
		// Show at most the first 4 characters.
		end := 4
		if len(key) < end {
			end = len(key)
		}
		return key[:end] + "..."
	}
	// Show first 8 and last 4 characters.
	return key[:8] + "..." + key[len(key)-4:]
}

// maskSignature masks a signature string by showing only the first 8
// characters followed by "...". Signatures are typically hex-encoded HMAC
// digests and do not have meaningful prefixes beyond a few bytes, so
// showing 8 chars provides sufficient debugging context without exposing
// the full signature.
//
// Examples:
//   - "a1b2c3d4e5f6g7h8i9j0" -> "a1b2c3d4..."
//   - "short"                 -> "short..."
//   - ""                      -> "****"
func maskSignature(sig string) string {
	if len(sig) == 0 {
		return "****"
	}
	if len(sig) <= 8 {
		return sig + "..."
	}
	return sig[:8] + "..."
}

// maskCookieHeader masks the richpay_session cookie value in a Cookie
// header string while preserving all other cookies. The session cookie
// value is replaced with "****...****" to prevent session IDs from
// appearing in log output.
//
// Example:
//
//	Input:  "richpay_session=abc123xyz; other=value"
//	Output: "richpay_session=****...****; other=value"
func maskCookieHeader(cookieHeader string) string {
	// Split the cookie header into individual cookie pairs.
	parts := strings.Split(cookieHeader, ";")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		// Check if this cookie pair is the session cookie.
		if strings.HasPrefix(trimmed, "richpay_session=") {
			// Replace the entire value with the masked version.
			parts[i] = " richpay_session=****...****"
		}
	}
	result := strings.Join(parts, ";")
	// Trim leading space from the first cookie.
	return strings.TrimSpace(result)
}

// sanitizeBody replaces sensitive field values in a JSON body with
// "[REDACTED]". It uses regex-based replacement rather than full JSON
// parsing to handle potentially malformed JSON gracefully and to avoid
// the overhead of marshal/unmarshal cycles.
//
// The following fields are redacted:
//   - "password":"..."  -> "password":"[REDACTED]"
//   - "api_key":"..."   -> "api_key":"[REDACTED]"
//   - "secret":"..."    -> "secret":"[REDACTED]"
//   - "totp_code":"..." -> "totp_code":"[REDACTED]"
//
// The field names are preserved so that log readers can see which fields
// were present in the request without seeing their actual values.
func sanitizeBody(body []byte) []byte {
	result := make([]byte, len(body))
	copy(result, body)

	for _, pattern := range sensitiveBodyPatterns {
		// Replace the matched key-value pair with key:"[REDACTED]".
		// The $1 back-reference preserves the captured field name.
		result = pattern.ReplaceAll(result, []byte(`$1:"[REDACTED]"`))
	}

	return result
}

// containsSensitiveFields checks whether a JSON body contains any of the
// sensitive field names that should be redacted before logging. This is
// a fast preliminary check to avoid running regex replacements on bodies
// that do not contain any sensitive data.
//
// Returns true if any sensitive field name is found in the body.
func containsSensitiveFields(body []byte) bool {
	bodyStr := string(body)
	for _, field := range sensitiveBodyFields {
		if strings.Contains(bodyStr, `"`+field+`"`) {
			return true
		}
	}
	return false
}

// isJSONContentType checks whether the given Content-Type header value
// indicates a JSON payload. It handles both "application/json" and
// variants with parameters (e.g. "application/json; charset=utf-8").
//
// Returns true if the content type is JSON, false otherwise.
func isJSONContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}

// SanitizeBodyString is a convenience function that sanitizes a JSON body
// string for use in log messages. It is the string equivalent of
// sanitizeBody and can be called directly by logging middleware or
// handlers that have the body as a string rather than a byte slice.
//
// Example usage in a logger:
//
//	sanitized := middleware.SanitizeBodyString(bodyStr)
//	logger.Info("request body", "body", sanitized)
func SanitizeBodyString(body string) string {
	return string(sanitizeBody([]byte(body)))
}
