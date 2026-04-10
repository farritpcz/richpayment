// Package handler provides HTTP transport for the auth service. It translates
// incoming JSON requests into service calls and writes JSON responses.
//
// Security note: This handler extracts the client IP address and User-Agent
// header from each request and passes them to the auth service for IP-based
// rate limiting, session IP binding, and User-Agent fingerprinting. The
// client IP is read from the X-Forwarded-For header (set by the reverse
// proxy / load balancer) or falls back to RemoteAddr if the header is absent.
package handler

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"

	"github.com/farritpcz/richpayment/services/auth/internal/model"
	"github.com/farritpcz/richpayment/services/auth/internal/service"
)

// AuthHandler exposes HTTP endpoints for authentication. It delegates all
// business logic to the injected AuthService and is responsible only for
// request parsing, response formatting, and HTTP status-code selection.
type AuthHandler struct {
	// auth is the core authentication service that performs login, logout,
	// and session validation.
	auth *service.AuthService
}

// NewAuthHandler creates a new AuthHandler wired to the given AuthService.
func NewAuthHandler(auth *service.AuthService) *AuthHandler {
	return &AuthHandler{auth: auth}
}

// RegisterRoutes wires the authentication handler methods onto the given
// ServeMux. It registers POST endpoints for login, logout, and session
// validation.
func (h *AuthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.HandleFunc("POST /auth/logout", h.Logout)
	mux.HandleFunc("POST /auth/validate", h.ValidateSession)
}

// --- request / response types ------------------------------------------------

// loginRequest is the JSON body expected by the Login endpoint.
type loginRequest struct {
	// Email is the user's email address used as the login identifier.
	Email string `json:"email"`
	// Password is the plaintext password to verify against the stored hash.
	Password string `json:"password"`
	// TOTPCode is the optional 6-digit TOTP code required when 2FA is enabled.
	TOTPCode string `json:"totp_code"`
	// UserType indicates the actor type (admin, merchant, agent) so the correct
	// user table is queried.
	UserType model.UserType `json:"user_type"`
}

// loginResponse is the JSON body returned on a successful login.
type loginResponse struct {
	// SessionID is the unique identifier for the newly created session.
	SessionID string `json:"session_id"`
	// UserID is the UUID of the authenticated user.
	UserID string `json:"user_id"`
	// Email is the authenticated user's email address.
	Email string `json:"email"`
	// Role is the human-readable role name (e.g. "admin", "operator").
	Role model.Role `json:"role"`
	// RoleMask is the effective permission bitmask for the session.
	RoleMask model.Permission `json:"role_mask"`
	// ExpiresAt is the RFC 3339 timestamp when the session expires.
	ExpiresAt string `json:"expires_at"`
}

// logoutRequest is the JSON body expected by the Logout endpoint.
type logoutRequest struct {
	// SessionID identifies the session to destroy.
	SessionID string `json:"session_id"`
}

// validateRequest is the JSON body expected by the ValidateSession endpoint.
type validateRequest struct {
	// SessionID identifies the session to validate.
	SessionID string `json:"session_id"`
}

// validateResponse is the JSON body returned by the ValidateSession endpoint.
type validateResponse struct {
	// Valid indicates whether the session is still active and not expired.
	Valid bool `json:"valid"`
	// UserID is the UUID of the session owner (omitted when invalid).
	UserID string `json:"user_id,omitempty"`
	// Email is the session owner's email (omitted when invalid).
	Email string `json:"email,omitempty"`
	// UserType is the actor type string (omitted when invalid).
	UserType string `json:"user_type,omitempty"`
	// Role is the session owner's role name (omitted when invalid).
	Role model.Role `json:"role,omitempty"`
	// RoleMask is the effective permission bitmask (omitted when invalid).
	RoleMask model.Permission `json:"role_mask,omitempty"`
}

// errorResponse is a generic JSON error envelope.
type errorResponse struct {
	// Error contains a human-readable error message.
	Error string `json:"error"`
}

// --- handlers ----------------------------------------------------------------

// Login handles POST /auth/login. It decodes the request body, validates
// required fields, extracts the client IP and User-Agent for rate limiting
// and session binding, then delegates to AuthService.Login. If the login
// is rate-limited, it returns HTTP 429 with a Retry-After header.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "email and password are required"})
		return
	}
	// Default to admin user type when the caller does not specify one.
	if req.UserType == "" {
		req.UserType = model.UserTypeAdmin
	}

	// Extract client IP and User-Agent for security features.
	// The IP is used for rate limiting and session binding; the
	// User-Agent is hashed and stored for session fingerprinting.
	clientIP := extractClientIP(r)
	userAgent := r.UserAgent()

	sess, retryAfter, err := h.auth.Login(r.Context(), req.Email, req.Password, req.TOTPCode, req.UserType, clientIP, userAgent)
	if err != nil {
		// If retryAfter > 0, the login was rejected by the IP rate
		// limiter. Return HTTP 429 with a Retry-After header so the
		// client knows how long to wait before retrying.
		if retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		SessionID: sess.ID,
		UserID:    sess.UserID.String(),
		Email:     sess.Email,
		Role:      sess.Role,
		RoleMask:  sess.RoleMask,
		ExpiresAt: sess.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// Logout handles POST /auth/logout. It destroys the session identified by the
// request's session_id field.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if req.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "session_id is required"})
		return
	}

	if err := h.auth.Logout(r.Context(), req.SessionID); err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ValidateSession handles POST /auth/validate. It checks whether the supplied
// session ID corresponds to an active, non-expired session. It also forwards
// the client IP and User-Agent to the service layer for IP binding and
// fingerprint verification. If either check fails, the session is
// automatically invalidated and the response indicates an invalid session.
func (h *AuthHandler) ValidateSession(w http.ResponseWriter, r *http.Request) {
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if req.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "session_id is required"})
		return
	}

	// Extract the client IP and User-Agent from the HTTP request so
	// the auth service can verify them against the values stored in
	// the session at login time.
	clientIP := extractClientIP(r)
	userAgent := r.UserAgent()

	sess, err := h.auth.ValidateSession(r.Context(), req.SessionID, clientIP, userAgent)
	if err != nil {
		writeJSON(w, http.StatusOK, validateResponse{Valid: false})
		return
	}

	writeJSON(w, http.StatusOK, validateResponse{
		Valid:    true,
		UserID:   sess.UserID.String(),
		Email:    sess.Email,
		UserType: string(sess.UserType),
		Role:     sess.Role,
		RoleMask: sess.RoleMask,
	})
}

// --- helpers -----------------------------------------------------------------

// writeJSON serialises v as JSON and writes it to the response writer with the
// given HTTP status code and a Content-Type: application/json header.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// extractClientIP extracts the real client IP address from the request.
// It first checks the X-Forwarded-For header (set by reverse proxies and
// load balancers), taking only the first (leftmost) IP which represents
// the original client. If the header is absent, it falls back to
// r.RemoteAddr, stripping the port number if present.
//
// This IP is used for:
//   - IP-based login rate limiting (max 10 attempts/hour per IP).
//   - Session IP binding (stored at login, verified on every validation).
func extractClientIP(r *http.Request) string {
	// X-Forwarded-For may contain multiple IPs: "client, proxy1, proxy2".
	// The first entry is the original client IP.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Parse just the first IP from the comma-separated list.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}

	// Fall back to RemoteAddr (e.g. "192.168.1.1:12345").
	// Strip the port if present.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port (e.g. in tests).
		return r.RemoteAddr
	}
	return host
}
