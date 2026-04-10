// Package handler implements the HTTP transport layer for the withdrawal-service.
// It defines JSON request/response types and maps HTTP routes to the
// corresponding service-layer methods for withdrawal operations.
package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/withdrawal/internal/service"
)

// WithdrawalHandler groups all HTTP handler methods for the withdrawal-service
// API. It holds a reference to the WithdrawalService and provides a Register
// method to bind routes to a mux.
type WithdrawalHandler struct {
	// svc handles withdrawal business logic including creation, approval,
	// rejection, and completion.
	svc *service.WithdrawalService
}

// NewWithdrawalHandler creates a new WithdrawalHandler with the given
// withdrawal service dependency.
//
// Parameters:
//   - svc: the withdrawal service for business logic operations.
//
// Returns a pointer to a fully initialised WithdrawalHandler.
func NewWithdrawalHandler(svc *service.WithdrawalService) *WithdrawalHandler {
	return &WithdrawalHandler{svc: svc}
}

// Register binds all withdrawal-service HTTP routes to the given ServeMux.
// Routes follow the pattern: METHOD /api/v1/withdrawals/{optional-id}/{action}.
//
// Withdrawal routes:
//
//	POST   /api/v1/withdrawals                  - Create a new withdrawal
//	GET    /api/v1/withdrawals/pending           - List pending withdrawals
//	GET    /api/v1/withdrawals/{id}              - Get a single withdrawal by ID
//	POST   /api/v1/withdrawals/{id}/approve      - Approve a pending withdrawal
//	POST   /api/v1/withdrawals/{id}/reject       - Reject a pending withdrawal
//	POST   /api/v1/withdrawals/{id}/complete     - Complete an approved withdrawal
func (h *WithdrawalHandler) Register(mux *http.ServeMux) {
	// Route: Create a new withdrawal request.
	mux.HandleFunc("POST /api/v1/withdrawals", h.handleCreate)

	// Route: List all pending withdrawals awaiting admin approval.
	mux.HandleFunc("GET /api/v1/withdrawals/pending", h.handleListPending)

	// Route: Retrieve a single withdrawal by its UUID.
	mux.HandleFunc("GET /api/v1/withdrawals/{id}", h.handleGet)

	// Route: Approve a pending withdrawal (admin action).
	mux.HandleFunc("POST /api/v1/withdrawals/{id}/approve", h.handleApprove)

	// Route: Reject a pending withdrawal with a reason (admin action).
	mux.HandleFunc("POST /api/v1/withdrawals/{id}/reject", h.handleReject)

	// Route: Complete an approved withdrawal after bank transfer.
	mux.HandleFunc("POST /api/v1/withdrawals/{id}/complete", h.handleComplete)
}

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// createWithdrawalRequest is the JSON body for POST /api/v1/withdrawals.
// Contains all fields required to initiate a new withdrawal.
type createWithdrawalRequest struct {
	// MerchantID is the UUID of the merchant requesting the withdrawal.
	MerchantID string `json:"merchant_id"`

	// Amount is the gross withdrawal amount as a decimal string (e.g. "50000.00").
	Amount string `json:"amount"`

	// Currency is the ISO 4217 currency code (e.g. "THB").
	Currency string `json:"currency"`

	// DestType is the destination type: "bank" or "promptpay".
	DestType string `json:"dest_type"`

	// DestDetails is a JSON-encoded string with destination-specific info
	// (e.g. bank name, account number, account holder name).
	DestDetails string `json:"dest_details"`
}

// approveRequest is the JSON body for POST /api/v1/withdrawals/{id}/approve.
type approveRequest struct {
	// AdminID is the UUID of the admin approving the withdrawal.
	AdminID string `json:"admin_id"`
}

// rejectRequest is the JSON body for POST /api/v1/withdrawals/{id}/reject.
type rejectRequest struct {
	// AdminID is the UUID of the admin rejecting the withdrawal.
	AdminID string `json:"admin_id"`

	// Reason is the human-readable explanation for the rejection.
	Reason string `json:"reason"`
}

// completeRequest is the JSON body for POST /api/v1/withdrawals/{id}/complete.
type completeRequest struct {
	// TransferRef is the external reference number from the bank transfer.
	TransferRef string `json:"transfer_ref"`

	// ProofURL is the URL to the transfer proof document.
	ProofURL string `json:"proof_url"`
}

// ---------------------------------------------------------------------------
// Handler methods
// ---------------------------------------------------------------------------

// handleCreate handles POST /api/v1/withdrawals.
// It decodes the JSON request body, validates inputs, delegates to the
// withdrawal service, and returns the created withdrawal as JSON.
func (h *WithdrawalHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	// Decode the incoming JSON request body.
	var req createWithdrawalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Parse the merchant UUID from the request.
	merchantID, err := uuid.Parse(req.MerchantID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid merchant_id"})
		return
	}

	// Parse the withdrawal amount as a decimal for precise arithmetic.
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
		return
	}

	// Validate that the amount is positive.
	if !amount.IsPositive() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be positive"})
		return
	}

	// Validate the destination type.
	destType := models.WithdrawalDestType(req.DestType)
	if destType != models.WithdrawalDestBank && destType != models.WithdrawalDestPromptPay {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid dest_type, must be 'bank' or 'promptpay'"})
		return
	}

	// Delegate to the withdrawal service to execute the full creation flow.
	withdrawal, err := h.svc.CreateWithdrawal(
		r.Context(), merchantID, amount, req.Currency, destType, req.DestDetails,
	)
	if err != nil {
		logger.Error("create withdrawal failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Return the created withdrawal as JSON with 201 Created status.
	writeJSON(w, http.StatusCreated, withdrawal)
}

// handleGet handles GET /api/v1/withdrawals/{id}.
// It extracts the withdrawal UUID from the URL path and returns it as JSON.
func (h *WithdrawalHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	// Extract the withdrawal ID from the URL path parameter.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid withdrawal id"})
		return
	}

	// Fetch the withdrawal from the service layer.
	withdrawal, err := h.svc.GetWithdrawal(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "withdrawal not found"})
		return
	}

	writeJSON(w, http.StatusOK, withdrawal)
}

// handleListPending handles GET /api/v1/withdrawals/pending?page=1&limit=20.
// It returns a paginated list of all withdrawals awaiting admin approval.
func (h *WithdrawalHandler) handleListPending(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters from the query string with defaults.
	page := parseQueryInt(r, "page", 1)
	limit := parseQueryInt(r, "limit", 20)

	// Fetch the paginated list of pending withdrawals.
	withdrawals, total, err := h.svc.ListPendingWithdrawals(r.Context(), page, limit)
	if err != nil {
		logger.Error("list pending withdrawals failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Return the list with pagination metadata.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":  withdrawals,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// handleApprove handles POST /api/v1/withdrawals/{id}/approve.
// It transitions a pending withdrawal to approved status.
func (h *WithdrawalHandler) handleApprove(w http.ResponseWriter, r *http.Request) {
	// Extract the withdrawal ID from the URL path parameter.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid withdrawal id"})
		return
	}

	// Decode the approval request body.
	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Parse the admin UUID.
	adminID, err := uuid.Parse(req.AdminID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid admin_id"})
		return
	}

	// Delegate to the withdrawal service to approve.
	if err := h.svc.ApproveWithdrawal(r.Context(), id, adminID); err != nil {
		logger.Error("approve withdrawal failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// handleReject handles POST /api/v1/withdrawals/{id}/reject.
// It transitions a pending withdrawal to rejected status and releases
// the held wallet balance.
func (h *WithdrawalHandler) handleReject(w http.ResponseWriter, r *http.Request) {
	// Extract the withdrawal ID from the URL path parameter.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid withdrawal id"})
		return
	}

	// Decode the rejection request body.
	var req rejectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Parse the admin UUID.
	adminID, err := uuid.Parse(req.AdminID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid admin_id"})
		return
	}

	// Delegate to the withdrawal service to reject.
	if err := h.svc.RejectWithdrawal(r.Context(), id, adminID, req.Reason); err != nil {
		logger.Error("reject withdrawal failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// handleComplete handles POST /api/v1/withdrawals/{id}/complete.
// It finalises an approved withdrawal after the bank transfer is confirmed.
func (h *WithdrawalHandler) handleComplete(w http.ResponseWriter, r *http.Request) {
	// Extract the withdrawal ID from the URL path parameter.
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid withdrawal id"})
		return
	}

	// Decode the completion request body.
	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Delegate to the withdrawal service to complete.
	if err := h.svc.CompleteWithdrawal(r.Context(), id, req.TransferRef, req.ProofURL); err != nil {
		logger.Error("complete withdrawal failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "completed"})
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// writeJSON serialises a value as JSON and writes it to the HTTP response
// with the given status code. Sets Content-Type to application/json.
// Encoding errors are silently ignored because the response has already
// been partially written.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// parseUUIDParam extracts a named path parameter from the request and
// parses it as a UUID. Uses Go 1.22+ PathValue, falling back to manual
// path segment extraction for compatibility.
//
// Parameters:
//   - r: the HTTP request containing the path parameter.
//   - name: the parameter name (e.g. "id").
//
// Returns the parsed UUID and nil error on success.
func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	// Try the Go 1.22+ PathValue method first.
	val := r.PathValue(name)
	if val == "" {
		// Fallback: extract from URL path by finding the segment after
		// the known prefix. This handles nested routes like
		// /api/v1/withdrawals/{id}/approve.
		parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
		// For routes like /api/v1/withdrawals/{id}/action, the id is
		// at index len-2; for /api/v1/withdrawals/{id}, it's at len-1.
		for i, p := range parts {
			if p == "withdrawals" && i+1 < len(parts) {
				val = parts[i+1]
				break
			}
		}
	}

	// Parse the extracted string as a UUID.
	return uuid.Parse(val)
}

// parseQueryInt reads a query parameter from the request URL and returns
// it as an integer. Returns the default value if the parameter is missing
// or cannot be parsed.
//
// Parameters:
//   - r: the HTTP request containing the query parameters.
//   - key: the query parameter name (e.g. "page").
//   - defaultVal: the fallback value.
//
// Returns the parsed integer or the default.
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
