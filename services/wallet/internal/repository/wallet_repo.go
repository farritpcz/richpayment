// Package repository defines the data-access interfaces and implementations
// for the wallet service. All database interactions are abstracted behind
// the WalletRepository interface so the service layer never depends on a
// concrete database driver.
package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/farritpcz/richpayment/pkg/models"
)

// -------------------------------------------------------------------------
// Sentinel errors
// -------------------------------------------------------------------------

// ErrVersionConflict is returned when an optimistic-locking update fails
// because the row's version column no longer matches the expected value.
// Callers (typically the service layer) should catch this error and decide
// whether to retry the operation.
var ErrVersionConflict = errors.New("wallet version conflict: row was modified by another transaction")

// ErrWalletNotFound is returned when no wallet row matches the query
// criteria (either by ID or by owner+currency composite key).
var ErrWalletNotFound = errors.New("wallet not found")

// -------------------------------------------------------------------------
// WalletRepository interface
// -------------------------------------------------------------------------

// WalletRepository abstracts all persistence operations for wallets and
// their associated ledger entries. Every method accepts a context.Context so
// that cancellations and deadlines propagate to the underlying database
// driver.
//
// Implementations MUST be safe for concurrent use by multiple goroutines
// because the HTTP server dispatches requests in parallel.
type WalletRepository interface {
	// GetByOwner retrieves a wallet by its composite natural key:
	// (owner_type, owner_id, currency). Returns ErrWalletNotFound when no
	// matching row exists.
	//
	// Parameters:
	//   - ctx:       request-scoped context for cancellation / tracing.
	//   - ownerType: the type of entity that owns the wallet (merchant, agent, etc.).
	//   - ownerID:   the UUID of the owning entity.
	//   - currency:  ISO 4217 currency code (e.g. "THB", "USD").
	//
	// Returns:
	//   - *models.Wallet: the wallet record, or nil on error.
	//   - error:          ErrWalletNotFound if no match, or a wrapped DB error.
	GetByOwner(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, currency string) (*models.Wallet, error)

	// GetByID retrieves a wallet by its primary key (UUID).
	// Returns ErrWalletNotFound when no matching row exists.
	//
	// Parameters:
	//   - ctx: request-scoped context.
	//   - id:  the wallet's primary-key UUID.
	//
	// Returns:
	//   - *models.Wallet: the wallet record, or nil on error.
	//   - error:          ErrWalletNotFound if no match, or a wrapped DB error.
	GetByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)

	// Create inserts a new wallet row. The caller is expected to populate
	// all fields of the Wallet struct before calling Create. If a wallet
	// with the same (owner_type, owner_id, currency) already exists, the
	// implementation should use INSERT ... ON CONFLICT DO NOTHING and
	// return nil (no error) without modifying the existing row.
	//
	// Parameters:
	//   - ctx:    request-scoped context.
	//   - wallet: the fully populated wallet record to persist.
	//
	// Returns:
	//   - error: nil on success (including a no-op conflict), or a wrapped DB error.
	Create(ctx context.Context, wallet *models.Wallet) error

	// UpdateBalance atomically sets a wallet's balance, hold_balance, and
	// bumps the version column. The update only succeeds when the current
	// version in the database matches expectedVersion (optimistic locking).
	//
	// If the version has changed since the caller last read the row, the
	// implementation MUST return ErrVersionConflict so the service layer
	// can retry the entire read-modify-write cycle.
	//
	// Parameters:
	//   - ctx:             request-scoped context.
	//   - id:              the wallet's primary-key UUID.
	//   - newBalance:      the new balance value to set.
	//   - newHold:         the new hold_balance value to set.
	//   - expectedVersion: the version the caller read; acts as the CAS guard.
	//
	// Returns:
	//   - error: ErrVersionConflict on mismatch, or a wrapped DB error.
	UpdateBalance(ctx context.Context, id uuid.UUID, newBalance, newHold string, expectedVersion int64) error

	// CreateLedgerEntry inserts a new row into the wallet_ledger table.
	// Every balance mutation (credit, debit, hold, release) MUST produce
	// a corresponding ledger entry for full auditability.
	//
	// Parameters:
	//   - ctx:   request-scoped context.
	//   - entry: the fully populated ledger entry to persist.
	//
	// Returns:
	//   - error: nil on success, or a wrapped DB error.
	CreateLedgerEntry(ctx context.Context, entry *models.WalletLedger) error
}
