// Package repository defines the data access interfaces and implementations
// for the order-service. All database interactions for deposit orders are
// abstracted behind the OrderRepository interface so that the business-logic
// layer (service package) stays decoupled from the persistence technology.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
)

// OrderRepository is the primary data-access interface for deposit orders.
// Every method accepts a context.Context to support request-scoped deadlines,
// cancellation, and tracing propagation.
type OrderRepository interface {
	// Create persists a new DepositOrder into the database.
	// The order.ID must be pre-generated (UUID v4) by the caller.
	// Returns an error if the insert fails (e.g. duplicate primary key,
	// connection issue, constraint violation).
	Create(ctx context.Context, order *models.DepositOrder) error

	// GetByID retrieves a single deposit order by its unique identifier.
	// Returns a pointer to the order and nil error on success.
	// Returns (nil, ErrNotFound) when no row matches the given id.
	GetByID(ctx context.Context, id uuid.UUID) (*models.DepositOrder, error)

	// UpdateStatus transitions an order to a new status and applies any
	// additional field changes described in the fields map. The fields map
	// uses column names as keys and the new values as values. This design
	// keeps the interface flexible for different status transitions that
	// require different columns to be updated (e.g. matched_by, actual_amount,
	// fee_amount, net_amount, matched_at, webhook_sent, etc.).
	UpdateStatus(ctx context.Context, id uuid.UUID, status models.OrderStatus, fields map[string]interface{}) error

	// FindPendingByAmount searches for a pending deposit order that is
	// assigned to a specific bank account and has the given adjusted amount.
	// This is the core lookup used by the SMS matcher to pair an incoming
	// bank notification with an outstanding deposit order.
	// Returns (nil, ErrNotFound) when no matching order exists.
	FindPendingByAmount(ctx context.Context, bankAccountID uuid.UUID, amount decimal.Decimal) (*models.DepositOrder, error)

	// FindExpired returns all deposit orders whose status is still "pending"
	// and whose expiry timestamp is before the given cutoff time. The expiry
	// worker calls this periodically to discover orders that should be moved
	// to the "expired" status.
	FindExpired(ctx context.Context, before time.Time) ([]models.DepositOrder, error)
}
