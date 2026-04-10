// Package handler implements the HTTP transport layer for the bank-service.
//
// It translates incoming HTTP requests into service calls and formats the
// responses as JSON. All business logic lives in the service package; the
// handler only handles parsing, validation, and serialisation.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/services/bank/internal/service"
)

// ---------------------------------------------------------------------------
// Standard API response envelope
// ---------------------------------------------------------------------------

// APIResponse is the standard envelope used for all JSON responses from the
// bank-service. Every response has a boolean success flag and either a data
// payload or an error message.
type APIResponse struct {
	// Success is true when the request was processed without errors.
	Success bool `json:"success"`

	// Data holds the response payload on success. Omitted on error.
	Data any `json:"data,omitempty"`

	// Error holds the human-readable error message on failure.
	Error string `json:"error,omitempty"`

	// Code is a machine-readable error code (e.g. "INVALID_INPUT").
	Code string `json:"code,omitempty"`
}

// ---------------------------------------------------------------------------
// BankHandler
// ---------------------------------------------------------------------------

// BankHandler is the HTTP handler for all bank-service endpoints. It
// depends on the Pool, Monitor, and TransferService for business logic.
type BankHandler struct {
	// pool handles bank account selection and pool management.
	pool *service.Pool

	// monitor provides account status and daily counter management.
	monitor *service.Monitor

	// transfer handles fund transfer operations.
	transfer *service.TransferService

	// log is the structured logger for request-level logging.
	log *slog.Logger
}

// NewBankHandler creates a new BankHandler with the given service
// dependencies. All parameters must be non-nil.
func NewBankHandler(pool *service.Pool, monitor *service.Monitor, transfer *service.TransferService, log *slog.Logger) *BankHandler {
	return &BankHandler{
		pool:     pool,
		monitor:  monitor,
		transfer: transfer,
		log:      log,
	}
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterRoutes creates a new HTTP mux and registers all bank-service
// endpoints. The mux is returned for use by the HTTP server in main.go.
//
// Routes:
//
//	POST   /internal/bank/select-account   — Select best account for a deposit
//	POST   /internal/bank/update-pool      — Rebuild account pool cache
//	POST   /internal/bank/auto-switch      — Disable account and reassign
//	GET    /bank/accounts                  — List all accounts with status
//	GET    /bank/accounts/{id}/status      — Get single account status
//	POST   /internal/bank/daily-received   — Increment daily received counter
//	POST   /internal/bank/reset-counters   — Reset all daily counters
//	POST   /bank/transfers                 — Create a new transfer
//	POST   /bank/transfers/{id}/complete   — Complete a transfer
//	GET    /bank/transfers                 — List transfers (paginated)
//	GET    /bank/transfers/daily-summary   — Get daily transfer summary
//	GET    /healthz                        — Health check endpoint
func (h *BankHandler) RegisterRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// --- Pool management (internal, service-to-service) ---
	mux.HandleFunc("POST /internal/bank/select-account", h.SelectAccount)
	mux.HandleFunc("POST /internal/bank/update-pool", h.UpdateAccountPool)
	mux.HandleFunc("POST /internal/bank/auto-switch", h.AutoSwitch)

	// --- Monitoring (admin dashboard) ---
	mux.HandleFunc("GET /bank/accounts", h.GetAllAccounts)
	mux.HandleFunc("GET /bank/accounts/{id}/status", h.GetAccountStatus)

	// --- Daily counter management (internal + scheduler) ---
	mux.HandleFunc("POST /internal/bank/daily-received", h.UpdateDailyReceived)
	mux.HandleFunc("POST /internal/bank/reset-counters", h.ResetDailyCounters)

	// --- Transfer management (admin) ---
	mux.HandleFunc("POST /bank/transfers", h.CreateTransfer)
	mux.HandleFunc("POST /bank/transfers/{id}/complete", h.CompleteTransfer)
	mux.HandleFunc("GET /bank/transfers", h.GetTransfers)
	mux.HandleFunc("GET /bank/transfers/daily-summary", h.GetDailyTransferSummary)

	// --- Health check ---
	mux.HandleFunc("GET /healthz", h.Healthz)

	return mux
}

// ---------------------------------------------------------------------------
// Helper functions for writing JSON responses
// ---------------------------------------------------------------------------

// writeJSON serialises the given value as JSON and writes it to the response
// writer with the specified HTTP status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// respondOK sends a 200 OK response with the given data payload.
func respondOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
	})
}

// respondCreated sends a 201 Created response with the given data payload.
func respondCreated(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusCreated, APIResponse{
		Success: true,
		Data:    data,
	})
}

// respondError sends an error response with the given HTTP status code,
// machine-readable error code, and human-readable message.
func respondError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, APIResponse{
		Success: false,
		Error:   message,
		Code:    code,
	})
}

// ---------------------------------------------------------------------------
// POST /internal/bank/select-account
// ---------------------------------------------------------------------------

// selectAccountRequest is the JSON body for the select-account endpoint.
type selectAccountRequest struct {
	// MerchantID is the UUID of the merchant requesting a bank account
	// for an incoming deposit.
	MerchantID uuid.UUID `json:"merchant_id"`
}

// SelectAccount handles the account selection endpoint. It picks the best
// available bank account for a deposit from the specified merchant.
//
// Request body:
//
//	{ "merchant_id": "uuid" }
//
// Response:
//
//	{ "success": true, "data": { bank account object } }
func (h *BankHandler) SelectAccount(w http.ResponseWriter, r *http.Request) {
	var req selectAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "failed to parse request body")
		return
	}

	// Validate the merchant ID.
	if req.MerchantID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "merchant_id is required")
		return
	}

	// Delegate to the Pool service.
	account, err := h.pool.SelectAccount(r.Context(), req.MerchantID)
	if err != nil {
		h.log.Error("account selection failed",
			slog.String("merchant_id", req.MerchantID.String()),
			"error", err,
		)
		respondError(w, http.StatusServiceUnavailable, "NO_ACCOUNT", err.Error())
		return
	}

	respondOK(w, account)
}

// ---------------------------------------------------------------------------
// POST /internal/bank/update-pool
// ---------------------------------------------------------------------------

// updatePoolRequest is the JSON body for the update-pool endpoint.
type updatePoolRequest struct {
	// MerchantID is the UUID of the merchant whose account pool should
	// be rebuilt in Redis.
	MerchantID uuid.UUID `json:"merchant_id"`
}

// UpdateAccountPool handles the pool rebuild endpoint. It refreshes the
// Redis sorted set for the specified merchant's account pool.
//
// Request body:
//
//	{ "merchant_id": "uuid" }
func (h *BankHandler) UpdateAccountPool(w http.ResponseWriter, r *http.Request) {
	var req updatePoolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "failed to parse request body")
		return
	}

	if req.MerchantID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "merchant_id is required")
		return
	}

	if err := h.pool.UpdateAccountPool(r.Context(), req.MerchantID); err != nil {
		h.log.Error("pool update failed", "error", err)
		respondError(w, http.StatusInternalServerError, "POOL_ERROR", "failed to update account pool")
		return
	}

	respondOK(w, map[string]string{"status": "pool_updated"})
}

// ---------------------------------------------------------------------------
// POST /internal/bank/auto-switch
// ---------------------------------------------------------------------------

// autoSwitchRequest is the JSON body for the auto-switch endpoint.
type autoSwitchRequest struct {
	// BankAccountID is the UUID of the account to disable.
	BankAccountID uuid.UUID `json:"bank_account_id"`

	// Reason is a human-readable explanation for why the switch is needed.
	Reason string `json:"reason"`
}

// AutoSwitch handles the auto-switch endpoint. It disables a bank account
// and reassigns all affected merchants to other available accounts.
//
// Request body:
//
//	{ "bank_account_id": "uuid", "reason": "daily_limit_reached" }
func (h *BankHandler) AutoSwitch(w http.ResponseWriter, r *http.Request) {
	var req autoSwitchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "failed to parse request body")
		return
	}

	if req.BankAccountID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "bank_account_id is required")
		return
	}

	if err := h.pool.AutoSwitch(r.Context(), req.BankAccountID, req.Reason); err != nil {
		h.log.Error("auto-switch failed", "error", err)
		respondError(w, http.StatusInternalServerError, "SWITCH_ERROR", "failed to auto-switch account")
		return
	}

	respondOK(w, map[string]string{"status": "switched"})
}

// ---------------------------------------------------------------------------
// GET /bank/accounts
// ---------------------------------------------------------------------------

// GetAllAccounts handles the account listing endpoint. It returns all bank
// accounts with their current status, including computed fields like
// remaining capacity and utilisation percentage.
func (h *BankHandler) GetAllAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.monitor.GetAllAccounts(r.Context())
	if err != nil {
		h.log.Error("failed to get all accounts", "error", err)
		respondError(w, http.StatusInternalServerError, "QUERY_ERROR", "failed to retrieve accounts")
		return
	}

	respondOK(w, accounts)
}

// ---------------------------------------------------------------------------
// GET /bank/accounts/{id}/status
// ---------------------------------------------------------------------------

// GetAccountStatus handles the single account status endpoint.
// The account ID is extracted from the URL path parameter.
func (h *BankHandler) GetAccountStatus(w http.ResponseWriter, r *http.Request) {
	// Extract the account ID from the URL path.
	idStr := r.PathValue("id")
	accountID, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_PARAM", "id must be a valid UUID")
		return
	}

	// Delegate to the Monitor service.
	status, err := h.monitor.GetAccountStatus(r.Context(), accountID)
	if err != nil {
		h.log.Error("failed to get account status", "error", err)
		respondError(w, http.StatusNotFound, "NOT_FOUND", "account not found")
		return
	}

	respondOK(w, status)
}

// ---------------------------------------------------------------------------
// POST /internal/bank/daily-received
// ---------------------------------------------------------------------------

// dailyReceivedRequest is the JSON body for updating the daily received counter.
type dailyReceivedRequest struct {
	// BankAccountID is the UUID of the account that received a deposit.
	BankAccountID uuid.UUID `json:"bank_account_id"`

	// Amount is the deposit amount in THB.
	Amount decimal.Decimal `json:"amount"`
}

// UpdateDailyReceived handles the daily received counter increment endpoint.
// This is called by the order-service each time a deposit is matched.
//
// Request body:
//
//	{ "bank_account_id": "uuid", "amount": "5000.00" }
func (h *BankHandler) UpdateDailyReceived(w http.ResponseWriter, r *http.Request) {
	var req dailyReceivedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "failed to parse request body")
		return
	}

	if req.BankAccountID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "bank_account_id is required")
		return
	}

	if err := h.monitor.UpdateDailyReceived(r.Context(), req.BankAccountID, req.Amount); err != nil {
		h.log.Error("failed to update daily received", "error", err)
		respondError(w, http.StatusInternalServerError, "UPDATE_ERROR", "failed to update daily received")
		return
	}

	respondOK(w, map[string]string{"status": "updated"})
}

// ---------------------------------------------------------------------------
// POST /internal/bank/reset-counters
// ---------------------------------------------------------------------------

// ResetDailyCounters handles the midnight counter reset endpoint.
// Called by the scheduler at midnight to reset all daily receiving counters.
func (h *BankHandler) ResetDailyCounters(w http.ResponseWriter, r *http.Request) {
	if err := h.monitor.ResetDailyCounters(r.Context()); err != nil {
		h.log.Error("failed to reset daily counters", "error", err)
		respondError(w, http.StatusInternalServerError, "RESET_ERROR", "failed to reset daily counters")
		return
	}

	respondOK(w, map[string]string{"status": "counters_reset"})
}

// ---------------------------------------------------------------------------
// POST /bank/transfers
// ---------------------------------------------------------------------------

// createTransferRequest is the JSON body for creating a new transfer.
type createTransferRequest struct {
	// FromAccountID is the bank account to transfer funds from.
	FromAccountID uuid.UUID `json:"from_account_id"`

	// ToHoldingID is the pre-approved holding account to transfer to.
	ToHoldingID uuid.UUID `json:"to_holding_id"`

	// Amount is the transfer amount in THB.
	Amount decimal.Decimal `json:"amount"`

	// AdminID is the UUID of the admin initiating the transfer.
	AdminID uuid.UUID `json:"admin_id"`
}

// CreateTransfer handles the transfer creation endpoint. It validates the
// destination against the holding accounts table and creates a pending
// transfer record.
//
// Request body:
//
//	{
//	  "from_account_id": "uuid",
//	  "to_holding_id": "uuid",
//	  "amount": "50000.00",
//	  "admin_id": "uuid"
//	}
func (h *BankHandler) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	var req createTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "failed to parse request body")
		return
	}

	// Validate required fields.
	if req.FromAccountID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "from_account_id is required")
		return
	}
	if req.ToHoldingID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "to_holding_id is required")
		return
	}
	if req.AdminID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "admin_id is required")
		return
	}

	// Delegate to the TransferService.
	transfer, err := h.transfer.CreateTransfer(r.Context(), req.FromAccountID, req.ToHoldingID, req.Amount, req.AdminID)
	if err != nil {
		h.log.Error("transfer creation failed", "error", err)
		respondError(w, http.StatusUnprocessableEntity, "TRANSFER_ERROR", err.Error())
		return
	}

	respondCreated(w, transfer)
}

// ---------------------------------------------------------------------------
// POST /bank/transfers/{id}/complete
// ---------------------------------------------------------------------------

// completeTransferRequest is the JSON body for completing a transfer.
type completeTransferRequest struct {
	// Reference is the bank-provided reference/confirmation number.
	Reference string `json:"reference"`
}

// CompleteTransfer handles the transfer completion endpoint. It marks a
// pending transfer as completed with the bank reference number.
//
// URL: POST /bank/transfers/{id}/complete
// Body: { "reference": "BANK-REF-12345" }
func (h *BankHandler) CompleteTransfer(w http.ResponseWriter, r *http.Request) {
	// Extract the transfer ID from the URL path.
	idStr := r.PathValue("id")
	transferID, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_PARAM", "id must be a valid UUID")
		return
	}

	var req completeTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "failed to parse request body")
		return
	}

	// Delegate to the TransferService.
	if err := h.transfer.CompleteTransfer(r.Context(), transferID, req.Reference); err != nil {
		h.log.Error("transfer completion failed", "error", err)
		respondError(w, http.StatusUnprocessableEntity, "COMPLETE_ERROR", err.Error())
		return
	}

	respondOK(w, map[string]string{"status": "completed"})
}

// ---------------------------------------------------------------------------
// GET /bank/transfers
// ---------------------------------------------------------------------------

// GetTransfers handles the transfer listing endpoint with pagination.
//
// Query parameters:
//   - page (optional, default 1): the page number
//   - limit (optional, default 20): the number of transfers per page
//
// Response includes pagination metadata alongside the transfer list.
func (h *BankHandler) GetTransfers(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters from query string.
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	// Default values for pagination.
	page := 1
	limit := 20

	// Parse page number if provided.
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Parse page size if provided.
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Delegate to the TransferService.
	transfers, total, err := h.transfer.GetTransfers(r.Context(), page, limit)
	if err != nil {
		h.log.Error("failed to get transfers", "error", err)
		respondError(w, http.StatusInternalServerError, "QUERY_ERROR", "failed to retrieve transfers")
		return
	}

	// Return the transfers with pagination metadata.
	respondOK(w, map[string]any{
		"transfers": transfers,
		"total":     total,
		"page":      page,
		"limit":     limit,
	})
}

// ---------------------------------------------------------------------------
// GET /bank/transfers/daily-summary
// ---------------------------------------------------------------------------

// GetDailyTransferSummary handles the daily transfer summary endpoint.
//
// Query parameters:
//   - date (required): the target date in YYYY-MM-DD format
//
// Response: aggregated transfer statistics for the specified date.
func (h *BankHandler) GetDailyTransferSummary(w http.ResponseWriter, r *http.Request) {
	// Parse the date parameter.
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		respondError(w, http.StatusBadRequest, "MISSING_PARAM", "date is required (format: YYYY-MM-DD)")
		return
	}

	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_PARAM", "date must be in YYYY-MM-DD format")
		return
	}

	// Delegate to the TransferService.
	summary, err := h.transfer.GetDailyTransferSummary(r.Context(), date)
	if err != nil {
		h.log.Error("failed to get daily transfer summary", "error", err)
		respondError(w, http.StatusInternalServerError, "QUERY_ERROR", "failed to retrieve daily transfer summary")
		return
	}

	respondOK(w, summary)
}

// ---------------------------------------------------------------------------
// GET /healthz
// ---------------------------------------------------------------------------

// Healthz is a lightweight health check endpoint used by Kubernetes liveness
// probes and load balancers to verify the bank-service is running.
//
// Response: { "success": true, "data": { "status": "ok", "service": "bank-service" } }
func (h *BankHandler) Healthz(w http.ResponseWriter, _ *http.Request) {
	respondOK(w, map[string]string{
		"status":  "ok",
		"service": "bank-service",
	})
}
