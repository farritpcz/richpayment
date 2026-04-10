package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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
// the API.
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
	ExpiresAt time.Time `json:"expires_at"`
	// CreatedAt is when the order was created.
	CreatedAt time.Time `json:"created_at"`
}

// DepositHandler handles deposit-related API endpoints.
type DepositHandler struct{}

// NewDepositHandler creates a new DepositHandler.
func NewDepositHandler() *DepositHandler {
	return &DepositHandler{}
}

// Create handles POST /api/v1/deposits.
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

	// Stub: return a mock deposit response.
	now := time.Now().UTC()
	resp := DepositResponse{
		ID:              uuid.New().String(),
		MerchantOrderID: req.MerchantOrderID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Status:          "pending",
		ExpiresAt:       now.Add(15 * time.Minute),
		CreatedAt:       now,
	}

	respondCreated(w, resp)
}

// Get handles GET /api/v1/deposits/{id}.
func (h *DepositHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if _, err := uuid.Parse(idStr); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_ID", "invalid deposit id format")
		return
	}

	// Stub: return a mock deposit.
	now := time.Now().UTC()
	resp := DepositResponse{
		ID:              idStr,
		MerchantOrderID: "stub-order-001",
		Amount:          decimal.NewFromInt(1000),
		Currency:        "THB",
		Status:          "pending",
		ExpiresAt:       now.Add(15 * time.Minute),
		CreatedAt:       now,
	}

	respondOK(w, resp)
}
