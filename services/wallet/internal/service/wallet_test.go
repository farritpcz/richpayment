package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/wallet/internal/repository"
)

// =============================================================================
// mockTx implements pgx.Tx for testing purposes.
//
// It is a minimal no-op transaction that satisfies the full pgx.Tx interface.
// Only Commit and Rollback are meaningful in our tests; all other methods
// panic if called, which acts as a safety net to catch unexpected usage.
// =============================================================================
type mockTx struct{}

// Begin starts a pseudo nested transaction (savepoint). Not used in wallet service.
func (m *mockTx) Begin(_ context.Context) (pgx.Tx, error) { return &mockTx{}, nil }

// Commit is a no-op in the mock — the mock repo applies changes immediately.
func (m *mockTx) Commit(_ context.Context) error { return nil }

// Rollback is a no-op in the mock — changes are already applied in-memory.
func (m *mockTx) Rollback(_ context.Context) error { return nil }

// CopyFrom is not used by the wallet service. Panics if called unexpectedly.
func (m *mockTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	panic("mockTx.CopyFrom: not implemented")
}

// SendBatch is not used by the wallet service. Panics if called unexpectedly.
func (m *mockTx) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults {
	panic("mockTx.SendBatch: not implemented")
}

// LargeObjects is not used by the wallet service. Panics if called unexpectedly.
func (m *mockTx) LargeObjects() pgx.LargeObjects {
	panic("mockTx.LargeObjects: not implemented")
}

// Prepare is not used by the wallet service. Panics if called unexpectedly.
func (m *mockTx) Prepare(_ context.Context, _, _ string) (*pgconn.StatementDescription, error) {
	panic("mockTx.Prepare: not implemented")
}

// Exec is not used directly by the wallet service (it calls repo methods instead).
func (m *mockTx) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	panic("mockTx.Exec: not implemented")
}

// Query is not used directly by the wallet service (it calls repo methods instead).
func (m *mockTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	panic("mockTx.Query: not implemented")
}

// QueryRow is not used directly by the wallet service (it calls repo methods instead).
func (m *mockTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	panic("mockTx.QueryRow: not implemented")
}

// Conn returns the underlying connection. Not used by the wallet service.
func (m *mockTx) Conn() *pgx.Conn {
	panic("mockTx.Conn: not implemented")
}

// =============================================================================
// mockWalletRepo is an in-memory mock implementation of repository.WalletRepository.
//
// It stores wallets in a map and simulates both the legacy optimistic locking
// and the new transactional (FOR UPDATE) flow. The mock is designed to:
//   - Return ErrWalletNotFound for unknown wallet IDs.
//   - Return ErrVersionConflict when the version doesn't match.
//   - Track ledger entries for verification and idempotency checking.
//   - Support configurable failures for testing error paths.
//
// Thread safety: protected by a mutex to support concurrent test scenarios.
// =============================================================================
type mockWalletRepo struct {
	mu      sync.Mutex
	wallets map[uuid.UUID]*models.Wallet
	ledger  []*models.WalletLedger

	// conflictCount tracks how many version conflicts to simulate before
	// allowing the update to succeed.
	conflictCount int
}

// newMockWalletRepo creates a new empty mock repository.
func newMockWalletRepo() *mockWalletRepo {
	return &mockWalletRepo{
		wallets: make(map[uuid.UUID]*models.Wallet),
	}
}

// GetByOwner looks up a wallet by owner+currency. Iterates all wallets
// (acceptable for test data sizes).
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

// UpdateBalance simulates an optimistic-lock update (legacy, non-transactional).
func (m *mockWalletRepo) UpdateBalance(_ context.Context, id uuid.UUID, newBalance, newHold string, expectedVersion int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateBalanceLocked(id, newBalance, newHold, expectedVersion)
}

// CreateLedgerEntry records a ledger entry in the mock's ledger slice (legacy).
func (m *mockWalletRepo) CreateLedgerEntry(_ context.Context, entry *models.WalletLedger) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ledger = append(m.ledger, entry)
	return nil
}

// -------------------------------------------------------------------------
// New transactional methods required by the updated WalletRepository interface
// -------------------------------------------------------------------------

// BeginTx returns a mock transaction. In tests, the "transaction" is a no-op
// because the mock repo operates entirely in-memory with mutex protection.
func (m *mockWalletRepo) BeginTx(_ context.Context) (repository.Tx, error) {
	return &mockTx{}, nil
}

// GetByIDForUpdate behaves identically to GetByID in the mock because
// in-memory access is already serialised by the mutex.
func (m *mockWalletRepo) GetByIDForUpdate(_ context.Context, _ repository.Tx, id uuid.UUID) (*models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.wallets[id]
	if !ok {
		return nil, repository.ErrWalletNotFound
	}
	cp := *w
	return &cp, nil
}

// UpdateBalanceInTx simulates a transactional balance update.
func (m *mockWalletRepo) UpdateBalanceInTx(_ context.Context, _ repository.Tx, id uuid.UUID, newBalance, newHold string, expectedVersion int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateBalanceLocked(id, newBalance, newHold, expectedVersion)
}

// CreateLedgerEntryInTx records a ledger entry (transactional version).
func (m *mockWalletRepo) CreateLedgerEntryInTx(_ context.Context, _ repository.Tx, entry *models.WalletLedger) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ledger = append(m.ledger, entry)
	return nil
}

// LedgerEntryExistsByRef checks whether any ledger entry has the given
// reference_id. Used for idempotency checking.
func (m *mockWalletRepo) LedgerEntryExistsByRef(_ context.Context, refID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.ledger {
		if entry.ReferenceID == refID {
			return true, nil
		}
	}
	return false, nil
}

// updateBalanceLocked is the shared implementation for UpdateBalance and
// UpdateBalanceInTx. Caller must hold m.mu.
func (m *mockWalletRepo) updateBalanceLocked(id uuid.UUID, newBalance, newHold string, expectedVersion int64) error {
	w, ok := m.wallets[id]
	if !ok {
		return repository.ErrWalletNotFound
	}

	// Simulate version conflicts for retry testing.
	if m.conflictCount > 0 {
		m.conflictCount--
		w.Version++
		return repository.ErrVersionConflict
	}

	if w.Version != expectedVersion {
		return repository.ErrVersionConflict
	}

	bal, _ := decimal.NewFromString(newBalance)
	hold, _ := decimal.NewFromString(newHold)
	w.Balance = bal
	w.HoldBalance = hold
	w.Version++
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
// =============================================================================
func TestCredit_Success(t *testing.T) {
	repo := newMockWalletRepo()
	// Pass nil for Redis client — distributed locking is skipped in tests.
	// The PostgreSQL FOR UPDATE lock (mocked here) is the primary defense.
	svc := NewWalletService(repo, nil)
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
// =============================================================================
func TestDebit_Success(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	refID := uuid.New()
	err := svc.Debit(ctx, walletID, decimal.NewFromInt(300),
		models.LedgerWithdrawalDebit, "withdrawal_request", refID, "withdrawal approved")
	if err != nil {
		t.Fatalf("Debit returned unexpected error: %v", err)
	}

	wallet, _ := repo.GetByID(ctx, walletID)
	expectedBalance := decimal.NewFromInt(700)
	if !wallet.Balance.Equal(expectedBalance) {
		t.Errorf("balance after debit = %s, want %s", wallet.Balance, expectedBalance)
	}

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
// =============================================================================
func TestDebit_InsufficientFunds(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(200), decimal.Zero)

	refID := uuid.New()
	err := svc.Debit(ctx, walletID, decimal.NewFromInt(500),
		models.LedgerWithdrawalDebit, "withdrawal_request", refID, "should fail")

	if err == nil {
		t.Fatal("Debit should return an error for insufficient funds, got nil")
	}
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("expected error to wrap ErrInsufficientFunds, got: %v", err)
	}

	wallet, _ := repo.GetByID(ctx, walletID)
	if !wallet.Balance.Equal(decimal.NewFromInt(200)) {
		t.Errorf("balance should remain 200 after failed debit, got %s", wallet.Balance)
	}

	if len(repo.ledger) != 0 {
		t.Errorf("expected 0 ledger entries after failed debit, got %d", len(repo.ledger))
	}
}

// =============================================================================
// TestHold_Success verifies that Hold moves funds from balance to hold_balance.
// =============================================================================
func TestHold_Success(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	refID := uuid.New()
	err := svc.Hold(ctx, walletID, decimal.NewFromInt(400), "withdrawal_request", refID)
	if err != nil {
		t.Fatalf("Hold returned unexpected error: %v", err)
	}

	wallet, _ := repo.GetByID(ctx, walletID)
	if !wallet.Balance.Equal(decimal.NewFromInt(600)) {
		t.Errorf("balance after hold = %s, want 600", wallet.Balance)
	}
	if !wallet.HoldBalance.Equal(decimal.NewFromInt(400)) {
		t.Errorf("hold_balance after hold = %s, want 400", wallet.HoldBalance)
	}

	if len(repo.ledger) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(repo.ledger))
	}
}

// =============================================================================
// TestRelease_Success verifies that Release moves funds from hold_balance
// back to the available balance.
// =============================================================================
func TestRelease_Success(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(600), decimal.NewFromInt(400))

	refID := uuid.New()
	err := svc.Release(ctx, walletID, decimal.NewFromInt(400), "withdrawal_request", refID)
	if err != nil {
		t.Fatalf("Release returned unexpected error: %v", err)
	}

	wallet, _ := repo.GetByID(ctx, walletID)
	if !wallet.Balance.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("balance after release = %s, want 1000", wallet.Balance)
	}
	if !wallet.HoldBalance.Equal(decimal.Zero) {
		t.Errorf("hold_balance after release = %s, want 0", wallet.HoldBalance)
	}
}

// =============================================================================
// TestCredit_Idempotency verifies that duplicate credits with the same
// reference_id are silently skipped (idempotent behavior).
//
// This is critical for preventing double-crediting when a client retries.
// =============================================================================
func TestCredit_Idempotency(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	refID := uuid.New()

	// First credit should succeed.
	err := svc.Credit(ctx, walletID, decimal.NewFromInt(500),
		models.LedgerDepositCredit, "deposit_order", refID, "first credit")
	if err != nil {
		t.Fatalf("First Credit returned unexpected error: %v", err)
	}

	// Second credit with the same refID should be silently skipped.
	err = svc.Credit(ctx, walletID, decimal.NewFromInt(500),
		models.LedgerDepositCredit, "deposit_order", refID, "duplicate credit")
	if err != nil {
		t.Fatalf("Duplicate Credit should succeed (idempotent skip), got error: %v", err)
	}

	// Verify the balance was only credited once: 1000 + 500 = 1500 (not 2000).
	wallet, _ := repo.GetByID(ctx, walletID)
	expectedBalance := decimal.NewFromInt(1500)
	if !wallet.Balance.Equal(expectedBalance) {
		t.Errorf("balance after idempotent credit = %s, want %s", wallet.Balance, expectedBalance)
	}

	// Only one ledger entry should exist.
	if len(repo.ledger) != 1 {
		t.Errorf("expected 1 ledger entry (idempotent), got %d", len(repo.ledger))
	}
}

// =============================================================================
// TestCredit_ZeroAmount verifies that crediting zero or negative amounts is
// rejected.
// =============================================================================
func TestCredit_ZeroAmount(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	err := svc.Credit(ctx, walletID, decimal.Zero,
		models.LedgerDepositCredit, "test", uuid.New(), "zero credit")
	if err == nil {
		t.Error("Credit with zero amount should return an error, got nil")
	}

	err = svc.Credit(ctx, walletID, decimal.NewFromInt(-100),
		models.LedgerDepositCredit, "test", uuid.New(), "negative credit")
	if err == nil {
		t.Error("Credit with negative amount should return an error, got nil")
	}
}

// =============================================================================
// TestDebit_ZeroAmount verifies that debiting zero or negative amounts is
// rejected.
// =============================================================================
func TestDebit_ZeroAmount(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
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
// =============================================================================
func TestHold_InsufficientFunds(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
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

// =============================================================================
// TestHold_Idempotency verifies that duplicate holds with the same reference_id
// are silently skipped.
// =============================================================================
func TestHold_Idempotency(t *testing.T) {
	repo := newMockWalletRepo()
	svc := NewWalletService(repo, nil)
	ctx := context.Background()

	walletID := uuid.New()
	repo.addWallet(walletID, decimal.NewFromInt(1000), decimal.Zero)

	refID := uuid.New()

	// First hold should succeed.
	err := svc.Hold(ctx, walletID, decimal.NewFromInt(400), "withdrawal_request", refID)
	if err != nil {
		t.Fatalf("First Hold returned unexpected error: %v", err)
	}

	// Second hold with same refID should be idempotent.
	err = svc.Hold(ctx, walletID, decimal.NewFromInt(400), "withdrawal_request", refID)
	if err != nil {
		t.Fatalf("Duplicate Hold should succeed (idempotent skip), got error: %v", err)
	}

	// Balance should only reflect one hold.
	wallet, _ := repo.GetByID(ctx, walletID)
	if !wallet.Balance.Equal(decimal.NewFromInt(600)) {
		t.Errorf("balance after idempotent hold = %s, want 600", wallet.Balance)
	}
	if !wallet.HoldBalance.Equal(decimal.NewFromInt(400)) {
		t.Errorf("hold_balance after idempotent hold = %s, want 400", wallet.HoldBalance)
	}
}
