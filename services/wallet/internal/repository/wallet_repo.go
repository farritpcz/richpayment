// Package repository defines the data-access interfaces and implementations
// for the wallet service. All database interactions are abstracted behind
// the WalletRepository interface so the service layer never depends on a
// concrete database driver.
//
// This file declares the interface, sentinel errors, and the Tx type alias
// used to pass PostgreSQL transactions through the service layer without
// leaking pgx types into the domain.
package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/farritpcz/richpayment/pkg/models"
)

// -------------------------------------------------------------------------
// Sentinel errors
// -------------------------------------------------------------------------

// ErrVersionConflict is returned when an optimistic-locking update fails
// because the row's version column no longer matches the expected value.
// Callers (typically the service layer) should catch this error and decide
// whether to retry the operation.
//
// NOTE: With the new transactional flow (SELECT FOR UPDATE), version
// conflicts should be extremely rare because the row-level lock prevents
// concurrent writers. However, the optimistic check is retained as a
// defense-in-depth safety net.
var ErrVersionConflict = errors.New("wallet version conflict: row was modified by another transaction")

// ErrWalletNotFound is returned when no wallet row matches the query
// criteria (either by ID or by owner+currency composite key).
var ErrWalletNotFound = errors.New("wallet not found")

// ErrDuplicateReference is returned when a ledger entry with the same
// reference_id already exists. This is used for idempotency checking:
// if a credit/debit/hold/release has already been processed for a given
// reference, we return this error so the service can skip re-processing
// and return success without creating a duplicate entry.
var ErrDuplicateReference = errors.New("ledger entry with this reference_id already exists")

// -------------------------------------------------------------------------
// Tx type alias
// -------------------------------------------------------------------------

// Tx is a type alias for pgx.Tx, exposed so that the service layer can
// pass transactions around without importing pgx directly. This keeps the
// service layer loosely coupled to the database driver while still enabling
// transactional operations that span multiple repository calls.
type Tx = pgx.Tx

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
//
// The interface now includes transactional methods that enable the service
// layer to execute multi-step wallet operations (read + update + ledger)
// inside a single database transaction with row-level locking, eliminating
// the TOCTOU race condition that existed in the previous optimistic-locking
// approach.
type WalletRepository interface {
	// -----------------------------------------------------------------
	// Read operations (non-transactional)
	// -----------------------------------------------------------------

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
	// This is used for non-critical reads (e.g. balance inquiries) where
	// row-level locking is not needed.
	//
	// Parameters:
	//   - ctx: request-scoped context.
	//   - id:  the wallet's primary-key UUID.
	//
	// Returns:
	//   - *models.Wallet: the wallet record, or nil on error.
	//   - error:          ErrWalletNotFound if no match, or a wrapped DB error.
	GetByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)

	// -----------------------------------------------------------------
	// Write operations (non-transactional, legacy)
	// -----------------------------------------------------------------

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
	// NOTE: This method is retained for backward compatibility. New code
	// should prefer UpdateBalanceInTx which operates within an explicit
	// transaction and benefits from the FOR UPDATE row lock.
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
	// NOTE: New code should prefer CreateLedgerEntryInTx to ensure the
	// ledger entry is created atomically with the balance update.
	//
	// Parameters:
	//   - ctx:   request-scoped context.
	//   - entry: the fully populated ledger entry to persist.
	//
	// Returns:
	//   - error: nil on success, or a wrapped DB error.
	CreateLedgerEntry(ctx context.Context, entry *models.WalletLedger) error

	// -----------------------------------------------------------------
	// Transaction management
	// -----------------------------------------------------------------

	// BeginTx starts a new PostgreSQL transaction and returns a Tx handle.
	// The caller is responsible for calling tx.Commit() on success or
	// tx.Rollback() on failure. The service layer uses this to wrap
	// multi-step wallet operations (read-for-update + balance-update +
	// ledger-insert) in a single atomic transaction.
	//
	// Parameters:
	//   - ctx: request-scoped context; the transaction inherits this context's
	//          deadline and cancellation.
	//
	// Returns:
	//   - Tx:    a transaction handle that can be passed to *InTx methods.
	//   - error: a wrapped DB error if the transaction could not be started.
	BeginTx(ctx context.Context) (Tx, error)

	// -----------------------------------------------------------------
	// Transactional read operations (with row-level locking)
	// -----------------------------------------------------------------

	// GetByIDForUpdate retrieves a wallet by its primary key within an
	// existing transaction, using SELECT ... FOR UPDATE to acquire a
	// PostgreSQL row-level exclusive lock. This lock is held until the
	// transaction commits or rolls back, preventing any other transaction
	// from reading (with FOR UPDATE) or modifying the same row.
	//
	// This is the PRIMARY defense against the TOCTOU race condition:
	// by locking the row at read time, no other transaction can modify
	// the balance between our read and our subsequent update.
	//
	// Parameters:
	//   - ctx: request-scoped context.
	//   - tx:  an active transaction obtained from BeginTx.
	//   - id:  the wallet's primary-key UUID.
	//
	// Returns:
	//   - *models.Wallet: the wallet record with an exclusive row lock held.
	//   - error:          ErrWalletNotFound if no match, or a wrapped DB error.
	GetByIDForUpdate(ctx context.Context, tx Tx, id uuid.UUID) (*models.Wallet, error)

	// -----------------------------------------------------------------
	// Transactional write operations
	// -----------------------------------------------------------------

	// UpdateBalanceInTx performs an optimistic-locking update on a wallet's
	// balance and hold_balance columns within an existing transaction.
	// Although the FOR UPDATE lock makes version conflicts nearly
	// impossible, the version check is retained as defense-in-depth.
	//
	// Parameters:
	//   - ctx:             request-scoped context.
	//   - tx:              an active transaction obtained from BeginTx.
	//   - id:              the wallet's primary-key UUID.
	//   - newBalance:      the new balance value as a decimal string.
	//   - newHold:         the new hold_balance value as a decimal string.
	//   - expectedVersion: the version read by GetByIDForUpdate.
	//
	// Returns:
	//   - error: ErrVersionConflict on mismatch (should be extremely rare
	//            when used with FOR UPDATE), or a wrapped DB error.
	UpdateBalanceInTx(ctx context.Context, tx Tx, id uuid.UUID, newBalance, newHold string, expectedVersion int64) error

	// CreateLedgerEntryInTx inserts a ledger entry within an existing
	// transaction. This ensures the ledger row is committed atomically
	// with the balance update — if either fails, both are rolled back.
	//
	// Parameters:
	//   - ctx:   request-scoped context.
	//   - tx:    an active transaction obtained from BeginTx.
	//   - entry: the fully populated ledger entry to persist.
	//
	// Returns:
	//   - error: nil on success, or a wrapped DB error.
	CreateLedgerEntryInTx(ctx context.Context, tx Tx, entry *models.WalletLedger) error

	// -----------------------------------------------------------------
	// Idempotency check
	// -----------------------------------------------------------------

	// LedgerEntryExistsByRef checks whether a ledger entry with the given
	// reference_id already exists in the wallet_ledger table. This is used
	// for idempotency: before processing a credit/debit/hold/release, the
	// service checks if the operation was already performed for this
	// reference. If it was, the service returns success without
	// re-processing, preventing duplicate balance mutations.
	//
	// Parameters:
	//   - ctx:   request-scoped context.
	//   - refID: the reference_id (UUID) to check for in the ledger.
	//
	// Returns:
	//   - bool:  true if a ledger entry with this reference_id exists.
	//   - error: a wrapped DB error on failure.
	LedgerEntryExistsByRef(ctx context.Context, refID uuid.UUID) (bool, error)
}
