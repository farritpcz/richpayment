// Package service implements the business logic for the user-service.
// This file contains the PartnerService which handles CRUD operations for
// partner accounts. Partners sit at the top of the commission hierarchy
// (Partner -> Agent -> Merchant) and earn commissions on transactions
// processed through their agent network.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/user/internal/repository"
)

// CreatePartnerInput holds all the parameters needed to create a new partner.
// Grouping the inputs into a struct keeps the CreatePartner signature concise.
type CreatePartnerInput struct {
	// Name is the partner's business name.
	Name string `json:"name"`

	// Email is the partner's unique login email address.
	Email string `json:"email"`

	// Password is the plaintext password (will be hashed with bcrypt).
	Password string `json:"password"`

	// CommissionPct is the partner's share of merchant fees routed through
	// their agent network. For example, 0.10 means 10% of the fee.
	CommissionPct decimal.Decimal `json:"commission_pct"`
}

// PartnerService encapsulates the business logic for managing partner accounts.
// It validates inputs, hashes passwords, and delegates persistence to the
// repository layer.
type PartnerService struct {
	// repo is the data access layer for all user-domain entities.
	repo repository.UserRepository
}

// NewPartnerService constructs a PartnerService with the given repository.
//
// Parameters:
//   - repo: the repository implementation for persisting partner records.
//
// Returns a pointer to a fully initialised PartnerService.
func NewPartnerService(repo repository.UserRepository) *PartnerService {
	return &PartnerService{repo: repo}
}

// CreatePartner creates a new partner account with the provided details.
// The plaintext password is hashed with bcrypt before storage.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - input: the CreatePartnerInput containing all partner details.
//
// Returns the created Partner model and nil error on success.
// Returns a validation error if required fields are missing, or an error
// if password hashing or repository insertion fails.
func (s *PartnerService) CreatePartner(ctx context.Context, input CreatePartnerInput) (*models.Partner, error) {
	// Validate that required fields are not empty.
	if input.Name == "" {
		return nil, errors.New("VALIDATION_ERROR", "name is required", 400)
	}
	if input.Email == "" {
		return nil, errors.New("VALIDATION_ERROR", "email is required", 400)
	}
	if input.Password == "" {
		return nil, errors.New("VALIDATION_ERROR", "password is required", 400)
	}

	// Hash the plaintext password using bcrypt with the default cost factor.
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash partner password: %w", err)
	}

	// Build the partner model with a new UUID and current timestamps.
	now := time.Now().UTC()
	partner := &models.Partner{
		ID:            uuid.New(),
		Name:          input.Name,
		Email:         input.Email,
		PasswordHash:  string(hash),
		CommissionPct: input.CommissionPct,
		Status:        models.PartnerStatusActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Persist the partner in the repository.
	if err := s.repo.CreatePartner(ctx, partner); err != nil {
		return nil, fmt.Errorf("create partner in repository: %w", err)
	}

	logger.Info("partner created",
		"partner_id", partner.ID.String(),
		"email", partner.Email,
	)

	return partner, nil
}

// GetPartner retrieves a partner by its unique identifier.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the partner to retrieve.
//
// Returns the Partner model and nil error on success.
// Returns ErrNotFound if no partner with the given ID exists.
func (s *PartnerService) GetPartner(ctx context.Context, id uuid.UUID) (*models.Partner, error) {
	// Delegate to the repository for the database lookup.
	partner, err := s.repo.GetPartnerByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get partner: %w", err)
	}
	return partner, nil
}

// ListPartners returns a paginated list of all partners in the system.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - page: the 1-based page number.
//   - limit: the maximum number of partners per page.
//
// Returns a slice of Partner models, the total count, and nil error.
func (s *PartnerService) ListPartners(ctx context.Context, page, limit int) ([]models.Partner, int, error) {
	// Convert 1-based page number to zero-based offset.
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	// Delegate the paginated query to the repository.
	partners, total, err := s.repo.ListPartners(ctx, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list partners: %w", err)
	}

	return partners, total, nil
}

// UpdatePartner applies partial updates to an existing partner record.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the partner to update.
//   - fields: a map of column names to new values. Supported fields include
//     "name" (string), "email" (string), "status" (PartnerStatus),
//     "commission_pct" (decimal.Decimal).
//
// Returns nil on success, or an error if the partner is not found.
func (s *PartnerService) UpdatePartner(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error {
	// Verify the partner exists before attempting the update.
	_, err := s.repo.GetPartnerByID(ctx, id)
	if err != nil {
		return fmt.Errorf("partner not found for update: %w", err)
	}

	// Delegate the partial update to the repository.
	if err := s.repo.UpdatePartner(ctx, id, fields); err != nil {
		return fmt.Errorf("update partner: %w", err)
	}

	logger.Info("partner updated", "partner_id", id.String())

	return nil
}
