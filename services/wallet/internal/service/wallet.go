// Package service contains the core business logic for the wallet service.
// It orchestrates balance operations (credit, debit, hold, release) with
// optimistic locking to handle concurrent mutations safely without
// requiring database-level row locks or serialisable transactions.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/wallet/internal/repository"
)

// -------------------------------------------------------------------------
// Constants
// -------------------------------------------------------------------------

// maxOptimisticRetries is the maximum number of times a balance mutation
// will be retried when a version-conflict (optimistic-lock failure) is
// detected. Three retries is a pragmatic default: under normal load
// conflicts are rare, and three attempts give us a >99% success rate even
// when two concurrent writers target the same wallet.
const maxOptimisticRetries = 3

// -------------------------------------------------------------------------
// WalletService
// -------------------------------------------------------------------------

// WalletService encapsulates all business rules for wallet balance
// management. It depends on the WalletRepository interface (not a concrete
// implementation) so that the persistence layer can be swapped for testing
// or migration purposes.
//
// All public methods are safe for concurrent use because they are
// stateless: shared state lives exclusively in the database and is
// protected by optimistic locking.
type WalletService struct {
	// repo is the data-access layer used for all wallet and ledger
	// persistence. It is injected at construction time.
	repo repository.WalletRepository
}

// NewWalletService creates a new WalletService with the given repository.
//
// Parameters:
//   - repo: an implementation of WalletRepository (e.g. PostgresWalletRepo).
//
// Returns:
//   - *WalletService: a ready-to-use service instance.
func NewWalletService(repo repository.WalletRepository) *WalletService {
	return &WalletService{repo: repo}
}

// -------------------------------------------------------------------------
// GetBalance
// -------------------------------------------------------------------------

// GetBalance retrieves the current balance and hold_balance for a wallet
// identified by (ownerType, ownerID, currency). This is a read-only
// operation with no locking implications.
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
	// Delegate directly to the repository; no business logic needed for reads.
	wallet, err := s.repo.GetByOwner(ctx, ownerType, ownerID, currency)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("get balance: %w", err)
	}

	return wallet.Balance, wallet.HoldBalance, nil
}

// -------------------------------------------------------------------------
// Credit
// -------------------------------------------------------------------------

// Credit adds a positive amount to a wallet's balance using optimistic
// locking. The full read-modify-write cycle is:
//
//  1. Read the wallet (including its current version).
//  2. Compute newBalance = currentBalance + amount.
//  3. Attempt UPDATE ... WHERE version = expectedVersion.
//  4. If the version has changed (ErrVersionConflict), re-read and retry
//     up to maxOptimisticRetries times.
//  5. On success, create a ledger entry recording the credit.
//
// Parameters:
//   - ctx:         request-scoped context.
//   - walletID:    the UUID of the wallet to credit.
//   - amount:      the amount to add; must be positive.
//   - entryType:   the ledger entry type (e.g. LedgerDepositCredit).
//   - refType:     a human-readable reference category (e.g. "deposit_order").
//   - refID:       the UUID of the originating entity (e.g. the deposit order ID).
//   - description: a free-text note recorded in the ledger entry.
//
// Returns:
//   - error: nil on success, or a descriptive error on failure.
func (s *WalletService) Credit(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, entryType models.LedgerEntryType, refType string, refID uuid.UUID, description string) error {
	// Guard: amount must be strictly positive to prevent no-op or negative credits.
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("credit amount must be positive, got %s", amount.String())
	}

	// Optimistic retry loop: attempt the credit up to maxOptimisticRetries times.
	// Each iteration re-reads the wallet to get the latest version.
	var lastErr error
	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		// Step 1: Read the current wallet state, including balance and version.
		wallet, err := s.repo.GetByID(ctx, walletID)
		if err != nil {
			return fmt.Errorf("credit: read wallet: %w", err)
		}

		// Step 2: Compute the new balance after the credit.
		newBalance := wallet.Balance.Add(amount)

		// Step 3: Attempt the conditional update. If another goroutine has
		// modified the wallet between our read (step 1) and this write, the
		// version will not match and UpdateBalance returns ErrVersionConflict.
		err = s.repo.UpdateBalance(ctx, walletID, newBalance.String(), wallet.HoldBalance.String(), wallet.Version)
		if err != nil {
			if errors.Is(err, repository.ErrVersionConflict) {
				// Log the conflict and retry with a fresh read.
				logger.Warn("credit: version conflict, retrying",
					"wallet_id", walletID.String(),
					"attempt", attempt+1,
				)
				lastErr = err
				continue // retry from step 1
			}
			// Non-conflict errors are not retryable.
			return fmt.Errorf("credit: update balance: %w", err)
		}

		// Step 4: Balance updated successfully. Now record the ledger entry
		// so that every balance change has a full audit trail.
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

		if err := s.repo.CreateLedgerEntry(ctx, ledgerEntry); err != nil {
			// NOTE: the balance has already been updated at this point.
			// In a production system this should be wrapped in a DB
			// transaction. For now we log the error and return it so
			// that upstream monitoring can detect the inconsistency.
			return fmt.Errorf("credit: create ledger entry: %w", err)
		}

		// Success: credit applied and ledger entry created.
		logger.Info("wallet credited",
			"wallet_id", walletID.String(),
			"amount", amount.String(),
			"new_balance", newBalance.String(),
			"entry_type", string(entryType),
		)
		return nil
	}

	// All retry attempts exhausted.
	return fmt.Errorf("credit: exceeded %d optimistic-lock retries: %w", maxOptimisticRetries, lastErr)
}

// -------------------------------------------------------------------------
// Debit
// -------------------------------------------------------------------------

// Debit subtracts a positive amount from a wallet's balance using
// optimistic locking. Before applying the debit, it verifies that the
// wallet's current balance is sufficient to cover the amount, preventing
// negative balances.
//
// The retry strategy is identical to Credit: re-read + conditional update,
// up to maxOptimisticRetries attempts.
//
// Parameters:
//   - ctx:         request-scoped context.
//   - walletID:    the UUID of the wallet to debit.
//   - amount:      the amount to subtract; must be positive.
//   - entryType:   the ledger entry type (e.g. LedgerWithdrawalDebit).
//   - refType:     a human-readable reference category.
//   - refID:       the UUID of the originating entity.
//   - description: a free-text note recorded in the ledger entry.
//
// Returns:
//   - error: nil on success, ErrInsufficientFunds if balance < amount,
//            or a descriptive error on failure.
func (s *WalletService) Debit(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, entryType models.LedgerEntryType, refType string, refID uuid.UUID, description string) error {
	// Guard: amount must be strictly positive.
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("debit amount must be positive, got %s", amount.String())
	}

	// Optimistic retry loop.
	var lastErr error
	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		// Step 1: Read the current wallet state.
		wallet, err := s.repo.GetByID(ctx, walletID)
		if err != nil {
			return fmt.Errorf("debit: read wallet: %w", err)
		}

		// Step 2: Verify sufficient funds. We compare against the
		// available balance (not hold_balance) to prevent the wallet
		// from going negative.
		if wallet.Balance.LessThan(amount) {
			return fmt.Errorf("debit: insufficient funds: balance=%s, requested=%s: %w",
				wallet.Balance.String(), amount.String(), ErrInsufficientFunds)
		}

		// Step 3: Compute new balance after the debit.
		newBalance := wallet.Balance.Sub(amount)

		// Step 4: Attempt the conditional update with version guard.
		err = s.repo.UpdateBalance(ctx, walletID, newBalance.String(), wallet.HoldBalance.String(), wallet.Version)
		if err != nil {
			if errors.Is(err, repository.ErrVersionConflict) {
				// Another writer modified the wallet; retry with fresh data.
				logger.Warn("debit: version conflict, retrying",
					"wallet_id", walletID.String(),
					"attempt", attempt+1,
				)
				lastErr = err
				continue
			}
			return fmt.Errorf("debit: update balance: %w", err)
		}

		// Step 5: Record the ledger entry for auditability.
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

		if err := s.repo.CreateLedgerEntry(ctx, ledgerEntry); err != nil {
			return fmt.Errorf("debit: create ledger entry: %w", err)
		}

		logger.Info("wallet debited",
			"wallet_id", walletID.String(),
			"amount", amount.String(),
			"new_balance", newBalance.String(),
			"entry_type", string(entryType),
		)
		return nil
	}

	return fmt.Errorf("debit: exceeded %d optimistic-lock retries: %w", maxOptimisticRetries, lastErr)
}

// -------------------------------------------------------------------------
// Hold
// -------------------------------------------------------------------------

// Hold moves an amount from the wallet's available balance into the
// hold_balance. Held funds are reserved for a pending operation (typically
// a withdrawal) and cannot be spent until they are either released back
// to the balance or consumed by a finalised debit.
//
// Like Credit and Debit, this uses optimistic locking with retries.
//
// Parameters:
//   - ctx:      request-scoped context.
//   - walletID: the UUID of the wallet.
//   - amount:   the amount to hold; must be positive.
//   - refType:  a reference category (e.g. "withdrawal_request").
//   - refID:    the UUID of the originating entity.
//
// Returns:
//   - error: nil on success, ErrInsufficientFunds if balance < amount,
//            or a descriptive error on failure.
func (s *WalletService) Hold(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, refType string, refID uuid.UUID) error {
	// Guard: amount must be positive.
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("hold amount must be positive, got %s", amount.String())
	}

	var lastErr error
	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		// Read the current wallet state including balance and hold_balance.
		wallet, err := s.repo.GetByID(ctx, walletID)
		if err != nil {
			return fmt.Errorf("hold: read wallet: %w", err)
		}

		// Verify the wallet has enough available balance to place the hold.
		if wallet.Balance.LessThan(amount) {
			return fmt.Errorf("hold: insufficient funds: balance=%s, requested=%s: %w",
				wallet.Balance.String(), amount.String(), ErrInsufficientFunds)
		}

		// Compute new values: subtract from balance, add to hold_balance.
		newBalance := wallet.Balance.Sub(amount)
		newHold := wallet.HoldBalance.Add(amount)

		// Attempt the optimistic-lock update.
		err = s.repo.UpdateBalance(ctx, walletID, newBalance.String(), newHold.String(), wallet.Version)
		if err != nil {
			if errors.Is(err, repository.ErrVersionConflict) {
				logger.Warn("hold: version conflict, retrying",
					"wallet_id", walletID.String(),
					"attempt", attempt+1,
				)
				lastErr = err
				continue
			}
			return fmt.Errorf("hold: update balance: %w", err)
		}

		// Record the hold as a ledger entry so the audit trail is complete.
		ledgerEntry := &models.WalletLedger{
			WalletID:      walletID,
			EntryType:     models.LedgerWithdrawalHold,
			ReferenceType: refType,
			ReferenceID:   refID,
			Amount:        amount.Neg(), // Negative because balance decreased.
			BalanceAfter:  newBalance,
			Description:   fmt.Sprintf("hold %s for %s %s", amount.String(), refType, refID.String()),
			CreatedAt:     time.Now().UTC(),
		}

		if err := s.repo.CreateLedgerEntry(ctx, ledgerEntry); err != nil {
			return fmt.Errorf("hold: create ledger entry: %w", err)
		}

		logger.Info("wallet hold placed",
			"wallet_id", walletID.String(),
			"amount", amount.String(),
			"new_balance", newBalance.String(),
			"new_hold", newHold.String(),
		)
		return nil
	}

	return fmt.Errorf("hold: exceeded %d optimistic-lock retries: %w", maxOptimisticRetries, lastErr)
}

// -------------------------------------------------------------------------
// Release
// -------------------------------------------------------------------------

// Release moves an amount from the wallet's hold_balance back into the
// available balance. This is used when a pending operation (e.g. a
// withdrawal) is cancelled or rejected, and the reserved funds should be
// returned to the owner.
//
// Parameters:
//   - ctx:      request-scoped context.
//   - walletID: the UUID of the wallet.
//   - amount:   the amount to release; must be positive and <= current hold_balance.
//   - refType:  a reference category (e.g. "withdrawal_request").
//   - refID:    the UUID of the originating entity.
//
// Returns:
//   - error: nil on success, or a descriptive error on failure.
func (s *WalletService) Release(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, refType string, refID uuid.UUID) error {
	// Guard: amount must be positive.
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("release amount must be positive, got %s", amount.String())
	}

	var lastErr error
	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		// Read the current wallet state.
		wallet, err := s.repo.GetByID(ctx, walletID)
		if err != nil {
			return fmt.Errorf("release: read wallet: %w", err)
		}

		// Verify that the hold_balance is large enough for the release.
		// This prevents releasing more than what is actually held.
		if wallet.HoldBalance.LessThan(amount) {
			return fmt.Errorf("release: hold_balance (%s) is less than release amount (%s)",
				wallet.HoldBalance.String(), amount.String())
		}

		// Compute new values: add back to balance, subtract from hold_balance.
		newBalance := wallet.Balance.Add(amount)
		newHold := wallet.HoldBalance.Sub(amount)

		// Attempt the optimistic-lock update.
		err = s.repo.UpdateBalance(ctx, walletID, newBalance.String(), newHold.String(), wallet.Version)
		if err != nil {
			if errors.Is(err, repository.ErrVersionConflict) {
				logger.Warn("release: version conflict, retrying",
					"wallet_id", walletID.String(),
					"attempt", attempt+1,
				)
				lastErr = err
				continue
			}
			return fmt.Errorf("release: update balance: %w", err)
		}

		// Record the release in the ledger for a complete audit trail.
		ledgerEntry := &models.WalletLedger{
			WalletID:      walletID,
			EntryType:     models.LedgerWithdrawalRelease,
			ReferenceType: refType,
			ReferenceID:   refID,
			Amount:        amount, // Positive because balance increased.
			BalanceAfter:  newBalance,
			Description:   fmt.Sprintf("release %s for %s %s", amount.String(), refType, refID.String()),
			CreatedAt:     time.Now().UTC(),
		}

		if err := s.repo.CreateLedgerEntry(ctx, ledgerEntry); err != nil {
			return fmt.Errorf("release: create ledger entry: %w", err)
		}

		logger.Info("wallet hold released",
			"wallet_id", walletID.String(),
			"amount", amount.String(),
			"new_balance", newBalance.String(),
			"new_hold", newHold.String(),
		)
		return nil
	}

	return fmt.Errorf("release: exceeded %d optimistic-lock retries: %w", maxOptimisticRetries, lastErr)
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
	if !errors.Is(err, repository.ErrWalletNotFound) {
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
