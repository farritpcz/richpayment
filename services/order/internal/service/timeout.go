package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/order/internal/repository"
)

// ExpiryWorker is a background goroutine that periodically scans for deposit
// orders that have exceeded their expiry deadline without being matched. When
// an expired order is found, the worker transitions it to "expired" status in
// PostgreSQL and removes it from the Redis pending and expiry sorted sets.
//
// The worker runs on a fixed polling interval (default 10 seconds) and is
// designed to be started once at application boot via StartExpiryWorker.
// It respects context cancellation for graceful shutdown.
type ExpiryWorker struct {
	// repo provides database access for querying and updating expired orders.
	repo repository.OrderRepository

	// rdb is the Redis client for removing orders from the pending and expiry
	// sorted sets after they are marked as expired.
	rdb *redis.Client

	// pollInterval controls how often the worker checks for expired orders.
	// A shorter interval means faster expiry detection but more Redis/DB load.
	// Default is 10 seconds.
	pollInterval time.Duration
}

// NewExpiryWorker creates a new ExpiryWorker with the given dependencies.
//
// Parameters:
//   - repo: the order repository for database operations.
//   - rdb: the Redis client for sorted set cleanup.
//   - pollInterval: how often to poll for expired orders (e.g. 10*time.Second).
func NewExpiryWorker(repo repository.OrderRepository, rdb *redis.Client, pollInterval time.Duration) *ExpiryWorker {
	return &ExpiryWorker{
		repo:         repo,
		rdb:          rdb,
		pollInterval: pollInterval,
	}
}

// StartExpiryWorker launches the expiry polling loop in a new goroutine.
// The loop runs until the provided context is cancelled (e.g. on SIGTERM).
//
// On each tick the worker:
//  1. Queries the Redis "expiry_orders" sorted set for members whose score
//     (Unix timestamp) is less than or equal to the current time.
//  2. For each expired order ID found, it updates the order status to
//     "expired" in PostgreSQL.
//  3. Removes the order from both the "expiry_orders" set and the
//     bank-account-specific "pending_orders:{bank_account_id}" set.
//  4. Decrements the bank account's load in the merchant's account pool.
//
// Errors during individual order expiry are logged but do not stop the worker.
// The worker will retry the failed order on the next polling cycle.
//
// Parameters:
//   - ctx: the application-scoped context; cancelling it stops the worker.
func (w *ExpiryWorker) StartExpiryWorker(ctx context.Context) {
	// Launch the polling loop in a separate goroutine so the caller
	// does not block.
	go func() {
		logger.Info("expiry worker started",
			"poll_interval", w.pollInterval.String(),
		)

		// Create a ticker that fires every pollInterval.
		ticker := time.NewTicker(w.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// Context was cancelled (shutdown signal received).
				logger.Info("expiry worker stopped: context cancelled")
				return

			case <-ticker.C:
				// Tick: scan for expired orders and process them.
				w.processExpiredOrders(ctx)
			}
		}
	}()
}

// processExpiredOrders is the core logic executed on each polling tick.
// It queries Redis for order IDs whose expiry timestamp has passed, then
// transitions each order to "expired" status and cleans up Redis state.
//
// This method is called internally by StartExpiryWorker on each tick.
// It handles errors gracefully by logging them and continuing to the next
// order, so one failure does not block processing of other expired orders.
func (w *ExpiryWorker) processExpiredOrders(ctx context.Context) {
	// Query the "expiry_orders" sorted set for all members with a score
	// (Unix timestamp) up to the current time. These are orders whose
	// expiry deadline has passed.
	now := time.Now().UTC()
	maxScore := fmt.Sprintf("%d", now.Unix())

	// ZRANGEBYSCORE returns order IDs scored at or before the current time.
	expiredIDs, err := w.rdb.ZRangeByScore(ctx, "expiry_orders", &redis.ZRangeBy{
		Min: "-inf",
		Max: maxScore,
	}).Result()
	if err != nil {
		logger.Error("expiry worker: failed to query expiry_orders set",
			"error", err,
		)
		return
	}

	// If no expired orders were found, return early (nothing to do).
	if len(expiredIDs) == 0 {
		return
	}

	logger.Info("expiry worker: found expired orders",
		"count", len(expiredIDs),
	)

	// Process each expired order individually. Errors are logged per-order
	// so that one failure does not prevent other orders from being processed.
	for _, orderIDStr := range expiredIDs {
		if err := w.expireSingleOrder(ctx, orderIDStr); err != nil {
			logger.Error("expiry worker: failed to expire order",
				"order_id", orderIDStr,
				"error", err,
			)
			// Continue processing remaining orders despite this error.
			continue
		}
	}
}

// expireSingleOrder transitions one deposit order from "pending" to "expired".
// It performs the following cleanup steps:
//
//  1. Load the order from PostgreSQL to get the bank_account_id and merchant_id.
//  2. Update the order status to "expired" in PostgreSQL.
//  3. Remove the order from the "expiry_orders" Redis sorted set.
//  4. Remove the order from the "pending_orders:{bank_account_id}" sorted set.
//  5. Decrement the bank account's load score in the merchant's pool.
//
// Parameters:
//   - orderIDStr: the string representation of the order's UUID.
//
// Returns an error if any step fails.
func (w *ExpiryWorker) expireSingleOrder(ctx context.Context, orderIDStr string) error {
	// Parse the order UUID from the string stored in Redis.
	orderID, err := parseUUID(orderIDStr)
	if err != nil {
		return fmt.Errorf("parse expired order id: %w", err)
	}

	// Load the order from PostgreSQL to get associated metadata (bank account
	// ID, merchant ID) needed for Redis cleanup.
	order, err := w.repo.GetByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("load expired order from db: %w", err)
	}

	// Only expire orders that are still pending. If the order was already
	// matched/completed between the Redis query and now, skip it.
	if order.Status != models.OrderStatusPending {
		// Order was already processed; just clean up the Redis entry.
		w.rdb.ZRem(ctx, "expiry_orders", orderIDStr)
		return nil
	}

	// Update the order status to "expired" in PostgreSQL.
	if err := w.repo.UpdateStatus(ctx, orderID, models.OrderStatusExpired, nil); err != nil {
		return fmt.Errorf("update order status to expired: %w", err)
	}

	// Remove the order from the Redis expiry tracking sorted set.
	w.rdb.ZRem(ctx, "expiry_orders", orderIDStr)

	// Remove the order from the bank-account-specific pending sorted set.
	pendingKey := fmt.Sprintf("pending_orders:%s", order.BankAccountID.String())
	w.rdb.ZRem(ctx, pendingKey, orderIDStr)

	// Decrement the bank account's load in the merchant's account pool.
	poolKey := fmt.Sprintf("account_pool:%s", order.MerchantID.String())
	w.rdb.ZIncrBy(ctx, poolKey, -1, order.BankAccountID.String())

	logger.Info("order expired by timeout worker",
		"order_id", orderIDStr,
		"bank_account_id", order.BankAccountID.String(),
	)

	return nil
}

// parseUUID is a small helper that wraps uuid.Parse and returns a
// consistently formatted error message. Used by the expiry worker
// to convert Redis member strings back to uuid.UUID values.
func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid uuid %q: %w", s, err)
	}
	return id, nil
}
