package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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
	CreatedAt time.Time `json:"created_at"`
}

// WithdrawalHandler handles withdrawal-related API endpoints.
type WithdrawalHandler struct{}

// NewWithdrawalHandler creates a new WithdrawalHandler.
func NewWithdrawalHandler() *WithdrawalHandler {
	return &WithdrawalHandler{}
}

// Create handles POST /api/v1/withdrawals.
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

	// Stub: return a mock withdrawal response.
	now := time.Now().UTC()
	resp := WithdrawalResponse{
		ID:              uuid.New().String(),
		MerchantOrderID: req.MerchantOrderID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Status:          "pending",
		CreatedAt:       now,
	}

	respondCreated(w, resp)
}

// Get handles GET /api/v1/withdrawals/{id}.
func (h *WithdrawalHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if _, err := uuid.Parse(idStr); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_ID", "invalid withdrawal id format")
		return
	}

	// Stub: return a mock withdrawal.
	now := time.Now().UTC()
	resp := WithdrawalResponse{
		ID:              idStr,
		MerchantOrderID: "stub-order-001",
		Amount:          decimal.NewFromInt(500),
		Currency:        "THB",
		Status:          "pending",
		CreatedAt:       now,
	}

	respondOK(w, resp)
}
