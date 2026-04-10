package handler

import (
	"net/http"

	"github.com/shopspring/decimal"
)

// WalletBalanceResponse is the JSON representation of a wallet balance
// returned by the API.
type WalletBalanceResponse struct {
	// Currency is the ISO 4217 currency code.
	Currency string `json:"currency"`
	// Balance is the total wallet balance including held funds.
	Balance decimal.Decimal `json:"balance"`
	// HoldBalance is the portion of the balance reserved for pending withdrawals.
	HoldBalance decimal.Decimal `json:"hold_balance"`
	// Available is the spendable balance (Balance - HoldBalance).
	Available decimal.Decimal `json:"available"`
}

// WalletHandler handles wallet-related API endpoints.
type WalletHandler struct{}

// NewWalletHandler creates a new WalletHandler.
func NewWalletHandler() *WalletHandler {
	return &WalletHandler{}
}

// Balance handles GET /api/v1/wallet/balance.
func (h *WalletHandler) Balance(w http.ResponseWriter, r *http.Request) {
	// Stub: return a mock balance.
	balance := decimal.NewFromInt(50000)
	hold := decimal.NewFromInt(2000)
	resp := WalletBalanceResponse{
		Currency:    "THB",
		Balance:     balance,
		HoldBalance: hold,
		Available:   balance.Sub(hold),
	}

	respondOK(w, resp)
}
