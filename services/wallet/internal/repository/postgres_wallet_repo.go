// Package repository (postgres_wallet_repo.go) provides the PostgreSQL-backed
// implementation of the WalletRepository interface. It uses pgxpool for
// connection pooling and supports both non-transactional (legacy) and
// transactional operations with SELECT ... FOR UPDATE row-level locking
// to prevent TOCTOU race conditions in concurrent balance mutations.
//
// CONCURRENCY MODEL:
// The old approach used optimistic locking (version column) which suffered
// from a TOCTOU vulnerability: two concurrent requests could both read the
// same balance, both pass the "sufficient funds" check, and both subtract
// funds — allowing a user to withdraw more than their balance.
//
// The new approach uses:
//   1. PostgreSQL SELECT ... FOR UPDATE — acquires an exclusive row lock at
//      read time, preventing any other transaction from modifying the row
//      until the current transaction commits or rolls back.
//   2. Explicit transactions (BeginTx) — the entire read-check-update-ledger
//      flow executes inside a single transaction, ensuring atomicity.
//   3. Version column (retained) — defense-in-depth; even with row locks,
//      the version check catches any unexpected concurrent modification.
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
// GetByOwner (non-transactional)
// -------------------------------------------------------------------------

// GetByOwner looks up a wallet by its composite natural key
// (owner_type, owner_id, currency). This is the primary lookup path used
// by external callers who only know the business entity, not the wallet UUID.
//
// This method does NOT acquire a row lock. It is suitable for read-only
// operations like balance inquiries. For balance-modifying operations, use
// GetByIDForUpdate within a transaction instead.
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
	// No FOR UPDATE here because this is a read-only lookup.
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
	// decimal.NewFromString never fails for valid NUMERIC output, but we
	// ignore the error to keep the code concise (database output is trusted).
	w.Balance, _ = decimal.NewFromString(balanceStr)
	w.HoldBalance, _ = decimal.NewFromString(holdStr)

	return &w, nil
}

// -------------------------------------------------------------------------
// GetByID (non-transactional)
// -------------------------------------------------------------------------

// GetByID retrieves a wallet by its primary-key UUID. This is used
// internally when the service already knows the wallet ID (e.g. during
// read-only balance inquiries).
//
// This method does NOT acquire a row lock. For balance-modifying operations,
// use GetByIDForUpdate within a transaction instead.
//
// Parameters:
//   - ctx: request-scoped context.
//   - id:  the wallet's UUID primary key.
//
// Returns:
//   - *models.Wallet: the matched wallet record.
//   - error:          ErrWalletNotFound if no row matches, or a wrapped DB error.
func (r *PostgresWalletRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	// SQL: select all wallet columns by primary key. No row lock.
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
// Create (non-transactional)
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
// UpdateBalance (non-transactional, legacy)
// -------------------------------------------------------------------------

// UpdateBalance performs an optimistic-locking update on a wallet's balance
// and hold_balance columns WITHOUT an explicit transaction. This is the
// legacy method retained for backward compatibility.
//
// NOTE: New code should use UpdateBalanceInTx inside a transaction started
// by BeginTx, combined with GetByIDForUpdate, to prevent TOCTOU races.
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
// CreateLedgerEntry (non-transactional, legacy)
// -------------------------------------------------------------------------

// CreateLedgerEntry inserts a row into the wallet_ledger table WITHOUT an
// explicit transaction. This is the legacy method retained for backward
// compatibility.
//
// NOTE: New code should use CreateLedgerEntryInTx to ensure the ledger
// entry is committed atomically with the balance update.
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

// =========================================================================
// NEW TRANSACTIONAL METHODS — these methods eliminate the TOCTOU race
// condition by operating within explicit PostgreSQL transactions with
// row-level locking (SELECT ... FOR UPDATE).
// =========================================================================

// -------------------------------------------------------------------------
// BeginTx — start a new transaction
// -------------------------------------------------------------------------

// BeginTx starts a new PostgreSQL transaction using the connection pool.
// The transaction uses the default isolation level (READ COMMITTED) which
// is sufficient because SELECT ... FOR UPDATE provides the necessary
// serialisation guarantees at the row level.
//
// The caller MUST either commit (tx.Commit) or rollback (tx.Rollback) the
// returned transaction. A common pattern is:
//
//	tx, err := repo.BeginTx(ctx)
//	if err != nil { return err }
//	defer tx.Rollback(ctx) // no-op if already committed
//	// ... do work ...
//	return tx.Commit(ctx)
//
// Parameters:
//   - ctx: request-scoped context; the transaction inherits this context's
//          deadline and cancellation. If the context is cancelled, the
//          transaction is automatically rolled back by pgx.
//
// Returns:
//   - Tx:    a pgx.Tx handle that can be passed to *InTx and *ForUpdate methods.
//   - error: a wrapped DB error if the transaction could not be started
//            (e.g. pool exhausted, connection failure).
func (r *PostgresWalletRepo) BeginTx(ctx context.Context) (Tx, error) {
	// Begin a new transaction from the pool. pgx automatically acquires
	// a connection from the pool and holds it until Commit or Rollback.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return tx, nil
}

// -------------------------------------------------------------------------
// GetByIDForUpdate — row-level exclusive lock
// -------------------------------------------------------------------------

// GetByIDForUpdate retrieves a wallet by its primary key within an existing
// transaction, using SELECT ... FOR UPDATE to acquire a PostgreSQL row-level
// exclusive lock. This is the PRIMARY defense against the TOCTOU race.
//
// HOW IT PREVENTS THE RACE CONDITION:
//
//	Time    Tx A                          Tx B
//	----    ----                          ----
//	T1      SELECT ... FOR UPDATE         (blocked, waiting for lock)
//	T2      Check balance: 1000 >= 800
//	T3      UPDATE balance = 200
//	T4      COMMIT (lock released)
//	T5                                    SELECT ... FOR UPDATE (now unblocked)
//	T6                                    Check balance: 200 < 800 → FAIL
//
// Without FOR UPDATE, both Tx A and Tx B would read balance=1000 at T1,
// both would pass the check, and both would subtract 800, leaving -600.
//
// The lock is automatically released when the transaction commits or rolls
// back. There is no risk of deadlock as long as each transaction locks at
// most one wallet row (which is always the case in our wallet operations).
//
// Parameters:
//   - ctx: request-scoped context.
//   - tx:  an active transaction obtained from BeginTx. The FOR UPDATE lock
//          is tied to this transaction's lifetime.
//   - id:  the wallet's primary-key UUID.
//
// Returns:
//   - *models.Wallet: the wallet record with an exclusive row lock held.
//                     The lock prevents any other FOR UPDATE reader or
//                     UPDATE/DELETE from touching this row until we commit.
//   - error:          ErrWalletNotFound if no row matches, or a wrapped DB error.
func (r *PostgresWalletRepo) GetByIDForUpdate(ctx context.Context, tx Tx, id uuid.UUID) (*models.Wallet, error) {
	// SQL: SELECT ... FOR UPDATE acquires a row-level exclusive lock.
	// Any other transaction that tries to SELECT ... FOR UPDATE or UPDATE
	// the same row will block until this transaction completes.
	//
	// We select all columns because the service layer needs the current
	// balance, hold_balance, and version to compute new values and perform
	// the defense-in-depth version check.
	const query = `
		SELECT id, owner_type, owner_id, currency, balance, hold_balance, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
		FOR UPDATE
	`

	var w models.Wallet
	var balanceStr, holdStr string

	// Execute the query within the provided transaction.
	// This is critical — using r.pool directly would bypass the transaction
	// and the FOR UPDATE lock would not be held.
	err := tx.QueryRow(ctx, query, id).Scan(
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
		return nil, fmt.Errorf("query wallet by id for update: %w", err)
	}

	// Convert NUMERIC string representations to decimal.Decimal.
	w.Balance, _ = decimal.NewFromString(balanceStr)
	w.HoldBalance, _ = decimal.NewFromString(holdStr)

	return &w, nil
}

// -------------------------------------------------------------------------
// UpdateBalanceInTx — update balance within a transaction
// -------------------------------------------------------------------------

// UpdateBalanceInTx performs a version-guarded update on a wallet's balance
// and hold_balance columns within an existing transaction. The version
// column is bumped atomically in the UPDATE statement itself.
//
// Because this method is always used after GetByIDForUpdate (which holds a
// row-level exclusive lock), version conflicts should be extremely rare.
// The version check is retained as defense-in-depth: if a conflict somehow
// occurs, it means our locking assumptions are broken and we should fail
// loudly rather than corrupt data.
//
// Parameters:
//   - ctx:             request-scoped context.
//   - tx:              an active transaction obtained from BeginTx. The UPDATE
//                      executes within this transaction.
//   - id:              the wallet's primary-key UUID.
//   - newBalance:      the new balance value as a decimal string.
//   - newHold:         the new hold_balance value as a decimal string.
//   - expectedVersion: the version read by GetByIDForUpdate in the same
//                      transaction. Should always match because of the lock.
//
// Returns:
//   - error: ErrVersionConflict if the version guard fails (should never
//            happen with correct FOR UPDATE usage), or a wrapped DB error.
func (r *PostgresWalletRepo) UpdateBalanceInTx(ctx context.Context, tx Tx, id uuid.UUID, newBalance, newHold string, expectedVersion int64) error {
	// SQL: conditional update guarded by the version column.
	// The version is bumped in the same statement to guarantee atomicity.
	// This is identical to UpdateBalance but uses the transaction connection.
	const query = `
		UPDATE wallets
		SET balance      = $1,
		    hold_balance = $2,
		    version      = version + 1,
		    updated_at   = now()
		WHERE id = $3 AND version = $4
	`

	// Execute within the transaction so the update is part of the same
	// atomic unit as the SELECT ... FOR UPDATE and the ledger insert.
	cmdTag, err := tx.Exec(ctx, query, newBalance, newHold, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("update wallet balance in tx: %w", err)
	}

	// Zero rows affected means the version has changed. This should be
	// nearly impossible when used with FOR UPDATE, but we check anyway
	// as a safety net.
	if cmdTag.RowsAffected() == 0 {
		return ErrVersionConflict
	}

	return nil
}

// -------------------------------------------------------------------------
// CreateLedgerEntryInTx — insert ledger entry within a transaction
// -------------------------------------------------------------------------

// CreateLedgerEntryInTx inserts a row into the wallet_ledger table within
// an existing transaction. By executing the ledger insert in the same
// transaction as the balance update, we guarantee that:
//
//   - If the ledger insert fails, the balance update is rolled back.
//   - If the balance update fails, no orphaned ledger entry is created.
//   - The wallet balance and its audit trail are always consistent.
//
// This fixes the bug in the old non-transactional flow where the balance
// could be updated but the ledger entry creation could fail, leaving the
// system in an inconsistent state.
//
// Parameters:
//   - ctx:   request-scoped context.
//   - tx:    an active transaction obtained from BeginTx.
//   - entry: the fully populated ledger entry; the ID field is auto-generated
//            by the database (BIGSERIAL) and does not need to be set.
//
// Returns:
//   - error: nil on success, or a wrapped DB error.
func (r *PostgresWalletRepo) CreateLedgerEntryInTx(ctx context.Context, tx Tx, entry *models.WalletLedger) error {
	// SQL: insert a new ledger row within the transaction.
	// The id column is auto-incremented by the database.
	const query = `
		INSERT INTO wallet_ledger (wallet_id, entry_type, reference_type, reference_id, amount, balance_after, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	// Execute within the transaction to maintain atomicity with the
	// balance update in UpdateBalanceInTx.
	_, err := tx.Exec(ctx, query,
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
		return fmt.Errorf("create ledger entry in tx: %w", err)
	}

	return nil
}

// -------------------------------------------------------------------------
// LedgerEntryExistsByRef — idempotency check
// -------------------------------------------------------------------------

// LedgerEntryExistsByRef checks whether a ledger entry with the given
// reference_id already exists. This enables idempotent processing of
// wallet operations: if a client retries a request (e.g. due to a network
// timeout), we detect the duplicate by reference_id and return success
// without re-processing, preventing double-crediting or double-debiting.
//
// The query uses a simple EXISTS subquery which is very efficient — it
// short-circuits as soon as one matching row is found.
//
// Parameters:
//   - ctx:   request-scoped context.
//   - refID: the reference_id (UUID) to check for in the wallet_ledger table.
//
// Returns:
//   - bool:  true if at least one ledger entry with this reference_id exists.
//   - error: a wrapped DB error on failure.
func (r *PostgresWalletRepo) LedgerEntryExistsByRef(ctx context.Context, refID uuid.UUID) (bool, error) {
	// SQL: EXISTS is optimal for "does it exist?" checks because the
	// database stops scanning as soon as it finds one match. Much faster
	// than COUNT(*) which must scan all matching rows.
	const query = `
		SELECT EXISTS(
			SELECT 1 FROM wallet_ledger WHERE reference_id = $1
		)
	`

	var exists bool
	err := r.pool.QueryRow(ctx, query, refID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check ledger entry exists by ref: %w", err)
	}

	return exists, nil
}
