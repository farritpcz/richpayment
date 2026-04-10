// Package service implements the business logic for the user-service.
// This file contains the AgentService which handles CRUD operations for
// agent accounts. Agents are intermediaries who manage merchant portfolios
// and earn commissions on their merchants' transactions.
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

// CreateAgentInput holds all the parameters needed to create a new agent.
// Grouping the inputs into a struct keeps the CreateAgent signature concise.
type CreateAgentInput struct {
	// Name is the agent's display name.
	Name string `json:"name"`

	// Email is the agent's unique login email address.
	Email string `json:"email"`

	// Password is the plaintext password (will be hashed with bcrypt).
	Password string `json:"password"`

	// PartnerID is the optional UUID of the partner this agent belongs to.
	// Nil means the agent operates independently without a partner.
	PartnerID *uuid.UUID `json:"partner_id,omitempty"`

	// CommissionPct is the agent's share of merchant fees.
	// For example, 0.30 means the agent receives 30% of the merchant fee.
	CommissionPct decimal.Decimal `json:"commission_pct"`
}

// AgentService encapsulates the business logic for managing agent accounts.
// It validates inputs, hashes passwords, and delegates persistence to the
// repository layer.
type AgentService struct {
	// repo is the data access layer for all user-domain entities.
	repo repository.UserRepository
}

// NewAgentService constructs an AgentService with the given repository.
//
// Parameters:
//   - repo: the repository implementation for persisting agent records.
//
// Returns a pointer to a fully initialised AgentService.
func NewAgentService(repo repository.UserRepository) *AgentService {
	return &AgentService{repo: repo}
}

// CreateAgent creates a new agent account with the provided details.
// The plaintext password is hashed with bcrypt before storage.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - input: the CreateAgentInput containing all agent details.
//
// Returns the created Agent model and nil error on success.
// Returns a validation error if required fields are missing, or an error
// if password hashing or repository insertion fails.
func (s *AgentService) CreateAgent(ctx context.Context, input CreateAgentInput) (*models.Agent, error) {
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
		return nil, fmt.Errorf("hash agent password: %w", err)
	}

	// Build the agent model with a new UUID and current timestamps.
	now := time.Now().UTC()
	agent := &models.Agent{
		ID:            uuid.New(),
		Name:          input.Name,
		Email:         input.Email,
		PasswordHash:  string(hash),
		PartnerID:     input.PartnerID,
		CommissionPct: input.CommissionPct,
		Status:        models.AgentStatusActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Persist the agent in the repository.
	if err := s.repo.CreateAgent(ctx, agent); err != nil {
		return nil, fmt.Errorf("create agent in repository: %w", err)
	}

	logger.Info("agent created",
		"agent_id", agent.ID.String(),
		"email", agent.Email,
	)

	return agent, nil
}

// GetAgent retrieves an agent by its unique identifier.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the agent to retrieve.
//
// Returns the Agent model and nil error on success.
// Returns ErrNotFound if no agent with the given ID exists.
func (s *AgentService) GetAgent(ctx context.Context, id uuid.UUID) (*models.Agent, error) {
	// Delegate to the repository for the database lookup.
	agent, err := s.repo.GetAgentByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	return agent, nil
}

// ListAgents returns a paginated list of agents, optionally filtered by
// the partner they belong to.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - partnerID: if non-nil, only agents belonging to this partner are returned.
//     If nil, all agents are returned.
//   - page: the 1-based page number.
//   - limit: the maximum number of agents per page.
//
// Returns a slice of Agent models, the total count, and nil error.
func (s *AgentService) ListAgents(
	ctx context.Context,
	partnerID *uuid.UUID,
	page, limit int,
) ([]models.Agent, int, error) {
	// Convert 1-based page number to zero-based offset.
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	// Delegate the paginated and filtered query to the repository.
	agents, total, err := s.repo.ListAgents(ctx, partnerID, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list agents: %w", err)
	}

	return agents, total, nil
}

// UpdateAgent applies partial updates to an existing agent record.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the agent to update.
//   - fields: a map of column names to new values. Supported fields include
//     "name" (string), "email" (string), "status" (AgentStatus),
//     "commission_pct" (decimal.Decimal).
//
// Returns nil on success, or an error if the agent is not found.
func (s *AgentService) UpdateAgent(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error {
	// Verify the agent exists before attempting the update.
	_, err := s.repo.GetAgentByID(ctx, id)
	if err != nil {
		return fmt.Errorf("agent not found for update: %w", err)
	}

	// Delegate the partial update to the repository.
	if err := s.repo.UpdateAgent(ctx, id, fields); err != nil {
		return fmt.Errorf("update agent: %w", err)
	}

	logger.Info("agent updated", "agent_id", id.String())

	return nil
}
