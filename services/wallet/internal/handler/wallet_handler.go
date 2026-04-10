// Package handler exposes the wallet service's business logic over HTTP.
// Each handler method parses the incoming request, delegates to the service
// layer, and writes a JSON response. Input validation happens here so that
// the service layer can assume well-formed arguments.
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/wallet/internal/repository"
	"github.com/farritpcz/richpayment/services/wallet/internal/service"
)

// -------------------------------------------------------------------------
// WalletHandler
// -------------------------------------------------------------------------

// WalletHandler holds a reference to the WalletService and exposes HTTP
// handler methods. It is the translation layer between the HTTP transport
// and the domain logic; it never contains business rules itself.
type WalletHandler struct {
	// svc is the wallet business-logic layer injected at construction time.
	svc *service.WalletService
}

// NewWalletHandler creates a new WalletHandler wired to the given service.
//
// Parameters:
//   - svc: a fully initialised WalletService instance.
//
// Returns:
//   - *WalletHandler: the handler ready to be registered on a ServeMux.
func NewWalletHandler(svc *service.WalletService) *WalletHandler {
	return &WalletHandler{svc: svc}
}

// RegisterRoutes attaches all wallet HTTP endpoints to the provided
// ServeMux. The Go 1.22+ pattern syntax is used to bind both the HTTP
// method and the path in a single registration call.
//
// Registered routes:
//   - GET  /wallet/balance  – query a wallet's balance by owner+currency.
//   - POST /wallet/credit   – add funds to a wallet.
//   - POST /wallet/debit    – subtract funds from a wallet.
//
// Parameters:
//   - mux: the http.ServeMux to register routes on.
func (h *WalletHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /wallet/balance", h.GetBalance)
	mux.HandleFunc("POST /wallet/credit", h.Credit)
	mux.HandleFunc("POST /wallet/debit", h.Debit)
}

// -------------------------------------------------------------------------
// GET /wallet/balance
// -------------------------------------------------------------------------

// balanceResponse is the JSON response body returned by the GetBalance
// endpoint. It exposes the wallet's available balance and held amount as
// strings to preserve decimal precision across the JSON boundary.
type balanceResponse struct {
	// WalletID is the wallet's primary-key UUID.
	WalletID string `json:"wallet_id"`

	// OwnerType is the category of the wallet owner (merchant, agent, etc.).
	OwnerType string `json:"owner_type"`

	// OwnerID is the UUID of the entity that owns the wallet.
	OwnerID string `json:"owner_id"`

	// Currency is the ISO 4217 code for the wallet's denomination.
	Currency string `json:"currency"`

	// Balance is the available (spendable) balance as a decimal string.
	Balance string `json:"balance"`

	// HoldBalance is the amount reserved for pending operations.
	HoldBalance string `json:"hold_balance"`
}

// GetBalance handles GET /wallet/balance requests. It expects three query
// parameters: owner_type, owner_id, and currency. If the wallet does not
// exist, it is auto-created via EnsureWalletExists so that callers always
// get a valid response.
//
// Query parameters:
//   - owner_type: required; one of "merchant", "agent", "partner", "system".
//   - owner_id:   required; a valid UUID string.
//   - currency:   required; an ISO 4217 currency code (e.g. "THB").
//
// Response (200 OK):
//
//	{ "wallet_id": "...", "owner_type": "...", "owner_id": "...",
//	  "currency": "...", "balance": "0.00", "hold_balance": "0.00" }
func (h *WalletHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	// Parse and validate the required query parameters.
	ownerType := r.URL.Query().Get("owner_type")
	ownerIDStr := r.URL.Query().Get("owner_id")
	currency := r.URL.Query().Get("currency")

	// Validate that none of the required parameters are missing.
	if ownerType == "" || ownerIDStr == "" || currency == "" {
		writeError(w, http.StatusBadRequest, "MISSING_PARAMS", "owner_type, owner_id, and currency are required")
		return
	}

	// Parse the owner_id string into a UUID.
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_OWNER_ID", "owner_id must be a valid UUID")
		return
	}

	// Validate that owner_type is one of the known enum values.
	ot := models.OwnerType(ownerType)
	if !isValidOwnerType(ot) {
		writeError(w, http.StatusBadRequest, "INVALID_OWNER_TYPE",
			"owner_type must be one of: merchant, agent, partner, system")
		return
	}

	// Ensure the wallet exists before querying. This creates it on-the-fly
	// with zero balance if it hasn't been created yet, so the caller never
	// receives a "not found" error for a valid owner.
	walletID, err := h.svc.EnsureWalletExists(r.Context(), ot, ownerID, currency)
	if err != nil {
		logger.Error("ensure wallet exists failed", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to ensure wallet exists")
		return
	}

	// Fetch the current balance and hold_balance.
	balance, holdBalance, err := h.svc.GetBalance(r.Context(), ot, ownerID, currency)
	if err != nil {
		logger.Error("get balance failed", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get balance")
		return
	}

	// Build and send the JSON response.
	writeJSON(w, http.StatusOK, balanceResponse{
		WalletID:    walletID.String(),
		OwnerType:   ownerType,
		OwnerID:     ownerIDStr,
		Currency:    currency,
		Balance:     balance.StringFixed(2),
		HoldBalance: holdBalance.StringFixed(2),
	})
}

// -------------------------------------------------------------------------
// POST /wallet/credit
// -------------------------------------------------------------------------

// creditRequest is the expected JSON body for the POST /wallet/credit
// endpoint.
type creditRequest struct {
	// WalletID is the UUID of the wallet to credit.
	WalletID string `json:"wallet_id"`

	// Amount is the decimal amount to add to the wallet's balance.
	// Must be a positive number represented as a string for precision.
	Amount string `json:"amount"`

	// EntryType categorises the credit in the ledger (e.g. "deposit_credit").
	EntryType string `json:"entry_type"`

	// ReferenceType is a human-readable category for the source of the
	// credit (e.g. "deposit_order").
	ReferenceType string `json:"reference_type"`

	// ReferenceID is the UUID of the entity that triggered this credit
	// (e.g. the deposit order ID).
	ReferenceID string `json:"reference_id"`

	// Description is an optional free-text note stored in the ledger entry.
	Description string `json:"description"`
}

// Credit handles POST /wallet/credit requests. It parses the JSON body,
// validates all fields, and delegates to the service layer's Credit method.
//
// Request body (JSON):
//
//	{ "wallet_id": "uuid", "amount": "100.50", "entry_type": "deposit_credit",
//	  "reference_type": "deposit_order", "reference_id": "uuid",
//	  "description": "Deposit via bank transfer" }
//
// Response (200 OK):
//
//	{ "status": "ok" }
func (h *WalletHandler) Credit(w http.ResponseWriter, r *http.Request) {
	// Parse the JSON request body into the creditRequest struct.
	var req creditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must be valid JSON")
		return
	}

	// Validate wallet_id is a valid UUID.
	walletID, err := uuid.Parse(req.WalletID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_WALLET_ID", "wallet_id must be a valid UUID")
		return
	}

	// Parse and validate the credit amount.
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		writeError(w, http.StatusBadRequest, "INVALID_AMOUNT", "amount must be a positive decimal number")
		return
	}

	// Validate reference_id is a valid UUID.
	refID, err := uuid.Parse(req.ReferenceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REFERENCE_ID", "reference_id must be a valid UUID")
		return
	}

	// Validate that entry_type and reference_type are not empty.
	if req.EntryType == "" || req.ReferenceType == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "entry_type and reference_type are required")
		return
	}

	// Delegate to the service layer.
	err = h.svc.Credit(
		r.Context(),
		walletID,
		amount,
		models.LedgerEntryType(req.EntryType),
		req.ReferenceType,
		refID,
		req.Description,
	)
	if err != nil {
		// Check if the wallet was not found.
		if errors.Is(err, repository.ErrWalletNotFound) {
			writeError(w, http.StatusNotFound, "WALLET_NOT_FOUND", "wallet does not exist")
			return
		}
		logger.Error("credit failed", "err", err, "wallet_id", req.WalletID)
		writeError(w, http.StatusInternalServerError, "CREDIT_FAILED", "failed to credit wallet")
		return
	}

	// Success response.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// -------------------------------------------------------------------------
// POST /wallet/debit
// -------------------------------------------------------------------------

// debitRequest is the expected JSON body for the POST /wallet/debit
// endpoint. Its fields mirror creditRequest because debit is the inverse
// operation.
type debitRequest struct {
	// WalletID is the UUID of the wallet to debit.
	WalletID string `json:"wallet_id"`

	// Amount is the decimal amount to subtract from the wallet's balance.
	Amount string `json:"amount"`

	// EntryType categorises the debit in the ledger (e.g. "withdrawal_debit").
	EntryType string `json:"entry_type"`

	// ReferenceType is a human-readable category for the debit source.
	ReferenceType string `json:"reference_type"`

	// ReferenceID is the UUID of the entity that triggered this debit.
	ReferenceID string `json:"reference_id"`

	// Description is an optional free-text note stored in the ledger entry.
	Description string `json:"description"`
}

// Debit handles POST /wallet/debit requests. It parses the JSON body,
// validates all fields, and delegates to the service layer's Debit method.
// If the wallet has insufficient funds, a 400 Bad Request response is
// returned with the INSUFFICIENT_FUNDS error code.
//
// Request body (JSON):
//
//	{ "wallet_id": "uuid", "amount": "50.00", "entry_type": "withdrawal_debit",
//	  "reference_type": "withdrawal_request", "reference_id": "uuid",
//	  "description": "Payout to bank" }
//
// Response (200 OK):
//
//	{ "status": "ok" }
//
// Response (400 Bad Request):
//
//	{ "code": "INSUFFICIENT_FUNDS", "message": "..." }
func (h *WalletHandler) Debit(w http.ResponseWriter, r *http.Request) {
	// Parse the JSON request body.
	var req debitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must be valid JSON")
		return
	}

	// Validate wallet_id.
	walletID, err := uuid.Parse(req.WalletID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_WALLET_ID", "wallet_id must be a valid UUID")
		return
	}

	// Parse and validate the debit amount.
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		writeError(w, http.StatusBadRequest, "INVALID_AMOUNT", "amount must be a positive decimal number")
		return
	}

	// Validate reference_id.
	refID, err := uuid.Parse(req.ReferenceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REFERENCE_ID", "reference_id must be a valid UUID")
		return
	}

	// Validate required string fields.
	if req.EntryType == "" || req.ReferenceType == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "entry_type and reference_type are required")
		return
	}

	// Delegate to the service layer.
	err = h.svc.Debit(
		r.Context(),
		walletID,
		amount,
		models.LedgerEntryType(req.EntryType),
		req.ReferenceType,
		refID,
		req.Description,
	)
	if err != nil {
		// Map domain errors to appropriate HTTP responses.
		if errors.Is(err, service.ErrInsufficientFunds) {
			writeError(w, http.StatusBadRequest, "INSUFFICIENT_FUNDS", err.Error())
			return
		}
		if errors.Is(err, repository.ErrWalletNotFound) {
			writeError(w, http.StatusNotFound, "WALLET_NOT_FOUND", "wallet does not exist")
			return
		}
		logger.Error("debit failed", "err", err, "wallet_id", req.WalletID)
		writeError(w, http.StatusInternalServerError, "DEBIT_FAILED", "failed to debit wallet")
		return
	}

	// Success response.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// -------------------------------------------------------------------------
// Helper functions
// -------------------------------------------------------------------------

// errorResponse is the standard JSON error envelope returned by all wallet
// endpoints when a request fails.
type errorResponse struct {
	// Code is a machine-readable error code (e.g. "INSUFFICIENT_FUNDS").
	Code string `json:"code"`

	// Message is a human-readable description of the error.
	Message string `json:"message"`
}

// writeJSON serialises the given value as JSON and writes it to the
// ResponseWriter with the specified HTTP status code. It sets the
// Content-Type header to application/json.
//
// Parameters:
//   - w:      the http.ResponseWriter to write to.
//   - status: the HTTP status code (e.g. 200, 400, 500).
//   - v:      the value to serialise as JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// Encode the value directly to the writer. If encoding fails, we
	// cannot change the status code because headers have already been
	// sent, but we log the error for debugging.
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("failed to encode JSON response", "err", err)
	}
}

// writeError writes a standard JSON error response.
//
// Parameters:
//   - w:       the http.ResponseWriter.
//   - status:  the HTTP status code.
//   - code:    a machine-readable error code string.
//   - message: a human-readable error description.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{
		Code:    code,
		Message: fmt.Sprintf("%s", message),
	})
}

// isValidOwnerType checks whether the given OwnerType is one of the
// defined constants in the models package. This prevents arbitrary strings
// from being persisted as owner types.
//
// Parameters:
//   - ot: the OwnerType value to validate.
//
// Returns:
//   - bool: true if the value matches a known owner type, false otherwise.
func isValidOwnerType(ot models.OwnerType) bool {
	switch ot {
	case models.OwnerTypeMerchant, models.OwnerTypeAgent, models.OwnerTypePartner, models.OwnerTypeSystem:
		return true
	default:
		return false
	}
}
