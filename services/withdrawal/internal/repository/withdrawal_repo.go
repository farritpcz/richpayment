// Package repository defines the data access interfaces and implementations
// for the withdrawal-service. All database interactions for withdrawal
// requests are abstracted behind the WithdrawalRepository interface so that
// the business-logic layer (service package) remains decoupled from the
// persistence technology.
package repository

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/models"
)

// WithdrawalRepository is the primary data-access interface for withdrawal
// records. Every method accepts a context.Context to support request-scoped
// deadlines, cancellation, and tracing propagation.
type WithdrawalRepository interface {
	// Create persists a new withdrawal record into the database.
	// The withdrawal.ID must be pre-generated (UUID v4) by the caller.
	// Returns an error if the insert fails (e.g. duplicate ID).
	Create(ctx context.Context, w *models.Withdrawal) error

	// GetByID retrieves a single withdrawal by its unique identifier.
	// Returns (nil, ErrNotFound) when no row matches the given id.
	GetByID(ctx context.Context, id uuid.UUID) (*models.Withdrawal, error)

	// UpdateStatus transitions a withdrawal to a new status and applies
	// any additional field changes described in the fields map. The fields
	// map uses column names as keys and new values as values.
	UpdateStatus(ctx context.Context, id uuid.UUID, status models.WithdrawalStatus, fields map[string]interface{}) error

	// ListPending returns all withdrawals with status "pending", ordered by
	// created_at ascending (oldest first). Supports pagination via offset
	// and limit. Also returns the total count of pending withdrawals for
	// pagination metadata.
	ListPending(ctx context.Context, offset, limit int) ([]models.Withdrawal, int, error)

	// SumDailyWithdrawals calculates the total withdrawal amount for a
	// merchant on the given date. This is used to enforce daily withdrawal
	// limits. Only withdrawals with status "pending", "approved", or
	// "completed" are included in the sum (rejected/failed are excluded).
	SumDailyWithdrawals(ctx context.Context, merchantID uuid.UUID, date time.Time) (decimal.Decimal, error)
}

// ---------------------------------------------------------------------------
// StubWithdrawalRepo — in-memory implementation
// ---------------------------------------------------------------------------

// StubWithdrawalRepo is an in-memory implementation of WithdrawalRepository
// suitable for development, testing, and compilation verification. It stores
// all withdrawals in a Go map protected by a read-write mutex.
type StubWithdrawalRepo struct {
	// mu protects the withdrawals map from concurrent access.
	mu sync.RWMutex

	// withdrawals stores withdrawal records keyed by their UUID.
	withdrawals map[uuid.UUID]*models.Withdrawal
}

// NewStubWithdrawalRepo creates and returns a new StubWithdrawalRepo with
// an initialised (empty) map. Call this once during service bootstrap.
func NewStubWithdrawalRepo() *StubWithdrawalRepo {
	return &StubWithdrawalRepo{
		withdrawals: make(map[uuid.UUID]*models.Withdrawal),
	}
}

// Create stores a new withdrawal record in the in-memory map.
// Returns an error if a withdrawal with the same ID already exists.
func (r *StubWithdrawalRepo) Create(_ context.Context, w *models.Withdrawal) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate ID to simulate a primary key constraint.
	if _, exists := r.withdrawals[w.ID]; exists {
		return errors.New("DUPLICATE_WITHDRAWAL", "withdrawal with this ID already exists", 409)
	}

	// Store a copy to prevent the caller from mutating the stored record.
	clone := *w
	r.withdrawals[w.ID] = &clone
	return nil
}

// GetByID retrieves a withdrawal by its UUID from the in-memory map.
// Returns ErrNotFound if no withdrawal with the given ID exists.
func (r *StubWithdrawalRepo) GetByID(_ context.Context, id uuid.UUID) (*models.Withdrawal, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Look up the withdrawal in the map.
	w, exists := r.withdrawals[id]
	if !exists {
		return nil, errors.ErrNotFound
	}

	// Return a copy to prevent the caller from mutating the stored record.
	clone := *w
	return &clone, nil
}

// UpdateStatus transitions a withdrawal to a new status and applies the
// given field updates. Returns ErrNotFound if the withdrawal does not exist.
func (r *StubWithdrawalRepo) UpdateStatus(_ context.Context, id uuid.UUID, status models.WithdrawalStatus, fields map[string]interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Look up the withdrawal to update.
	w, exists := r.withdrawals[id]
	if !exists {
		return errors.ErrNotFound
	}

	// Apply the new status.
	w.Status = status

	// Apply each additional field update.
	for key, val := range fields {
		switch key {
		case "approved_by":
			// Set the admin UUID who approved the withdrawal.
			if v, ok := val.(*uuid.UUID); ok {
				w.ApprovedBy = v
			}
		case "approved_at":
			// Set the approval timestamp.
			if v, ok := val.(*time.Time); ok {
				w.ApprovedAt = v
			}
		case "rejected_by":
			// Set the admin UUID who rejected the withdrawal.
			if v, ok := val.(*uuid.UUID); ok {
				w.RejectedBy = v
			}
		case "rejected_at":
			// Set the rejection timestamp.
			if v, ok := val.(*time.Time); ok {
				w.RejectedAt = v
			}
		case "rejection_reason":
			// Set the human-readable rejection reason.
			if v, ok := val.(string); ok {
				w.RejectionReason = v
			}
		case "transfer_ref":
			// Set the external bank transfer reference number.
			if v, ok := val.(string); ok {
				w.TransferRef = v
			}
		case "proof_url":
			// Set the URL to the transfer proof document.
			if v, ok := val.(string); ok {
				w.ProofURL = v
			}
		case "completed_at":
			// Set the completion timestamp.
			if v, ok := val.(*time.Time); ok {
				w.CompletedAt = v
			}
		case "fee_amount":
			// Set the calculated fee amount.
			if v, ok := val.(decimal.Decimal); ok {
				w.FeeAmount = v
			}
		case "net_amount":
			// Set the calculated net amount after fees.
			if v, ok := val.(decimal.Decimal); ok {
				w.NetAmount = v
			}
		}
	}

	return nil
}

// ListPending returns all withdrawals with status "pending", applying
// pagination via offset and limit. Returns the paginated slice and the
// total count of pending withdrawals.
func (r *StubWithdrawalRepo) ListPending(_ context.Context, offset, limit int) ([]models.Withdrawal, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect all pending withdrawals into a slice.
	pending := make([]models.Withdrawal, 0)
	for _, w := range r.withdrawals {
		if w.Status == models.WithdrawalStatusPending {
			pending = append(pending, *w)
		}
	}

	// Calculate total before pagination.
	total := len(pending)

	// Apply offset: skip the first `offset` records.
	if offset >= len(pending) {
		return nil, total, nil
	}
	pending = pending[offset:]

	// Apply limit: return at most `limit` records.
	if limit > 0 && limit < len(pending) {
		pending = pending[:limit]
	}

	return pending, total, nil
}

// SumDailyWithdrawals calculates the total withdrawal amount for a merchant
// on the given date. Only withdrawals with status pending, approved, or
// completed are included. Rejected and failed withdrawals are excluded
// because those funds have been (or will be) released back to the wallet.
func (r *StubWithdrawalRepo) SumDailyWithdrawals(_ context.Context, merchantID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Initialise the running sum to zero.
	sum := decimal.Zero

	// Determine the start and end of the target day (UTC).
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)

	// Iterate over all withdrawals and accumulate matching amounts.
	for _, w := range r.withdrawals {
		// Skip withdrawals for other merchants.
		if w.MerchantID != merchantID {
			continue
		}

		// Skip withdrawals outside the target date range.
		if w.CreatedAt.Before(dayStart) || !w.CreatedAt.Before(dayEnd) {
			continue
		}

		// Skip rejected and failed withdrawals — those funds are released.
		if w.Status == models.WithdrawalStatusRejected || w.Status == models.WithdrawalStatusFailed {
			continue
		}

		// Add this withdrawal's amount to the daily sum.
		sum = sum.Add(w.Amount)
	}

	return sum, nil
}

// Compile-time assertion: StubWithdrawalRepo must implement WithdrawalRepository.
var _ WithdrawalRepository = (*StubWithdrawalRepo)(nil)
