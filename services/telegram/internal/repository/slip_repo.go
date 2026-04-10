// Package repository defines the data-access interfaces and stub
// implementations for the telegram-service. These interfaces abstract away
// the underlying storage engine (PostgreSQL) so that the service layer can
// be tested with mock implementations and is not coupled to a specific
// database driver.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// SlipVerification — domain model for a verified slip record.
// ---------------------------------------------------------------------------

// SlipVerificationStatus represents the outcome of a slip verification attempt.
// Each status maps to a specific result from the verification pipeline.
type SlipVerificationStatus string

const (
	// SlipStatusVerified means the slip was successfully verified and matched
	// to a pending deposit order.
	SlipStatusVerified SlipVerificationStatus = "verified"

	// SlipStatusDuplicate means the slip (by image hash or transaction ref)
	// has already been submitted and processed.
	SlipStatusDuplicate SlipVerificationStatus = "duplicate"

	// SlipStatusNoMatch means the slip was valid but no pending order matched
	// the slip amount and merchant combination.
	SlipStatusNoMatch SlipVerificationStatus = "no_match"

	// SlipStatusAlreadyCompleted means the matching order was already
	// completed by another channel (e.g. SMS notification arrived first).
	SlipStatusAlreadyCompleted SlipVerificationStatus = "already_completed"

	// SlipStatusFailed means the slip verification failed (e.g. EasySlip
	// API rejected it, image was unreadable, or an internal error occurred).
	SlipStatusFailed SlipVerificationStatus = "failed"
)

// SlipVerification represents a single slip verification attempt stored in
// the slip_verifications table. It captures the full context of the
// verification: who submitted it, what was extracted, and the outcome.
type SlipVerification struct {
	// ID is the unique identifier for this verification record (UUID v4).
	ID uuid.UUID

	// MerchantID is the UUID of the merchant whose Telegram group received
	// the slip photo.
	MerchantID uuid.UUID

	// TelegramGroupID is the Telegram chat ID of the group where the slip
	// was posted. Used for sending reply messages.
	TelegramGroupID int64

	// TelegramMessageID is the message ID of the original slip photo in
	// the Telegram group. Used for reply-to functionality.
	TelegramMessageID int

	// ImageHash is the SHA-256 hex digest of the raw slip image bytes.
	// Used for duplicate detection — if two slips produce the same hash,
	// the second is rejected as a duplicate.
	ImageHash string

	// TransactionRef is the unique transaction reference extracted from the
	// slip by the EasySlip API (e.g. the bank's reference number).
	// Also used for duplicate detection at the transaction level.
	TransactionRef string

	// Amount is the transfer amount extracted from the slip by EasySlip.
	Amount decimal.Decimal

	// SenderName is the name of the person who initiated the transfer,
	// as extracted from the slip data.
	SenderName string

	// ReceiverName is the name of the transfer recipient, as extracted
	// from the slip data.
	ReceiverName string

	// OrderID is the UUID of the deposit order that was matched to this
	// slip. Nil if no match was found or if verification failed.
	OrderID *uuid.UUID

	// Status is the final outcome of the verification attempt.
	Status SlipVerificationStatus

	// StatusDetail provides a human-readable explanation of the status,
	// such as "duplicate image hash" or "order already completed by SMS".
	StatusDetail string

	// RawResponse stores the full JSON response from the EasySlip API
	// for debugging and audit purposes.
	RawResponse string

	// CreatedAt records when this verification was performed (UTC).
	CreatedAt time.Time
}

// ---------------------------------------------------------------------------
// SlipRepository — interface for slip verification persistence.
// ---------------------------------------------------------------------------

// SlipRepository defines the persistence operations for slip verifications.
// Implementations must be safe for concurrent use by multiple goroutines.
type SlipRepository interface {
	// Create inserts a new SlipVerification record into the database.
	// The ID and CreatedAt fields should be populated before calling this
	// method. Returns an error if the insert fails (e.g. constraint violation).
	Create(ctx context.Context, sv *SlipVerification) error

	// GetByImageHash looks up a slip verification by its SHA-256 image hash.
	// Returns nil and no error if no record exists with that hash.
	// This is used for duplicate detection at the image level.
	GetByImageHash(ctx context.Context, imageHash string) (*SlipVerification, error)

	// GetByTransactionRef looks up a slip verification by its bank
	// transaction reference. Returns nil and no error if no record exists.
	// This is used for duplicate detection at the transaction level.
	GetByTransactionRef(ctx context.Context, ref string) (*SlipVerification, error)
}

// ---------------------------------------------------------------------------
// StubSlipRepository — in-memory stub for development and testing.
// ---------------------------------------------------------------------------

// StubSlipRepository is an in-memory implementation of SlipRepository that
// stores slip verifications in a slice. It is NOT safe for concurrent use
// and is intended only for local development and unit testing.
type StubSlipRepository struct {
	// records holds all slip verifications that have been created through
	// this stub. Searched linearly for lookups.
	records []*SlipVerification
}

// NewStubSlipRepository constructs a new StubSlipRepository with an empty
// record set. Use this in tests or during local development when a real
// PostgreSQL connection is not available.
func NewStubSlipRepository() *StubSlipRepository {
	return &StubSlipRepository{
		records: make([]*SlipVerification, 0),
	}
}

// Create appends the given SlipVerification to the in-memory records slice.
// Always returns nil (no error) because there are no real constraints to
// violate in the stub.
func (r *StubSlipRepository) Create(_ context.Context, sv *SlipVerification) error {
	// Append the record to the in-memory store.
	r.records = append(r.records, sv)
	return nil
}

// GetByImageHash performs a linear scan of all records to find one matching
// the given SHA-256 image hash. Returns the first match, or nil if no
// record has that hash.
func (r *StubSlipRepository) GetByImageHash(_ context.Context, imageHash string) (*SlipVerification, error) {
	// Iterate through all records looking for a matching image hash.
	for _, rec := range r.records {
		if rec.ImageHash == imageHash {
			return rec, nil
		}
	}
	// No match found — this is not an error, just means no duplicate.
	return nil, nil
}

// GetByTransactionRef performs a linear scan of all records to find one
// matching the given transaction reference. Returns the first match, or
// nil if no record has that reference.
func (r *StubSlipRepository) GetByTransactionRef(_ context.Context, ref string) (*SlipVerification, error) {
	// Iterate through all records looking for a matching transaction ref.
	for _, rec := range r.records {
		if rec.TransactionRef == ref {
			return rec, nil
		}
	}
	// No match found — not an error.
	return nil, nil
}
