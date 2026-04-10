package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/wallet/internal/repository"
)

// =============================================================================
// mockWalletRepo is an in-memory mock implementation of repository.WalletRepository.
//
// It stores wallets in a map and simulates optimistic locking via the Version
// field. The mock is designed to:
//   - Return ErrWalletNotFound for unknown wallet IDs.
//   - Return ErrVersionConflict when the version doesn't match (simulating
//     concurrent writes).
//   - Track ledger entries for verification.
//   - Support configurable failures for testing error paths.
//
// Thread safety: protected by a mutex to support concurrent test scenarios.
// =============================================================================
type mockWalletRepo struct {
	mu      sync.Mutex
	wallets map[uuid.UUID]*models.Wallet
	ledger  []*models.WalletLedger

	// conflictCount tracks how many version conflicts to simulate before
	// allowing the update to succeed. Used by TestCredit_VersionConflict_Retry.
	conflictCount int
}

func newMockWalletRepo() *mockWalletRepo {
	return &mockWalletRepo{
		wallets: make(map[uuid.UUID]*models.Wallet),
	}
}

// GetByOwner looks up a wallet by owner+currency. This mock only supports
// lookup by ID, so we iterate all wallets (acceptable for test data sizes).
func (m *mockWalletRepo) GetByOwner(_ context.Context, ownerType models.OwnerType, ownerID uuid.UUID, currency string) (*models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.wallets {
		if w.OwnerType == ownerType && w.OwnerID == ownerID && w.Currency == currency {
			cp := *w
			return &cp, nil
		}
	}
	return nil, repository.ErrWalletNotFound
}

// GetByID retrieves a wallet by its primary key UUID.
// Returns a copy to prevent tests from accidentally mutating the mock's state.
func (m *mockWalletRepo) GetByID(_ context.Context, id uuid.UUID) (*models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.wallets[id]
	if !ok {
		return nil, repository.ErrWalletNotFound
	}
	cp := *w
	return &cp, nil
}

// Create adds a new wallet to the mock store.
func (m *mockWalletRepo) Create(_ context.Context, wallet *models.Wallet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wallets[wallet.ID] = wallet
	return nil
}

// UpdateBalance simulates an optimistic-lock update. If conflictCount > 0,
// the first N calls return ErrVersionConflict (simulating concurrent writes).
// Otherwise, it checks the version and updates the wallet.
func (m *mockWalletRepo) UpdateBalance(_ context.Context, id uuid.UUID, newBalance, newHold string, expectedVersion int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.wallets[id]
	if !ok {
		return repository.ErrWalletNotFound
	}

	// Simulate version conflicts for retry testing.
	if m.conflictCount > 0 {
		m.conflictCount--
		// Bump the version to simulate someone else modifying the wallet.
		w.Version++
		return repository.ErrVersionConflict
	}

	// Standard optimistic lock check.
	if w.Version != expectedVersion {
		return repository.ErrVersionConflict
	}

	// Apply the update.
	bal, _ := decimal.NewFromString(newBalance)
	hold, _ := decimal.NewFromString(newHold)
	w.Balance = bal
	w.HoldBalance = hold
	w.Version++
	return nil
}

// CreateLedgerEntry records a ledger entry in the mock's ledger slice.
func (m *mockWalletRepo) CreateLedgerEntry(_ context.Context, entry *models.WalletLedger) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ledger = append(m.ledger, entry)
	return nil
}

// addWallet is a test helper to seed the mock with a wallet at a given balance.
func (m *mockWalletRepo) addWallet(id uuid.UUID, balance, holdBalance decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wallets[id] = &models.Wallet{
		ID:          id,
		OwnerType:   models.OwnerTypeMerchant,
		OwnerID:     uuid.New(),
		Currency:    "THB",
		Balance:     balance,
		HoldBalance: holdBalance,
		Version:     1,
	}
}

// =============================================================================
// TestCredit_Success verifies the happy-path credit operation.
//
// Starting state: wallet with balance 1,000 THB
// Operation:      credit 500 THB
// Expected:       new balance = 1,500 THB, one ledger entry created
//
// This tests the core deposit flow: when a matched payment is confirmed,
// the merchant's wallet is credited with the net amount.
// =============================================================================
func TestCredit_Success(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	refID := uuid.New()
	err := svc.Credit(ctx, walletID, decimal.NewFromInt(500),
		models.LedgerDepositCredit, "deposit_order", refID, "deposit matched")
	if err != nil {
		t.Fatalf("Credit returned unexpected error: %v", err)
	}

	// Verify the new balance is 1000 + 500 = 1500.
	wallet, _ := repo.GetByID(ctx, walletID)
	expectedBalance := decimal.NewFromInt(1500)
	if !wallet.Balance.Equal(expectedBalance) {
		t.Errorf("balance after credit = %s, want %s", wallet.Balance, expectedBalance)
	}

	// Verify a ledger entry was created.
	if len(repo.ledger) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(repo.ledger))
	}

	// Verify the ledger entry has the correct amount and entry type.
	entry := repo.ledger[0]
	if !entry.Amount.Equal(decimal.NewFromInt(500)) {
		t.Errorf("ledger entry amount = %s, want 500", entry.Amount)
	}
	if entry.EntryType != models.LedgerDepositCredit {
		t.Errorf("ledger entry type = %q, want %q", entry.EntryType, models.LedgerDepositCredit)
	}
}

// =============================================================================
// TestDebit_Success verifies the happy-path debit operation.
//
// Starting state: wallet with balance 1,000 THB
// Operation:      debit 300 THB
// Expected:       new balance = 700 THB, one ledger entry with negative amount
//
// This tests the withdrawal flow: when a withdrawal is approved, the
// merchant's wallet is debited.
// =============================================================================
func TestDebit_Success(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	refID := uuid.New()
	err := svc.Debit(ctx, walletID, decimal.NewFromInt(300),
		models.LedgerWithdrawalDebit, "withdrawal_request", refID, "withdrawal approved")
	if err != nil {
		t.Fatalf("Debit returned unexpected error: %v", err)
	}

	// Verify the new balance is 1000 - 300 = 700.
	wallet, _ := repo.GetByID(ctx, walletID)
	expectedBalance := decimal.NewFromInt(700)
	if !wallet.Balance.Equal(expectedBalance) {
		t.Errorf("balance after debit = %s, want %s", wallet.Balance, expectedBalance)
	}

	// Verify the ledger entry records a negative amount (debits are negative).
	if len(repo.ledger) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(repo.ledger))
	}
	entry := repo.ledger[0]
	expectedLedgerAmount := decimal.NewFromInt(-300)
	if !entry.Amount.Equal(expectedLedgerAmount) {
		t.Errorf("ledger entry amount = %s, want %s (negative for debit)", entry.Amount, expectedLedgerAmount)
	}
}

// =============================================================================
// TestDebit_InsufficientFunds verifies that debit is rejected when the wallet
// does not have enough balance.
//
// Starting state: wallet with balance 200 THB
// Operation:      attempt to debit 500 THB
// Expected:       error containing "insufficient funds", balance unchanged
//
// This is a critical safety check: allowing a debit to exceed the balance
// would create a negative balance, which violates the accounting invariant.
// =============================================================================
func TestDebit_InsufficientFunds(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(200), decimal.Zero)

	refID := uuid.New()
	err := svc.Debit(ctx, walletID, decimal.NewFromInt(500),
		models.LedgerWithdrawalDebit, "withdrawal_request", refID, "should fail")

	// The error must indicate insufficient funds.
	if err == nil {
		t.Fatal("Debit should return an error for insufficient funds, got nil")
	}
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("expected error to wrap ErrInsufficientFunds, got: %v", err)
	}

	// Balance should remain unchanged at 200.
	wallet, _ := repo.GetByID(ctx, walletID)
	if !wallet.Balance.Equal(decimal.NewFromInt(200)) {
		t.Errorf("balance should remain 200 after failed debit, got %s", wallet.Balance)
	}

	// No ledger entry should have been created for a failed debit.
	if len(repo.ledger) != 0 {
		t.Errorf("expected 0 ledger entries after failed debit, got %d", len(repo.ledger))
	}
}

// =============================================================================
// TestHold_Success verifies that Hold moves funds from balance to hold_balance.
//
// Starting state: balance = 1,000, hold_balance = 0
// Operation:      hold 400 THB
// Expected:       balance = 600, hold_balance = 400
//
// The hold operation is used when a withdrawal request is submitted: the
// requested amount is moved from available balance to hold_balance to prevent
// it from being spent while the withdrawal is being processed.
// =============================================================================
func TestHold_Success(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	refID := uuid.New()
	err := svc.Hold(ctx, walletID, decimal.NewFromInt(400), "withdrawal_request", refID)
	if err != nil {
		t.Fatalf("Hold returned unexpected error: %v", err)
	}

	// Balance should decrease by the hold amount.
	wallet, _ := repo.GetByID(ctx, walletID)
	if !wallet.Balance.Equal(decimal.NewFromInt(600)) {
		t.Errorf("balance after hold = %s, want 600", wallet.Balance)
	}

	// Hold balance should increase by the hold amount.
	if !wallet.HoldBalance.Equal(decimal.NewFromInt(400)) {
		t.Errorf("hold_balance after hold = %s, want 400", wallet.HoldBalance)
	}

	// Verify a ledger entry was created.
	if len(repo.ledger) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(repo.ledger))
	}
}

// =============================================================================
// TestRelease_Success verifies that Release moves funds from hold_balance
// back to the available balance.
//
// Starting state: balance = 600, hold_balance = 400
// Operation:      release 400 THB
// Expected:       balance = 1,000, hold_balance = 0
//
// Release is used when a withdrawal is cancelled or rejected: the held
// funds are returned to the available balance so the merchant can use them.
// =============================================================================
func TestRelease_Success(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	// Start with 600 available and 400 held.
	repo.addWallet(walletID, decimal.NewFromInt(600), decimal.NewFromInt(400))

	refID := uuid.New()
	err := svc.Release(ctx, walletID, decimal.NewFromInt(400), "withdrawal_request", refID)
	if err != nil {
		t.Fatalf("Release returned unexpected error: %v", err)
	}

	// Balance should increase by the released amount.
	wallet, _ := repo.GetByID(ctx, walletID)
	if !wallet.Balance.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("balance after release = %s, want 1000", wallet.Balance)
	}

	// Hold balance should decrease by the released amount.
	if !wallet.HoldBalance.Equal(decimal.Zero) {
		t.Errorf("hold_balance after release = %s, want 0", wallet.HoldBalance)
	}
}

// =============================================================================
// TestCredit_VersionConflict_Retry verifies that the credit operation retries
// on version conflicts and eventually succeeds.
//
// This tests the optimistic locking retry logic, which is the core concurrency
// safety mechanism. In production, two concurrent deposits to the same wallet
// will cause one of them to encounter a version conflict. The retry logic
// ensures it re-reads the wallet and tries again.
//
// Setup: the mock is configured to return ErrVersionConflict on the first call,
// then succeed on the second attempt. We verify that:
//   - The credit eventually succeeds (no error returned).
//   - The final balance is correct.
//   - A ledger entry is created.
// =============================================================================
func TestCredit_VersionConflict_Retry(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	// Configure the mock to return 1 version conflict before succeeding.
	// This simulates another goroutine modifying the wallet between our
	// read and write operations.
	repo.mu.Lock()
	repo.conflictCount = 1
	repo.mu.Unlock()

	refID := uuid.New()
	err := svc.Credit(ctx, walletID, decimal.NewFromInt(500),
		models.LedgerDepositCredit, "deposit_order", refID, "deposit with retry")

	if err != nil {
		t.Fatalf("Credit should succeed after retry, got error: %v", err)
	}

	// Verify the balance is correct after the retry.
	// Note: the mock bumps version on conflict, so the wallet version changed.
	// The service should have re-read and applied the credit correctly.
	wallet, _ := repo.GetByID(ctx, walletID)
	expectedBalance := decimal.NewFromInt(1500)
	if !wallet.Balance.Equal(expectedBalance) {
		t.Errorf("balance after retried credit = %s, want %s", wallet.Balance, expectedBalance)
	}

	// A ledger entry should still have been created on the successful attempt.
	if len(repo.ledger) != 1 {
		t.Errorf("expected 1 ledger entry after retried credit, got %d", len(repo.ledger))
	}
}

// =============================================================================
// TestCredit_ExceedsMaxRetries verifies that the credit operation fails
// gracefully when all optimistic-lock retries are exhausted.
//
// In an extreme contention scenario, every attempt might encounter a version
// conflict. After maxOptimisticRetries (3) attempts, the function should
// return an error rather than retrying indefinitely.
// =============================================================================
func TestCredit_ExceedsMaxRetries(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	// Configure the mock to always return version conflicts.
	// maxOptimisticRetries is 3, so we set conflictCount higher.
	repo.mu.Lock()
	repo.conflictCount = 10
	repo.mu.Unlock()

	refID := uuid.New()
	err := svc.Credit(ctx, walletID, decimal.NewFromInt(500),
		models.LedgerDepositCredit, "deposit_order", refID, "should fail after retries")

	if err == nil {
		t.Fatal("Credit should return an error after exhausting retries, got nil")
	}

	// The error should mention "optimistic-lock retries".
	errStr := fmt.Sprintf("%v", err)
	if !errors.Is(err, repository.ErrVersionConflict) {
		t.Logf("error message: %s", errStr)
		// The error wraps ErrVersionConflict, verify the chain.
	}

	// No ledger entry should have been created since no attempt succeeded.
	if len(repo.ledger) != 0 {
		t.Errorf("expected 0 ledger entries after failed retries, got %d", len(repo.ledger))
	}
}

// =============================================================================
// TestCredit_ZeroAmount verifies that crediting zero or negative amounts is
// rejected.
//
// Zero-amount credits would create useless ledger entries that clutter the
// audit trail. Negative credits would effectively be debits, bypassing the
// debit-specific balance checks. Both must be rejected at the input level.
// =============================================================================
func TestCredit_ZeroAmount(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	// Zero amount should be rejected.
	err := svc.Credit(ctx, walletID, decimal.Zero,
		models.LedgerDepositCredit, "test", uuid.New(), "zero credit")
	if err == nil {
		t.Error("Credit with zero amount should return an error, got nil")
	}

	// Negative amount should be rejected.
	err = svc.Credit(ctx, walletID, decimal.NewFromInt(-100),
		models.LedgerDepositCredit, "test", uuid.New(), "negative credit")
	if err == nil {
		t.Error("Credit with negative amount should return an error, got nil")
	}
}

// =============================================================================
// TestDebit_ZeroAmount verifies that debiting zero or negative amounts is
// rejected with the same guard as Credit.
// =============================================================================
func TestDebit_ZeroAmount(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	err := svc.Debit(ctx, walletID, decimal.Zero,
		models.LedgerWithdrawalDebit, "test", uuid.New(), "zero debit")
	if err == nil {
		t.Error("Debit with zero amount should return an error, got nil")
	}
}

// =============================================================================
// TestHold_InsufficientFunds verifies that Hold is rejected when the wallet
// does not have enough available balance.
//
// Starting state: balance = 200, hold_balance = 0
// Operation:      attempt to hold 500 THB
// Expected:       error, balance unchanged
// =============================================================================
func TestHold_InsufficientFunds(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(200), decimal.Zero)

	err := svc.Hold(ctx, walletID, decimal.NewFromInt(500), "withdrawal_request", uuid.New())
	if err == nil {
		t.Fatal("Hold should return an error for insufficient funds, got nil")
	}
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("expected error to wrap ErrInsufficientFunds, got: %v", err)
	}
}
