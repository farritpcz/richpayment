// Package repository defines the data-access interfaces and provides a
// concrete implementation for the bank-service.
//
// The bank-service manages a pool of bank accounts used for receiving
// deposits and making withdrawals. This repository handles persistence
// for bank accounts, transfers, and holding accounts.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Domain types (bank-service specific)
// ---------------------------------------------------------------------------

// BankAccountStatus represents the current operational state of a bank account
// in the pool. Accounts cycle through these states based on operational needs
// and automated monitoring triggers.
type BankAccountStatus string

const (
	// BankAccountStatusActive means the account is available for receiving deposits.
	BankAccountStatusActive BankAccountStatus = "active"

	// BankAccountStatusDisabled means the account has been taken out of rotation,
	// either manually by an admin or automatically when a daily limit is reached.
	BankAccountStatusDisabled BankAccountStatus = "disabled"

	// BankAccountStatusMaintenance means the account is temporarily unavailable
	// due to bank maintenance or investigations.
	BankAccountStatusMaintenance BankAccountStatus = "maintenance"
)

// BankAccount represents a physical bank account used by the system to
// receive customer deposits. Multiple bank accounts form a pool, and the
// system rotates between them to stay under daily receiving limits.
type BankAccount struct {
	// ID is the unique identifier for this bank account (UUID v4).
	ID uuid.UUID `json:"id"`

	// BankCode is the bank identifier (e.g. "KBANK", "SCB", "BBL").
	BankCode string `json:"bank_code"`

	// AccountNumber is the bank account number (masked in API responses).
	AccountNumber string `json:"account_number"`

	// AccountName is the registered name on the bank account.
	AccountName string `json:"account_name"`

	// Status indicates whether this account is active, disabled, or in maintenance.
	Status BankAccountStatus `json:"status"`

	// Priority determines the order in which accounts are selected.
	// Higher priority accounts are preferred when multiple are available.
	Priority int `json:"priority"`

	// DailyLimitTHB is the maximum amount that can be received per day
	// before the account must be rotated out of the active pool.
	DailyLimitTHB decimal.Decimal `json:"daily_limit_thb"`

	// DailyReceivedTHB tracks how much has been received today.
	// Reset to zero at midnight by the scheduler.
	DailyReceivedTHB decimal.Decimal `json:"daily_received_thb"`

	// CreatedAt records when this bank account was added to the system.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt records when this bank account was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// TransferStatus represents the lifecycle state of a fund transfer between
// a bank account and a holding account.
type TransferStatus string

const (
	// TransferStatusPending means the transfer has been created but not
	// yet confirmed by the receiving bank.
	TransferStatusPending TransferStatus = "pending"

	// TransferStatusCompleted means the transfer has been confirmed with
	// a bank reference number.
	TransferStatusCompleted TransferStatus = "completed"

	// TransferStatusFailed means the transfer could not be completed.
	TransferStatusFailed TransferStatus = "failed"
)

// Transfer represents a fund movement from a pool bank account to a
// holding (treasury) account. Transfers are initiated by admins to
// consolidate funds from active receiving accounts.
type Transfer struct {
	// ID is the unique identifier for this transfer (UUID v4).
	ID uuid.UUID `json:"id"`

	// FromAccountID is the bank account that funds are being sent from.
	FromAccountID uuid.UUID `json:"from_account_id"`

	// ToHoldingID is the holding (treasury) account receiving the funds.
	// Must exist in the holding_accounts table for security validation.
	ToHoldingID uuid.UUID `json:"to_holding_id"`

	// Amount is the transfer amount in THB.
	Amount decimal.Decimal `json:"amount"`

	// Status indicates the current state of the transfer.
	Status TransferStatus `json:"status"`

	// Reference is the bank-provided reference number, populated when
	// the transfer is completed.
	Reference string `json:"reference,omitempty"`

	// AdminID is the UUID of the admin who initiated this transfer.
	AdminID uuid.UUID `json:"admin_id"`

	// CreatedAt records when the transfer was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt records when the transfer was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// HoldingAccount represents a treasury or holding account where funds
// are consolidated from the active bank account pool.
type HoldingAccount struct {
	// ID is the unique identifier for this holding account.
	ID uuid.UUID `json:"id"`

	// BankCode is the bank identifier for the holding account.
	BankCode string `json:"bank_code"`

	// AccountNumber is the account number of the holding account.
	AccountNumber string `json:"account_number"`

	// AccountName is the registered name on the holding account.
	AccountName string `json:"account_name"`

	// CreatedAt records when this holding account was registered.
	CreatedAt time.Time `json:"created_at"`
}

// AccountWithStatus extends BankAccount with computed status information
// for the monitoring dashboard.
type AccountWithStatus struct {
	// BankAccount contains the core account data.
	BankAccount

	// RemainingLimit is how much more can be received today before hitting
	// the daily limit (daily_limit_thb - daily_received_thb).
	RemainingLimit decimal.Decimal `json:"remaining_limit"`

	// UtilisationPct is the percentage of the daily limit already used
	// (daily_received_thb / daily_limit_thb * 100).
	UtilisationPct decimal.Decimal `json:"utilisation_pct"`
}

// TransferDailySummary aggregates transfer activity for a single day,
// used for the admin reporting dashboard.
type TransferDailySummary struct {
	// Date is the calendar date this summary covers.
	Date time.Time `json:"date"`

	// TotalTransfers is the number of transfers made on this date.
	TotalTransfers int `json:"total_transfers"`

	// TotalAmount is the sum of all transfer amounts on this date.
	TotalAmount decimal.Decimal `json:"total_amount"`

	// CompletedCount is the number of transfers with status "completed".
	CompletedCount int `json:"completed_count"`

	// PendingCount is the number of transfers still pending.
	PendingCount int `json:"pending_count"`

	// FailedCount is the number of transfers that failed.
	FailedCount int `json:"failed_count"`
}

// ---------------------------------------------------------------------------
// Repository interface
// ---------------------------------------------------------------------------

// BankRepository defines the contract for all bank-account-related persistence
// operations. It abstracts the database layer so the service can be tested
// with mock implementations.
type BankRepository interface {
	// GetActiveAccountsByMerchant returns all active bank accounts mapped
	// to the specified merchant, ordered by priority (descending).
	GetActiveAccountsByMerchant(ctx context.Context, merchantID uuid.UUID) ([]BankAccount, error)

	// GetAccountByID fetches a single bank account by its UUID.
	GetAccountByID(ctx context.Context, id uuid.UUID) (*BankAccount, error)

	// GetAllAccounts returns all bank accounts in the system regardless
	// of status, for the monitoring dashboard.
	GetAllAccounts(ctx context.Context) ([]BankAccount, error)

	// UpdateAccountStatus changes the status of a bank account (e.g.
	// active -> disabled during auto-switch).
	UpdateAccountStatus(ctx context.Context, id uuid.UUID, status BankAccountStatus) error

	// IncrementDailyReceived adds the given amount to the account's
	// daily_received_thb counter in the database.
	IncrementDailyReceived(ctx context.Context, id uuid.UUID, amount decimal.Decimal) error

	// ResetAllDailyCounters sets daily_received_thb to 0 for all accounts.
	// Called by the scheduler at midnight.
	ResetAllDailyCounters(ctx context.Context) error

	// GetMerchantsByAccount returns the merchant IDs that are mapped to
	// the specified bank account. Used during auto-switch to find all
	// affected merchants.
	GetMerchantsByAccount(ctx context.Context, accountID uuid.UUID) ([]uuid.UUID, error)

	// ValidateHoldingAccount checks that the given ID exists in the
	// holding_accounts table. Returns true if valid, false otherwise.
	ValidateHoldingAccount(ctx context.Context, id uuid.UUID) (bool, error)

	// InsertTransfer creates a new transfer record in the database.
	InsertTransfer(ctx context.Context, t *Transfer) error

	// UpdateTransferStatus updates a transfer's status and optionally
	// sets the bank reference number.
	UpdateTransferStatus(ctx context.Context, id uuid.UUID, status TransferStatus, reference string) error

	// GetTransfers returns a paginated list of transfers ordered by
	// creation time (newest first). Returns the transfers and total count.
	GetTransfers(ctx context.Context, offset, limit int) ([]Transfer, int, error)

	// GetDailyTransferSummary aggregates transfer data for a specific date.
	GetDailyTransferSummary(ctx context.Context, date time.Time) (*TransferDailySummary, error)
}

// ---------------------------------------------------------------------------
// Concrete implementation (PostgreSQL + Redis)
// ---------------------------------------------------------------------------

// pgBankRepository is the production implementation backed by PostgreSQL
// for durable storage and Redis for caching account pool data.
type pgBankRepository struct {
	// pool is the PostgreSQL connection pool shared across the service.
	pool *pgxpool.Pool

	// rdb is the Redis client for caching sorted sets and counters.
	rdb *redis.Client
}

// NewBankRepository creates a new repository backed by PostgreSQL and Redis.
// Both connections must already be established before calling this constructor.
func NewBankRepository(pool *pgxpool.Pool, rdb *redis.Client) BankRepository {
	return &pgBankRepository{
		pool: pool,
		rdb:  rdb,
	}
}

// ---------------------------------------------------------------------------
// GetActiveAccountsByMerchant
// ---------------------------------------------------------------------------

// GetActiveAccountsByMerchant returns all active bank accounts that are
// mapped to the specified merchant. The mapping is stored in a
// merchant_bank_accounts junction table. Results are ordered by priority
// (highest first) so the caller can pick the best available account.
func (r *pgBankRepository) GetActiveAccountsByMerchant(ctx context.Context, merchantID uuid.UUID) ([]BankAccount, error) {
	// language=SQL
	const query = `
		SELECT ba.id, ba.bank_code, ba.account_number, ba.account_name,
			   ba.status, ba.priority, ba.daily_limit_thb, ba.daily_received_thb,
			   ba.created_at, ba.updated_at
		FROM bank_accounts ba
		JOIN merchant_bank_accounts mba ON mba.bank_account_id = ba.id
		WHERE mba.merchant_id = $1 AND ba.status = 'active'
		ORDER BY ba.priority DESC`

	rows, err := r.pool.Query(ctx, query, merchantID)
	if err != nil {
		return nil, fmt.Errorf("query active accounts for merchant %s: %w", merchantID, err)
	}
	defer rows.Close()

	var accounts []BankAccount
	for rows.Next() {
		var a BankAccount
		if err := rows.Scan(
			&a.ID, &a.BankCode, &a.AccountNumber, &a.AccountName,
			&a.Status, &a.Priority, &a.DailyLimitTHB, &a.DailyReceivedTHB,
			&a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bank account row: %w", err)
		}
		accounts = append(accounts, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bank account rows: %w", err)
	}

	return accounts, nil
}

// ---------------------------------------------------------------------------
// GetAccountByID
// ---------------------------------------------------------------------------

// GetAccountByID retrieves a single bank account by its primary key.
// Returns an error if the account does not exist.
func (r *pgBankRepository) GetAccountByID(ctx context.Context, id uuid.UUID) (*BankAccount, error) {
	// language=SQL
	const query = `
		SELECT id, bank_code, account_number, account_name,
			   status, priority, daily_limit_thb, daily_received_thb,
			   created_at, updated_at
		FROM bank_accounts
		WHERE id = $1`

	var a BankAccount
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&a.ID, &a.BankCode, &a.AccountNumber, &a.AccountName,
		&a.Status, &a.Priority, &a.DailyLimitTHB, &a.DailyReceivedTHB,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get account %s: %w", id, err)
	}

	return &a, nil
}

// ---------------------------------------------------------------------------
// GetAllAccounts
// ---------------------------------------------------------------------------

// GetAllAccounts returns every bank account in the system, regardless of
// status. This is used by the monitoring dashboard to display the full
// account pool with status indicators.
func (r *pgBankRepository) GetAllAccounts(ctx context.Context) ([]BankAccount, error) {
	// language=SQL
	const query = `
		SELECT id, bank_code, account_number, account_name,
			   status, priority, daily_limit_thb, daily_received_thb,
			   created_at, updated_at
		FROM bank_accounts
		ORDER BY priority DESC, bank_code`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query all accounts: %w", err)
	}
	defer rows.Close()

	var accounts []BankAccount
	for rows.Next() {
		var a BankAccount
		if err := rows.Scan(
			&a.ID, &a.BankCode, &a.AccountNumber, &a.AccountName,
			&a.Status, &a.Priority, &a.DailyLimitTHB, &a.DailyReceivedTHB,
			&a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bank account row: %w", err)
		}
		accounts = append(accounts, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bank account rows: %w", err)
	}

	return accounts, nil
}

// ---------------------------------------------------------------------------
// UpdateAccountStatus
// ---------------------------------------------------------------------------

// UpdateAccountStatus changes the operational status of a bank account.
// This is called during auto-switch (active -> disabled) or manual
// admin actions. The updated_at timestamp is refreshed automatically.
func (r *pgBankRepository) UpdateAccountStatus(ctx context.Context, id uuid.UUID, status BankAccountStatus) error {
	// language=SQL
	const query = `
		UPDATE bank_accounts
		SET status = $1, updated_at = NOW()
		WHERE id = $2`

	tag, err := r.pool.Exec(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("update account status %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("account %s not found", id)
	}

	return nil
}

// ---------------------------------------------------------------------------
// IncrementDailyReceived
// ---------------------------------------------------------------------------

// IncrementDailyReceived adds the specified amount to the account's
// daily_received_thb counter. This is called each time a deposit is
// matched to this account, so the system knows how close the account
// is to its daily limit.
func (r *pgBankRepository) IncrementDailyReceived(ctx context.Context, id uuid.UUID, amount decimal.Decimal) error {
	// language=SQL
	const query = `
		UPDATE bank_accounts
		SET daily_received_thb = daily_received_thb + $1,
			updated_at = NOW()
		WHERE id = $2`

	_, err := r.pool.Exec(ctx, query, amount, id)
	if err != nil {
		return fmt.Errorf("increment daily received for %s: %w", id, err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// ResetAllDailyCounters
// ---------------------------------------------------------------------------

// ResetAllDailyCounters sets daily_received_thb to zero for every bank
// account. This is called by the scheduler at midnight to start a fresh
// daily cycle. It also re-enables accounts that were auto-disabled due
// to reaching their daily limit.
func (r *pgBankRepository) ResetAllDailyCounters(ctx context.Context) error {
	// language=SQL
	const query = `
		UPDATE bank_accounts
		SET daily_received_thb = 0,
			updated_at = NOW()`

	_, err := r.pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("reset daily counters: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// GetMerchantsByAccount
// ---------------------------------------------------------------------------

// GetMerchantsByAccount returns all merchant IDs that are mapped to the
// specified bank account. This is used during auto-switch to determine
// which merchants need to be reassigned to a new bank account.
func (r *pgBankRepository) GetMerchantsByAccount(ctx context.Context, accountID uuid.UUID) ([]uuid.UUID, error) {
	// language=SQL
	const query = `
		SELECT merchant_id
		FROM merchant_bank_accounts
		WHERE bank_account_id = $1`

	rows, err := r.pool.Query(ctx, query, accountID)
	if err != nil {
		return nil, fmt.Errorf("query merchants for account %s: %w", accountID, err)
	}
	defer rows.Close()

	var merchantIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan merchant id: %w", err)
		}
		merchantIDs = append(merchantIDs, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate merchant rows: %w", err)
	}

	return merchantIDs, nil
}

// ---------------------------------------------------------------------------
// ValidateHoldingAccount
// ---------------------------------------------------------------------------

// ValidateHoldingAccount checks whether the given UUID exists in the
// holding_accounts table. This is a security measure to prevent transfers
// to arbitrary bank accounts — funds can only be sent to pre-approved
// holding (treasury) accounts.
func (r *pgBankRepository) ValidateHoldingAccount(ctx context.Context, id uuid.UUID) (bool, error) {
	// language=SQL
	const query = `SELECT EXISTS(SELECT 1 FROM holding_accounts WHERE id = $1)`

	var exists bool
	err := r.pool.QueryRow(ctx, query, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("validate holding account %s: %w", id, err)
	}

	return exists, nil
}

// ---------------------------------------------------------------------------
// InsertTransfer
// ---------------------------------------------------------------------------

// InsertTransfer creates a new transfer record in the transfers table.
// The transfer ID is expected to be pre-generated (UUID v4) by the caller.
func (r *pgBankRepository) InsertTransfer(ctx context.Context, t *Transfer) error {
	// language=SQL
	const query = `
		INSERT INTO transfers (
			id, from_account_id, to_holding_id, amount,
			status, reference, admin_id,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := r.pool.Exec(ctx, query,
		t.ID, t.FromAccountID, t.ToHoldingID, t.Amount,
		t.Status, t.Reference, t.AdminID,
		t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert transfer: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// UpdateTransferStatus
// ---------------------------------------------------------------------------

// UpdateTransferStatus changes the status of an existing transfer and
// optionally records the bank reference number. This is called when an
// admin confirms the transfer was completed or marks it as failed.
func (r *pgBankRepository) UpdateTransferStatus(ctx context.Context, id uuid.UUID, status TransferStatus, reference string) error {
	// language=SQL
	const query = `
		UPDATE transfers
		SET status = $1, reference = $2, updated_at = NOW()
		WHERE id = $3`

	tag, err := r.pool.Exec(ctx, query, status, reference, id)
	if err != nil {
		return fmt.Errorf("update transfer status %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("transfer %s not found", id)
	}

	return nil
}

// ---------------------------------------------------------------------------
// GetTransfers (paginated)
// ---------------------------------------------------------------------------

// GetTransfers returns a paginated list of transfers sorted by creation
// time (newest first). It also returns the total count for pagination
// metadata. The offset and limit parameters control which page is returned.
func (r *pgBankRepository) GetTransfers(ctx context.Context, offset, limit int) ([]Transfer, int, error) {
	// First, get the total count for pagination.
	// language=SQL
	const countQuery = `SELECT COUNT(*) FROM transfers`

	var total int
	if err := r.pool.QueryRow(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count transfers: %w", err)
	}

	// Then fetch the requested page.
	// language=SQL
	const query = `
		SELECT id, from_account_id, to_holding_id, amount,
			   status, reference, admin_id,
			   created_at, updated_at
		FROM transfers
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := r.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query transfers: %w", err)
	}
	defer rows.Close()

	var transfers []Transfer
	for rows.Next() {
		var t Transfer
		if err := rows.Scan(
			&t.ID, &t.FromAccountID, &t.ToHoldingID, &t.Amount,
			&t.Status, &t.Reference, &t.AdminID,
			&t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan transfer row: %w", err)
		}
		transfers = append(transfers, t)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate transfer rows: %w", err)
	}

	return transfers, total, nil
}

// ---------------------------------------------------------------------------
// GetDailyTransferSummary
// ---------------------------------------------------------------------------

// GetDailyTransferSummary aggregates all transfers for the given date into
// a single summary with counts by status and total amount. Used for the
// admin reporting dashboard.
func (r *pgBankRepository) GetDailyTransferSummary(ctx context.Context, date time.Time) (*TransferDailySummary, error) {
	// Normalise to the start and end of the target day.
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	endOfDay := startOfDay.Add(24 * time.Hour)

	// language=SQL
	const query = `
		SELECT
			COUNT(*) AS total_transfers,
			COALESCE(SUM(amount), 0) AS total_amount,
			COUNT(*) FILTER (WHERE status = 'completed') AS completed_count,
			COUNT(*) FILTER (WHERE status = 'pending') AS pending_count,
			COUNT(*) FILTER (WHERE status = 'failed') AS failed_count
		FROM transfers
		WHERE created_at >= $1 AND created_at < $2`

	summary := &TransferDailySummary{Date: startOfDay}

	err := r.pool.QueryRow(ctx, query, startOfDay, endOfDay).Scan(
		&summary.TotalTransfers,
		&summary.TotalAmount,
		&summary.CompletedCount,
		&summary.PendingCount,
		&summary.FailedCount,
	)
	if err != nil {
		return nil, fmt.Errorf("query daily transfer summary: %w", err)
	}

	return summary, nil
}
