package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// CreateDepositRequest is the payload for creating a deposit order.
type CreateDepositRequest struct {
	MerchantOrderID  string          `json:"merchant_order_id"`
	CustomerName     string          `json:"customer_name"`
	CustomerBankCode string          `json:"customer_bank_code"`
	Amount           decimal.Decimal `json:"amount"`
	Currency         string          `json:"currency"`
	CallbackURL      string          `json:"callback_url"`
}

// DepositResponse is the API representation of a deposit order.
type DepositResponse struct {
	ID              string          `json:"id"`
	MerchantOrderID string          `json:"merchant_order_id"`
	Amount          decimal.Decimal `json:"amount"`
	Currency        string          `json:"currency"`
	Status          string          `json:"status"`
	QRPayload       string          `json:"qr_payload,omitempty"`
	ExpiresAt       time.Time       `json:"expires_at"`
	CreatedAt       time.Time       `json:"created_at"`
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
