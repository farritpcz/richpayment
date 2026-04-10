package handler

import (
	"encoding/json"
	"net/http"

	"github.com/farritpcz/richpayment/pkg/httpclient"
)

// WalletBalanceResponse is the JSON representation of a wallet balance
// returned by the API.
type WalletBalanceResponse struct {
	// Currency is the ISO 4217 currency code.
	Currency string `json:"currency"`
	// Balance is the total wallet balance including held funds.
	Balance string `json:"balance"`
	// HoldBalance is the portion of the balance reserved for pending withdrawals.
	HoldBalance string `json:"hold_balance"`
	// Available is the spendable balance (Balance - HoldBalance).
	Available string `json:"available"`
}

// WalletHandler handles wallet-related API endpoints in the gateway.
//
// INTER-SERVICE COMMUNICATION FLOW:
// The gateway proxies wallet balance queries to the wallet-service, which
// owns all wallet state (balances, holds, ledger entries). The flow is:
//
//   Client (merchant) -> gateway-api (:8080) -> wallet-service (:8084)
//
// The wallet-service manages:
//   - Balance queries (GET /wallet/balance)
//   - Credits from completed deposits (POST /wallet/credit)
//   - Debits for completed withdrawals (POST /wallet/debit)
//
// The gateway only exposes the balance query endpoint to merchants.
// Credit and debit operations are internal-only, triggered by the
// order-service (deposits) and withdrawal-service (withdrawals).
type WalletHandler struct {
	// walletClient is the HTTP client configured to call the wallet-service.
	// The wallet-service runs on port 8084 and manages all wallet balances,
	// holds, and ledger entries for merchants, agents, and partners.
	walletClient *httpclient.ServiceClient
}

// NewWalletHandler creates a new WalletHandler wired to the wallet-service.
//
// Parameters:
//   - walletClient: an httpclient.ServiceClient pointing at the wallet-service
//     base URL (e.g. http://localhost:8084). Used to proxy balance queries
//     from merchants to the wallet-service.
func NewWalletHandler(walletClient *httpclient.ServiceClient) *WalletHandler {
	return &WalletHandler{
		walletClient: walletClient,
	}
}

// Balance handles GET /api/v1/wallet/balance.
//
// INTER-SERVICE COMMUNICATION FLOW:
//
//  1. Merchant sends GET /api/v1/wallet/balance to the gateway (:8080).
//  2. Gateway forwards the request to the wallet-service at
//     GET http://wallet-service:8084/wallet/balance?owner_type=merchant&owner_id={id}&currency={cur}.
//  3. The wallet-service:
//     a. Ensures the wallet exists (auto-creates if missing with zero balance).
//     b. Queries PostgreSQL for the current balance and hold_balance.
//     c. Returns the wallet state as JSON.
//  4. The response flows back through the gateway to the merchant.
//
// This gives merchants a real-time view of their wallet balance without the
// gateway needing any direct database access or wallet state knowledge.
func (h *WalletHandler) Balance(w http.ResponseWriter, r *http.Request) {
	// ---------------------------------------------------------------
	// Forward the balance query to the wallet-service.
	//
	// The wallet-service expects query parameters: owner_type, owner_id,
	// and currency. We construct the query path with the merchant's
	// identity. Currently using a placeholder merchant ID until the
	// auth middleware provides the real one.
	//
	// Network path: gateway (:8080) --> wallet-service (:8084)
	// Endpoint:     GET /wallet/balance?owner_type=merchant&owner_id=...&currency=THB
	// ---------------------------------------------------------------
	merchantID := "00000000-0000-0000-0000-000000000001" // TODO: extract from auth middleware
	currency := r.URL.Query().Get("currency")
	if currency == "" {
		currency = "THB"
	}

	// Build the query path for the wallet-service balance endpoint.
	// The wallet-service uses query parameters rather than path params
	// for the balance lookup because a wallet is identified by the
	// combination of owner_type + owner_id + currency (composite key).
	path := "/wallet/balance?owner_type=merchant&owner_id=" + merchantID + "&currency=" + currency

	// result holds the raw JSON from the wallet-service so we can
	// forward it without needing to parse the wallet-specific fields.
	var result json.RawMessage
	if err := h.walletClient.Get(r.Context(), path, &result); err != nil {
		// The wallet-service is unreachable or returned an error.
		respondError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "failed to get balance from wallet-service: "+err.Error())
		return
	}

	// Forward the wallet-service response directly to the merchant.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(result)
}
