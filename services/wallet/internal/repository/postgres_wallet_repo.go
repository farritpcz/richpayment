// Package repository (postgres_wallet_repo.go) provides the PostgreSQL-backed
// implementation of the WalletRepository interface. It uses pgxpool for
// connection pooling and leverages optimistic locking via a version column
// to guarantee safe concurrent balance mutations without explicit row locks.
package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
)

// -------------------------------------------------------------------------
// PostgresWalletRepo – concrete implementation
// -------------------------------------------------------------------------

// PostgresWalletRepo implements WalletRepository using a PostgreSQL
// connection pool. All queries use parameterised statements to prevent SQL
// injection. The struct is safe for concurrent use because pgxpool.Pool
// handles connection multiplexing internally.
type PostgresWalletRepo struct {
	// pool is the pgx connection pool shared across all repository calls.
	// It is created once at application startup and closed on shutdown.
	pool *pgxpool.Pool
}

// NewPostgresWalletRepo constructs a new PostgresWalletRepo. The caller
// owns the pool's lifecycle and is responsible for calling pool.Close()
// when the application shuts down.
//
// Parameters:
//   - pool: an already-initialised pgxpool.Pool connected to the target database.
//
// Returns:
//   - *PostgresWalletRepo: a ready-to-use repository instance.
func NewPostgresWalletRepo(pool *pgxpool.Pool) *PostgresWalletRepo {
	return &PostgresWalletRepo{pool: pool}
}

// -------------------------------------------------------------------------
// GetByOwner
// -------------------------------------------------------------------------

// GetByOwner looks up a wallet by its composite natural key
// (owner_type, owner_id, currency). This is the primary lookup path used
// by external callers who only know the business entity, not the wallet UUID.
//
// Parameters:
//   - ctx:       request-scoped context for cancellation and deadline propagation.
//   - ownerType: the category of wallet owner (merchant, agent, partner, system).
//   - ownerID:   the UUID that identifies the owning entity.
//   - currency:  ISO 4217 currency code, e.g. "THB".
//
// Returns:
//   - *models.Wallet: the matched wallet record.
//   - error:          ErrWalletNotFound if no row matches, or a wrapped DB error.
func (r *PostgresWalletRepo) GetByOwner(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, currency string) (*models.Wallet, error) {
	// SQL: select all wallet columns filtered by the composite natural key.
	const query = `
		SELECT id, owner_type, owner_id, currency, balance, hold_balance, version, created_at, updated_at
		FROM wallets
		WHERE owner_type = $1 AND owner_id = $2 AND currency = $3
	`

	// w will hold the scanned result.
	var w models.Wallet

	// balanceStr and holdStr are intermediate string representations that
	// we parse into decimal.Decimal after scanning, because pgx returns
	// NUMERIC columns as strings.
	var balanceStr, holdStr string

	err := r.pool.QueryRow(ctx, query, string(ownerType), ownerID, currency).Scan(
		&w.ID,
		&w.OwnerType,
		&w.OwnerID,
		&w.Currency,
		&balanceStr,
		&holdStr,
		&w.Version,
		&w.CreatedAt,
		&w.UpdatedAt,
	)
	if err != nil {
		// pgx.ErrNoRows means the wallet does not exist yet for this owner/currency pair.
		if err == pgx.ErrNoRows {
			return nil, ErrWalletNotFound
		}
		return nil, fmt.Errorf("query wallet by owner: %w", err)
	}

	// Parse the string representations into high-precision decimals.
	w.Balance, _ = decimal.NewFromString(balanceStr)
	w.HoldBalance, _ = decimal.NewFromString(holdStr)

	return &w, nil
}

// -------------------------------------------------------------------------
// GetByID
// -------------------------------------------------------------------------

// GetByID retrieves a wallet by its primary-key UUID. This is used
// internally when the service already knows the wallet ID (e.g. during
// credit/debit operations that receive the wallet ID directly).
//
// Parameters:
//   - ctx: request-scoped context.
//   - id:  the wallet's UUID primary key.
//
// Returns:
//   - *models.Wallet: the matched wallet record.
//   - error:          ErrWalletNotFound if no row matches, or a wrapped DB error.
func (r *PostgresWalletRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	// SQL: select all wallet columns by primary key.
	const query = `
		SELECT id, owner_type, owner_id, currency, balance, hold_balance, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
	`

	var w models.Wallet
	var balanceStr, holdStr string

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&w.ID,
		&w.OwnerType,
		&w.OwnerID,
		&w.Currency,
		&balanceStr,
		&holdStr,
		&w.Version,
		&w.CreatedAt,
		&w.UpdatedAt,
	)
	if err != nil {
		// Map pgx's "no rows" sentinel to our domain-specific error.
		if err == pgx.ErrNoRows {
			return nil, ErrWalletNotFound
		}
		return nil, fmt.Errorf("query wallet by id: %w", err)
	}

	// Convert NUMERIC string representations to decimal.Decimal.
	w.Balance, _ = decimal.NewFromString(balanceStr)
	w.HoldBalance, _ = decimal.NewFromString(holdStr)

	return &w, nil
}

// -------------------------------------------------------------------------
// Create
// -------------------------------------------------------------------------

// Create inserts a new wallet row into the wallets table. If a wallet
// with the same (owner_type, owner_id, currency) composite key already
// exists, the ON CONFLICT DO NOTHING clause ensures idempotency: the
// existing row is left untouched and no error is returned.
//
// This design means the caller can safely call Create in a
// "ensure-exists" pattern without worrying about race conditions between
// concurrent requests for the same owner+currency.
//
// Parameters:
//   - ctx:    request-scoped context.
//   - wallet: the wallet record to insert; all fields must be populated.
//
// Returns:
//   - error: nil on success (including a no-op conflict), or a wrapped DB error.
func (r *PostgresWalletRepo) Create(ctx context.Context, wallet *models.Wallet) error {
	// SQL: insert with conflict-safe upsert guard on the natural key.
	// The unique constraint on (owner_type, owner_id, currency) must exist
	// in the database schema for ON CONFLICT to work correctly.
	const query = `
		INSERT INTO wallets (id, owner_type, owner_id, currency, balance, hold_balance, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (owner_type, owner_id, currency) DO NOTHING
	`

	_, err := r.pool.Exec(ctx, query,
		wallet.ID,
		string(wallet.OwnerType),
		wallet.OwnerID,
		wallet.Currency,
		wallet.Balance.String(),
		wallet.HoldBalance.String(),
		wallet.Version,
		wallet.CreatedAt,
		wallet.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create wallet: %w", err)
	}

	return nil
}

// -------------------------------------------------------------------------
// UpdateBalance
// -------------------------------------------------------------------------

// UpdateBalance performs an optimistic-locking update on a wallet's balance
// and hold_balance columns. The UPDATE statement includes a WHERE clause
// that checks both the wallet ID and the expected version number. If
// another transaction has already bumped the version, zero rows are
// affected and the method returns ErrVersionConflict.
//
// The version column is incremented atomically inside the UPDATE itself
// (version = version + 1) so that the bump and the balance change form
// a single atomic operation.
//
// Parameters:
//   - ctx:             request-scoped context.
//   - id:              the wallet's UUID primary key.
//   - newBalance:      the new balance value as a decimal string.
//   - newHold:         the new hold_balance value as a decimal string.
//   - expectedVersion: the version the caller read; if the DB version differs,
//                      the update is rejected to prevent lost-update anomalies.
//
// Returns:
//   - error: ErrVersionConflict if the version guard fails,
//            or a wrapped DB error on other failures.
func (r *PostgresWalletRepo) UpdateBalance(ctx context.Context, id uuid.UUID, newBalance, newHold string, expectedVersion int64) error {
	// SQL: conditional update guarded by the version column.
	// The version is bumped in the same statement to guarantee atomicity.
	const query = `
		UPDATE wallets
		SET balance      = $1,
		    hold_balance = $2,
		    version      = version + 1,
		    updated_at   = now()
		WHERE id = $3 AND version = $4
	`

	// Execute the update and inspect how many rows were affected.
	cmdTag, err := r.pool.Exec(ctx, query, newBalance, newHold, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("update wallet balance: %w", err)
	}

	// If zero rows were affected, the version in the database no longer
	// matches expectedVersion, meaning another process modified the wallet
	// between our read and this write. The caller should retry.
	if cmdTag.RowsAffected() == 0 {
		return ErrVersionConflict
	}

	return nil
}

// -------------------------------------------------------------------------
// CreateLedgerEntry
// -------------------------------------------------------------------------

// CreateLedgerEntry inserts a row into the wallet_ledger table. Every
// balance mutation (credit, debit, hold, release) MUST create a ledger
// entry so that the full history of a wallet's balance changes is
// auditable. The ledger is append-only: entries are never updated or
// deleted.
//
// Parameters:
//   - ctx:   request-scoped context.
//   - entry: the fully populated ledger entry; the ID field is auto-generated
//            by the database (BIGSERIAL) and does not need to be set.
//
// Returns:
//   - error: nil on success, or a wrapped DB error.
func (r *PostgresWalletRepo) CreateLedgerEntry(ctx context.Context, entry *models.WalletLedger) error {
	// SQL: insert a new ledger row. The id column is auto-incremented by
	// the database, so we omit it from the INSERT column list.
	const query = `
		INSERT INTO wallet_ledger (wallet_id, entry_type, reference_type, reference_id, amount, balance_after, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.pool.Exec(ctx, query,
		entry.WalletID,
		string(entry.EntryType),
		entry.ReferenceType,
		entry.ReferenceID,
		entry.Amount.String(),
		entry.BalanceAfter.String(),
		entry.Description,
		entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create ledger entry: %w", err)
	}

	return nil
}
