// Package handler provides HTTP transport for the auth service. It translates
// incoming JSON requests into service calls and writes JSON responses.
package handler

import (
	"encoding/json"
	"net/http"

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
// required fields, delegates to AuthService.Login, and returns either a
// session payload or an error.
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

	sess, err := h.auth.Login(r.Context(), req.Email, req.Password, req.TOTPCode, req.UserType)
	if err != nil {
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
// session ID corresponds to an active, non-expired session and returns the
// session metadata when valid.
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

	sess, err := h.auth.ValidateSession(r.Context(), req.SessionID)
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
