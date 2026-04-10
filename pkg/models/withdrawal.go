// Package models defines the core domain types shared across all services in
// the RichPayment platform. This file contains the Withdrawal domain model
// used by the withdrawal-service.
package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Withdrawal status lifecycle
// ---------------------------------------------------------------------------

// WithdrawalStatus represents the current state of a withdrawal request
// as it moves through the approval and settlement pipeline.
type WithdrawalStatus string

const (
	// WithdrawalStatusPending means the withdrawal has been created and the
	// merchant's wallet balance has been held, awaiting admin approval.
	WithdrawalStatusPending WithdrawalStatus = "pending"

	// WithdrawalStatusApproved means an admin has approved the withdrawal
	// and it is ready for the finance team to execute the bank transfer.
	WithdrawalStatusApproved WithdrawalStatus = "approved"

	// WithdrawalStatusRejected means an admin has rejected the withdrawal.
	// The held balance has been released back to the merchant's wallet.
	WithdrawalStatusRejected WithdrawalStatus = "rejected"

	// WithdrawalStatusCompleted means the bank transfer has been executed
	// and confirmed. The held balance has been debited and commission recorded.
	WithdrawalStatusCompleted WithdrawalStatus = "completed"

	// WithdrawalStatusFailed means the bank transfer failed after approval.
	// The held balance should be released back to the merchant's wallet.
	WithdrawalStatusFailed WithdrawalStatus = "failed"
)

// ---------------------------------------------------------------------------
// Destination type
// ---------------------------------------------------------------------------

// WithdrawalDestType indicates the type of destination for the withdrawal
// funds (e.g. bank transfer, crypto wallet, e-wallet).
type WithdrawalDestType string

const (
	// WithdrawalDestBank is a standard bank account transfer.
	WithdrawalDestBank WithdrawalDestType = "bank"

	// WithdrawalDestPromptPay is a PromptPay (Thai instant payment) transfer.
	WithdrawalDestPromptPay WithdrawalDestType = "promptpay"
)

// ---------------------------------------------------------------------------
// Withdrawal model
// ---------------------------------------------------------------------------

// Withdrawal represents a merchant's request to withdraw funds from their
// RichPayment wallet to an external bank account or payment method. The
// withdrawal goes through a multi-step approval process:
//
//  1. Merchant creates the withdrawal -> status = pending, balance held.
//  2. Admin approves or rejects -> status = approved / rejected.
//  3. Finance team executes the transfer -> status = completed / failed.
//
// Each transition triggers corresponding wallet ledger entries and, on
// completion, a commission record for the withdrawal fee.
type Withdrawal struct {
	// ID is the unique identifier for the withdrawal (UUID v4).
	ID uuid.UUID `json:"id"`

	// MerchantID is the UUID of the merchant requesting the withdrawal.
	MerchantID uuid.UUID `json:"merchant_id"`

	// Amount is the gross withdrawal amount in the specified currency.
	Amount decimal.Decimal `json:"amount"`

	// FeeAmount is the withdrawal fee deducted from the gross amount.
	// Calculated as Amount * merchant's withdrawal fee percentage.
	FeeAmount decimal.Decimal `json:"fee_amount"`

	// NetAmount is the amount actually transferred to the destination
	// after deducting fees: NetAmount = Amount - FeeAmount.
	NetAmount decimal.Decimal `json:"net_amount"`

	// Currency is the ISO 4217 currency code (e.g. "THB").
	Currency string `json:"currency"`

	// DestType indicates the type of destination (bank, promptpay, etc.).
	DestType WithdrawalDestType `json:"dest_type"`

	// DestDetails is a JSON-encoded string containing destination-specific
	// information (e.g. bank name, account number, account holder name for
	// bank transfers, or phone number for PromptPay).
	DestDetails string `json:"dest_details"`

	// Status is the current lifecycle state of the withdrawal.
	Status WithdrawalStatus `json:"status"`

	// ApprovedBy is the UUID of the admin who approved the withdrawal.
	// Nil if the withdrawal has not been approved yet.
	ApprovedBy *uuid.UUID `json:"approved_by,omitempty"`

	// ApprovedAt is the timestamp when the withdrawal was approved.
	// Nil if the withdrawal has not been approved yet.
	ApprovedAt *time.Time `json:"approved_at,omitempty"`

	// RejectedBy is the UUID of the admin who rejected the withdrawal.
	// Nil if the withdrawal has not been rejected.
	RejectedBy *uuid.UUID `json:"rejected_by,omitempty"`

	// RejectedAt is the timestamp when the withdrawal was rejected.
	// Nil if the withdrawal has not been rejected.
	RejectedAt *time.Time `json:"rejected_at,omitempty"`

	// RejectionReason is the human-readable reason for rejection.
	// Empty string if the withdrawal has not been rejected.
	RejectionReason string `json:"rejection_reason,omitempty"`

	// TransferRef is the external reference number from the bank transfer
	// (e.g. transaction ID from the bank). Set when the withdrawal completes.
	TransferRef string `json:"transfer_ref,omitempty"`

	// ProofURL is the URL to the proof of transfer document (e.g. screenshot
	// or PDF of the bank transfer confirmation). Set when completing.
	ProofURL string `json:"proof_url,omitempty"`

	// CompletedAt is the timestamp when the bank transfer was confirmed.
	// Nil if the withdrawal has not been completed yet.
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// CreatedAt records when the withdrawal was created (UTC).
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt records when the withdrawal was last modified (UTC).
	UpdatedAt time.Time `json:"updated_at"`
}
