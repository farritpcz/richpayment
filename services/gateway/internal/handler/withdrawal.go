package handler

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/httpclient"
)

// CreateWithdrawalRequest is the JSON payload for creating a new withdrawal order.
type CreateWithdrawalRequest struct {
	// MerchantOrderID is the merchant's own reference ID for idempotency.
	MerchantOrderID string `json:"merchant_order_id"`
	// Amount is the withdrawal amount; must be positive.
	Amount decimal.Decimal `json:"amount"`
	// Currency is the ISO 4217 currency code (e.g. "THB").
	Currency string `json:"currency"`
	// BeneficiaryName is the name of the person receiving the funds.
	BeneficiaryName string `json:"beneficiary_name"`
	// BeneficiaryAccount is the destination bank account number.
	BeneficiaryAccount string `json:"beneficiary_account"`
	// BeneficiaryBank is the destination bank code or name.
	BeneficiaryBank string `json:"beneficiary_bank"`
	// CallbackURL is the merchant's webhook endpoint for status updates.
	CallbackURL string `json:"callback_url"`
}

// WithdrawalResponse is the JSON representation of a withdrawal order returned
// by the API.
type WithdrawalResponse struct {
	// ID is the platform-generated unique withdrawal identifier.
	ID string `json:"id"`
	// MerchantOrderID is the merchant's own reference echoed back.
	MerchantOrderID string `json:"merchant_order_id"`
	// Amount is the withdrawal amount.
	Amount decimal.Decimal `json:"amount"`
	// Currency is the ISO 4217 currency code.
	Currency string `json:"currency"`
	// Status is the current withdrawal status (e.g. "pending", "approved").
	Status string `json:"status"`
	// CreatedAt is when the withdrawal was created.
	CreatedAt string `json:"created_at"`
}

// WithdrawalHandler handles withdrawal-related API endpoints in the gateway.
//
// INTER-SERVICE COMMUNICATION FLOW:
// The gateway proxies all withdrawal requests to the withdrawal-service, which
// owns the full withdrawal lifecycle (creation with balance hold, admin
// approval/rejection, completion with bank transfer). The flow is:
//
//   Client (merchant) -> gateway-api (:8080) -> withdrawal-service (:8085)
//
// The withdrawal-service in turn communicates with:
//   - wallet-service (:8084) for balance checks, holds, releases, and debits
//   - commission-service (:8086) for recording fee splits on completion
//
// This creates a chain of inter-service calls:
//   merchant -> gateway -> withdrawal-service -> wallet-service
//                                             -> commission-service
type WithdrawalHandler struct {
	// withdrawalClient is the HTTP client configured to call the withdrawal-service.
	// The withdrawal-service runs on port 8085 and manages the complete withdrawal
	// lifecycle including balance holds, approval workflows, and bank transfers.
	withdrawalClient *httpclient.ServiceClient
}

// NewWithdrawalHandler creates a new WithdrawalHandler wired to the withdrawal-service.
//
// Parameters:
//   - withdrawalClient: an httpclient.ServiceClient pointing at the withdrawal-service
//     base URL (e.g. http://localhost:8085). Used for all withdrawal-related
//     inter-service calls from the gateway.
func NewWithdrawalHandler(withdrawalClient *httpclient.ServiceClient) *WithdrawalHandler {
	return &WithdrawalHandler{
		withdrawalClient: withdrawalClient,
	}
}

// Create handles POST /api/v1/withdrawals.
//
// INTER-SERVICE COMMUNICATION FLOW:
//
//  1. Merchant sends POST /api/v1/withdrawals to the gateway (:8080).
//  2. Gateway validates basic request structure (required fields, positive amount).
//  3. Gateway forwards the request via HTTP POST to the withdrawal-service at
//     POST http://withdrawal-service:8085/api/v1/withdrawals.
//  4. The withdrawal-service executes the full creation flow:
//     a. Checks the merchant's daily withdrawal limit (via merchant config).
//     b. Calls wallet-service GET /wallet/balance to verify sufficient funds.
//     c. Calls wallet-service POST /wallet/debit (or hold endpoint) to reserve funds.
//     d. Persists the withdrawal record in PostgreSQL with status "pending".
//  5. The withdrawal-service response (created withdrawal) flows back through
//     the gateway to the merchant.
//
// This design keeps the gateway stateless while the withdrawal-service
// orchestrates the multi-step creation process with wallet interactions.
func (h *WithdrawalHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateWithdrawalRequest
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
	if req.BeneficiaryAccount == "" {
		respondError(w, http.StatusBadRequest, "MISSING_FIELD", "beneficiary_account is required")
		return
	}

	// ---------------------------------------------------------------
	// Forward the withdrawal creation request to the withdrawal-service.
	//
	// The withdrawal-service expects merchant_id, amount, currency,
	// dest_type, and dest_details. We map the gateway's beneficiary
	// fields into the withdrawal-service's destination format.
	//
	// Network path: gateway (:8080) --> withdrawal-service (:8085)
	// Endpoint:     POST /api/v1/withdrawals
	// ---------------------------------------------------------------
	withdrawalReq := map[string]string{
		"merchant_id":  "00000000-0000-0000-0000-000000000001", // TODO: extract from auth middleware
		"amount":       req.Amount.String(),
		"currency":     req.Currency,
		"dest_type":    "bank",
		"dest_details": `{"bank":"` + req.BeneficiaryBank + `","account":"` + req.BeneficiaryAccount + `","name":"` + req.BeneficiaryName + `"}`,
	}

	// result holds the raw JSON from the withdrawal-service so we can
	// forward it as-is back to the merchant without schema coupling.
	var result json.RawMessage
	if err := h.withdrawalClient.Post(r.Context(), "/api/v1/withdrawals", withdrawalReq, &result); err != nil {
		// The withdrawal-service is unreachable or returned an error.
		// Return 502 Bad Gateway to indicate upstream failure.
		respondError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "failed to create withdrawal via withdrawal-service: "+err.Error())
		return
	}

	// Forward the withdrawal-service response directly to the merchant.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	w.Write(result)
}

// Get handles GET /api/v1/withdrawals/{id}.
//
// INTER-SERVICE COMMUNICATION FLOW:
//
//  1. Merchant sends GET /api/v1/withdrawals/{id} to the gateway (:8080).
//  2. Gateway validates the UUID format.
//  3. Gateway forwards to the withdrawal-service at
//     GET http://withdrawal-service:8085/api/v1/withdrawals/{id}.
//  4. The withdrawal-service fetches from PostgreSQL and returns the withdrawal.
//  5. The response flows back through the gateway to the merchant.
func (h *WithdrawalHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if _, err := uuid.Parse(idStr); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_ID", "invalid withdrawal id format")
		return
	}

	// ---------------------------------------------------------------
	// Forward the withdrawal lookup to the withdrawal-service.
	//
	// Network path: gateway (:8080) --> withdrawal-service (:8085)
	// Endpoint:     GET /api/v1/withdrawals/{id}
	// ---------------------------------------------------------------
	var result json.RawMessage
	if err := h.withdrawalClient.Get(r.Context(), "/api/v1/withdrawals/"+idStr, &result); err != nil {
		// The withdrawal-service could not find the record or is unreachable.
		respondError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "failed to get withdrawal from withdrawal-service: "+err.Error())
		return
	}

	// Forward the response as-is to the merchant.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(result)
}
