// Package service implements the business logic for the withdrawal-service.
// This file contains the WithdrawalService which handles the complete
// withdrawal lifecycle: creation (with daily limit and balance checks),
// approval, rejection, and completion (with fee calculation and commission).
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/httpclient"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/withdrawal/internal/repository"
)

// WalletClient defines the interface for interacting with the wallet-service.
// In production, this would make HTTP/gRPC calls to the wallet-service.
// For now, we provide a stub implementation for compilation and testing.
type WalletClient interface {
	// GetBalance returns the available (non-held) balance for a merchant's wallet.
	// Used to verify the merchant has sufficient funds before creating a withdrawal.
	GetBalance(ctx context.Context, merchantID uuid.UUID, currency string) (decimal.Decimal, error)

	// HoldBalance places a hold on the specified amount in the merchant's wallet.
	// The held amount is deducted from the available balance but not yet debited.
	// This prevents the merchant from spending funds reserved for a withdrawal.
	HoldBalance(ctx context.Context, merchantID uuid.UUID, amount decimal.Decimal, currency string, referenceID uuid.UUID) error

	// ReleaseHold releases a previously placed hold, returning the amount to
	// the merchant's available balance. Called when a withdrawal is rejected.
	ReleaseHold(ctx context.Context, merchantID uuid.UUID, amount decimal.Decimal, currency string, referenceID uuid.UUID) error

	// DebitHold converts a held amount into a permanent debit. Called when a
	// withdrawal is completed and the bank transfer has been confirmed.
	DebitHold(ctx context.Context, merchantID uuid.UUID, amount decimal.Decimal, currency string, referenceID uuid.UUID) error
}

// CommissionClient defines the interface for recording commission entries.
// In production, this would call the commission-service.
type CommissionClient interface {
	// RecordWithdrawalCommission records the commission split for a completed
	// withdrawal. The fee amount is divided between the system, agent, and
	// partner according to their configured percentages.
	RecordWithdrawalCommission(ctx context.Context, withdrawalID uuid.UUID, merchantID uuid.UUID, feeAmount decimal.Decimal, currency string) error
}

// MerchantClient defines the interface for fetching merchant configuration.
// In production, this would call the user-service to retrieve merchant details.
type MerchantClient interface {
	// GetWithdrawalFeePct returns the merchant's configured withdrawal fee
	// percentage. For example, 0.01 means 1% fee on each withdrawal.
	GetWithdrawalFeePct(ctx context.Context, merchantID uuid.UUID) (decimal.Decimal, error)

	// GetDailyWithdrawalLimit returns the merchant's configured maximum
	// daily withdrawal amount in their base currency.
	GetDailyWithdrawalLimit(ctx context.Context, merchantID uuid.UUID) (decimal.Decimal, error)
}

// WithdrawalService encapsulates the business logic for the entire withdrawal
// lifecycle. It coordinates between the repository, wallet, commission, and
// merchant services to process withdrawal requests through their multi-step
// approval pipeline.
type WithdrawalService struct {
	// repo is the persistence layer for withdrawal records.
	repo repository.WithdrawalRepository

	// walletClient interacts with the wallet-service for balance operations.
	walletClient WalletClient

	// commissionClient interacts with the commission-service for fee recording.
	commissionClient CommissionClient

	// merchantClient interacts with the user-service for merchant configuration.
	merchantClient MerchantClient
}

// NewWithdrawalService constructs a WithdrawalService with all required
// dependencies.
//
// Parameters:
//   - repo: the database repository for withdrawal CRUD operations.
//   - walletClient: client for wallet balance holds and debits.
//   - commissionClient: client for recording commission splits.
//   - merchantClient: client for fetching merchant fee configuration.
//
// Returns a pointer to a fully initialised WithdrawalService.
func NewWithdrawalService(
	repo repository.WithdrawalRepository,
	walletClient WalletClient,
	commissionClient CommissionClient,
	merchantClient MerchantClient,
) *WithdrawalService {
	return &WithdrawalService{
		repo:             repo,
		walletClient:     walletClient,
		commissionClient: commissionClient,
		merchantClient:   merchantClient,
	}
}

// CreateWithdrawal initiates a new withdrawal request for a merchant.
// It performs the following validation and processing steps:
//
//  1. Check the merchant's daily withdrawal limit to ensure this request
//     would not exceed it.
//  2. Check the merchant's wallet balance to ensure they have sufficient
//     available funds.
//  3. Place a hold on the withdrawal amount in the merchant's wallet.
//  4. Create the withdrawal record in the database with status "pending".
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - merchantID: the UUID of the merchant requesting the withdrawal.
//   - amount: the gross withdrawal amount.
//   - currency: the ISO 4217 currency code (e.g. "THB").
//   - destType: the destination type (bank, promptpay, etc.).
//   - destDetails: JSON-encoded destination details (bank name, account, etc.).
//
// Returns the created Withdrawal model and nil error on success.
// Returns errors for daily limit exceeded, insufficient funds, or
// persistence failures.
func (s *WithdrawalService) CreateWithdrawal(
	ctx context.Context,
	merchantID uuid.UUID,
	amount decimal.Decimal,
	currency string,
	destType models.WithdrawalDestType,
	destDetails string,
) (*models.Withdrawal, error) {

	// ---------------------------------------------------------------
	// Step 1: Check daily withdrawal limit.
	// Sum all withdrawals for this merchant today and verify that
	// adding the new amount would not exceed the configured limit.
	// ---------------------------------------------------------------
	dailyLimit, err := s.merchantClient.GetDailyWithdrawalLimit(ctx, merchantID)
	if err != nil {
		return nil, fmt.Errorf("get daily withdrawal limit: %w", err)
	}

	// Calculate the sum of today's withdrawals for this merchant.
	today := time.Now().UTC()
	dailySum, err := s.repo.SumDailyWithdrawals(ctx, merchantID, today)
	if err != nil {
		return nil, fmt.Errorf("sum daily withdrawals: %w", err)
	}

	// Check if adding the new withdrawal would exceed the daily limit.
	// dailyLimit of zero means no limit is configured (unlimited withdrawals).
	if dailyLimit.IsPositive() && dailySum.Add(amount).GreaterThan(dailyLimit) {
		return nil, errors.New(
			"DAILY_LIMIT_EXCEEDED",
			fmt.Sprintf("daily withdrawal limit of %s %s would be exceeded (current: %s, requested: %s)",
				dailyLimit.String(), currency, dailySum.String(), amount.String()),
			400,
		)
	}

	// ---------------------------------------------------------------
	// Step 2: Check wallet balance.
	// Ensure the merchant has enough available (non-held) funds to
	// cover the withdrawal amount.
	// ---------------------------------------------------------------
	balance, err := s.walletClient.GetBalance(ctx, merchantID, currency)
	if err != nil {
		return nil, fmt.Errorf("get wallet balance: %w", err)
	}

	// The available balance must be greater than or equal to the
	// requested withdrawal amount.
	if balance.LessThan(amount) {
		return nil, errors.Wrap(
			fmt.Errorf("balance %s < requested %s", balance.String(), amount.String()),
			"INSUFFICIENT_FUNDS",
			"insufficient wallet balance for withdrawal",
			400,
		)
	}

	// ---------------------------------------------------------------
	// Step 3: Hold balance in the merchant's wallet.
	// This reserves the funds so they cannot be used for other
	// operations while the withdrawal is pending approval.
	// ---------------------------------------------------------------
	withdrawalID := uuid.New()
	if err := s.walletClient.HoldBalance(ctx, merchantID, amount, currency, withdrawalID); err != nil {
		return nil, fmt.Errorf("hold wallet balance: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 4: Create the withdrawal record with status "pending".
	// ---------------------------------------------------------------
	now := time.Now().UTC()
	withdrawal := &models.Withdrawal{
		ID:          withdrawalID,
		MerchantID:  merchantID,
		Amount:      amount,
		FeeAmount:   decimal.Zero, // Fee is calculated at completion time.
		NetAmount:   decimal.Zero, // Net amount is calculated at completion time.
		Currency:    currency,
		DestType:    destType,
		DestDetails: destDetails,
		Status:      models.WithdrawalStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Persist the withdrawal in the repository.
	if err := s.repo.Create(ctx, withdrawal); err != nil {
		// If persistence fails, we should release the hold.
		// In production, this would be wrapped in a transaction or saga.
		releaseErr := s.walletClient.ReleaseHold(ctx, merchantID, amount, currency, withdrawalID)
		if releaseErr != nil {
			logger.Error("failed to release hold after create failure",
				"withdrawal_id", withdrawalID.String(),
				"error", releaseErr,
			)
		}
		return nil, fmt.Errorf("create withdrawal in repository: %w", err)
	}

	logger.Info("withdrawal created",
		"withdrawal_id", withdrawal.ID.String(),
		"merchant_id", merchantID.String(),
		"amount", amount.String(),
		"currency", currency,
	)

	return withdrawal, nil
}

// ApproveWithdrawal marks a pending withdrawal as approved by an admin.
// After approval, the withdrawal is ready for the finance team to execute
// the bank transfer.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - withdrawalID: the UUID of the withdrawal to approve.
//   - adminID: the UUID of the admin performing the approval.
//
// Returns nil on success. Returns an error if the withdrawal is not found,
// is not in "pending" status, or the update fails.
func (s *WithdrawalService) ApproveWithdrawal(
	ctx context.Context,
	withdrawalID uuid.UUID,
	adminID uuid.UUID,
) error {
	// Load the withdrawal and verify its current status.
	withdrawal, err := s.repo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("get withdrawal for approval: %w", err)
	}

	// Only pending withdrawals can be approved.
	if withdrawal.Status != models.WithdrawalStatusPending {
		return errors.New(
			"INVALID_STATUS",
			fmt.Sprintf("cannot approve withdrawal with status %q, expected pending", withdrawal.Status),
			409,
		)
	}

	// Build the update fields with approval metadata.
	now := time.Now().UTC()
	adminIDCopy := adminID
	fields := map[string]interface{}{
		"approved_by": &adminIDCopy,
		"approved_at": &now,
	}

	// Transition the withdrawal to "approved" status.
	if err := s.repo.UpdateStatus(ctx, withdrawalID, models.WithdrawalStatusApproved, fields); err != nil {
		return fmt.Errorf("update withdrawal to approved: %w", err)
	}

	logger.Info("withdrawal approved",
		"withdrawal_id", withdrawalID.String(),
		"admin_id", adminID.String(),
	)

	return nil
}

// RejectWithdrawal marks a pending withdrawal as rejected by an admin and
// releases the held balance back to the merchant's available wallet balance.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - withdrawalID: the UUID of the withdrawal to reject.
//   - adminID: the UUID of the admin performing the rejection.
//   - reason: a human-readable explanation for why the withdrawal was rejected.
//
// Returns nil on success. Returns an error if the withdrawal is not found,
// is not in "pending" status, or the update/release fails.
func (s *WithdrawalService) RejectWithdrawal(
	ctx context.Context,
	withdrawalID uuid.UUID,
	adminID uuid.UUID,
	reason string,
) error {
	// Load the withdrawal and verify its current status.
	withdrawal, err := s.repo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("get withdrawal for rejection: %w", err)
	}

	// Only pending withdrawals can be rejected.
	if withdrawal.Status != models.WithdrawalStatusPending {
		return errors.New(
			"INVALID_STATUS",
			fmt.Sprintf("cannot reject withdrawal with status %q, expected pending", withdrawal.Status),
			409,
		)
	}

	// ---------------------------------------------------------------
	// Release the held balance back to the merchant's available balance.
	// This must happen before the status update to ensure the funds are
	// available even if the status update fails.
	// ---------------------------------------------------------------
	if err := s.walletClient.ReleaseHold(
		ctx, withdrawal.MerchantID, withdrawal.Amount, withdrawal.Currency, withdrawalID,
	); err != nil {
		return fmt.Errorf("release held balance on rejection: %w", err)
	}

	// Build the update fields with rejection metadata.
	now := time.Now().UTC()
	adminIDCopy := adminID
	fields := map[string]interface{}{
		"rejected_by":      &adminIDCopy,
		"rejected_at":      &now,
		"rejection_reason": reason,
	}

	// Transition the withdrawal to "rejected" status.
	if err := s.repo.UpdateStatus(ctx, withdrawalID, models.WithdrawalStatusRejected, fields); err != nil {
		return fmt.Errorf("update withdrawal to rejected: %w", err)
	}

	logger.Info("withdrawal rejected",
		"withdrawal_id", withdrawalID.String(),
		"admin_id", adminID.String(),
		"reason", reason,
	)

	return nil
}

// CompleteWithdrawal finalises an approved withdrawal after the bank transfer
// has been executed and confirmed. It performs the following steps:
//
//  1. Verify the withdrawal is in "approved" status.
//  2. Fetch the merchant's withdrawal fee percentage.
//  3. Calculate the fee amount and net amount.
//  4. Debit the held balance from the merchant's wallet.
//  5. Record the commission split for the withdrawal fee.
//  6. Update the withdrawal record to "completed" status.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - withdrawalID: the UUID of the withdrawal to complete.
//   - transferRef: the external reference number from the bank transfer.
//   - proofURL: the URL to the transfer proof document.
//
// Returns nil on success. Returns an error if the withdrawal is not found,
// is not in "approved" status, or any step in the completion flow fails.
func (s *WithdrawalService) CompleteWithdrawal(
	ctx context.Context,
	withdrawalID uuid.UUID,
	transferRef string,
	proofURL string,
) error {
	// ---------------------------------------------------------------
	// Step 1: Load the withdrawal and verify its current status.
	// ---------------------------------------------------------------
	withdrawal, err := s.repo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("get withdrawal for completion: %w", err)
	}

	// Only approved withdrawals can be completed.
	if withdrawal.Status != models.WithdrawalStatusApproved {
		return errors.New(
			"INVALID_STATUS",
			fmt.Sprintf("cannot complete withdrawal with status %q, expected approved", withdrawal.Status),
			409,
		)
	}

	// ---------------------------------------------------------------
	// Step 2: Fetch the merchant's withdrawal fee percentage.
	// ---------------------------------------------------------------
	feePct, err := s.merchantClient.GetWithdrawalFeePct(ctx, withdrawal.MerchantID)
	if err != nil {
		return fmt.Errorf("get merchant withdrawal fee pct: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 3: Calculate fee and net amounts.
	// Fee = Amount * feePct (rounded to 2 decimal places).
	// NetAmount = Amount - Fee (the amount actually sent to the merchant).
	// ---------------------------------------------------------------
	feeAmount := withdrawal.Amount.Mul(feePct).Round(2)
	netAmount := withdrawal.Amount.Sub(feeAmount)

	// ---------------------------------------------------------------
	// Step 4: Debit the held balance from the merchant's wallet.
	// This permanently removes the funds from the wallet.
	// ---------------------------------------------------------------
	if err := s.walletClient.DebitHold(
		ctx, withdrawal.MerchantID, withdrawal.Amount, withdrawal.Currency, withdrawalID,
	); err != nil {
		return fmt.Errorf("debit held balance on completion: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 5: Record the commission split for the withdrawal fee.
	// The commission-service will divide the fee between system, agent,
	// and partner according to their configured percentages.
	// ---------------------------------------------------------------
	if err := s.commissionClient.RecordWithdrawalCommission(
		ctx, withdrawalID, withdrawal.MerchantID, feeAmount, withdrawal.Currency,
	); err != nil {
		// Log the error but do not fail the completion — the commission
		// can be reconciled later. The bank transfer has already happened.
		logger.Error("failed to record withdrawal commission",
			"withdrawal_id", withdrawalID.String(),
			"error", err,
		)
	}

	// ---------------------------------------------------------------
	// Step 6: Update the withdrawal record to "completed" status.
	// ---------------------------------------------------------------
	now := time.Now().UTC()
	fields := map[string]interface{}{
		"transfer_ref": transferRef,
		"proof_url":    proofURL,
		"completed_at": &now,
		"fee_amount":   feeAmount,
		"net_amount":   netAmount,
	}

	if err := s.repo.UpdateStatus(ctx, withdrawalID, models.WithdrawalStatusCompleted, fields); err != nil {
		return fmt.Errorf("update withdrawal to completed: %w", err)
	}

	logger.Info("withdrawal completed",
		"withdrawal_id", withdrawalID.String(),
		"merchant_id", withdrawal.MerchantID.String(),
		"amount", withdrawal.Amount.String(),
		"fee_amount", feeAmount.String(),
		"net_amount", netAmount.String(),
		"transfer_ref", transferRef,
	)

	return nil
}

// GetWithdrawal retrieves a withdrawal by its unique identifier.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the withdrawal to retrieve.
//
// Returns the Withdrawal model and nil error on success.
// Returns ErrNotFound if no withdrawal with the given ID exists.
func (s *WithdrawalService) GetWithdrawal(ctx context.Context, id uuid.UUID) (*models.Withdrawal, error) {
	// Delegate to the repository for the database lookup.
	withdrawal, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get withdrawal: %w", err)
	}
	return withdrawal, nil
}

// ListPendingWithdrawals returns a paginated list of all withdrawals that
// are currently in "pending" status, awaiting admin approval.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - page: the 1-based page number.
//   - limit: the maximum number of withdrawals per page.
//
// Returns a slice of Withdrawal models, the total count, and nil error.
func (s *WithdrawalService) ListPendingWithdrawals(
	ctx context.Context,
	page, limit int,
) ([]models.Withdrawal, int, error) {
	// Convert 1-based page number to zero-based offset.
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	// Delegate the paginated query to the repository.
	withdrawals, total, err := s.repo.ListPending(ctx, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list pending withdrawals: %w", err)
	}

	return withdrawals, total, nil
}

// ---------------------------------------------------------------------------
// Stub client implementations for compilation and testing
// ---------------------------------------------------------------------------

// StubWalletClient is a no-op implementation of WalletClient for development
// and testing. It always returns successful responses with default values.
type StubWalletClient struct{}

// GetBalance returns a large default balance so withdrawals always pass
// the balance check in development/testing environments.
func (c *StubWalletClient) GetBalance(_ context.Context, _ uuid.UUID, _ string) (decimal.Decimal, error) {
	// Return 1,000,000 as the default available balance.
	return decimal.NewFromInt(1_000_000), nil
}

// HoldBalance is a no-op in the stub — it always succeeds.
func (c *StubWalletClient) HoldBalance(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string, _ uuid.UUID) error {
	return nil
}

// ReleaseHold is a no-op in the stub — it always succeeds.
func (c *StubWalletClient) ReleaseHold(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string, _ uuid.UUID) error {
	return nil
}

// DebitHold is a no-op in the stub — it always succeeds.
func (c *StubWalletClient) DebitHold(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string, _ uuid.UUID) error {
	return nil
}

// StubCommissionClient is a no-op implementation of CommissionClient for
// development and testing. It always returns success.
type StubCommissionClient struct{}

// RecordWithdrawalCommission is a no-op in the stub — it always succeeds.
func (c *StubCommissionClient) RecordWithdrawalCommission(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ decimal.Decimal, _ string) error {
	return nil
}

// StubMerchantClient is a stub implementation of MerchantClient for
// development and testing. It returns sensible default values.
type StubMerchantClient struct{}

// GetWithdrawalFeePct returns a default withdrawal fee of 1% (0.01).
func (c *StubMerchantClient) GetWithdrawalFeePct(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	// Default: 1% withdrawal fee.
	return decimal.NewFromFloat(0.01), nil
}

// GetDailyWithdrawalLimit returns a default daily limit of 500,000.
func (c *StubMerchantClient) GetDailyWithdrawalLimit(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	// Default: 500,000 THB daily withdrawal limit.
	return decimal.NewFromInt(500_000), nil
}

// ---------------------------------------------------------------------------
// HTTP-based client implementations for real inter-service communication
// ---------------------------------------------------------------------------
//
// These implementations replace the stub clients above for production use.
// They make synchronous HTTP calls to the wallet-service and commission-service
// to perform wallet operations and record commissions.
//
// INTER-SERVICE COMMUNICATION ARCHITECTURE:
//
// The withdrawal-service communicates with two other services during the
// withdrawal lifecycle:
//
//   withdrawal-service (:8085) --> wallet-service (:8084)
//     - GET /wallet/balance     — Check merchant's available balance
//     - POST /wallet/debit      — Hold, release, or debit wallet balance
//     - POST /wallet/credit     — Release a hold (credit back)
//
//   withdrawal-service (:8085) --> commission-service (:8086)
//     - POST /internal/commission/calculate — Record fee split on completion
//
// The wallet interactions happen at multiple points in the withdrawal lifecycle:
//   1. CreateWithdrawal:   Check balance + place hold on funds
//   2. RejectWithdrawal:   Release the hold (return funds to available balance)
//   3. CompleteWithdrawal: Debit the held amount + record commission
//
// ---------------------------------------------------------------------------

// HTTPWalletClient implements the WalletClient interface by making HTTP calls
// to the wallet-service. It is used in production to perform real balance
// checks, holds, releases, and debits on merchant wallets.
//
// INTER-SERVICE CALLS:
//
//   withdrawal-service (:8085) --> wallet-service (:8084)
//
// The wallet-service exposes three endpoints used by this client:
//   - GET  /wallet/balance — Returns available and held balances.
//   - POST /wallet/credit  — Adds funds (used for releasing holds).
//   - POST /wallet/debit   — Subtracts funds (used for holds and debits).
type HTTPWalletClient struct {
	// client is the HTTP client configured to call the wallet-service.
	// It is created with httpclient.New() pointing at the wallet-service
	// base URL (e.g. http://localhost:8084).
	client *httpclient.ServiceClient
}

// NewHTTPWalletClient creates a new HTTPWalletClient for the given wallet-service URL.
//
// Parameters:
//   - client: an httpclient.ServiceClient pointing at the wallet-service
//     (e.g. httpclient.New("http://localhost:8084", 10*time.Second)).
func NewHTTPWalletClient(client *httpclient.ServiceClient) *HTTPWalletClient {
	return &HTTPWalletClient{client: client}
}

// walletBalanceResponse is the JSON response from the wallet-service
// GET /wallet/balance endpoint. We only need the Balance field to check
// available funds.
type walletBalanceResponse struct {
	Balance string `json:"balance"`
}

// GetBalance calls the wallet-service to retrieve the merchant's available
// (non-held) balance.
//
// INTER-SERVICE CALL: withdrawal-service --> wallet-service (:8084)
// Endpoint: GET /wallet/balance?owner_type=merchant&owner_id={id}&currency={cur}
//
// This is called during CreateWithdrawal (Step 2) to verify the merchant
// has sufficient funds before placing a hold.
func (c *HTTPWalletClient) GetBalance(ctx context.Context, merchantID uuid.UUID, currency string) (decimal.Decimal, error) {
	// Build the query path for the wallet-service balance endpoint.
	// The wallet-service identifies wallets by owner_type + owner_id + currency.
	path := fmt.Sprintf("/wallet/balance?owner_type=merchant&owner_id=%s&currency=%s",
		merchantID.String(), currency)

	var resp walletBalanceResponse
	if err := c.client.Get(ctx, path, &resp); err != nil {
		return decimal.Zero, fmt.Errorf("http wallet client: get balance: %w", err)
	}

	// Parse the balance string into a decimal for precise comparison.
	balance, err := decimal.NewFromString(resp.Balance)
	if err != nil {
		return decimal.Zero, fmt.Errorf("http wallet client: parse balance %q: %w", resp.Balance, err)
	}

	return balance, nil
}

// HoldBalance calls the wallet-service to place a hold on the specified
// amount in the merchant's wallet.
//
// INTER-SERVICE CALL: withdrawal-service --> wallet-service (:8084)
// Endpoint: POST /wallet/debit
//
// This is called during CreateWithdrawal (Step 3) to reserve funds. The held
// amount is deducted from available balance but not permanently removed.
// If the withdrawal is later rejected, the hold is released. If completed,
// the hold is converted to a permanent debit.
//
// We use the debit endpoint with a "withdrawal_hold" entry type so the
// wallet-service can track holds separately in the ledger.
func (c *HTTPWalletClient) HoldBalance(ctx context.Context, merchantID uuid.UUID, amount decimal.Decimal, currency string, referenceID uuid.UUID) error {
	// The wallet-service needs a wallet_id. In a full implementation,
	// we would first look up the wallet by owner_type+owner_id+currency.
	// For now, we use the merchantID as a wallet identifier placeholder.
	req := map[string]string{
		"wallet_id":      merchantID.String(), // TODO: look up actual wallet_id
		"amount":         amount.String(),
		"entry_type":     "withdrawal_hold",
		"reference_type": "withdrawal",
		"reference_id":   referenceID.String(),
		"description":    fmt.Sprintf("Hold for withdrawal %s", referenceID.String()),
	}

	if err := c.client.Post(ctx, "/wallet/debit", req, nil); err != nil {
		return fmt.Errorf("http wallet client: hold balance: %w", err)
	}

	return nil
}

// ReleaseHold calls the wallet-service to release a previously placed hold,
// returning the amount to the merchant's available balance.
//
// INTER-SERVICE CALL: withdrawal-service --> wallet-service (:8084)
// Endpoint: POST /wallet/credit
//
// This is called during RejectWithdrawal to reverse the hold. The credit
// operation adds the amount back to the wallet, effectively undoing the
// hold that was placed during CreateWithdrawal.
func (c *HTTPWalletClient) ReleaseHold(ctx context.Context, merchantID uuid.UUID, amount decimal.Decimal, currency string, referenceID uuid.UUID) error {
	req := map[string]string{
		"wallet_id":      merchantID.String(), // TODO: look up actual wallet_id
		"amount":         amount.String(),
		"entry_type":     "withdrawal_release",
		"reference_type": "withdrawal",
		"reference_id":   referenceID.String(),
		"description":    fmt.Sprintf("Release hold for rejected withdrawal %s", referenceID.String()),
	}

	if err := c.client.Post(ctx, "/wallet/credit", req, nil); err != nil {
		return fmt.Errorf("http wallet client: release hold: %w", err)
	}

	return nil
}

// DebitHold calls the wallet-service to convert a held amount into a
// permanent debit.
//
// INTER-SERVICE CALL: withdrawal-service --> wallet-service (:8084)
// Endpoint: POST /wallet/debit
//
// This is called during CompleteWithdrawal (Step 4) after the bank transfer
// has been confirmed. The held funds are permanently removed from the wallet.
// The entry_type "withdrawal_debit" distinguishes this from the initial hold.
func (c *HTTPWalletClient) DebitHold(ctx context.Context, merchantID uuid.UUID, amount decimal.Decimal, currency string, referenceID uuid.UUID) error {
	req := map[string]string{
		"wallet_id":      merchantID.String(), // TODO: look up actual wallet_id
		"amount":         amount.String(),
		"entry_type":     "withdrawal_debit",
		"reference_type": "withdrawal",
		"reference_id":   referenceID.String(),
		"description":    fmt.Sprintf("Debit for completed withdrawal %s", referenceID.String()),
	}

	if err := c.client.Post(ctx, "/wallet/debit", req, nil); err != nil {
		return fmt.Errorf("http wallet client: debit hold: %w", err)
	}

	return nil
}

// HTTPCommissionClient implements the CommissionClient interface by making
// HTTP calls to the commission-service. It is used in production to record
// fee splits when withdrawals are completed.
//
// INTER-SERVICE CALL:
//
//   withdrawal-service (:8085) --> commission-service (:8086)
//   Endpoint: POST /internal/commission/calculate
//
// The commission-service calculates how the withdrawal fee is divided between
// the system, agent, and partner, then records the split and credits the
// respective wallets.
type HTTPCommissionClient struct {
	// client is the HTTP client configured to call the commission-service.
	client *httpclient.ServiceClient
}

// NewHTTPCommissionClient creates a new HTTPCommissionClient for the given
// commission-service URL.
//
// Parameters:
//   - client: an httpclient.ServiceClient pointing at the commission-service
//     (e.g. httpclient.New("http://localhost:8086", 10*time.Second)).
func NewHTTPCommissionClient(client *httpclient.ServiceClient) *HTTPCommissionClient {
	return &HTTPCommissionClient{client: client}
}

// RecordWithdrawalCommission calls the commission-service to record the
// commission split for a completed withdrawal.
//
// INTER-SERVICE CALL: withdrawal-service --> commission-service (:8086)
// Endpoint: POST /internal/commission/calculate
//
// This is called during CompleteWithdrawal (Step 5) after the bank transfer
// has been executed and the wallet has been debited. The commission-service
// receives the transaction details and fee amount, calculates the split
// between system/agent/partner according to their configured percentages,
// and records the result.
//
// Parameters:
//   - withdrawalID: the UUID of the completed withdrawal (used as transaction_id).
//   - merchantID: the UUID of the merchant (for looking up commission config).
//   - feeAmount: the gross fee charged to the merchant.
//   - currency: the ISO 4217 currency code (e.g. "THB").
func (c *HTTPCommissionClient) RecordWithdrawalCommission(ctx context.Context, withdrawalID uuid.UUID, merchantID uuid.UUID, feeAmount decimal.Decimal, currency string) error {
	req := map[string]string{
		"transaction_type":   "withdrawal",
		"transaction_id":     withdrawalID.String(),
		"merchant_id":        merchantID.String(),
		"transaction_amount": feeAmount.String(), // The fee amount is the commission base
		"merchant_fee_pct":   "0.01",             // TODO: fetch from merchant config
		"currency":           currency,
	}

	if err := c.client.Post(ctx, "/internal/commission/calculate", req, nil); err != nil {
		return fmt.Errorf("http commission client: record withdrawal commission: %w", err)
	}

	return nil
}
