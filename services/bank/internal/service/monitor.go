// Package service contains the core business logic for the bank-service.
//
// This file implements the balance monitoring subsystem, which tracks
// how much each bank account has received during the current day, checks
// whether accounts have hit their daily limits, and provides status
// information for the admin monitoring dashboard.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/services/bank/internal/repository"
)

// ---------------------------------------------------------------------------
// Monitor service
// ---------------------------------------------------------------------------

// Monitor provides real-time visibility into the bank account pool's health.
// It tracks daily receiving volumes per account, triggers auto-switches
// when limits are reached, and exposes status data for the dashboard.
type Monitor struct {
	// repo provides access to bank account data in PostgreSQL.
	repo repository.BankRepository

	// rdb is the Redis client used for fast daily counter increments.
	// Redis counters mirror the database values for read performance.
	rdb *redis.Client

	// pool is the Pool service, used to trigger auto-switch when an
	// account reaches its daily limit.
	pool *Pool

	// log is the structured logger for monitoring events.
	log *slog.Logger
}

// NewMonitor creates a new Monitor service with all required dependencies.
// The pool parameter is used to trigger auto-switching when limits are hit.
func NewMonitor(repo repository.BankRepository, rdb *redis.Client, pool *Pool, log *slog.Logger) *Monitor {
	return &Monitor{
		repo: repo,
		rdb:  rdb,
		pool: pool,
		log:  log,
	}
}

// ---------------------------------------------------------------------------
// GetAccountStatus — fetch a single account's current status
// ---------------------------------------------------------------------------

// GetAccountStatus retrieves the current status of a specific bank account,
// including its daily received amount and remaining capacity. This is used
// by the admin dashboard for detailed account inspection.
//
// Returns an AccountWithStatus that includes computed fields like
// remaining_limit and utilisation_pct that are not stored in the database.
func (m *Monitor) GetAccountStatus(ctx context.Context, bankAccountID uuid.UUID) (*repository.AccountWithStatus, error) {
	// Fetch the raw account data from the database.
	account, err := m.repo.GetAccountByID(ctx, bankAccountID)
	if err != nil {
		return nil, fmt.Errorf("get account status: %w", err)
	}

	// Compute the derived status fields.
	status := m.buildAccountStatus(account)
	return status, nil
}

// ---------------------------------------------------------------------------
// GetAllAccounts — fetch all accounts with computed status fields
// ---------------------------------------------------------------------------

// GetAllAccounts returns every bank account in the system with computed
// status fields (remaining capacity, utilisation percentage). This powers
// the monitoring dashboard's overview table.
//
// The function fetches all accounts from the database in a single query,
// then computes the derived fields in-memory for each account.
func (m *Monitor) GetAllAccounts(ctx context.Context) ([]repository.AccountWithStatus, error) {
	// Fetch all accounts from the database.
	accounts, err := m.repo.GetAllAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all accounts: %w", err)
	}

	// Build the status response for each account.
	result := make([]repository.AccountWithStatus, 0, len(accounts))
	for i := range accounts {
		status := m.buildAccountStatus(&accounts[i])
		result = append(result, *status)
	}

	return result, nil
}

// buildAccountStatus computes the derived status fields for a bank account.
// These fields are not stored in the database but are calculated on the fly:
//   - RemainingLimit: how much more the account can receive today
//   - UtilisationPct: percentage of the daily limit already used
func (m *Monitor) buildAccountStatus(account *repository.BankAccount) *repository.AccountWithStatus {
	// Calculate remaining capacity (clamped to zero).
	remaining := account.DailyLimitTHB.Sub(account.DailyReceivedTHB)
	if remaining.IsNegative() {
		remaining = decimal.Zero
	}

	// Calculate utilisation percentage. Guard against division by zero
	// in case DailyLimitTHB is zero (misconfigured account).
	var utilisation decimal.Decimal
	if account.DailyLimitTHB.IsPositive() {
		utilisation = account.DailyReceivedTHB.Div(account.DailyLimitTHB).
			Mul(decimal.NewFromInt(100)).
			Round(2)
	}

	return &repository.AccountWithStatus{
		BankAccount:    *account,
		RemainingLimit: remaining,
		UtilisationPct: utilisation,
	}
}

// ---------------------------------------------------------------------------
// UpdateDailyReceived — increment the daily counter for a bank account
// ---------------------------------------------------------------------------

// UpdateDailyReceived adds the specified amount to a bank account's daily
// received counter. This is called by the order-service (via internal API)
// each time a deposit is matched to a bank account.
//
// The function performs the following steps:
//  1. Increment the counter in PostgreSQL (source of truth).
//  2. Increment the counter in Redis (for fast reads by SelectAccount).
//  3. Check if the updated total has reached the daily limit.
//  4. If the limit is reached, trigger auto-switch to disable the account
//     and reassign affected merchants.
//
// Parameters:
//   - bankAccountID: the UUID of the account that received a deposit
//   - amount: the deposit amount in THB
func (m *Monitor) UpdateDailyReceived(ctx context.Context, bankAccountID uuid.UUID, amount decimal.Decimal) error {
	m.log.Info("updating daily received",
		slog.String("account_id", bankAccountID.String()),
		slog.String("amount", amount.String()),
	)

	// -----------------------------------------------------------------------
	// Step 1: Increment the counter in PostgreSQL.
	// This is the source of truth for the daily received amount.
	// -----------------------------------------------------------------------
	if err := m.repo.IncrementDailyReceived(ctx, bankAccountID, amount); err != nil {
		return fmt.Errorf("update daily received: db increment: %w", err)
	}

	// -----------------------------------------------------------------------
	// Step 2: Increment the counter in Redis.
	// We use INCRBYFLOAT for the Redis counter. The amount is converted
	// to float64 for Redis, but the database retains full precision.
	// -----------------------------------------------------------------------
	redisKey := redisDailyReceivedKey(bankAccountID)
	amountFloat, _ := amount.Float64()
	if err := m.rdb.IncrByFloat(ctx, redisKey, amountFloat).Err(); err != nil {
		// Redis failure is not fatal — the database is still accurate.
		// Log a warning and continue.
		m.log.Warn("failed to increment Redis daily counter",
			slog.String("account_id", bankAccountID.String()),
			"error", err,
		)
	}

	// -----------------------------------------------------------------------
	// Step 3: Check if the account has hit its daily limit.
	// Re-read the account from the database to get the updated total.
	// -----------------------------------------------------------------------
	account, err := m.repo.GetAccountByID(ctx, bankAccountID)
	if err != nil {
		return fmt.Errorf("update daily received: re-read account: %w", err)
	}

	// -----------------------------------------------------------------------
	// Step 4: Trigger auto-switch if the daily limit has been reached.
	// -----------------------------------------------------------------------
	if account.DailyReceivedTHB.GreaterThanOrEqual(account.DailyLimitTHB) {
		m.log.Warn("account reached daily limit, triggering auto-switch",
			slog.String("account_id", bankAccountID.String()),
			slog.String("received", account.DailyReceivedTHB.String()),
			slog.String("limit", account.DailyLimitTHB.String()),
		)

		// AutoSwitch will disable the account and reassign merchants.
		if err := m.pool.AutoSwitch(ctx, bankAccountID, "daily_limit_reached"); err != nil {
			return fmt.Errorf("update daily received: auto-switch: %w", err)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// ResetDailyCounters — midnight reset for all accounts
// ---------------------------------------------------------------------------

// ResetDailyCounters sets daily_received_thb to zero for all bank accounts.
// This function is called by the scheduler at midnight (Bangkok time) to
// start a fresh daily receiving cycle.
//
// The reset process:
//  1. Reset all database counters to zero.
//  2. Delete all Redis daily counter keys.
//  3. Log the reset event for audit purposes.
//
// Note: Accounts that were auto-disabled due to reaching their daily limit
// are NOT automatically re-enabled by this function. Re-enabling must be
// done explicitly by an admin, because there may be other reasons an
// account was disabled (e.g. bank investigation).
func (m *Monitor) ResetDailyCounters(ctx context.Context) error {
	m.log.Info("resetting daily counters for all accounts")

	// -----------------------------------------------------------------------
	// Step 1: Reset counters in the database.
	// -----------------------------------------------------------------------
	if err := m.repo.ResetAllDailyCounters(ctx); err != nil {
		return fmt.Errorf("reset daily counters: db reset: %w", err)
	}

	// -----------------------------------------------------------------------
	// Step 2: Delete all Redis daily counter keys.
	// We find all matching keys and delete them in bulk.
	// -----------------------------------------------------------------------
	var cursor uint64
	for {
		// Scan for keys matching the daily received pattern.
		keys, nextCursor, err := m.rdb.Scan(ctx, cursor, "bank:daily_received:*", 100).Result()
		if err != nil {
			m.log.Warn("failed to scan Redis keys for daily reset", "error", err)
			break
		}

		// Delete the found keys.
		if len(keys) > 0 {
			if err := m.rdb.Del(ctx, keys...).Err(); err != nil {
				m.log.Warn("failed to delete Redis daily counter keys", "error", err)
			}
		}

		// If the cursor is 0, we've scanned all keys.
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	m.log.Info("daily counters reset completed",
		slog.String("time", time.Now().UTC().Format(time.RFC3339)),
	)

	return nil
}
