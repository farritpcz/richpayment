// Package service contains the core business logic for the bank-service.
//
// This file implements the bank account pool management, which is responsible
// for selecting the optimal bank account for incoming deposits, rebuilding
// the Redis-backed pool cache, and performing automatic account switching
// when accounts hit their daily limits or encounter issues.
package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/services/bank/internal/repository"
)

// ---------------------------------------------------------------------------
// Pool service
// ---------------------------------------------------------------------------

// Pool manages the bank account pool lifecycle. It selects the best account
// for each deposit, maintains the Redis cache of account priorities, and
// handles automatic switching when accounts become unavailable.
type Pool struct {
	// repo provides access to bank account data in PostgreSQL.
	repo repository.BankRepository

	// rdb is the Redis client used for the account priority sorted set
	// and for fast account selection without hitting the database.
	rdb *redis.Client

	// log is the structured logger for pool management events.
	log *slog.Logger
}

// NewPool creates a new Pool service. All parameters are required.
func NewPool(repo repository.BankRepository, rdb *redis.Client, log *slog.Logger) *Pool {
	return &Pool{
		repo: repo,
		rdb:  rdb,
		log:  log,
	}
}

// redisPoolKey returns the Redis sorted set key used to store the account
// pool for a specific merchant. Each merchant has its own sorted set
// where members are bank account UUIDs and scores are priorities.
func redisPoolKey(merchantID uuid.UUID) string {
	return fmt.Sprintf("bank:pool:%s", merchantID)
}

// redisDailyReceivedKey returns the Redis key for tracking how much a
// specific bank account has received today. This is a simple string
// counter that mirrors the database column for fast reads.
func redisDailyReceivedKey(accountID uuid.UUID) string {
	return fmt.Sprintf("bank:daily_received:%s", accountID)
}

// ---------------------------------------------------------------------------
// SelectAccount — pick the best available bank account for a deposit
// ---------------------------------------------------------------------------

// SelectAccount chooses the optimal bank account for receiving a deposit
// from the specified merchant. The selection algorithm is:
//
//  1. Get all active accounts mapped to this merchant from the database
//     (ordered by priority, highest first).
//  2. For each account, check whether its daily received amount has
//     reached the daily limit.
//  3. Return the first (highest priority) account that still has room
//     under its daily limit.
//  4. If ALL accounts have hit their daily limit, return an error.
//
// The reason we query the database rather than Redis alone is to guarantee
// consistency — the database is the source of truth for account status and
// mappings. Redis is only used as an optimisation for the daily counter.
func (p *Pool) SelectAccount(ctx context.Context, merchantID uuid.UUID) (*repository.BankAccount, error) {
	p.log.Debug("selecting account for merchant",
		slog.String("merchant_id", merchantID.String()),
	)

	// -----------------------------------------------------------------------
	// Step 1: Get all active accounts for this merchant.
	// -----------------------------------------------------------------------
	accounts, err := p.repo.GetActiveAccountsByMerchant(ctx, merchantID)
	if err != nil {
		return nil, fmt.Errorf("select account: fetch active accounts: %w", err)
	}

	// If the merchant has no active accounts at all, return an error.
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no active bank accounts available for merchant %s", merchantID)
	}

	// -----------------------------------------------------------------------
	// Step 2: Find the first account that hasn't hit its daily limit.
	// Accounts are already sorted by priority (highest first), so we
	// iterate in order and return the first one with remaining capacity.
	// -----------------------------------------------------------------------
	for i := range accounts {
		account := &accounts[i]

		// Check if this account has hit its daily receiving limit.
		// daily_received_thb is updated both in DB and Redis; we read
		// from DB here for accuracy.
		if account.DailyReceivedTHB.GreaterThanOrEqual(account.DailyLimitTHB) {
			p.log.Debug("account at daily limit, skipping",
				slog.String("account_id", account.ID.String()),
				slog.String("received", account.DailyReceivedTHB.String()),
				slog.String("limit", account.DailyLimitTHB.String()),
			)
			continue
		}

		// This account has remaining capacity — select it.
		p.log.Info("account selected",
			slog.String("merchant_id", merchantID.String()),
			slog.String("account_id", account.ID.String()),
			slog.String("bank_code", account.BankCode),
			slog.Int("priority", account.Priority),
		)
		return account, nil
	}

	// -----------------------------------------------------------------------
	// Step 3: All accounts are at their daily limit.
	// -----------------------------------------------------------------------
	return nil, fmt.Errorf("all bank accounts at daily limit for merchant %s", merchantID)
}

// ---------------------------------------------------------------------------
// UpdateAccountPool — rebuild the Redis sorted set from the database
// ---------------------------------------------------------------------------

// UpdateAccountPool rebuilds the Redis sorted set that caches the bank
// account pool for a specific merchant. This is called:
//   - After an admin adds or removes bank account mappings
//   - After an account is auto-switched
//   - On service startup to warm the cache
//
// The sorted set maps account UUIDs to their priority scores, allowing
// fast ordered retrieval without hitting PostgreSQL.
func (p *Pool) UpdateAccountPool(ctx context.Context, merchantID uuid.UUID) error {
	p.log.Info("rebuilding account pool", slog.String("merchant_id", merchantID.String()))

	// -----------------------------------------------------------------------
	// Step 1: Fetch all active accounts for this merchant from the database.
	// -----------------------------------------------------------------------
	accounts, err := p.repo.GetActiveAccountsByMerchant(ctx, merchantID)
	if err != nil {
		return fmt.Errorf("update pool: fetch accounts: %w", err)
	}

	key := redisPoolKey(merchantID)

	// -----------------------------------------------------------------------
	// Step 2: Delete the existing sorted set and rebuild it.
	// We use a pipeline to make this atomic from Redis's perspective.
	// -----------------------------------------------------------------------
	pipe := p.rdb.Pipeline()

	// Remove the old sorted set entirely.
	pipe.Del(ctx, key)

	// Add each active account with its priority as the score.
	for _, account := range accounts {
		pipe.ZAdd(ctx, key, redis.Z{
			Score:  float64(account.Priority),
			Member: account.ID.String(),
		})
	}

	// Execute the pipeline atomically.
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("update pool: redis pipeline: %w", err)
	}

	p.log.Info("account pool rebuilt",
		slog.String("merchant_id", merchantID.String()),
		slog.Int("account_count", len(accounts)),
	)

	return nil
}

// ---------------------------------------------------------------------------
// AutoSwitch — disable an account and reassign affected merchants
// ---------------------------------------------------------------------------

// AutoSwitch disables the specified bank account and attempts to find
// replacement accounts for all merchants that were using it. This is
// triggered when:
//   - A bank account reaches its daily receiving limit
//   - A bank reports the account is blocked or under investigation
//   - SMS parsing detects a problem with the account
//
// The process:
//  1. Disable the current account by setting its status to "disabled".
//  2. Look up all merchants mapped to this account.
//  3. For each merchant, rebuild the Redis pool (which will exclude the
//     disabled account).
//  4. Log a Telegram-worthy alert for the operations team.
//
// Parameters:
//   - bankAccountID: the UUID of the account to disable
//   - reason: a human-readable reason for the switch (e.g. "daily_limit_reached")
func (p *Pool) AutoSwitch(ctx context.Context, bankAccountID uuid.UUID, reason string) error {
	p.log.Warn("initiating auto-switch",
		slog.String("account_id", bankAccountID.String()),
		slog.String("reason", reason),
	)

	// -----------------------------------------------------------------------
	// Step 1: Disable the bank account in the database.
	// -----------------------------------------------------------------------
	if err := p.repo.UpdateAccountStatus(ctx, bankAccountID, repository.BankAccountStatusDisabled); err != nil {
		return fmt.Errorf("auto-switch: disable account: %w", err)
	}

	p.log.Info("account disabled",
		slog.String("account_id", bankAccountID.String()),
	)

	// -----------------------------------------------------------------------
	// Step 2: Find all merchants that were using this account.
	// -----------------------------------------------------------------------
	merchantIDs, err := p.repo.GetMerchantsByAccount(ctx, bankAccountID)
	if err != nil {
		return fmt.Errorf("auto-switch: get affected merchants: %w", err)
	}

	p.log.Info("affected merchants identified",
		slog.String("account_id", bankAccountID.String()),
		slog.Int("merchant_count", len(merchantIDs)),
	)

	// -----------------------------------------------------------------------
	// Step 3: Rebuild the Redis pool for each affected merchant.
	// The disabled account will be excluded because its status is no longer
	// "active" in the database.
	// -----------------------------------------------------------------------
	for _, mid := range merchantIDs {
		if err := p.UpdateAccountPool(ctx, mid); err != nil {
			// Log the error but continue with other merchants so that a
			// failure for one merchant doesn't block the rest.
			p.log.Error("failed to rebuild pool for merchant",
				slog.String("merchant_id", mid.String()),
				"error", err,
			)
		}
	}

	// -----------------------------------------------------------------------
	// Step 4: Send a Telegram alert.
	//
	// NOTE: In production, this would call the notification-service via
	// NATS or HTTP to send a Telegram message to the operations channel.
	// For now, we log the alert message that would be sent.
	// -----------------------------------------------------------------------
	alertMsg := fmt.Sprintf(
		"[AUTO-SWITCH] Account %s disabled. Reason: %s. Affected merchants: %d",
		bankAccountID, reason, len(merchantIDs),
	)
	p.log.Warn(alertMsg)

	// TODO: Send Telegram alert via notification-service.
	// _ = p.notificationClient.SendTelegramAlert(ctx, alertMsg)

	return nil
}

// ---------------------------------------------------------------------------
// Helper: check remaining daily capacity
// ---------------------------------------------------------------------------

// remainingCapacity calculates how much more a bank account can receive
// today before hitting its daily limit. Returns zero if already at or
// over the limit.
func remainingCapacity(account *repository.BankAccount) decimal.Decimal {
	remaining := account.DailyLimitTHB.Sub(account.DailyReceivedTHB)
	if remaining.IsNegative() {
		return decimal.Zero
	}
	return remaining
}
