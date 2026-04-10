// Package handler implements the HTTP request handlers for the gateway-api
// service. The gateway acts as the public entry point for merchant API calls
// and proxies requests to the appropriate internal microservices.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/httpclient"
)

// CreateDepositRequest is the JSON payload for creating a new deposit order.
type CreateDepositRequest struct {
	// MerchantOrderID is the merchant's own reference ID for idempotency.
	MerchantOrderID string `json:"merchant_order_id"`
	// CustomerName is the name of the end customer initiating the deposit.
	CustomerName string `json:"customer_name"`
	// CustomerBankCode identifies the customer's bank (e.g. "KBANK", "SCB").
	CustomerBankCode string `json:"customer_bank_code"`
	// Amount is the deposit amount; must be positive.
	Amount decimal.Decimal `json:"amount"`
	// Currency is the ISO 4217 currency code (e.g. "THB").
	Currency string `json:"currency"`
	// CallbackURL is the merchant's webhook endpoint for order status updates.
	CallbackURL string `json:"callback_url"`
}

// DepositResponse is the JSON representation of a deposit order returned by
// the API. This struct is used to decode the response from the order-service
// when proxying deposit requests through the gateway.
type DepositResponse struct {
	// ID is the platform-generated unique order identifier.
	ID string `json:"id"`
	// MerchantOrderID is the merchant's own reference echoed back.
	MerchantOrderID string `json:"merchant_order_id"`
	// Amount is the deposit amount.
	Amount decimal.Decimal `json:"amount"`
	// Currency is the ISO 4217 currency code.
	Currency string `json:"currency"`
	// Status is the current order status (e.g. "pending", "completed").
	Status string `json:"status"`
	// QRPayload is the PromptPay QR payload string, if applicable.
	QRPayload string `json:"qr_payload,omitempty"`
	// ExpiresAt is the deadline after which the order will expire.
	ExpiresAt string `json:"expires_at"`
	// CreatedAt is when the order was created.
	CreatedAt string `json:"created_at"`
}

// DepositHandler handles deposit-related API endpoints in the gateway.
//
// INTER-SERVICE COMMUNICATION FLOW:
// The gateway does NOT process deposits itself. Instead, it acts as a reverse
// proxy, forwarding deposit requests to the order-service which owns the full
// deposit lifecycle (creation, matching, completion). The flow is:
//
//   Client (merchant) -> gateway-api (:8080) -> order-service (:8083)
//
// The gateway validates basic request structure, then forwards the entire
// request body to the order-service via HTTP POST/GET. The order-service
// response is returned directly to the merchant.
type DepositHandler struct {
	// orderClient is the HTTP client configured to call the order-service.
	// The order-service runs on port 8083 and handles the complete deposit
	// order lifecycle: creation with bank account selection, QR generation,
	// SMS matching, and completion with settlement.
	orderClient *httpclient.ServiceClient
}

// NewDepositHandler creates a new DepositHandler wired to the order-service.
//
// Parameters:
//   - orderClient: an httpclient.ServiceClient pointing at the order-service
//     base URL (e.g. http://localhost:8083). This client is used for all
//     deposit-related inter-service calls.
func NewDepositHandler(orderClient *httpclient.ServiceClient) *DepositHandler {
	return &DepositHandler{
		orderClient: orderClient,
	}
}

// Create handles POST /api/v1/deposits.
//
// INTER-SERVICE COMMUNICATION FLOW:
//
//  1. Merchant sends POST /api/v1/deposits to the gateway (:8080).
//  2. Gateway validates the request body (required fields, positive amount).
//  3. Gateway forwards the request via HTTP POST to the order-service at
//     POST http://order-service:8083/api/v1/deposits.
//  4. The order-service executes the full deposit creation flow:
//     - Selects a bank account from the Redis pool.
//     - Adjusts the amount for uniqueness (if using unique_amount strategy).
//     - Generates a PromptPay QR code.
//     - Persists the order in PostgreSQL.
//     - Registers the order in Redis for matching and expiry.
//  5. The order-service response is returned to the merchant through the gateway.
//
// This proxy pattern keeps the gateway thin (no database, no business logic)
// while the order-service handles the complex deposit orchestration.
func (h *DepositHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateDepositRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.MerchantOrderID == "" {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "merchant_order_id is required")
		return
	}
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		respondError(w, http.StatusBadRequest, "INVALID_AMOUNT", "amount must be positive")
		return
	}

	// ---------------------------------------------------------------
	// Forward the deposit creation request to the order-service.
	//
	// The order-service expects a slightly different request shape (it needs
	// merchant_id, customer_name, customer_bank, and amount as a string).
	// We map the gateway request fields to the order-service format.
	//
	// Network path: gateway (:8080) --> order-service (:8083)
	// Endpoint:     POST /api/v1/deposits
	// ---------------------------------------------------------------
	orderReq := map[string]string{
		"merchant_id":       "00000000-0000-0000-0000-000000000001", // TODO: extract from auth middleware
		"merchant_order_id": req.MerchantOrderID,
		"amount":            req.Amount.String(),
		"customer_name":     req.CustomerName,
		"customer_bank":     req.CustomerBankCode,
	}

	// result will hold the raw JSON response from the order-service.
	// We use json.RawMessage so we can forward it as-is to the merchant
	// without needing to know the exact response schema.
	var result json.RawMessage
	if err := h.orderClient.Post(r.Context(), "/api/v1/deposits", orderReq, &result); err != nil {
		// The order-service is unreachable or returned an error.
		// Log the error and return a 502 Bad Gateway to the merchant,
		// indicating that the upstream service failed.
		respondError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "failed to create deposit via order-service: "+err.Error())
		return
	}

	// Forward the order-service response directly to the merchant.
	// The response includes the order ID, QR payload, and expiry time.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	w.Write(result)
}

// Get handles GET /api/v1/deposits/{id}.
//
// INTER-SERVICE COMMUNICATION FLOW:
//
//  1. Merchant sends GET /api/v1/deposits/{id} to the gateway (:8080).
//  2. Gateway validates the ID format (must be a valid UUID).
//  3. Gateway forwards the request via HTTP GET to the order-service at
//     GET http://order-service:8083/api/v1/deposits/{id}.
//  4. The order-service fetches the order from PostgreSQL and returns it.
//  5. The response is forwarded back to the merchant through the gateway.
//
// This allows merchants to check deposit status (pending, completed, expired)
// without the gateway needing direct database access.
func (h *DepositHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if _, err := uuid.Parse(idStr); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_ID", "invalid deposit id format")
		return
	}

	// ---------------------------------------------------------------
	// Forward the deposit lookup request to the order-service.
	//
	// Network path: gateway (:8080) --> order-service (:8083)
	// Endpoint:     GET /api/v1/deposits/{id}
	// ---------------------------------------------------------------
	var result json.RawMessage
	if err := h.orderClient.Get(r.Context(), "/api/v1/deposits/"+idStr, &result); err != nil {
		// The order-service could not find the deposit or is unreachable.
		respondError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "failed to get deposit from order-service: "+err.Error())
		return
	}

	// Forward the order-service response directly to the merchant.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(result)
}
