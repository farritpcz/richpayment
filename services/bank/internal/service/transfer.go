// Package service contains the core business logic for the bank-service.
//
// This file implements the fund transfer subsystem, which manages the
// movement of funds from active bank accounts to holding (treasury)
// accounts. Transfers are initiated by admins and go through a pending
// -> completed/failed lifecycle.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/services/bank/internal/repository"
)

// ---------------------------------------------------------------------------
// Transfer service
// ---------------------------------------------------------------------------

// TransferService handles the creation, completion, and querying of fund
// transfers between pool bank accounts and holding accounts. It enforces
// security checks (e.g. validating the holding account) and maintains
// an audit trail of all transfer operations.
type TransferService struct {
	// repo provides access to transfer and holding account data.
	repo repository.BankRepository

	// log is the structured logger for transfer events.
	log *slog.Logger
}

// NewTransferService creates a new TransferService with the given
// repository and logger. Both parameters must not be nil.
func NewTransferService(repo repository.BankRepository, log *slog.Logger) *TransferService {
	return &TransferService{
		repo: repo,
		log:  log,
	}
}

// ---------------------------------------------------------------------------
// CreateTransfer — initiate a new fund transfer
// ---------------------------------------------------------------------------

// CreateTransfer creates a new pending transfer from a pool bank account
// to a holding account. Before creating the transfer, it validates that:
//   - The from_account_id references a valid bank account
//   - The to_holding_id exists in the holding_accounts table (security!)
//   - The amount is positive
//
// Security note: The toHoldingID validation is critical. Without it, an
// attacker who compromises an admin account could transfer funds to any
// arbitrary bank account. By restricting transfers to pre-approved holding
// accounts, we limit the blast radius of a compromised admin session.
//
// Parameters:
//   - fromAccountID: the bank account to transfer funds from
//   - toHoldingID: the holding account to transfer funds to (must be pre-approved)
//   - amount: the transfer amount in THB (must be positive)
//   - adminID: the UUID of the admin initiating the transfer (for audit trail)
//
// Returns the created Transfer object with status "pending".
func (s *TransferService) CreateTransfer(ctx context.Context, fromAccountID, toHoldingID uuid.UUID, amount decimal.Decimal, adminID uuid.UUID) (*repository.Transfer, error) {
	s.log.Info("creating transfer",
		slog.String("from_account_id", fromAccountID.String()),
		slog.String("to_holding_id", toHoldingID.String()),
		slog.String("amount", amount.String()),
		slog.String("admin_id", adminID.String()),
	)

	// -----------------------------------------------------------------------
	// Step 1: Validate the transfer amount is positive.
	// -----------------------------------------------------------------------
	if amount.IsZero() || amount.IsNegative() {
		return nil, fmt.Errorf("transfer amount must be positive, got %s", amount)
	}

	// -----------------------------------------------------------------------
	// Step 2: Validate that the source bank account exists and is valid.
	// -----------------------------------------------------------------------
	_, err := s.repo.GetAccountByID(ctx, fromAccountID)
	if err != nil {
		return nil, fmt.Errorf("create transfer: invalid from_account_id: %w", err)
	}

	// -----------------------------------------------------------------------
	// Step 3: SECURITY CHECK — validate that the destination is a
	// pre-approved holding account.
	//
	// This prevents funds from being transferred to arbitrary bank accounts
	// even if an admin's session is compromised. Only accounts registered
	// in the holding_accounts table are valid destinations.
	// -----------------------------------------------------------------------
	valid, err := s.repo.ValidateHoldingAccount(ctx, toHoldingID)
	if err != nil {
		return nil, fmt.Errorf("create transfer: validate holding account: %w", err)
	}
	if !valid {
		s.log.Error("transfer to non-holding account rejected",
			slog.String("to_holding_id", toHoldingID.String()),
			slog.String("admin_id", adminID.String()),
		)
		return nil, fmt.Errorf("holding account %s is not registered — transfer rejected for security", toHoldingID)
	}

	// -----------------------------------------------------------------------
	// Step 4: Create the transfer record with "pending" status.
	// -----------------------------------------------------------------------
	now := time.Now().UTC()
	transfer := &repository.Transfer{
		ID:            uuid.New(), // Generate a unique ID for this transfer.
		FromAccountID: fromAccountID,
		ToHoldingID:   toHoldingID,
		Amount:        amount,
		Status:        repository.TransferStatusPending,
		Reference:     "",       // Will be set when completed.
		AdminID:       adminID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Insert the transfer into the database.
	if err := s.repo.InsertTransfer(ctx, transfer); err != nil {
		return nil, fmt.Errorf("create transfer: insert: %w", err)
	}

	s.log.Info("transfer created",
		slog.String("transfer_id", transfer.ID.String()),
		slog.String("status", string(transfer.Status)),
	)

	return transfer, nil
}

// ---------------------------------------------------------------------------
// CompleteTransfer — mark a transfer as completed with a bank reference
// ---------------------------------------------------------------------------

// CompleteTransfer updates a pending transfer to "completed" status and
// records the bank reference number. This is called by an admin after
// they have confirmed the bank transfer was processed successfully.
//
// Parameters:
//   - transferID: the UUID of the transfer to complete
//   - reference: the bank-provided reference/confirmation number
//
// Returns an error if the transfer does not exist or the update fails.
func (s *TransferService) CompleteTransfer(ctx context.Context, transferID uuid.UUID, reference string) error {
	s.log.Info("completing transfer",
		slog.String("transfer_id", transferID.String()),
		slog.String("reference", reference),
	)

	// Validate that a reference number was provided.
	if reference == "" {
		return fmt.Errorf("bank reference is required to complete a transfer")
	}

	// Update the transfer status to "completed" with the reference.
	if err := s.repo.UpdateTransferStatus(ctx, transferID, repository.TransferStatusCompleted, reference); err != nil {
		return fmt.Errorf("complete transfer: %w", err)
	}

	s.log.Info("transfer completed",
		slog.String("transfer_id", transferID.String()),
		slog.String("reference", reference),
	)

	return nil
}

// ---------------------------------------------------------------------------
// GetTransfers — paginated list of all transfers
// ---------------------------------------------------------------------------

// GetTransfers returns a paginated list of transfers ordered by creation
// time (newest first). This powers the admin dashboard's transfer history
// table.
//
// Parameters:
//   - page: the 1-based page number (converted to offset internally)
//   - limit: the number of transfers per page (max 100)
//
// Returns:
//   - transfers: the list of transfers for the requested page
//   - total: the total number of transfers (for pagination metadata)
//   - error: any error that occurred during the query
func (s *TransferService) GetTransfers(ctx context.Context, page, limit int) ([]repository.Transfer, int, error) {
	// Validate and clamp pagination parameters.
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20 // Default page size.
	}
	if limit > 100 {
		limit = 100 // Maximum page size to prevent abuse.
	}

	// Convert page number to offset.
	offset := (page - 1) * limit

	// Delegate to the repository.
	transfers, total, err := s.repo.GetTransfers(ctx, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("get transfers: %w", err)
	}

	return transfers, total, nil
}

// ---------------------------------------------------------------------------
// GetDailyTransferSummary — aggregate transfer data for a day
// ---------------------------------------------------------------------------

// GetDailyTransferSummary returns aggregated transfer statistics for the
// specified date. This includes total count, total amount, and breakdowns
// by status (completed, pending, failed).
//
// Parameters:
//   - date: the calendar date to summarise (only the date portion is used)
//
// Returns a TransferDailySummary with the aggregated data.
func (s *TransferService) GetDailyTransferSummary(ctx context.Context, date time.Time) (*repository.TransferDailySummary, error) {
	summary, err := s.repo.GetDailyTransferSummary(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("get daily transfer summary: %w", err)
	}

	return summary, nil
}
