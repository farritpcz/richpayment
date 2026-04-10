// Package repository provides data access implementations for the user-service.
// This file contains a stub (in-memory) implementation of UserRepository
// suitable for development, testing, and compilation verification. It stores
// all entities in Go maps protected by a read-write mutex.
package repository

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/models"
)

// StubUserRepo is an in-memory implementation of the UserRepository interface.
// It stores all user-domain entities (admins, merchants, agents, partners)
// in Go maps. All operations are thread-safe via a read-write mutex.
//
// This implementation is intended for:
//   - Local development without a running PostgreSQL instance.
//   - Unit testing of the service layer.
//   - Build verification (ensuring the interface contract is satisfied).
type StubUserRepo struct {
	// mu protects all map fields below from concurrent access.
	mu sync.RWMutex

	// admins stores admin records keyed by their UUID.
	admins map[uuid.UUID]*models.Admin

	// merchants stores merchant records keyed by their UUID.
	merchants map[uuid.UUID]*models.Merchant

	// agents stores agent records keyed by their UUID.
	agents map[uuid.UUID]*models.Agent

	// partners stores partner records keyed by their UUID.
	partners map[uuid.UUID]*models.Partner
}

// NewStubUserRepo creates and returns a new StubUserRepo with initialised
// (empty) maps for all entity types. Call this once during service bootstrap
// and pass the returned value to the service constructors.
func NewStubUserRepo() *StubUserRepo {
	return &StubUserRepo{
		admins:    make(map[uuid.UUID]*models.Admin),
		merchants: make(map[uuid.UUID]*models.Merchant),
		agents:    make(map[uuid.UUID]*models.Agent),
		partners:  make(map[uuid.UUID]*models.Partner),
	}
}

// ---------------------------------------------------------------------------
// Admin operations
// ---------------------------------------------------------------------------

// CreateAdmin stores a new admin record in the in-memory map.
// Returns an error if an admin with the same ID already exists.
func (r *StubUserRepo) CreateAdmin(_ context.Context, admin *models.Admin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate ID to simulate a primary key constraint.
	if _, exists := r.admins[admin.ID]; exists {
		return errors.New("DUPLICATE_ADMIN", "admin with this ID already exists", 409)
	}

	// Store a copy to prevent the caller from mutating the stored record.
	clone := *admin
	r.admins[admin.ID] = &clone
	return nil
}

// GetAdminByID retrieves an admin by its UUID from the in-memory map.
// Returns ErrNotFound if no admin with the given ID exists.
func (r *StubUserRepo) GetAdminByID(_ context.Context, id uuid.UUID) (*models.Admin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Look up the admin in the map.
	admin, exists := r.admins[id]
	if !exists {
		return nil, errors.ErrNotFound
	}

	// Return a copy to prevent the caller from mutating the stored record.
	clone := *admin
	return &clone, nil
}

// ListAdmins returns a paginated slice of admins and the total count.
// The offset and limit parameters control which page of results is returned.
// Since this is an in-memory implementation, ordering is non-deterministic
// (map iteration order). A production PostgreSQL implementation would use
// ORDER BY created_at DESC.
func (r *StubUserRepo) ListAdmins(_ context.Context, offset, limit int) ([]models.Admin, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect all admins into a slice for pagination.
	all := make([]models.Admin, 0, len(r.admins))
	for _, a := range r.admins {
		all = append(all, *a)
	}

	// Calculate the total count before applying pagination.
	total := len(all)

	// Apply offset: skip the first `offset` records.
	if offset >= len(all) {
		return nil, total, nil
	}
	all = all[offset:]

	// Apply limit: return at most `limit` records.
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}

	return all, total, nil
}

// UpdateAdmin applies partial updates to an admin record in the in-memory map.
// Supported fields: "display_name", "role_mask", "status", "email".
// Returns ErrNotFound if no admin with the given ID exists.
func (r *StubUserRepo) UpdateAdmin(_ context.Context, id uuid.UUID, fields map[string]interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Look up the admin to update.
	admin, exists := r.admins[id]
	if !exists {
		return errors.ErrNotFound
	}

	// Apply each field update to the stored record.
	for key, val := range fields {
		switch key {
		case "display_name":
			// Update the admin's display name.
			if v, ok := val.(string); ok {
				admin.DisplayName = v
			}
		case "role_mask":
			// Update the admin's role bitmask.
			if v, ok := val.(int64); ok {
				admin.RoleMask = v
			}
		case "status":
			// Update the admin's status.
			if v, ok := val.(models.AdminStatus); ok {
				admin.Status = v
			}
		case "email":
			// Update the admin's email address.
			if v, ok := val.(string); ok {
				admin.Email = v
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Merchant operations
// ---------------------------------------------------------------------------

// CreateMerchant stores a new merchant record in the in-memory map.
// Returns an error if a merchant with the same ID already exists.
func (r *StubUserRepo) CreateMerchant(_ context.Context, merchant *models.Merchant) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate ID to simulate a primary key constraint.
	if _, exists := r.merchants[merchant.ID]; exists {
		return errors.New("DUPLICATE_MERCHANT", "merchant with this ID already exists", 409)
	}

	// Store a copy to prevent external mutation.
	clone := *merchant
	r.merchants[merchant.ID] = &clone
	return nil
}

// GetMerchantByID retrieves a merchant by its UUID from the in-memory map.
// Returns ErrNotFound if no merchant with the given ID exists.
func (r *StubUserRepo) GetMerchantByID(_ context.Context, id uuid.UUID) (*models.Merchant, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Look up the merchant in the map.
	merchant, exists := r.merchants[id]
	if !exists {
		return nil, errors.ErrNotFound
	}

	// Return a copy to prevent the caller from mutating the stored record.
	clone := *merchant
	return &clone, nil
}

// ListMerchants returns a paginated slice of merchants and the total count.
// If agentID is non-nil, only merchants managed by that agent are returned.
// If agentID is nil, all merchants are returned.
func (r *StubUserRepo) ListMerchants(_ context.Context, agentID *uuid.UUID, offset, limit int) ([]models.Merchant, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect merchants, optionally filtering by agent ID.
	all := make([]models.Merchant, 0, len(r.merchants))
	for _, m := range r.merchants {
		// If an agent filter is provided, skip merchants that don't match.
		if agentID != nil && (m.AgentID == nil || *m.AgentID != *agentID) {
			continue
		}
		all = append(all, *m)
	}

	// Calculate total before pagination.
	total := len(all)

	// Apply offset.
	if offset >= len(all) {
		return nil, total, nil
	}
	all = all[offset:]

	// Apply limit.
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}

	return all, total, nil
}

// UpdateMerchant applies partial updates to a merchant record.
// Supported fields: "name", "email", "webhook_url", "status",
// "deposit_fee_pct", "withdrawal_fee_pct", "daily_withdrawal_limit",
// "api_key_hash", "hmac_secret".
// Returns ErrNotFound if no merchant with the given ID exists.
func (r *StubUserRepo) UpdateMerchant(_ context.Context, id uuid.UUID, fields map[string]interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Look up the merchant to update.
	merchant, exists := r.merchants[id]
	if !exists {
		return errors.ErrNotFound
	}

	// Apply each field update to the stored record.
	for key, val := range fields {
		switch key {
		case "name":
			// Update the merchant's business name.
			if v, ok := val.(string); ok {
				merchant.Name = v
			}
		case "email":
			// Update the merchant's contact email.
			if v, ok := val.(string); ok {
				merchant.Email = v
			}
		case "webhook_url":
			// Update the merchant's webhook callback URL.
			if v, ok := val.(string); ok {
				merchant.WebhookURL = v
			}
		case "status":
			// Update the merchant's lifecycle status.
			if v, ok := val.(models.MerchantStatus); ok {
				merchant.Status = v
			}
		case "api_key_hash":
			// Update the merchant's API key hash (after key rotation).
			if v, ok := val.(string); ok {
				merchant.APIKeyHash = v
			}
		case "hmac_secret":
			// Update the merchant's HMAC signing secret.
			if v, ok := val.(string); ok {
				merchant.HMACSecret = v
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Agent operations
// ---------------------------------------------------------------------------

// CreateAgent stores a new agent record in the in-memory map.
// Returns an error if an agent with the same ID already exists.
func (r *StubUserRepo) CreateAgent(_ context.Context, agent *models.Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate ID.
	if _, exists := r.agents[agent.ID]; exists {
		return errors.New("DUPLICATE_AGENT", "agent with this ID already exists", 409)
	}

	// Store a copy to prevent external mutation.
	clone := *agent
	r.agents[agent.ID] = &clone
	return nil
}

// GetAgentByID retrieves an agent by its UUID from the in-memory map.
// Returns ErrNotFound if no agent with the given ID exists.
func (r *StubUserRepo) GetAgentByID(_ context.Context, id uuid.UUID) (*models.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Look up the agent in the map.
	agent, exists := r.agents[id]
	if !exists {
		return nil, errors.ErrNotFound
	}

	// Return a copy.
	clone := *agent
	return &clone, nil
}

// ListAgents returns a paginated slice of agents and the total count.
// If partnerID is non-nil, only agents belonging to that partner are returned.
// If partnerID is nil, all agents are returned.
func (r *StubUserRepo) ListAgents(_ context.Context, partnerID *uuid.UUID, offset, limit int) ([]models.Agent, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect agents, optionally filtering by partner ID.
	all := make([]models.Agent, 0, len(r.agents))
	for _, a := range r.agents {
		// If a partner filter is provided, skip agents that don't match.
		if partnerID != nil && (a.PartnerID == nil || *a.PartnerID != *partnerID) {
			continue
		}
		all = append(all, *a)
	}

	// Calculate total before pagination.
	total := len(all)

	// Apply offset.
	if offset >= len(all) {
		return nil, total, nil
	}
	all = all[offset:]

	// Apply limit.
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}

	return all, total, nil
}

// UpdateAgent applies partial updates to an agent record.
// Supported fields: "name", "email", "status", "commission_pct".
// Returns ErrNotFound if no agent with the given ID exists.
func (r *StubUserRepo) UpdateAgent(_ context.Context, id uuid.UUID, fields map[string]interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Look up the agent to update.
	agent, exists := r.agents[id]
	if !exists {
		return errors.ErrNotFound
	}

	// Apply each field update.
	for key, val := range fields {
		switch key {
		case "name":
			// Update the agent's display name.
			if v, ok := val.(string); ok {
				agent.Name = v
			}
		case "email":
			// Update the agent's contact email.
			if v, ok := val.(string); ok {
				agent.Email = v
			}
		case "status":
			// Update the agent's lifecycle status.
			if v, ok := val.(models.AgentStatus); ok {
				agent.Status = v
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Partner operations
// ---------------------------------------------------------------------------

// CreatePartner stores a new partner record in the in-memory map.
// Returns an error if a partner with the same ID already exists.
func (r *StubUserRepo) CreatePartner(_ context.Context, partner *models.Partner) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate ID.
	if _, exists := r.partners[partner.ID]; exists {
		return errors.New("DUPLICATE_PARTNER", "partner with this ID already exists", 409)
	}

	// Store a copy to prevent external mutation.
	clone := *partner
	r.partners[partner.ID] = &clone
	return nil
}

// GetPartnerByID retrieves a partner by its UUID from the in-memory map.
// Returns ErrNotFound if no partner with the given ID exists.
func (r *StubUserRepo) GetPartnerByID(_ context.Context, id uuid.UUID) (*models.Partner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Look up the partner in the map.
	partner, exists := r.partners[id]
	if !exists {
		return nil, errors.ErrNotFound
	}

	// Return a copy.
	clone := *partner
	return &clone, nil
}

// ListPartners returns a paginated slice of partners and the total count.
// Results are ordered non-deterministically in this stub implementation.
func (r *StubUserRepo) ListPartners(_ context.Context, offset, limit int) ([]models.Partner, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect all partners into a slice.
	all := make([]models.Partner, 0, len(r.partners))
	for _, p := range r.partners {
		all = append(all, *p)
	}

	// Calculate total before pagination.
	total := len(all)

	// Apply offset.
	if offset >= len(all) {
		return nil, total, nil
	}
	all = all[offset:]

	// Apply limit.
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}

	return all, total, nil
}

// UpdatePartner applies partial updates to a partner record.
// Supported fields: "name", "email", "status", "commission_pct".
// Returns ErrNotFound if no partner with the given ID exists.
func (r *StubUserRepo) UpdatePartner(_ context.Context, id uuid.UUID, fields map[string]interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Look up the partner to update.
	partner, exists := r.partners[id]
	if !exists {
		return errors.ErrNotFound
	}

	// Apply each field update.
	for key, val := range fields {
		switch key {
		case "name":
			// Update the partner's business name.
			if v, ok := val.(string); ok {
				partner.Name = v
			}
		case "email":
			// Update the partner's contact email.
			if v, ok := val.(string); ok {
				partner.Email = v
			}
		case "status":
			// Update the partner's lifecycle status.
			if v, ok := val.(models.PartnerStatus); ok {
				partner.Status = v
			}
		}
	}

	return nil
}

// Compile-time assertion: StubUserRepo must implement UserRepository.
var _ UserRepository = (*StubUserRepo)(nil)
