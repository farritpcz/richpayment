// Package handler implements the HTTP transport layer for the user-service.
// It defines JSON request/response types and maps HTTP routes to the
// corresponding service-layer methods for admins, merchants, agents, and
// partners.
package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/services/user/internal/service"
)

// UserHandler groups all HTTP handler methods for the user-service API.
// It holds references to every service-layer component (admin, merchant,
// agent, partner) and provides a Register method to bind routes to a mux.
type UserHandler struct {
	// adminSvc handles admin CRUD business logic.
	adminSvc *service.AdminService

	// merchantSvc handles merchant CRUD and API key management.
	merchantSvc *service.MerchantService

	// agentSvc handles agent CRUD business logic.
	agentSvc *service.AgentService

	// partnerSvc handles partner CRUD business logic.
	partnerSvc *service.PartnerService
}

// NewUserHandler creates a new UserHandler with all required service
// dependencies.
//
// Parameters:
//   - adminSvc: the admin service for admin CRUD operations.
//   - merchantSvc: the merchant service for merchant CRUD and key management.
//   - agentSvc: the agent service for agent CRUD operations.
//   - partnerSvc: the partner service for partner CRUD operations.
//
// Returns a pointer to a fully initialised UserHandler.
func NewUserHandler(
	adminSvc *service.AdminService,
	merchantSvc *service.MerchantService,
	agentSvc *service.AgentService,
	partnerSvc *service.PartnerService,
) *UserHandler {
	return &UserHandler{
		adminSvc:    adminSvc,
		merchantSvc: merchantSvc,
		agentSvc:    agentSvc,
		partnerSvc:  partnerSvc,
	}
}

// Register binds all user-service HTTP routes to the given ServeMux.
// Routes follow the pattern: METHOD /api/v1/{entity}/{optional-id}/{action}.
//
// Admin routes:
//
//	POST   /api/v1/admins          - Create a new admin
//	GET    /api/v1/admins          - List admins (paginated)
//	GET    /api/v1/admins/{id}     - Get a single admin by ID
//	PUT    /api/v1/admins/{id}     - Update an admin
//
// Merchant routes:
//
//	POST   /api/v1/merchants                     - Create a new merchant
//	GET    /api/v1/merchants                     - List merchants (paginated)
//	GET    /api/v1/merchants/{id}                - Get a single merchant by ID
//	PUT    /api/v1/merchants/{id}                - Update a merchant
//	POST   /api/v1/merchants/{id}/revoke-key     - Revoke and rotate API key
//
// Agent routes:
//
//	POST   /api/v1/agents          - Create a new agent
//	GET    /api/v1/agents          - List agents (paginated)
//	GET    /api/v1/agents/{id}     - Get a single agent by ID
//	PUT    /api/v1/agents/{id}     - Update an agent
//
// Partner routes:
//
//	POST   /api/v1/partners        - Create a new partner
//	GET    /api/v1/partners        - List partners (paginated)
//	GET    /api/v1/partners/{id}   - Get a single partner by ID
//	PUT    /api/v1/partners/{id}   - Update a partner
func (h *UserHandler) Register(mux *http.ServeMux) {
	// --- Admin routes ---
	mux.HandleFunc("POST /api/v1/admins", h.handleCreateAdmin)
	mux.HandleFunc("GET /api/v1/admins", h.handleListAdmins)
	mux.HandleFunc("GET /api/v1/admins/{id}", h.handleGetAdmin)
	mux.HandleFunc("PUT /api/v1/admins/{id}", h.handleUpdateAdmin)

	// --- Merchant routes ---
	mux.HandleFunc("POST /api/v1/merchants", h.handleCreateMerchant)
	mux.HandleFunc("GET /api/v1/merchants", h.handleListMerchants)
	mux.HandleFunc("GET /api/v1/merchants/{id}", h.handleGetMerchant)
	mux.HandleFunc("PUT /api/v1/merchants/{id}", h.handleUpdateMerchant)
	mux.HandleFunc("POST /api/v1/merchants/{id}/revoke-key", h.handleRevokeAPIKey)

	// --- Agent routes ---
	mux.HandleFunc("POST /api/v1/agents", h.handleCreateAgent)
	mux.HandleFunc("GET /api/v1/agents", h.handleListAgents)
	mux.HandleFunc("GET /api/v1/agents/{id}", h.handleGetAgent)
	mux.HandleFunc("PUT /api/v1/agents/{id}", h.handleUpdateAgent)

	// --- Partner routes ---
	mux.HandleFunc("POST /api/v1/partners", h.handleCreatePartner)
	mux.HandleFunc("GET /api/v1/partners", h.handleListPartners)
	mux.HandleFunc("GET /api/v1/partners/{id}", h.handleGetPartner)
	mux.HandleFunc("PUT /api/v1/partners/{id}", h.handleUpdatePartner)
}

// ---------------------------------------------------------------------------
// Admin handlers
// ---------------------------------------------------------------------------

// createAdminRequest is the JSON body for POST /api/v1/admins.
// Contains all fields required to create a new admin account.
type createAdminRequest struct {
	// Email is the admin's login email address.
	Email string `json:"email"`

	// Password is the plaintext password (will be hashed server-side).
	Password string `json:"password"`

	// DisplayName is the human-readable name shown in the admin dashboard.
	DisplayName string `json:"display_name"`

	// RoleMask is the bitmask of permissions for this admin.
	RoleMask int64 `json:"role_mask"`
}

// handleCreateAdmin handles POST /api/v1/admins.
// It decodes the JSON request body, delegates to the admin service,
// and returns the created admin as JSON with a 201 Created status.
func (h *UserHandler) handleCreateAdmin(w http.ResponseWriter, r *http.Request) {
	// Decode the incoming JSON request body.
	var req createAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Delegate to the admin service to create the admin.
	admin, err := h.adminSvc.CreateAdmin(r.Context(), req.Email, req.Password, req.DisplayName, req.RoleMask)
	if err != nil {
		logger.Error("create admin failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Return the created admin as JSON.
	writeJSON(w, http.StatusCreated, admin)
}

// handleGetAdmin handles GET /api/v1/admins/{id}.
// It extracts the admin UUID from the URL path and returns the admin as JSON.
func (h *UserHandler) handleGetAdmin(w http.ResponseWriter, r *http.Request) {
	// Extract the admin ID from the URL path parameter.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid admin id"})
		return
	}

	// Fetch the admin from the service layer.
	admin, err := h.adminSvc.GetAdmin(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "admin not found"})
		return
	}

	writeJSON(w, http.StatusOK, admin)
}

// handleListAdmins handles GET /api/v1/admins?page=1&limit=20.
// It reads pagination parameters from query strings and returns a paginated
// list of admins with metadata.
func (h *UserHandler) handleListAdmins(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters from the query string with defaults.
	page := parseQueryInt(r, "page", 1)
	limit := parseQueryInt(r, "limit", 20)

	// Fetch the paginated list from the service layer.
	admins, total, err := h.adminSvc.ListAdmins(r.Context(), page, limit)
	if err != nil {
		logger.Error("list admins failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Return the list with pagination metadata.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":  admins,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// updateAdminRequest is the JSON body for PUT /api/v1/admins/{id}.
// All fields are optional; only provided fields will be updated.
type updateAdminRequest struct {
	// DisplayName is the new display name (optional).
	DisplayName *string `json:"display_name,omitempty"`

	// RoleMask is the new role bitmask (optional).
	RoleMask *int64 `json:"role_mask,omitempty"`

	// Status is the new status string (optional).
	Status *string `json:"status,omitempty"`

	// Email is the new email address (optional).
	Email *string `json:"email,omitempty"`
}

// handleUpdateAdmin handles PUT /api/v1/admins/{id}.
// It decodes the partial update request, builds a fields map from
// non-nil values, and delegates to the admin service.
func (h *UserHandler) handleUpdateAdmin(w http.ResponseWriter, r *http.Request) {
	// Extract the admin ID from the URL path.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid admin id"})
		return
	}

	// Decode the partial update request body.
	var req updateAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Build the dynamic fields map from non-nil request fields.
	// Only fields that were explicitly provided in the JSON body are included.
	fields := make(map[string]interface{})
	if req.DisplayName != nil {
		fields["display_name"] = *req.DisplayName
	}
	if req.RoleMask != nil {
		fields["role_mask"] = *req.RoleMask
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}
	if req.Email != nil {
		fields["email"] = *req.Email
	}

	// Delegate to the admin service.
	if err := h.adminSvc.UpdateAdmin(r.Context(), id, fields); err != nil {
		logger.Error("update admin failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// ---------------------------------------------------------------------------
// Merchant handlers
// ---------------------------------------------------------------------------

// createMerchantRequest is the JSON body for POST /api/v1/merchants.
type createMerchantRequest struct {
	// Name is the merchant's business name.
	Name string `json:"name"`

	// Email is the merchant's contact email.
	Email string `json:"email"`

	// WebhookURL is the callback endpoint for order notifications.
	WebhookURL string `json:"webhook_url"`

	// AgentID is the optional UUID of the managing agent (as string).
	AgentID string `json:"agent_id,omitempty"`

	// DepositFeePct is the deposit fee percentage (e.g. "0.02").
	DepositFeePct string `json:"deposit_fee_pct"`

	// WithdrawalFeePct is the withdrawal fee percentage (e.g. "0.01").
	WithdrawalFeePct string `json:"withdrawal_fee_pct"`

	// DailyWithdrawalLimit is the maximum daily withdrawal (e.g. "100000").
	DailyWithdrawalLimit string `json:"daily_withdrawal_limit"`
}

// handleCreateMerchant handles POST /api/v1/merchants.
// It decodes the JSON body, parses decimal fields, delegates to the
// merchant service, and returns the created merchant with its API key.
func (h *UserHandler) handleCreateMerchant(w http.ResponseWriter, r *http.Request) {
	// Decode the incoming JSON request body.
	var req createMerchantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Parse optional agent ID from string to *uuid.UUID.
	var agentID *uuid.UUID
	if req.AgentID != "" {
		parsed, err := uuid.Parse(req.AgentID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent_id"})
			return
		}
		agentID = &parsed
	}

	// Parse decimal fields for fee percentages and withdrawal limit.
	depositFeePct, err := decimal.NewFromString(req.DepositFeePct)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deposit_fee_pct"})
		return
	}

	withdrawalFeePct, err := decimal.NewFromString(req.WithdrawalFeePct)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid withdrawal_fee_pct"})
		return
	}

	dailyLimit, err := decimal.NewFromString(req.DailyWithdrawalLimit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid daily_withdrawal_limit"})
		return
	}

	// Build the service input struct.
	input := service.CreateMerchantInput{
		Name:                 req.Name,
		Email:                req.Email,
		WebhookURL:           req.WebhookURL,
		AgentID:              agentID,
		DepositFeePct:        depositFeePct,
		WithdrawalFeePct:     withdrawalFeePct,
		DailyWithdrawalLimit: dailyLimit,
	}

	// Delegate to the merchant service.
	merchant, apiKey, err := h.merchantSvc.CreateMerchant(r.Context(), input)
	if err != nil {
		logger.Error("create merchant failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Return the merchant along with the raw API key.
	// The API key is only available at creation time.
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"merchant": merchant,
		"api_key":  apiKey,
	})
}

// handleGetMerchant handles GET /api/v1/merchants/{id}.
func (h *UserHandler) handleGetMerchant(w http.ResponseWriter, r *http.Request) {
	// Extract the merchant ID from the URL path parameter.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid merchant id"})
		return
	}

	// Fetch the merchant from the service layer.
	merchant, err := h.merchantSvc.GetMerchant(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "merchant not found"})
		return
	}

	writeJSON(w, http.StatusOK, merchant)
}

// handleListMerchants handles GET /api/v1/merchants?agent_id=...&page=1&limit=20.
func (h *UserHandler) handleListMerchants(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters.
	page := parseQueryInt(r, "page", 1)
	limit := parseQueryInt(r, "limit", 20)

	// Parse optional agent_id filter from query string.
	var agentID *uuid.UUID
	if agentIDStr := r.URL.Query().Get("agent_id"); agentIDStr != "" {
		parsed, err := uuid.Parse(agentIDStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent_id filter"})
			return
		}
		agentID = &parsed
	}

	// Fetch the paginated list from the service layer.
	merchants, total, err := h.merchantSvc.ListMerchants(r.Context(), agentID, page, limit)
	if err != nil {
		logger.Error("list merchants failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":  merchants,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// updateMerchantRequest is the JSON body for PUT /api/v1/merchants/{id}.
type updateMerchantRequest struct {
	// Name is the new business name (optional).
	Name *string `json:"name,omitempty"`

	// Email is the new contact email (optional).
	Email *string `json:"email,omitempty"`

	// WebhookURL is the new callback URL (optional).
	WebhookURL *string `json:"webhook_url,omitempty"`

	// Status is the new status string (optional).
	Status *string `json:"status,omitempty"`
}

// handleUpdateMerchant handles PUT /api/v1/merchants/{id}.
func (h *UserHandler) handleUpdateMerchant(w http.ResponseWriter, r *http.Request) {
	// Extract the merchant ID from the URL path.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid merchant id"})
		return
	}

	// Decode the partial update request body.
	var req updateMerchantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Build the dynamic fields map from non-nil request fields.
	fields := make(map[string]interface{})
	if req.Name != nil {
		fields["name"] = *req.Name
	}
	if req.Email != nil {
		fields["email"] = *req.Email
	}
	if req.WebhookURL != nil {
		fields["webhook_url"] = *req.WebhookURL
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}

	// Delegate to the merchant service.
	if err := h.merchantSvc.UpdateMerchant(r.Context(), id, fields); err != nil {
		logger.Error("update merchant failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// revokeAPIKeyRequest is the JSON body for POST /api/v1/merchants/{id}/revoke-key.
type revokeAPIKeyRequest struct {
	// TOTPCode is the time-based one-time password for admin verification.
	TOTPCode string `json:"totp_code"`
}

// handleRevokeAPIKey handles POST /api/v1/merchants/{id}/revoke-key.
// It requires a TOTP code for additional security verification.
func (h *UserHandler) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	// Extract the merchant ID from the URL path.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid merchant id"})
		return
	}

	// Decode the revocation request body.
	var req revokeAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Delegate to the merchant service to rotate the key.
	newAPIKey, err := h.merchantSvc.RevokeAPIKey(r.Context(), id, req.TOTPCode)
	if err != nil {
		logger.Error("revoke api key failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Return the new API key. This is the only time it will be visible.
	writeJSON(w, http.StatusOK, map[string]string{
		"new_api_key": newAPIKey,
		"message":     "API key has been rotated. Store the new key securely.",
	})
}

// ---------------------------------------------------------------------------
// Agent handlers
// ---------------------------------------------------------------------------

// createAgentRequest is the JSON body for POST /api/v1/agents.
type createAgentRequest struct {
	// Name is the agent's display name.
	Name string `json:"name"`

	// Email is the agent's login email.
	Email string `json:"email"`

	// Password is the plaintext password.
	Password string `json:"password"`

	// PartnerID is the optional UUID of the managing partner (as string).
	PartnerID string `json:"partner_id,omitempty"`

	// CommissionPct is the commission percentage (e.g. "0.30" for 30%).
	CommissionPct string `json:"commission_pct"`
}

// handleCreateAgent handles POST /api/v1/agents.
func (h *UserHandler) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	// Decode the incoming JSON request body.
	var req createAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Parse optional partner ID.
	var partnerID *uuid.UUID
	if req.PartnerID != "" {
		parsed, err := uuid.Parse(req.PartnerID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid partner_id"})
			return
		}
		partnerID = &parsed
	}

	// Parse commission percentage as decimal.
	commissionPct, err := decimal.NewFromString(req.CommissionPct)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid commission_pct"})
		return
	}

	// Build the service input and delegate.
	input := service.CreateAgentInput{
		Name:          req.Name,
		Email:         req.Email,
		Password:      req.Password,
		PartnerID:     partnerID,
		CommissionPct: commissionPct,
	}

	agent, err := h.agentSvc.CreateAgent(r.Context(), input)
	if err != nil {
		logger.Error("create agent failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, agent)
}

// handleGetAgent handles GET /api/v1/agents/{id}.
func (h *UserHandler) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	// Extract the agent ID from the URL path parameter.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent id"})
		return
	}

	// Fetch the agent from the service layer.
	agent, err := h.agentSvc.GetAgent(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// handleListAgents handles GET /api/v1/agents?partner_id=...&page=1&limit=20.
func (h *UserHandler) handleListAgents(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters.
	page := parseQueryInt(r, "page", 1)
	limit := parseQueryInt(r, "limit", 20)

	// Parse optional partner_id filter from query string.
	var partnerID *uuid.UUID
	if partnerIDStr := r.URL.Query().Get("partner_id"); partnerIDStr != "" {
		parsed, err := uuid.Parse(partnerIDStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid partner_id filter"})
			return
		}
		partnerID = &parsed
	}

	// Fetch the paginated list from the service layer.
	agents, total, err := h.agentSvc.ListAgents(r.Context(), partnerID, page, limit)
	if err != nil {
		logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":  agents,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// updateAgentRequest is the JSON body for PUT /api/v1/agents/{id}.
type updateAgentRequest struct {
	// Name is the new display name (optional).
	Name *string `json:"name,omitempty"`

	// Email is the new email (optional).
	Email *string `json:"email,omitempty"`

	// Status is the new status string (optional).
	Status *string `json:"status,omitempty"`
}

// handleUpdateAgent handles PUT /api/v1/agents/{id}.
func (h *UserHandler) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	// Extract the agent ID from the URL path.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent id"})
		return
	}

	// Decode the partial update request body.
	var req updateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Build the dynamic fields map from non-nil request fields.
	fields := make(map[string]interface{})
	if req.Name != nil {
		fields["name"] = *req.Name
	}
	if req.Email != nil {
		fields["email"] = *req.Email
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}

	// Delegate to the agent service.
	if err := h.agentSvc.UpdateAgent(r.Context(), id, fields); err != nil {
		logger.Error("update agent failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// ---------------------------------------------------------------------------
// Partner handlers
// ---------------------------------------------------------------------------

// createPartnerRequest is the JSON body for POST /api/v1/partners.
type createPartnerRequest struct {
	// Name is the partner's business name.
	Name string `json:"name"`

	// Email is the partner's login email.
	Email string `json:"email"`

	// Password is the plaintext password.
	Password string `json:"password"`

	// CommissionPct is the commission percentage (e.g. "0.10" for 10%).
	CommissionPct string `json:"commission_pct"`
}

// handleCreatePartner handles POST /api/v1/partners.
func (h *UserHandler) handleCreatePartner(w http.ResponseWriter, r *http.Request) {
	// Decode the incoming JSON request body.
	var req createPartnerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Parse commission percentage as decimal.
	commissionPct, err := decimal.NewFromString(req.CommissionPct)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid commission_pct"})
		return
	}

	// Build the service input and delegate.
	input := service.CreatePartnerInput{
		Name:          req.Name,
		Email:         req.Email,
		Password:      req.Password,
		CommissionPct: commissionPct,
	}

	partner, err := h.partnerSvc.CreatePartner(r.Context(), input)
	if err != nil {
		logger.Error("create partner failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, partner)
}

// handleGetPartner handles GET /api/v1/partners/{id}.
func (h *UserHandler) handleGetPartner(w http.ResponseWriter, r *http.Request) {
	// Extract the partner ID from the URL path parameter.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid partner id"})
		return
	}

	// Fetch the partner from the service layer.
	partner, err := h.partnerSvc.GetPartner(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "partner not found"})
		return
	}

	writeJSON(w, http.StatusOK, partner)
}

// handleListPartners handles GET /api/v1/partners?page=1&limit=20.
func (h *UserHandler) handleListPartners(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters.
	page := parseQueryInt(r, "page", 1)
	limit := parseQueryInt(r, "limit", 20)

	// Fetch the paginated list from the service layer.
	partners, total, err := h.partnerSvc.ListPartners(r.Context(), page, limit)
	if err != nil {
		logger.Error("list partners failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":  partners,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// updatePartnerRequest is the JSON body for PUT /api/v1/partners/{id}.
type updatePartnerRequest struct {
	// Name is the new business name (optional).
	Name *string `json:"name,omitempty"`

	// Email is the new email (optional).
	Email *string `json:"email,omitempty"`

	// Status is the new status string (optional).
	Status *string `json:"status,omitempty"`
}

// handleUpdatePartner handles PUT /api/v1/partners/{id}.
func (h *UserHandler) handleUpdatePartner(w http.ResponseWriter, r *http.Request) {
	// Extract the partner ID from the URL path.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid partner id"})
		return
	}

	// Decode the partial update request body.
	var req updatePartnerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Build the dynamic fields map from non-nil request fields.
	fields := make(map[string]interface{})
	if req.Name != nil {
		fields["name"] = *req.Name
	}
	if req.Email != nil {
		fields["email"] = *req.Email
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}

	// Delegate to the partner service.
	if err := h.partnerSvc.UpdatePartner(r.Context(), id, fields); err != nil {
		logger.Error("update partner failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// writeJSON serialises a value as JSON and writes it to the HTTP response
// with the given status code. It sets the Content-Type header to
// application/json. Encoding errors are silently ignored because the
// response has already been partially written at that point.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// parseUUIDParam extracts a named path parameter from the request and
// parses it as a UUID. It uses the Go 1.22+ ServeMux path parameter
// syntax (e.g. {id}). Falls back to manual path parsing for compatibility.
//
// Parameters:
//   - r: the HTTP request containing the path parameter.
//   - name: the parameter name (e.g. "id").
//
// Returns the parsed UUID and nil error on success.
// Returns uuid.Nil and an error if the parameter is missing or invalid.
func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	// Try the Go 1.22+ PathValue method first.
	val := r.PathValue(name)
	if val == "" {
		// Fallback: extract the last path segment manually.
		// This handles routes like /api/v1/admins/{id} where the id
		// is the last segment in the URL path.
		parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
		if len(parts) > 0 {
			val = parts[len(parts)-1]
		}
	}

	// Parse the extracted string as a UUID.
	return uuid.Parse(val)
}

// parseQueryInt reads a query parameter from the request URL and returns
// it as an integer. If the parameter is missing or cannot be parsed,
// the provided default value is returned instead.
//
// Parameters:
//   - r: the HTTP request containing the query parameters.
//   - key: the query parameter name (e.g. "page").
//   - defaultVal: the fallback value if the parameter is absent or invalid.
//
// Returns the parsed integer value or the default.
func parseQueryInt(r *http.Request, key string, defaultVal int) int {
	// Read the raw query parameter string.
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultVal
	}

	// Attempt to parse the string as an integer.
	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}

	return n
}
