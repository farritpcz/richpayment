// Package handler implements the HTTP transport layer for the commission-service.
//
// It translates incoming HTTP requests into service calls and formats the
// responses as JSON. All business logic lives in the service package; the
// handler only handles parsing, validation, and serialisation.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/commission/internal/service"
)

// ---------------------------------------------------------------------------
// Standard API response envelope
// ---------------------------------------------------------------------------

// APIResponse is the standard envelope used for all JSON responses from the
// commission-service. Every response has a boolean success flag and either
// a data payload or an error message.
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
// CommissionHandler
// ---------------------------------------------------------------------------

// CommissionHandler is the HTTP handler for all commission-related endpoints.
// It depends on the Calculator service (for calculating and recording
// commissions) and the Aggregator service (for summary queries).
type CommissionHandler struct {
	// calculator handles commission calculation and recording.
	calculator *service.Calculator

	// aggregator handles daily and monthly summary queries.
	aggregator *service.Aggregator

	// log is the structured logger for request-level logging.
	log *slog.Logger
}

// NewCommissionHandler creates a new CommissionHandler with the given
// dependencies. Both services and the logger must be non-nil.
func NewCommissionHandler(calc *service.Calculator, agg *service.Aggregator, log *slog.Logger) *CommissionHandler {
	return &CommissionHandler{
		calculator: calc,
		aggregator: agg,
		log:        log,
	}
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterRoutes creates a new HTTP mux and registers all commission-service
// endpoints. The mux is returned for use by the HTTP server in main.go.
//
// Routes:
//
//	POST   /internal/commission/calculate  — Calculate and record a commission
//	GET    /commission/summary/daily       — Get daily commission summaries
//	GET    /commission/summary/monthly     — Get monthly commission summary
//	GET    /healthz                        — Health check endpoint
func (h *CommissionHandler) RegisterRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Internal endpoint: called by other services (e.g. order-service) when
	// a transaction completes. It calculates the commission split and records
	// it in the database + credits wallets.
	mux.HandleFunc("POST /internal/commission/calculate", h.CalculateAndRecord)

	// External endpoints: used by the admin dashboard to view commission
	// summaries. These are read-only and cacheable.
	mux.HandleFunc("GET /commission/summary/daily", h.GetDailySummary)
	mux.HandleFunc("GET /commission/summary/monthly", h.GetMonthlySummary)

	// Health check: used by load balancers and Kubernetes liveness probes
	// to verify the service is running.
	mux.HandleFunc("GET /healthz", h.Healthz)

	return mux
}

// ---------------------------------------------------------------------------
// Helper functions for writing JSON responses
// ---------------------------------------------------------------------------

// writeJSON serialises the given value as JSON and writes it to the response
// writer with the specified HTTP status code. It sets the Content-Type header
// to application/json.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// respondOK sends a 200 OK response with the given data payload wrapped
// in the standard APIResponse envelope.
func respondOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, APIResponse{
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
// POST /internal/commission/calculate
// ---------------------------------------------------------------------------

// CalculateAndRecord handles the commission calculation endpoint. It expects
// a JSON body matching the CommissionInput struct. On success, it calculates
// the fee split, records the commission in the database, credits the
// relevant wallets, and returns the calculated amounts.
//
// Request body:
//
//	{
//	  "transaction_type": "deposit",
//	  "transaction_id": "uuid",
//	  "merchant_id": "uuid",
//	  "transaction_amount": "1000.00",
//	  "merchant_fee_pct": "0.025",
//	  "agent_id": "uuid",
//	  "agent_commission_pct": "0.005",
//	  "partner_id": "uuid",
//	  "partner_commission_pct": "0.003",
//	  "currency": "THB"
//	}
//
// Response:
//
//	{
//	  "success": true,
//	  "data": { "commission": { ... } }
//	}
func (h *CommissionHandler) CalculateAndRecord(w http.ResponseWriter, r *http.Request) {
	// Parse the JSON request body into the CommissionInput struct.
	var input service.CommissionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		h.log.Warn("invalid request body", "error", err)
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "failed to parse request body")
		return
	}

	// Validate required fields.
	if input.TransactionID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "transaction_id is required")
		return
	}
	if input.MerchantID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "merchant_id is required")
		return
	}
	if input.TransactionAmount.IsZero() || input.TransactionAmount.IsNegative() {
		respondError(w, http.StatusBadRequest, "INVALID_AMOUNT", "transaction_amount must be positive")
		return
	}

	// -----------------------------------------------------------------------
	// Step 1: Calculate the commission split.
	// -----------------------------------------------------------------------
	result, err := h.calculator.CalculateCommission(r.Context(), input)
	if err != nil {
		h.log.Error("commission calculation failed", "error", err)
		respondError(w, http.StatusUnprocessableEntity, "CALC_ERROR", err.Error())
		return
	}

	// -----------------------------------------------------------------------
	// Step 2: Record the commission and credit wallets.
	// -----------------------------------------------------------------------
	if err := h.calculator.RecordCommission(r.Context(), result); err != nil {
		h.log.Error("commission recording failed", "error", err)
		respondError(w, http.StatusInternalServerError, "RECORD_ERROR", "failed to record commission")
		return
	}

	// Return the full commission details to the caller.
	respondOK(w, result)
}

// ---------------------------------------------------------------------------
// GET /commission/summary/daily
// ---------------------------------------------------------------------------

// GetDailySummary handles requests for daily commission summaries.
//
// Query parameters:
//   - owner_type (required): "agent", "partner", "system", or "merchant"
//   - owner_id (required): UUID of the owner
//   - from (required): start date in YYYY-MM-DD format (inclusive)
//   - to (required): end date in YYYY-MM-DD format (inclusive)
//
// Example:
//
//	GET /commission/summary/daily?owner_type=agent&owner_id=xxx&from=2024-01-01&to=2024-01-31
//
// Response:
//
//	{
//	  "success": true,
//	  "data": [ { "summary_date": "2024-01-01", ... }, ... ]
//	}
func (h *CommissionHandler) GetDailySummary(w http.ResponseWriter, r *http.Request) {
	// Parse and validate the owner_type query parameter.
	ownerTypeStr := r.URL.Query().Get("owner_type")
	if ownerTypeStr == "" {
		respondError(w, http.StatusBadRequest, "MISSING_PARAM", "owner_type is required")
		return
	}
	ownerType := models.OwnerType(ownerTypeStr)

	// Parse and validate the owner_id query parameter.
	ownerIDStr := r.URL.Query().Get("owner_id")
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_PARAM", "owner_id must be a valid UUID")
		return
	}

	// Parse the date range parameters.
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_PARAM", "from must be in YYYY-MM-DD format")
		return
	}

	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_PARAM", "to must be in YYYY-MM-DD format")
		return
	}

	// Validate that from <= to to prevent nonsensical queries.
	if from.After(to) {
		respondError(w, http.StatusBadRequest, "INVALID_RANGE", "from must not be after to")
		return
	}

	// Delegate to the aggregator service.
	summaries, err := h.aggregator.GetDailySummary(r.Context(), ownerType, ownerID, from, to)
	if err != nil {
		h.log.Error("failed to get daily summary", "error", err)
		respondError(w, http.StatusInternalServerError, "QUERY_ERROR", "failed to retrieve daily summary")
		return
	}

	respondOK(w, summaries)
}

// ---------------------------------------------------------------------------
// GET /commission/summary/monthly
// ---------------------------------------------------------------------------

// GetMonthlySummary handles requests for a monthly aggregated commission
// summary.
//
// Query parameters:
//   - owner_type (required): "agent", "partner", "system", or "merchant"
//   - owner_id (required): UUID of the owner
//   - month (required): the target month in YYYY-MM format
//
// Example:
//
//	GET /commission/summary/monthly?owner_type=agent&owner_id=xxx&month=2024-01
//
// Response:
//
//	{
//	  "success": true,
//	  "data": { "summary_date": "2024-01", ... }
//	}
func (h *CommissionHandler) GetMonthlySummary(w http.ResponseWriter, r *http.Request) {
	// Parse and validate the owner_type query parameter.
	ownerTypeStr := r.URL.Query().Get("owner_type")
	if ownerTypeStr == "" {
		respondError(w, http.StatusBadRequest, "MISSING_PARAM", "owner_type is required")
		return
	}
	ownerType := models.OwnerType(ownerTypeStr)

	// Parse and validate the owner_id query parameter.
	ownerIDStr := r.URL.Query().Get("owner_id")
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_PARAM", "owner_id must be a valid UUID")
		return
	}

	// Parse the month parameter. Expected format: "2006-01".
	monthStr := r.URL.Query().Get("month")
	if monthStr == "" {
		respondError(w, http.StatusBadRequest, "MISSING_PARAM", "month is required (format: YYYY-MM)")
		return
	}

	// Delegate to the aggregator service.
	summary, err := h.aggregator.GetMonthlySummary(r.Context(), ownerType, ownerID, monthStr)
	if err != nil {
		h.log.Error("failed to get monthly summary", "error", err)
		respondError(w, http.StatusInternalServerError, "QUERY_ERROR", "failed to retrieve monthly summary")
		return
	}

	respondOK(w, summary)
}

// ---------------------------------------------------------------------------
// GET /healthz
// ---------------------------------------------------------------------------

// Healthz is a lightweight health check endpoint. It returns a simple JSON
// response indicating the service is running. This is used by Kubernetes
// liveness probes and load balancers.
//
// Response:
//
//	{ "success": true, "data": { "status": "ok", "service": "commission-service" } }
func (h *CommissionHandler) Healthz(w http.ResponseWriter, _ *http.Request) {
	respondOK(w, map[string]string{
		"status":  "ok",
		"service": "commission-service",
	})
}
