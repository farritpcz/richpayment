package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// CreateWithdrawalRequest is the payload for creating a withdrawal order.
type CreateWithdrawalRequest struct {
	MerchantOrderID    string          `json:"merchant_order_id"`
	Amount             decimal.Decimal `json:"amount"`
	Currency           string          `json:"currency"`
	BeneficiaryName    string          `json:"beneficiary_name"`
	BeneficiaryAccount string          `json:"beneficiary_account"`
	BeneficiaryBank    string          `json:"beneficiary_bank"`
	CallbackURL        string          `json:"callback_url"`
}

// WithdrawalResponse is the API representation of a withdrawal order.
type WithdrawalResponse struct {
	ID              string          `json:"id"`
	MerchantOrderID string          `json:"merchant_order_id"`
	Amount          decimal.Decimal `json:"amount"`
	Currency        string          `json:"currency"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
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
