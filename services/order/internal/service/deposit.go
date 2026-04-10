package service

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/order/internal/repository"
)

// DepositService encapsulates the core deposit order lifecycle: creation,
// retrieval, and completion. It coordinates between the PostgreSQL repository,
// Redis caches/sets, QR code generation, and external webhook delivery.
type DepositService struct {
	// repo is the persistence layer for deposit orders (PostgreSQL).
	repo repository.OrderRepository

	// rdb is the Redis client used for the bank-account pool, pending order
	// sorted sets, and order expiry tracking.
	rdb *redis.Client

	// orderExpiry defines how long a deposit order stays in "pending" status
	// before the timeout worker marks it as expired. Defaults to 15 minutes.
	orderExpiry time.Duration

	// feePercent is the merchant deposit fee percentage applied when an
	// order is completed. For example, decimal.NewFromFloat(0.02) means 2%.
	feePercent decimal.Decimal

	// matchStrategy controls how incoming SMS amounts are matched to pending
	// orders. Supported values:
	//   - "unique_amount": adjust the requested amount by a small random
	//     decimal so every pending order has a distinct amount.
	//   - "time_based": match by exact amount and pick the oldest pending order.
	matchStrategy string
}

// NewDepositService constructs a DepositService with all required dependencies.
//
// Parameters:
//   - repo: the database repository for CRUD operations on deposit orders.
//   - rdb: the Redis client for pool lookups, pending sets, and expiry tracking.
//   - orderExpiry: the duration after which an unmatched order expires.
//   - feePercent: the merchant fee rate (e.g. 0.02 for 2%).
//   - matchStrategy: either "unique_amount" or "time_based".
func NewDepositService(
	repo repository.OrderRepository,
	rdb *redis.Client,
	orderExpiry time.Duration,
	feePercent decimal.Decimal,
	matchStrategy string,
) *DepositService {
	return &DepositService{
		repo:          repo,
		rdb:           rdb,
		orderExpiry:   orderExpiry,
		feePercent:    feePercent,
		matchStrategy: matchStrategy,
	}
}

// CreateDepositOrder is the primary entry point for initiating a new deposit.
// It performs the following steps in order:
//
//  1. Select a bank account from the Redis sorted-set pool for the merchant.
//  2. Optionally adjust the requested amount for uniqueness (if strategy is
//     "unique_amount") to avoid collisions between concurrent deposits.
//  3. Generate a PromptPay QR code payload and base64 PNG image.
//  4. Persist the order in PostgreSQL with status "pending".
//  5. Add the order to the Redis pending_orders:{bank_account_id} sorted set
//     (scored by adjusted amount) for fast lookup by the matcher.
//  6. Add the order to the Redis expiry_orders sorted set (scored by Unix
//     expiry timestamp) for the timeout worker.
//
// Parameters:
//   - merchantID: UUID of the merchant requesting the deposit.
//   - merchantOrderID: the merchant's own reference/order ID for reconciliation.
//   - amount: the requested deposit amount in THB (must be positive).
//   - customerName: the depositing customer's display name.
//   - customerBank: the bank code of the customer's originating bank.
//
// Returns the fully populated DepositOrder and nil error on success.
func (s *DepositService) CreateDepositOrder(
	ctx context.Context,
	merchantID uuid.UUID,
	merchantOrderID string,
	amount decimal.Decimal,
	customerName string,
	customerBank string,
) (*models.DepositOrder, error) {

	// ---------------------------------------------------------------
	// Step 1: Select a bank account from the Redis pool.
	// The sorted set "account_pool:{merchant_id}" ranks bank accounts
	// by their current load (number of pending orders). We pick the
	// account with the lowest score (least busy) using ZRANGEBYSCORE.
	// ---------------------------------------------------------------
	poolKey := fmt.Sprintf("account_pool:%s", merchantID.String())

	// ZRANGE with LIMIT 0 1 returns the member with the lowest score.
	accounts, err := s.rdb.ZRangeByScore(ctx, poolKey, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    "+inf",
		Offset: 0,
		Count:  1,
	}).Result()
	if err != nil || len(accounts) == 0 {
		return nil, errors.Wrap(
			fmt.Errorf("no available bank account in pool for merchant %s", merchantID),
			"NO_BANK_ACCOUNT",
			"no bank account available in the pool",
			503,
		)
	}

	// Parse the selected bank account UUID from the sorted-set member.
	bankAccountID, err := uuid.Parse(accounts[0])
	if err != nil {
		return nil, fmt.Errorf("parse bank account id from pool: %w", err)
	}

	// Increment the bank account's load score by 1 so subsequent orders
	// are spread across accounts (simple round-robin via score).
	s.rdb.ZIncrBy(ctx, poolKey, 1, bankAccountID.String())

	// ---------------------------------------------------------------
	// Step 2: Adjust amount for uniqueness (if using unique_amount strategy).
	// We add a small random satang offset (0.01 - 0.99 THB) to the
	// requested amount so that every pending order for the same bank
	// account has a unique adjusted amount. This makes SMS matching
	// deterministic even when multiple customers deposit similar amounts.
	// ---------------------------------------------------------------
	adjustedAmount := amount
	if s.matchStrategy == "unique_amount" {
		adjustedAmount = adjustAmountForUniqueness(ctx, s.rdb, bankAccountID, amount)
	}

	// ---------------------------------------------------------------
	// Step 3: Generate PromptPay QR code.
	// The QR encodes the bank account number and the adjusted amount
	// so the customer transfers the exact adjusted amount.
	// ---------------------------------------------------------------
	// TODO: In production, fetch the actual PromptPay-registered account
	// number from the bank_accounts table. For now we use the UUID string
	// as a placeholder account identifier.
	qrPayload, _, err := GeneratePromptPayQR(bankAccountID.String(), adjustedAmount)
	if err != nil {
		return nil, fmt.Errorf("generate promptpay qr: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 4: Build and persist the deposit order.
	// ---------------------------------------------------------------
	now := time.Now().UTC()
	order := &models.DepositOrder{
		ID:               uuid.New(),
		MerchantID:       merchantID,
		MerchantOrderID:  merchantOrderID,
		CustomerName:     customerName,
		CustomerBankCode: customerBank,
		RequestedAmount:  amount,
		AdjustedAmount:   adjustedAmount,
		Currency:         "THB",
		BankAccountID:    bankAccountID,
		Status:           models.OrderStatusPending,
		QRPayload:        qrPayload,
		ExpiresAt:        now.Add(s.orderExpiry),
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// Persist the order in PostgreSQL.
	if err := s.repo.Create(ctx, order); err != nil {
		return nil, fmt.Errorf("create deposit order in db: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 5: Add to Redis pending_orders sorted set for fast matcher lookup.
	// The score is the adjusted amount (as a float64) so the matcher
	// can query by exact score to find the matching order.
	// ---------------------------------------------------------------
	pendingKey := fmt.Sprintf("pending_orders:%s", bankAccountID.String())
	score, _ := adjustedAmount.Float64()
	s.rdb.ZAdd(ctx, pendingKey, redis.Z{
		Score:  score,
		Member: order.ID.String(),
	})

	// ---------------------------------------------------------------
	// Step 6: Add to Redis expiry tracking sorted set.
	// The score is the Unix timestamp when the order expires. The
	// timeout worker polls this set to discover expired orders.
	// ---------------------------------------------------------------
	s.rdb.ZAdd(ctx, "expiry_orders", redis.Z{
		Score:  float64(order.ExpiresAt.Unix()),
		Member: order.ID.String(),
	})

	logger.Info("deposit order created",
		"order_id", order.ID.String(),
		"merchant_id", merchantID.String(),
		"adjusted_amount", adjustedAmount.String(),
		"bank_account_id", bankAccountID.String(),
	)

	return order, nil
}

// GetDepositOrder retrieves a deposit order by its unique ID.
// Delegates directly to the repository layer. Returns the shared
// ErrNotFound error if the order does not exist.
func (s *DepositService) GetDepositOrder(ctx context.Context, orderID uuid.UUID) (*models.DepositOrder, error) {
	// Fetch the order from PostgreSQL via the repository interface.
	order, err := s.repo.GetByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get deposit order: %w", err)
	}
	return order, nil
}

// CompleteDeposit finalises a matched deposit order. This is called after the
// matcher has successfully paired an incoming bank notification (SMS/email/slip)
// with a pending order. The method performs these steps:
//
//  1. Load the order and verify it is still in "pending" status.
//  2. Calculate the merchant fee and net amount.
//  3. Update the order in PostgreSQL to "completed" status with all
//     settlement fields populated.
//  4. Credit the merchant's wallet (via Redis pub/sub or direct call).
//  5. Record the commission split.
//  6. Trigger the merchant webhook notification.
//
// Parameters:
//   - orderID: the UUID of the order to complete.
//   - matchedBy: how the order was matched (e.g. "sms", "email", "slip").
//   - actualAmount: the actual transfer amount observed in the bank notification.
func (s *DepositService) CompleteDeposit(
	ctx context.Context,
	orderID uuid.UUID,
	matchedBy models.MatchedBy,
	actualAmount decimal.Decimal,
) error {
	// ---------------------------------------------------------------
	// Step 1: Load the order and validate its current status.
	// ---------------------------------------------------------------
	order, err := s.repo.GetByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("load order for completion: %w", err)
	}

	// Only pending orders can be completed. Any other status means the
	// order was already processed, expired, or cancelled.
	if order.Status != models.OrderStatusPending {
		return errors.Wrap(
			fmt.Errorf("order %s has status %s, expected pending", orderID, order.Status),
			"INVALID_ORDER_STATUS",
			"order is not in pending status",
			409,
		)
	}

	// ---------------------------------------------------------------
	// Step 2: Calculate fee and net amount.
	// Fee = actualAmount * feePercent (rounded to 2 decimal places).
	// NetAmount = actualAmount - fee.
	// ---------------------------------------------------------------
	feeAmount := actualAmount.Mul(s.feePercent).Round(2)
	netAmount := actualAmount.Sub(feeAmount)

	// ---------------------------------------------------------------
	// Step 3: Update the order in PostgreSQL to completed status.
	// We pass all settlement-related fields in the dynamic fields map.
	// ---------------------------------------------------------------
	now := time.Now().UTC()
	updateFields := map[string]interface{}{
		"matched_by":    string(matchedBy),
		"matched_at":    now,
		"actual_amount": actualAmount,
		"fee_amount":    feeAmount,
		"net_amount":    netAmount,
	}

	if err := s.repo.UpdateStatus(ctx, orderID, models.OrderStatusCompleted, updateFields); err != nil {
		return fmt.Errorf("update order to completed: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 4: Credit the merchant's wallet.
	// We publish a wallet credit event to Redis so the wallet-service
	// can process it asynchronously. This decouples the order-service
	// from direct wallet database writes.
	// ---------------------------------------------------------------
	walletEvent := fmt.Sprintf(
		`{"merchant_id":"%s","order_id":"%s","amount":"%s","currency":"THB"}`,
		order.MerchantID.String(), orderID.String(), netAmount.String(),
	)
	s.rdb.Publish(ctx, "wallet:credit", walletEvent)

	// ---------------------------------------------------------------
	// Step 5: Record commission split.
	// Publish a commission event for the commission-service to process
	// the fee split between system, agent, and partner.
	// ---------------------------------------------------------------
	commissionEvent := fmt.Sprintf(
		`{"transaction_type":"deposit","transaction_id":"%s","merchant_id":"%s","fee_amount":"%s","currency":"THB"}`,
		orderID.String(), order.MerchantID.String(), feeAmount.String(),
	)
	s.rdb.Publish(ctx, "commission:record", commissionEvent)

	// ---------------------------------------------------------------
	// Step 6: Trigger merchant webhook notification.
	// Publish a webhook event so the notification-service can deliver
	// the deposit completion callback to the merchant's endpoint.
	// ---------------------------------------------------------------
	webhookEvent := fmt.Sprintf(
		`{"order_id":"%s","merchant_id":"%s","merchant_order_id":"%s","status":"completed","amount":"%s","net_amount":"%s"}`,
		orderID.String(), order.MerchantID.String(), order.MerchantOrderID,
		actualAmount.String(), netAmount.String(),
	)
	s.rdb.Publish(ctx, "webhook:send", webhookEvent)

	// Remove the order from the pending and expiry Redis sets since
	// it is now completed and no longer needs matching or timeout.
	pendingKey := fmt.Sprintf("pending_orders:%s", order.BankAccountID.String())
	s.rdb.ZRem(ctx, pendingKey, orderID.String())
	s.rdb.ZRem(ctx, "expiry_orders", orderID.String())

	// Decrement the bank account's load in the pool since this order
	// is no longer pending.
	poolKey := fmt.Sprintf("account_pool:%s", order.MerchantID.String())
	s.rdb.ZIncrBy(ctx, poolKey, -1, order.BankAccountID.String())

	logger.Info("deposit order completed",
		"order_id", orderID.String(),
		"matched_by", string(matchedBy),
		"actual_amount", actualAmount.String(),
		"fee_amount", feeAmount.String(),
		"net_amount", netAmount.String(),
	)

	return nil
}

// adjustAmountForUniqueness adds a small random decimal offset (between
// 0.01 and 0.99 THB) to the requested amount so that every pending order
// for the same bank account has a unique adjusted amount. This is critical
// for the "unique_amount" matching strategy because the SMS matcher relies
// on exact amount matching to pair notifications with orders.
//
// The function checks the Redis pending set to ensure the adjusted amount
// is not already in use. If a collision is detected, it retries with a
// different random offset (up to 100 attempts) before falling back to the
// original amount.
//
// Parameters:
//   - ctx: context for Redis operations.
//   - rdb: Redis client for checking existing pending amounts.
//   - bankAccountID: the bank account whose pending set is checked.
//   - baseAmount: the original requested deposit amount.
//
// Returns the adjusted amount with the random offset applied.
func adjustAmountForUniqueness(ctx context.Context, rdb *redis.Client, bankAccountID uuid.UUID, baseAmount decimal.Decimal) decimal.Decimal {
	pendingKey := fmt.Sprintf("pending_orders:%s", bankAccountID.String())

	// Try up to 100 random offsets to find a unique adjusted amount.
	for i := 0; i < 100; i++ {
		// Generate a random offset between 0.01 and 0.99 THB.
		// rand.Intn(99) produces 0..98; adding 1 gives 1..99;
		// dividing by 100 gives 0.01..0.99.
		offset := decimal.NewFromInt(int64(rand.Intn(99) + 1)).Div(decimal.NewFromInt(100))
		candidate := baseAmount.Add(offset)

		// Check if this amount already exists as a score in the pending set.
		// ZRANGEBYSCORE with min=max returns members with that exact score.
		candidateFloat, _ := candidate.Float64()
		scoreStr := fmt.Sprintf("%f", candidateFloat)
		existing, _ := rdb.ZRangeByScore(ctx, pendingKey, &redis.ZRangeBy{
			Min:   scoreStr,
			Max:   scoreStr,
			Count: 1,
		}).Result()

		// If no existing order has this score, the candidate is unique.
		if len(existing) == 0 {
			return candidate
		}
	}

	// Fallback: if all 100 attempts collide (extremely unlikely), return
	// the base amount. The matcher will use time-based fallback logic.
	logger.Warn("could not find unique amount after 100 attempts, using base amount",
		"bank_account_id", bankAccountID.String(),
		"base_amount", baseAmount.String(),
	)
	return baseAmount
}
