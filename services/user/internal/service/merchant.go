// Package service implements the business logic for the user-service.
// This file contains the MerchantService which handles CRUD operations for
// merchant accounts, including API key generation, HMAC secret management,
// and key rotation.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/user/internal/repository"
	"github.com/shopspring/decimal"
)

// CreateMerchantInput holds all the parameters needed to create a new merchant.
// It groups the various input fields into a single struct to keep the
// CreateMerchant function signature clean and readable.
type CreateMerchantInput struct {
	// Name is the merchant's business name.
	Name string `json:"name"`

	// Email is the merchant's contact email address.
	Email string `json:"email"`

	// WebhookURL is the endpoint where the merchant receives order callbacks.
	WebhookURL string `json:"webhook_url"`

	// AgentID is the optional UUID of the agent managing this merchant.
	// Nil means the merchant is managed directly by the system.
	AgentID *uuid.UUID `json:"agent_id,omitempty"`

	// DepositFeePct is the deposit fee percentage (e.g. "0.02" for 2%).
	DepositFeePct decimal.Decimal `json:"deposit_fee_pct"`

	// WithdrawalFeePct is the withdrawal fee percentage (e.g. "0.01" for 1%).
	WithdrawalFeePct decimal.Decimal `json:"withdrawal_fee_pct"`

	// DailyWithdrawalLimit is the maximum daily withdrawal amount.
	DailyWithdrawalLimit decimal.Decimal `json:"daily_withdrawal_limit"`
}

// MerchantService encapsulates the business logic for managing merchant
// accounts. It handles API key generation, HMAC secret creation, and
// delegates persistence to the repository layer.
type MerchantService struct {
	// repo is the data access layer for all user-domain entities.
	repo repository.UserRepository
}

// NewMerchantService constructs a MerchantService with the given repository.
//
// Parameters:
//   - repo: the repository implementation for persisting merchant records.
//
// Returns a pointer to a fully initialised MerchantService.
func NewMerchantService(repo repository.UserRepository) *MerchantService {
	return &MerchantService{repo: repo}
}

// CreateMerchant creates a new merchant account with an auto-generated API key
// and HMAC secret. The raw API key is returned to the caller exactly once —
// only the bcrypt hash is persisted. The HMAC secret is stored in plaintext
// because it must be retrievable for signature verification.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - input: the CreateMerchantInput struct containing all merchant details.
//
// Returns:
//   - merchant: the created Merchant model.
//   - apiKey: the raw API key string (only returned once at creation time).
//   - error: nil on success, or an error if generation/persistence fails.
func (s *MerchantService) CreateMerchant(
	ctx context.Context,
	input CreateMerchantInput,
) (*models.Merchant, string, error) {
	// Validate required fields.
	if input.Name == "" {
		return nil, "", errors.New("VALIDATION_ERROR", "name is required", 400)
	}
	if input.Email == "" {
		return nil, "", errors.New("VALIDATION_ERROR", "email is required", 400)
	}

	// Generate a cryptographically secure random API key (32 bytes = 64 hex chars).
	// This key is given to the merchant for authenticating API requests.
	apiKey, err := generateSecureToken(32)
	if err != nil {
		return nil, "", fmt.Errorf("generate api key: %w", err)
	}

	// Hash the API key using bcrypt before storing. The raw key is only
	// returned once to the caller and is never persisted in plaintext.
	apiKeyHash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash api key: %w", err)
	}

	// Generate a separate HMAC secret (32 bytes = 64 hex chars) for request
	// signing. Unlike the API key, the HMAC secret is stored in plaintext
	// because the server needs the original value to verify HMAC signatures.
	hmacSecret, err := generateSecureToken(32)
	if err != nil {
		return nil, "", fmt.Errorf("generate hmac secret: %w", err)
	}

	// Build the merchant model with a new UUID and current timestamps.
	now := time.Now().UTC()
	merchant := &models.Merchant{
		ID:                   uuid.New(),
		Name:                 input.Name,
		Email:                input.Email,
		APIKeyHash:           string(apiKeyHash),
		HMACSecret:           hmacSecret,
		WebhookURL:           input.WebhookURL,
		AgentID:              input.AgentID,
		DepositFeePct:        input.DepositFeePct,
		WithdrawalFeePct:     input.WithdrawalFeePct,
		DailyWithdrawalLimit: input.DailyWithdrawalLimit,
		Status:               models.MerchantStatusActive,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	// Persist the merchant in the repository.
	if err := s.repo.CreateMerchant(ctx, merchant); err != nil {
		return nil, "", fmt.Errorf("create merchant in repository: %w", err)
	}

	logger.Info("merchant created",
		"merchant_id", merchant.ID.String(),
		"name", merchant.Name,
	)

	// Return the merchant model and the raw API key. The caller should
	// display the API key to the admin and instruct them to share it
	// securely with the merchant. It cannot be retrieved again.
	return merchant, apiKey, nil
}

// GetMerchant retrieves a merchant by its unique identifier.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the merchant to retrieve.
//
// Returns the Merchant model and nil error on success.
// Returns ErrNotFound if no merchant with the given ID exists.
func (s *MerchantService) GetMerchant(ctx context.Context, id uuid.UUID) (*models.Merchant, error) {
	// Delegate to the repository for the database lookup.
	merchant, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get merchant: %w", err)
	}
	return merchant, nil
}

// ListMerchants returns a paginated list of merchants, optionally filtered
// by the agent who manages them.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - agentID: if non-nil, only merchants managed by this agent are returned.
//     If nil, all merchants are returned.
//   - page: the 1-based page number.
//   - limit: the maximum number of merchants per page.
//
// Returns a slice of Merchant models, the total count, and nil error.
func (s *MerchantService) ListMerchants(
	ctx context.Context,
	agentID *uuid.UUID,
	page, limit int,
) ([]models.Merchant, int, error) {
	// Convert 1-based page number to zero-based offset.
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	// Delegate the paginated and filtered query to the repository.
	merchants, total, err := s.repo.ListMerchants(ctx, agentID, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list merchants: %w", err)
	}

	return merchants, total, nil
}

// UpdateMerchant applies partial updates to an existing merchant record.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the merchant to update.
//   - fields: a map of column names to new values. Supported fields include
//     "name", "email", "webhook_url", "status", etc.
//
// Returns nil on success, or an error if the merchant is not found.
func (s *MerchantService) UpdateMerchant(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error {
	// Verify the merchant exists before attempting the update.
	_, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return fmt.Errorf("merchant not found for update: %w", err)
	}

	// Delegate the partial update to the repository.
	if err := s.repo.UpdateMerchant(ctx, id, fields); err != nil {
		return fmt.Errorf("update merchant: %w", err)
	}

	logger.Info("merchant updated", "merchant_id", id.String())

	return nil
}

// RevokeAPIKey generates a new API key for a merchant, invalidating the
// previous one. This is used when a merchant's API key is compromised or
// when periodic key rotation is required. A valid TOTP code must be provided
// as an additional security measure.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - merchantID: the UUID of the merchant whose key is being revoked.
//   - totpCode: the time-based one-time password for verification.
//     NOTE: TOTP validation is currently a placeholder (always passes).
//     In production, this would validate against the admin's TOTP seed.
//
// Returns:
//   - newAPIKey: the new raw API key (only returned once).
//   - error: nil on success, or an error if the merchant is not found or
//     key generation fails.
func (s *MerchantService) RevokeAPIKey(
	ctx context.Context,
	merchantID uuid.UUID,
	totpCode string,
) (string, error) {
	// Verify the merchant exists.
	_, err := s.repo.GetMerchantByID(ctx, merchantID)
	if err != nil {
		return "", fmt.Errorf("merchant not found for key revocation: %w", err)
	}

	// TODO: Validate the TOTP code against the admin's TOTP seed.
	// For now, we accept any non-empty code as valid.
	if totpCode == "" {
		return "", errors.New("VALIDATION_ERROR", "totp_code is required for key revocation", 400)
	}

	// Generate a new cryptographically secure API key.
	newAPIKey, err := generateSecureToken(32)
	if err != nil {
		return "", fmt.Errorf("generate new api key: %w", err)
	}

	// Hash the new API key before storing.
	newAPIKeyHash, err := bcrypt.GenerateFromPassword([]byte(newAPIKey), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash new api key: %w", err)
	}

	// Generate a new HMAC secret as well, since key rotation should
	// also rotate the signing secret to maintain security.
	newHMACSecret, err := generateSecureToken(32)
	if err != nil {
		return "", fmt.Errorf("generate new hmac secret: %w", err)
	}

	// Update the merchant's API key hash and HMAC secret in the repository.
	updateFields := map[string]interface{}{
		"api_key_hash": string(newAPIKeyHash),
		"hmac_secret":  newHMACSecret,
	}

	if err := s.repo.UpdateMerchant(ctx, merchantID, updateFields); err != nil {
		return "", fmt.Errorf("update merchant api key: %w", err)
	}

	logger.Info("merchant api key revoked",
		"merchant_id", merchantID.String(),
	)

	// Return the new raw API key. The admin should securely share this
	// with the merchant. It cannot be retrieved again.
	return newAPIKey, nil
}

// generateSecureToken creates a cryptographically secure random token of
// the specified byte length. The token is returned as a hex-encoded string,
// so the resulting string length is 2 * nBytes characters.
//
// Parameters:
//   - nBytes: the number of random bytes to generate. For example, 32 bytes
//     produces a 64-character hex string.
//
// Returns the hex-encoded token string and nil error on success.
// Returns an error if the system's cryptographic random number generator fails.
func generateSecureToken(nBytes int) (string, error) {
	// Allocate a buffer of the requested size.
	buf := make([]byte, nBytes)

	// Read cryptographically secure random bytes from the system CSPRNG.
	// This uses /dev/urandom on Unix and CryptGenRandom on Windows.
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	// Encode the random bytes as a lowercase hexadecimal string.
	return hex.EncodeToString(buf), nil
}
