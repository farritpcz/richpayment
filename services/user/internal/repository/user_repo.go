// Package repository defines the data access interfaces for the user-service.
// All database interactions for admins, merchants, agents, and partners are
// abstracted behind the UserRepository interface so that the business-logic
// layer (service package) remains decoupled from the persistence technology.
package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/farritpcz/richpayment/pkg/models"
)

// UserRepository is the primary data-access interface for all user-domain
// entities: admins, merchants, agents, and partners. Every method accepts a
// context.Context to support request-scoped deadlines, cancellation, and
// tracing propagation.
type UserRepository interface {
	// -----------------------------------------------------------------------
	// Admin operations
	// -----------------------------------------------------------------------

	// CreateAdmin persists a new admin record into the database.
	// The admin.ID must be pre-generated (UUID v4) by the caller.
	// Returns an error if the insert fails (e.g. duplicate email).
	CreateAdmin(ctx context.Context, admin *models.Admin) error

	// GetAdminByID retrieves a single admin by its unique identifier.
	// Returns (nil, ErrNotFound) when no row matches the given id.
	GetAdminByID(ctx context.Context, id uuid.UUID) (*models.Admin, error)

	// ListAdmins returns a paginated list of admins and the total count.
	// The offset and limit parameters control pagination. Results are
	// ordered by created_at descending (newest first).
	ListAdmins(ctx context.Context, offset, limit int) ([]models.Admin, int, error)

	// UpdateAdmin applies partial updates to an admin record. The fields
	// map uses column names as keys and new values as values. Only the
	// provided fields are updated; other columns remain unchanged.
	UpdateAdmin(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error

	// -----------------------------------------------------------------------
	// Merchant operations
	// -----------------------------------------------------------------------

	// CreateMerchant persists a new merchant record into the database.
	// The merchant.ID must be pre-generated (UUID v4) by the caller.
	// Returns an error if the insert fails (e.g. duplicate email).
	CreateMerchant(ctx context.Context, merchant *models.Merchant) error

	// GetMerchantByID retrieves a single merchant by its unique identifier.
	// Returns (nil, ErrNotFound) when no row matches the given id.
	GetMerchantByID(ctx context.Context, id uuid.UUID) (*models.Merchant, error)

	// ListMerchants returns a paginated list of merchants, optionally
	// filtered by the managing agent's UUID. If agentID is nil, all
	// merchants are returned. Results are ordered by created_at desc.
	ListMerchants(ctx context.Context, agentID *uuid.UUID, offset, limit int) ([]models.Merchant, int, error)

	// UpdateMerchant applies partial updates to a merchant record.
	// Only the provided fields are updated.
	UpdateMerchant(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error

	// -----------------------------------------------------------------------
	// Agent operations
	// -----------------------------------------------------------------------

	// CreateAgent persists a new agent record into the database.
	// The agent.ID must be pre-generated (UUID v4) by the caller.
	CreateAgent(ctx context.Context, agent *models.Agent) error

	// GetAgentByID retrieves a single agent by its unique identifier.
	// Returns (nil, ErrNotFound) when no row matches the given id.
	GetAgentByID(ctx context.Context, id uuid.UUID) (*models.Agent, error)

	// ListAgents returns a paginated list of agents, optionally filtered
	// by the partner's UUID. If partnerID is nil, all agents are returned.
	ListAgents(ctx context.Context, partnerID *uuid.UUID, offset, limit int) ([]models.Agent, int, error)

	// UpdateAgent applies partial updates to an agent record.
	// Only the provided fields are updated.
	UpdateAgent(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error

	// -----------------------------------------------------------------------
	// Partner operations
	// -----------------------------------------------------------------------

	// CreatePartner persists a new partner record into the database.
	// The partner.ID must be pre-generated (UUID v4) by the caller.
	CreatePartner(ctx context.Context, partner *models.Partner) error

	// GetPartnerByID retrieves a single partner by its unique identifier.
	// Returns (nil, ErrNotFound) when no row matches the given id.
	GetPartnerByID(ctx context.Context, id uuid.UUID) (*models.Partner, error)

	// ListPartners returns a paginated list of partners and the total count.
	// Results are ordered by created_at descending (newest first).
	ListPartners(ctx context.Context, offset, limit int) ([]models.Partner, int, error)

	// UpdatePartner applies partial updates to a partner record.
	// Only the provided fields are updated.
	UpdatePartner(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error
}
