package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/services/order/internal/repository"
)

// MatcherService handles the logic of pairing incoming bank notifications
// (SMS, email, or slip) with pending deposit orders. When a bank sends an
// SMS confirming a transfer, the parser-service extracts the amount and
// bank account, then calls MatchSMSToOrder to find the corresponding order.
//
// Two matching strategies are supported:
//
//   - "unique_amount": Each pending order has a unique adjusted amount, so
//     an exact score lookup in the Redis sorted set is sufficient. This is
//     the preferred strategy because it guarantees a one-to-one match.
//
//   - "time_based": Multiple orders may share the same requested amount.
//     The matcher picks the oldest pending order (lowest created_at) that
//     has the matching amount. This is a fallback for cases where amount
//     adjustment is not possible.
type MatcherService struct {
	// repo provides database access for order lookups and status updates.
	repo repository.OrderRepository

	// rdb is the Redis client for pending order sorted set operations.
	rdb *redis.Client

	// strategy is the matching strategy to use: "unique_amount" or "time_based".
	strategy string
}

// NewMatcherService constructs a MatcherService with the given dependencies.
//
// Parameters:
//   - repo: the order repository for database queries.
//   - rdb: the Redis client for sorted set lookups.
//   - strategy: the matching strategy ("unique_amount" or "time_based").
func NewMatcherService(repo repository.OrderRepository, rdb *redis.Client, strategy string) *MatcherService {
	return &MatcherService{
		repo:     repo,
		rdb:      rdb,
		strategy: strategy,
	}
}

// MatchSMSToOrder attempts to find a pending deposit order that matches an
// incoming bank SMS notification. The SMS notification provides the bank
// account that received the transfer, the transferred amount, and the
// timestamp of the SMS.
//
// Matching flow:
//  1. Look up the Redis sorted set "pending_orders:{bank_account_id}" for
//     an order whose score (adjusted amount) matches the incoming amount.
//  2. If using "unique_amount" strategy: exact score match is deterministic.
//  3. If using "time_based" strategy: query the database for the oldest
//     pending order with the matching amount.
//  4. If a match is found, remove the order from the Redis pending set
//     and return its UUID.
//
// Parameters:
//   - bankAccountID: UUID of the bank account that received the deposit.
//   - amount: the transferred amount extracted from the SMS text.
//   - smsTimestamp: the timestamp when the SMS was received (used for
//     logging and audit, not for matching logic).
//
// Returns:
//   - orderID: the UUID of the matched order (uuid.Nil if no match).
//   - err: non-nil if a system error occurred; ErrNotFound if no match.
func (m *MatcherService) MatchSMSToOrder(
	ctx context.Context,
	bankAccountID uuid.UUID,
	amount decimal.Decimal,
	smsTimestamp time.Time,
) (uuid.UUID, error) {
	logger.Info("attempting to match SMS to order",
		"bank_account_id", bankAccountID.String(),
		"amount", amount.String(),
		"sms_timestamp", smsTimestamp.Format(time.RFC3339),
	)

	// Build the Redis key for the pending orders sorted set of this bank account.
	pendingKey := fmt.Sprintf("pending_orders:%s", bankAccountID.String())

	// ---------------------------------------------------------------
	// Strategy: unique_amount
	// The adjusted amount is unique per pending order, so we can do an
	// exact score lookup in the sorted set. ZRANGEBYSCORE with min=max
	// returns the single member whose score equals the amount.
	// ---------------------------------------------------------------
	if m.strategy == "unique_amount" {
		return m.matchByUniqueAmount(ctx, pendingKey, bankAccountID, amount)
	}

	// ---------------------------------------------------------------
	// Strategy: time_based
	// Multiple orders may share the same amount. We query PostgreSQL
	// for the oldest pending order matching the amount and bank account.
	// ---------------------------------------------------------------
	return m.matchByTimeBased(ctx, pendingKey, bankAccountID, amount)
}

// matchByUniqueAmount performs an exact-score lookup in the Redis pending
// orders sorted set. Because each order has a unique adjusted amount, this
// lookup is O(log N) and returns at most one result.
//
// Parameters:
//   - pendingKey: the Redis sorted set key for the bank account's pending orders.
//   - bankAccountID: the bank account UUID (for logging).
//   - amount: the exact adjusted amount to search for.
//
// Returns the matched order UUID or ErrNotFound.
func (m *MatcherService) matchByUniqueAmount(
	ctx context.Context,
	pendingKey string,
	bankAccountID uuid.UUID,
	amount decimal.Decimal,
) (uuid.UUID, error) {
	// Convert the decimal amount to a float64 string for Redis score lookup.
	amountFloat, _ := amount.Float64()
	scoreStr := fmt.Sprintf("%f", amountFloat)

	// Query Redis for members with exactly this score.
	// ZRANGEBYSCORE with identical min and max returns exact matches.
	members, err := m.rdb.ZRangeByScore(ctx, pendingKey, &redis.ZRangeBy{
		Min:   scoreStr,
		Max:   scoreStr,
		Count: 1,
	}).Result()
	if err != nil {
		return uuid.Nil, fmt.Errorf("redis ZRANGEBYSCORE for unique amount match: %w", err)
	}

	// No member found means no pending order has this adjusted amount.
	if len(members) == 0 {
		logger.Warn("no pending order found for unique amount",
			"bank_account_id", bankAccountID.String(),
			"amount", amount.String(),
		)
		return uuid.Nil, errors.ErrNotFound
	}

	// Parse the order UUID from the sorted set member string.
	orderID, err := uuid.Parse(members[0])
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse matched order id: %w", err)
	}

	// Remove the matched order from the pending set so it cannot be
	// matched again by a subsequent SMS.
	if err := m.rdb.ZRem(ctx, pendingKey, orderID.String()).Err(); err != nil {
		logger.Error("failed to remove matched order from pending set",
			"order_id", orderID.String(),
			"error", err,
		)
	}

	logger.Info("order matched by unique amount",
		"order_id", orderID.String(),
		"amount", amount.String(),
	)

	return orderID, nil
}

// matchByTimeBased queries the PostgreSQL database for the oldest pending
// order that matches the given bank account and amount. This strategy is
// used when amount uniqueness is not guaranteed (e.g. when the merchant
// does not support amount adjustment). The oldest order is matched first
// (FIFO) to maintain fairness.
//
// Parameters:
//   - pendingKey: the Redis sorted set key (for cleanup after match).
//   - bankAccountID: the bank account UUID.
//   - amount: the deposit amount to match against.
//
// Returns the matched order UUID or ErrNotFound.
func (m *MatcherService) matchByTimeBased(
	ctx context.Context,
	pendingKey string,
	bankAccountID uuid.UUID,
	amount decimal.Decimal,
) (uuid.UUID, error) {
	// Query PostgreSQL for the oldest pending order with this amount.
	// The repository's FindPendingByAmount already orders by created_at ASC
	// and returns the first match.
	order, err := m.repo.FindPendingByAmount(ctx, bankAccountID, amount)
	if err != nil {
		return uuid.Nil, fmt.Errorf("find pending order by amount (time_based): %w", err)
	}

	// Remove the matched order from the Redis pending sorted set.
	if err := m.rdb.ZRem(ctx, pendingKey, order.ID.String()).Err(); err != nil {
		logger.Error("failed to remove time-based matched order from pending set",
			"order_id", order.ID.String(),
			"error", err,
		)
	}

	logger.Info("order matched by time-based strategy",
		"order_id", order.ID.String(),
		"amount", amount.String(),
	)

	return order.ID, nil
}
