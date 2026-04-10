// Package service contains the core business logic for the wallet service.
// It orchestrates balance operations (credit, debit, hold, release) using a
// multi-layered defense strategy against race conditions:
//
//   1. Redis distributed lock (SETNX with TTL) — fast-fail layer that prevents
//      most concurrent requests from even reaching the database.
//   2. PostgreSQL SELECT ... FOR UPDATE — row-level exclusive lock that provides
//      serialisation guarantees at the database level.
//   3. Optimistic version check — defense-in-depth retained from the original
//      design; catches any violations that somehow bypass the first two layers.
//   4. Idempotency via reference_id — prevents duplicate processing when clients
//      retry requests due to network timeouts or other transient failures.
//
// All balance-modifying operations (Credit, Debit, Hold, Release) execute the
// entire read-check-update-ledger flow inside a single PostgreSQL transaction,
// eliminating the TOCTOU vulnerability that existed in the previous
// optimistic-locking-only approach.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/database"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/wallet/internal/repository"
)

// -------------------------------------------------------------------------
// Constants
// -------------------------------------------------------------------------

// lockKeyPrefix is the Redis key prefix for wallet distributed locks.
// The full key format is "wallet_lock:{wallet_id}".
// Each wallet gets its own lock so that operations on different wallets
// can proceed concurrently without blocking each other.
const lockKeyPrefix = "wallet_lock:"

// -------------------------------------------------------------------------
// WalletService
// -------------------------------------------------------------------------

// WalletService encapsulates all business rules for wallet balance
// management. It uses a multi-layered concurrency control strategy:
//
//   - Redis distributed lock: prevents concurrent requests for the same wallet
//     from reaching the database. Acquired before any balance-modifying operation
//     and released after the operation completes (or via TTL if the process crashes).
//
//   - PostgreSQL SELECT ... FOR UPDATE: acquires a row-level exclusive lock on
//     the wallet row at the start of each balance-modifying transaction. This is
//     the primary defense against TOCTOU races.
//
//   - Idempotency via reference_id: before processing any operation, checks if a
//     ledger entry with the same reference_id already exists. If so, returns
//     success without re-processing, preventing duplicate mutations.
//
// All public methods are safe for concurrent use by multiple goroutines.
type WalletService struct {
	// repo is the data-access layer used for all wallet and ledger
	// persistence. It is injected at construction time and must implement
	// both the legacy and new transactional methods.
	repo repository.WalletRepository

	// redisClient is used for acquiring and releasing distributed locks.
	// It is shared across all service method calls and is safe for
	// concurrent use (go-redis handles connection pooling internally).
	redisClient *redis.Client
}

// NewWalletService creates a new WalletService with the given repository
// and Redis client for distributed locking.
//
// Parameters:
//   - repo:        an implementation of WalletRepository (e.g. PostgresWalletRepo).
//   - redisClient: a connected Redis client for distributed lock operations.
//                  Must not be nil; the service cannot function without the
//                  distributed lock layer.
//
// Returns:
//   - *WalletService: a ready-to-use service instance.
func NewWalletService(repo repository.WalletRepository, redisClient *redis.Client) *WalletService {
	return &WalletService{
		repo:        repo,
		redisClient: redisClient,
	}
}

// -------------------------------------------------------------------------
// acquireWalletLock — internal helper for Redis distributed lock
// -------------------------------------------------------------------------

// acquireWalletLock attempts to acquire a Redis distributed lock for the
// specified wallet. The lock key format is "wallet_lock:{wallet_id}" with
// a 10-second TTL that auto-releases the lock if the holder crashes.
//
// This lock is the FIRST line of defense: it causes concurrent requests
// targeting the same wallet to fail fast with an error, avoiding the cost
// of opening a database transaction only to block on FOR UPDATE.
//
// The lockValue is a unique identifier (typically a UUID) that ensures only
// the original acquirer can release the lock, preventing the scenario where
// process A's lock expires, process B acquires the lock, and then process A
// accidentally releases process B's lock.
//
// If the Redis client is nil (e.g. in test environments), the lock is
// skipped and the method returns nil. The PostgreSQL FOR UPDATE lock still
// provides full protection in this case.
//
// Parameters:
//   - ctx:       request-scoped context.
//   - walletID:  the UUID of the wallet to lock.
//   - lockValue: a unique string identifying this lock holder (e.g. uuid.New().String()).
//
// Returns:
//   - error: nil if the lock was acquired (or skipped), database.ErrLockNotAcquired
//            if another process holds it, or a wrapped Redis error.
func (s *WalletService) acquireWalletLock(ctx context.Context, walletID uuid.UUID, lockValue string) error {
	// If Redis is not configured (e.g. in tests), skip the distributed lock.
	// The PostgreSQL FOR UPDATE row lock still provides full race-condition
	// protection; the Redis lock is a secondary defense layer.
	if s.redisClient == nil {
		return nil
	}

	// Build the lock key: "wallet_lock:{wallet_id}"
	lockKey := lockKeyPrefix + walletID.String()

	// Attempt to acquire the lock with the default 10-second TTL.
	// If another process already holds the lock, this returns
	// database.ErrLockNotAcquired immediately (no blocking).
	if err := database.AcquireLock(ctx, s.redisClient, lockKey, lockValue, database.DefaultLockTTL); err != nil {
		return fmt.Errorf("acquire wallet lock for %s: %w", walletID.String(), err)
	}

	return nil
}

// releaseWalletLock releases a previously acquired Redis distributed lock.
// It uses a Lua script to atomically verify that the lock is still held by
// the caller before deleting, preventing accidental release of another
// process's lock.
//
// This should always be called via defer immediately after a successful
// acquireWalletLock call. If the Redis client is nil (test mode), this is
// a no-op.
//
// Parameters:
//   - ctx:       request-scoped context.
//   - walletID:  the UUID of the wallet to unlock.
//   - lockValue: the same unique string used when acquiring the lock.
func (s *WalletService) releaseWalletLock(ctx context.Context, walletID uuid.UUID, lockValue string) {
	// Skip if Redis is not configured (e.g. in tests).
	if s.redisClient == nil {
		return
	}

	lockKey := lockKeyPrefix + walletID.String()

	// Best-effort release: if this fails (e.g. Redis is down), the lock
	// will auto-expire after the TTL. We log the error but do not propagate
	// it because the critical section has already completed.
	if err := database.ReleaseLock(ctx, s.redisClient, lockKey, lockValue); err != nil {
		logger.Error("failed to release wallet lock",
			"wallet_id", walletID.String(),
			"error", err,
		)
	}
}

// -------------------------------------------------------------------------
// GetBalance (read-only, no lock needed)
// -------------------------------------------------------------------------

// GetBalance retrieves the current balance and hold_balance for a wallet
// identified by (ownerType, ownerID, currency). This is a read-only
// operation that does NOT acquire any locks because stale reads are
// acceptable for balance inquiries (the balance may change between the
// read and the caller's use of the value).
//
// Parameters:
//   - ctx:       request-scoped context for cancellation/deadline propagation.
//   - ownerType: the category of wallet owner (merchant, agent, partner, system).
//   - ownerID:   the UUID of the owning entity.
//   - currency:  ISO 4217 currency code (e.g. "THB").
//
// Returns:
//   - balance:     the wallet's available balance as a decimal.Decimal.
//   - holdBalance: the amount currently held (reserved for pending withdrawals).
//   - error:       nil on success, or repository.ErrWalletNotFound / DB error.
func (s *WalletService) GetBalance(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, currency string) (decimal.Decimal, decimal.Decimal, error) {
	// Delegate directly to the repository; no locking or transactions needed for reads.
	wallet, err := s.repo.GetByOwner(ctx, ownerType, ownerID, currency)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("get balance: %w", err)
	}

	return wallet.Balance, wallet.HoldBalance, nil
}

// -------------------------------------------------------------------------
// Credit — atomic balance addition
// -------------------------------------------------------------------------

// Credit adds a positive amount to a wallet's balance using the full
// concurrency protection stack:
//
//  1. Idempotency check: if a ledger entry with this refID already exists,
//     return success immediately without re-processing.
//  2. Redis distributed lock: acquire "wallet_lock:{walletID}" to prevent
//     concurrent credit/debit/hold/release operations on the same wallet.
//  3. PostgreSQL transaction with FOR UPDATE: read the wallet with an
//     exclusive row lock, compute the new balance, update balance + version,
//     and insert a ledger entry — all in a single atomic transaction.
//  4. Release the Redis lock (via defer).
//
// Parameters:
//   - ctx:         request-scoped context.
//   - walletID:    the UUID of the wallet to credit.
//   - amount:      the amount to add; must be positive.
//   - entryType:   the ledger entry type (e.g. LedgerDepositCredit).
//   - refType:     a human-readable reference category (e.g. "deposit_order").
//   - refID:       the UUID of the originating entity (e.g. the deposit order ID).
//                  Used for idempotency: duplicate refIDs are silently ignored.
//   - description: a free-text note recorded in the ledger entry.
//
// Returns:
//   - error: nil on success, or a descriptive error on failure.
func (s *WalletService) Credit(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, entryType models.LedgerEntryType, refType string, refID uuid.UUID, description string) error {
	// ---------------------------------------------------------------
	// Step 0: Input validation.
	// Guard: amount must be strictly positive to prevent no-op or negative credits.
	// ---------------------------------------------------------------
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("credit amount must be positive, got %s", amount.String())
	}

	// ---------------------------------------------------------------
	// Step 1: Idempotency check.
	// If a ledger entry with this reference_id already exists, it means
	// this exact credit was already processed (possibly the client is
	// retrying due to a timeout). Return success without re-processing
	// to prevent double-crediting the wallet.
	// ---------------------------------------------------------------
	exists, err := s.repo.LedgerEntryExistsByRef(ctx, refID)
	if err != nil {
		return fmt.Errorf("credit: idempotency check: %w", err)
	}
	if exists {
		// Already processed — return success silently.
		// This is the correct behavior for idempotent operations.
		logger.Info("credit: idempotent skip, ledger entry already exists",
			"wallet_id", walletID.String(),
			"reference_id", refID.String(),
		)
		return nil
	}

	// ---------------------------------------------------------------
	// Step 2: Acquire Redis distributed lock for this wallet.
	// This is the first line of defense: it prevents concurrent
	// balance-modifying operations from reaching the database.
	// ---------------------------------------------------------------
	lockValue := uuid.New().String() // Unique value to identify this lock holder.
	if err := s.acquireWalletLock(ctx, walletID, lockValue); err != nil {
		return fmt.Errorf("credit: %w", err)
	}
	// Always release the lock when we're done, regardless of success or failure.
	defer s.releaseWalletLock(ctx, walletID, lockValue)

	// ---------------------------------------------------------------
	// Step 3: Begin a PostgreSQL transaction.
	// All subsequent operations (read + update + ledger insert) happen
	// within this transaction and are committed or rolled back atomically.
	// ---------------------------------------------------------------
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("credit: begin tx: %w", err)
	}
	// Rollback is a no-op if the transaction was already committed.
	// This defer ensures cleanup on any error path.
	defer tx.Rollback(ctx)

	// ---------------------------------------------------------------
	// Step 4: Read the wallet with an exclusive row lock (FOR UPDATE).
	// This prevents any other transaction from reading or modifying
	// this wallet row until we commit.
	// ---------------------------------------------------------------
	wallet, err := s.repo.GetByIDForUpdate(ctx, tx, walletID)
	if err != nil {
		return fmt.Errorf("credit: read wallet for update: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 5: Compute the new balance after the credit.
	// Credits always succeed (no "insufficient funds" check needed)
	// because adding money can never cause a negative balance.
	// ---------------------------------------------------------------
	newBalance := wallet.Balance.Add(amount)

	// ---------------------------------------------------------------
	// Step 6: Update the balance with the optimistic version check.
	// The version check is defense-in-depth; the FOR UPDATE lock
	// already guarantees we're the only writer.
	// ---------------------------------------------------------------
	if err := s.repo.UpdateBalanceInTx(ctx, tx, walletID, newBalance.String(), wallet.HoldBalance.String(), wallet.Version); err != nil {
		return fmt.Errorf("credit: update balance: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 7: Create the ledger entry in the same transaction.
	// This ensures the balance change and its audit trail are committed
	// atomically — no more inconsistent state if the ledger insert fails.
	// ---------------------------------------------------------------
	ledgerEntry := &models.WalletLedger{
		WalletID:      walletID,
		EntryType:     entryType,
		ReferenceType: refType,
		ReferenceID:   refID,
		Amount:        amount,
		BalanceAfter:  newBalance,
		Description:   description,
		CreatedAt:     time.Now().UTC(),
	}

	if err := s.repo.CreateLedgerEntryInTx(ctx, tx, ledgerEntry); err != nil {
		return fmt.Errorf("credit: create ledger entry: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 8: Commit the transaction.
	// This makes the balance update and ledger entry visible to other
	// transactions and releases the FOR UPDATE row lock.
	// ---------------------------------------------------------------
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("credit: commit tx: %w", err)
	}

	// Success: credit applied and ledger entry created atomically.
	logger.Info("wallet credited",
		"wallet_id", walletID.String(),
		"amount", amount.String(),
		"new_balance", newBalance.String(),
		"entry_type", string(entryType),
	)
	return nil
}

// -------------------------------------------------------------------------
// Debit — atomic balance subtraction
// -------------------------------------------------------------------------

// Debit subtracts a positive amount from a wallet's balance using the full
// concurrency protection stack (idempotency + Redis lock + PG transaction
// with FOR UPDATE). Before applying the debit, it verifies that the wallet's
// current balance is sufficient to cover the amount, preventing negative
// balances.
//
// The entire operation — balance check, balance update, and ledger insert —
// executes inside a single PostgreSQL transaction with a row-level lock,
// eliminating the TOCTOU race condition where two concurrent debits could
// both pass the balance check and overdraw the wallet.
//
// Parameters:
//   - ctx:         request-scoped context.
//   - walletID:    the UUID of the wallet to debit.
//   - amount:      the amount to subtract; must be positive.
//   - entryType:   the ledger entry type (e.g. LedgerWithdrawalDebit).
//   - refType:     a human-readable reference category.
//   - refID:       the UUID of the originating entity. Used for idempotency.
//   - description: a free-text note recorded in the ledger entry.
//
// Returns:
//   - error: nil on success, ErrInsufficientFunds if balance < amount,
//            or a descriptive error on failure.
func (s *WalletService) Debit(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, entryType models.LedgerEntryType, refType string, refID uuid.UUID, description string) error {
	// ---------------------------------------------------------------
	// Step 0: Input validation.
	// ---------------------------------------------------------------
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("debit amount must be positive, got %s", amount.String())
	}

	// ---------------------------------------------------------------
	// Step 1: Idempotency check.
	// If this reference_id was already processed, skip re-processing.
	// ---------------------------------------------------------------
	exists, err := s.repo.LedgerEntryExistsByRef(ctx, refID)
	if err != nil {
		return fmt.Errorf("debit: idempotency check: %w", err)
	}
	if exists {
		logger.Info("debit: idempotent skip, ledger entry already exists",
			"wallet_id", walletID.String(),
			"reference_id", refID.String(),
		)
		return nil
	}

	// ---------------------------------------------------------------
	// Step 2: Acquire Redis distributed lock.
	// ---------------------------------------------------------------
	lockValue := uuid.New().String()
	if err := s.acquireWalletLock(ctx, walletID, lockValue); err != nil {
		return fmt.Errorf("debit: %w", err)
	}
	defer s.releaseWalletLock(ctx, walletID, lockValue)

	// ---------------------------------------------------------------
	// Step 3: Begin PostgreSQL transaction.
	// ---------------------------------------------------------------
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("debit: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// ---------------------------------------------------------------
	// Step 4: Read wallet with FOR UPDATE lock.
	// ---------------------------------------------------------------
	wallet, err := s.repo.GetByIDForUpdate(ctx, tx, walletID)
	if err != nil {
		return fmt.Errorf("debit: read wallet for update: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 5: Check sufficient funds.
	// Because we hold the FOR UPDATE lock, no other transaction can
	// modify the balance between this check and the update below.
	// This eliminates the TOCTOU vulnerability.
	// ---------------------------------------------------------------
	if wallet.Balance.LessThan(amount) {
		// Rollback is handled by defer. Return the insufficient funds error.
		return fmt.Errorf("debit: insufficient funds: balance=%s, requested=%s: %w",
			wallet.Balance.String(), amount.String(), ErrInsufficientFunds)
	}

	// ---------------------------------------------------------------
	// Step 6: Compute new balance and update.
	// ---------------------------------------------------------------
	newBalance := wallet.Balance.Sub(amount)

	if err := s.repo.UpdateBalanceInTx(ctx, tx, walletID, newBalance.String(), wallet.HoldBalance.String(), wallet.Version); err != nil {
		return fmt.Errorf("debit: update balance: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 7: Create ledger entry in the same transaction.
	// ---------------------------------------------------------------
	ledgerEntry := &models.WalletLedger{
		WalletID:      walletID,
		EntryType:     entryType,
		ReferenceType: refType,
		ReferenceID:   refID,
		Amount:        amount.Neg(), // Debits are stored as negative amounts in the ledger.
		BalanceAfter:  newBalance,
		Description:   description,
		CreatedAt:     time.Now().UTC(),
	}

	if err := s.repo.CreateLedgerEntryInTx(ctx, tx, ledgerEntry); err != nil {
		return fmt.Errorf("debit: create ledger entry: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 8: Commit the transaction atomically.
	// ---------------------------------------------------------------
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("debit: commit tx: %w", err)
	}

	logger.Info("wallet debited",
		"wallet_id", walletID.String(),
		"amount", amount.String(),
		"new_balance", newBalance.String(),
		"entry_type", string(entryType),
	)
	return nil
}

// -------------------------------------------------------------------------
// Hold — atomic balance reservation
// -------------------------------------------------------------------------

// Hold moves an amount from the wallet's available balance into the
// hold_balance. Held funds are reserved for a pending operation (typically
// a withdrawal) and cannot be spent until they are either released back
// to the balance or consumed by a finalised debit.
//
// This is the operation most vulnerable to the TOCTOU race condition:
// in the old code, two concurrent withdrawal requests could both read
// balance=1000, both check 1000 >= 800, and both subtract 800, leaving
// the wallet with -600 available. The new implementation prevents this by:
//
//  1. Acquiring a Redis distributed lock per wallet_id (fast-fail for concurrent requests).
//  2. Using SELECT ... FOR UPDATE to lock the wallet row for the duration of the transaction.
//  3. Performing the balance check, balance update, and ledger insert atomically.
//
// Parameters:
//   - ctx:      request-scoped context.
//   - walletID: the UUID of the wallet.
//   - amount:   the amount to hold; must be positive.
//   - refType:  a reference category (e.g. "withdrawal_request").
//   - refID:    the UUID of the originating entity. Used for idempotency.
//
// Returns:
//   - error: nil on success, ErrInsufficientFunds if balance < amount,
//            or a descriptive error on failure.
func (s *WalletService) Hold(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, refType string, refID uuid.UUID) error {
	// ---------------------------------------------------------------
	// Step 0: Input validation.
	// ---------------------------------------------------------------
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("hold amount must be positive, got %s", amount.String())
	}

	// ---------------------------------------------------------------
	// Step 1: Idempotency check.
	// If a hold was already placed for this reference_id, skip.
	// ---------------------------------------------------------------
	exists, err := s.repo.LedgerEntryExistsByRef(ctx, refID)
	if err != nil {
		return fmt.Errorf("hold: idempotency check: %w", err)
	}
	if exists {
		logger.Info("hold: idempotent skip, ledger entry already exists",
			"wallet_id", walletID.String(),
			"reference_id", refID.String(),
		)
		return nil
	}

	// ---------------------------------------------------------------
	// Step 2: Acquire Redis distributed lock.
	// Key format: "wallet_lock:{wallet_id}" with 10-second TTL.
	// This prevents concurrent hold/credit/debit/release operations
	// on the same wallet from reaching the database simultaneously.
	// ---------------------------------------------------------------
	lockValue := uuid.New().String()
	if err := s.acquireWalletLock(ctx, walletID, lockValue); err != nil {
		return fmt.Errorf("hold: %w", err)
	}
	defer s.releaseWalletLock(ctx, walletID, lockValue)

	// ---------------------------------------------------------------
	// Step 3: Begin PostgreSQL transaction.
	// ---------------------------------------------------------------
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("hold: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// ---------------------------------------------------------------
	// Step 4: Read wallet with FOR UPDATE lock.
	// The row-level lock prevents any concurrent transaction from
	// reading (with FOR UPDATE) or modifying this wallet row until
	// our transaction commits.
	// ---------------------------------------------------------------
	wallet, err := s.repo.GetByIDForUpdate(ctx, tx, walletID)
	if err != nil {
		return fmt.Errorf("hold: read wallet for update: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 5: Verify sufficient available balance.
	// Because the FOR UPDATE lock serialises access, no other
	// transaction can change the balance between this check and the
	// update below. This is the key fix for the race condition.
	// ---------------------------------------------------------------
	if wallet.Balance.LessThan(amount) {
		return fmt.Errorf("hold: insufficient funds: balance=%s, requested=%s: %w",
			wallet.Balance.String(), amount.String(), ErrInsufficientFunds)
	}

	// ---------------------------------------------------------------
	// Step 6: Compute new values and update balance.
	// balance decreases, hold_balance increases by the same amount.
	// ---------------------------------------------------------------
	newBalance := wallet.Balance.Sub(amount)
	newHold := wallet.HoldBalance.Add(amount)

	if err := s.repo.UpdateBalanceInTx(ctx, tx, walletID, newBalance.String(), newHold.String(), wallet.Version); err != nil {
		return fmt.Errorf("hold: update balance: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 7: Create ledger entry atomically in the same transaction.
	// ---------------------------------------------------------------
	ledgerEntry := &models.WalletLedger{
		WalletID:      walletID,
		EntryType:     models.LedgerWithdrawalHold,
		ReferenceType: refType,
		ReferenceID:   refID,
		Amount:        amount.Neg(), // Negative because available balance decreased.
		BalanceAfter:  newBalance,
		Description:   fmt.Sprintf("hold %s for %s %s", amount.String(), refType, refID.String()),
		CreatedAt:     time.Now().UTC(),
	}

	if err := s.repo.CreateLedgerEntryInTx(ctx, tx, ledgerEntry); err != nil {
		return fmt.Errorf("hold: create ledger entry: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 8: Commit the transaction.
	// Both the balance update and ledger entry become visible, and
	// the FOR UPDATE row lock is released.
	// ---------------------------------------------------------------
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("hold: commit tx: %w", err)
	}

	logger.Info("wallet hold placed",
		"wallet_id", walletID.String(),
		"amount", amount.String(),
		"new_balance", newBalance.String(),
		"new_hold", newHold.String(),
	)
	return nil
}

// -------------------------------------------------------------------------
// Release — atomic hold reversal
// -------------------------------------------------------------------------

// Release moves an amount from the wallet's hold_balance back into the
// available balance. This is used when a pending operation (e.g. a
// withdrawal) is cancelled or rejected, and the reserved funds should be
// returned to the owner.
//
// Like Hold, this uses the full concurrency protection stack (idempotency
// check + Redis lock + PG transaction with FOR UPDATE).
//
// Parameters:
//   - ctx:      request-scoped context.
//   - walletID: the UUID of the wallet.
//   - amount:   the amount to release; must be positive and <= current hold_balance.
//   - refType:  a reference category (e.g. "withdrawal_request").
//   - refID:    the UUID of the originating entity. Used for idempotency.
//
// Returns:
//   - error: nil on success, or a descriptive error on failure.
func (s *WalletService) Release(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, refType string, refID uuid.UUID) error {
	// ---------------------------------------------------------------
	// Step 0: Input validation.
	// ---------------------------------------------------------------
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("release amount must be positive, got %s", amount.String())
	}

	// ---------------------------------------------------------------
	// Step 1: Idempotency check.
	// ---------------------------------------------------------------
	exists, err := s.repo.LedgerEntryExistsByRef(ctx, refID)
	if err != nil {
		return fmt.Errorf("release: idempotency check: %w", err)
	}
	if exists {
		logger.Info("release: idempotent skip, ledger entry already exists",
			"wallet_id", walletID.String(),
			"reference_id", refID.String(),
		)
		return nil
	}

	// ---------------------------------------------------------------
	// Step 2: Acquire Redis distributed lock.
	// ---------------------------------------------------------------
	lockValue := uuid.New().String()
	if err := s.acquireWalletLock(ctx, walletID, lockValue); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	defer s.releaseWalletLock(ctx, walletID, lockValue)

	// ---------------------------------------------------------------
	// Step 3: Begin PostgreSQL transaction.
	// ---------------------------------------------------------------
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("release: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// ---------------------------------------------------------------
	// Step 4: Read wallet with FOR UPDATE lock.
	// ---------------------------------------------------------------
	wallet, err := s.repo.GetByIDForUpdate(ctx, tx, walletID)
	if err != nil {
		return fmt.Errorf("release: read wallet for update: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 5: Verify sufficient hold balance.
	// ---------------------------------------------------------------
	if wallet.HoldBalance.LessThan(amount) {
		return fmt.Errorf("release: hold_balance (%s) is less than release amount (%s)",
			wallet.HoldBalance.String(), amount.String())
	}

	// ---------------------------------------------------------------
	// Step 6: Compute new values and update balance.
	// balance increases, hold_balance decreases by the same amount.
	// ---------------------------------------------------------------
	newBalance := wallet.Balance.Add(amount)
	newHold := wallet.HoldBalance.Sub(amount)

	if err := s.repo.UpdateBalanceInTx(ctx, tx, walletID, newBalance.String(), newHold.String(), wallet.Version); err != nil {
		return fmt.Errorf("release: update balance: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 7: Create ledger entry atomically.
	// ---------------------------------------------------------------
	ledgerEntry := &models.WalletLedger{
		WalletID:      walletID,
		EntryType:     models.LedgerWithdrawalRelease,
		ReferenceType: refType,
		ReferenceID:   refID,
		Amount:        amount, // Positive because available balance increased.
		BalanceAfter:  newBalance,
		Description:   fmt.Sprintf("release %s for %s %s", amount.String(), refType, refID.String()),
		CreatedAt:     time.Now().UTC(),
	}

	if err := s.repo.CreateLedgerEntryInTx(ctx, tx, ledgerEntry); err != nil {
		return fmt.Errorf("release: create ledger entry: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 8: Commit the transaction.
	// ---------------------------------------------------------------
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("release: commit tx: %w", err)
	}

	logger.Info("wallet hold released",
		"wallet_id", walletID.String(),
		"amount", amount.String(),
		"new_balance", newBalance.String(),
		"new_hold", newHold.String(),
	)
	return nil
}

// -------------------------------------------------------------------------
// EnsureWalletExists
// -------------------------------------------------------------------------

// EnsureWalletExists guarantees that a wallet exists for the given
// (ownerType, ownerID, currency) triple. If the wallet already exists,
// its ID is returned. If it does not, a new wallet is created with zero
// balance and version 1, and its ID is returned.
//
// This method is idempotent: calling it multiple times with the same
// arguments always yields the same wallet ID without creating duplicates,
// thanks to the INSERT ... ON CONFLICT DO NOTHING pattern in the
// repository.
//
// No distributed lock is needed here because:
// - The CREATE uses ON CONFLICT DO NOTHING, so concurrent creates are safe.
// - No balance is being modified.
//
// Parameters:
//   - ctx:       request-scoped context.
//   - ownerType: the category of wallet owner.
//   - ownerID:   the UUID of the owning entity.
//   - currency:  ISO 4217 currency code.
//
// Returns:
//   - uuid.UUID: the wallet's primary-key UUID (new or existing).
//   - error:     nil on success, or a wrapped DB error.
func (s *WalletService) EnsureWalletExists(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, currency string) (uuid.UUID, error) {
	// Step 1: Try to find an existing wallet for this owner+currency.
	existing, err := s.repo.GetByOwner(ctx, ownerType, ownerID, currency)
	if err == nil {
		// Wallet already exists; return its ID immediately.
		return existing.ID, nil
	}

	// If the error is anything other than "not found", it's unexpected.
	if err != repository.ErrWalletNotFound {
		return uuid.Nil, fmt.Errorf("ensure wallet exists: lookup: %w", err)
	}

	// Step 2: Wallet does not exist yet. Create a new one with zero
	// balance and version 1. The ON CONFLICT DO NOTHING clause in the
	// repository handles the race where two concurrent requests both
	// reach this point for the same owner+currency.
	now := time.Now().UTC()
	newWallet := &models.Wallet{
		ID:          uuid.New(),
		OwnerType:   ownerType,
		OwnerID:     ownerID,
		Currency:    currency,
		Balance:     decimal.Zero,
		HoldBalance: decimal.Zero,
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.Create(ctx, newWallet); err != nil {
		return uuid.Nil, fmt.Errorf("ensure wallet exists: create: %w", err)
	}

	// Step 3: Re-read the wallet to get the definitive ID. If a concurrent
	// request created the wallet first (and our INSERT was a no-op), this
	// read returns the existing wallet's ID rather than the one we
	// generated above.
	created, err := s.repo.GetByOwner(ctx, ownerType, ownerID, currency)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ensure wallet exists: re-read: %w", err)
	}

	logger.Info("wallet ensured",
		"wallet_id", created.ID.String(),
		"owner_type", string(ownerType),
		"owner_id", ownerID.String(),
		"currency", currency,
	)

	return created.ID, nil
}

// -------------------------------------------------------------------------
// Sentinel errors (service-level)
// -------------------------------------------------------------------------

// ErrInsufficientFunds is returned when a debit or hold operation
// requests more than the wallet's available balance. It wraps the
// shared-pkg AppError for consistent HTTP status code mapping.
var ErrInsufficientFunds = fmt.Errorf("insufficient funds")
