// Package models defines the core domain types shared across all services in
// the RichPayment platform. This file contains the deposit-order domain model.
package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// OrderStatus represents the lifecycle state of a deposit order as it moves
// through creation, matching, and settlement.
type OrderStatus string

const (
	// OrderStatusPending means the order is waiting for a matching bank
	// transaction (SMS, email notification, or uploaded slip).
	OrderStatusPending OrderStatus = "pending"

	// OrderStatusMatched means an incoming bank transaction has been matched
	// to this order but settlement has not yet completed.
	OrderStatusMatched OrderStatus = "matched"

	// OrderStatusCompleted means the deposit has been fully settled and the
	// merchant's wallet has been credited.
	OrderStatusCompleted OrderStatus = "completed"

	// OrderStatusExpired means the order passed its ExpiresAt deadline
	// without being matched and is no longer valid.
	OrderStatusExpired OrderStatus = "expired"

	// OrderStatusFailed means processing failed after matching (e.g. a
	// ledger error). Manual intervention may be required.
	OrderStatusFailed OrderStatus = "failed"

	// OrderStatusCancelled means the order was explicitly cancelled before
	// matching, either by the merchant or an admin.
	OrderStatusCancelled OrderStatus = "cancelled"
)

// MatchedBy indicates the channel through which an incoming bank transaction
// was detected and matched to a deposit order.
type MatchedBy string

const (
	// MatchedBySMS means the deposit was matched via an incoming SMS
	// notification from the bank.
	MatchedBySMS MatchedBy = "sms"

	// MatchedByEmail means the deposit was matched via an incoming email
	// notification from the bank.
	MatchedByEmail MatchedBy = "email"

	// MatchedBySlip means the deposit was matched via a bank-transfer slip
	// (image) uploaded by the customer or operator.
	MatchedBySlip MatchedBy = "slip"
)

// DepositOrder represents a single deposit transaction in the system. A
// merchant creates a deposit order, and the system waits for an incoming bank
// transfer that matches the order's amount. Once matched, the order is settled
// and the net amount (after fees) is credited to the merchant's wallet.
type DepositOrder struct {
	// ID is the unique identifier for this deposit order (UUID v4).
	ID uuid.UUID

	// MerchantID is the UUID of the merchant that created the order.
	MerchantID uuid.UUID

	// MerchantOrderID is the merchant's own reference for this transaction,
	// used for idempotency and reconciliation on the merchant side.
	MerchantOrderID string

	// CustomerName is the name provided by the end customer making the deposit.
	CustomerName string

	// CustomerBankCode identifies the customer's bank (e.g. "KBANK", "SCB").
	CustomerBankCode string

	// RequestedAmount is the original amount the merchant requested.
	RequestedAmount decimal.Decimal

	// AdjustedAmount is the amount after any small adjustment applied to make
	// the transfer uniquely identifiable (e.g. adding satangs).
	AdjustedAmount decimal.Decimal

	// ActualAmount is the amount actually received from the bank transaction.
	// This may differ slightly from AdjustedAmount due to bank rounding.
	ActualAmount decimal.Decimal

	// FeeAmount is the platform fee charged on this deposit, calculated as
	// ActualAmount * merchant's deposit fee percentage.
	FeeAmount decimal.Decimal

	// NetAmount is the amount credited to the merchant's wallet after
	// subtracting the fee: NetAmount = ActualAmount - FeeAmount.
	NetAmount decimal.Decimal

	// Currency is the ISO 4217 currency code (e.g. "THB").
	Currency string

	// BankAccountID is the UUID of the platform's receiving bank account
	// that was assigned to this deposit order.
	BankAccountID uuid.UUID

	// MatchedBy indicates how the incoming transaction was detected (SMS,
	// email, or slip upload).
	MatchedBy MatchedBy

	// MatchedAt is the timestamp when the order was matched to an incoming
	// bank transaction. Nil while the order is still pending.
	MatchedAt *time.Time

	// Status is the current lifecycle state of the order.
	Status OrderStatus

	// QRPayload contains the PromptPay QR code payload string that the
	// customer can scan to initiate the transfer. Empty if QR is not used.
	QRPayload string

	// WebhookSent indicates whether the merchant's callback URL has been
	// successfully notified of the order's final status.
	WebhookSent bool

	// WebhookAttempts tracks how many times the system has tried to deliver
	// the webhook notification (for retry logic).
	WebhookAttempts int

	// ExpiresAt is the deadline after which the order will be marked as
	// expired if no matching transaction has been found.
	ExpiresAt time.Time

	// CreatedAt records when the order was created (UTC).
	CreatedAt time.Time

	// UpdatedAt records when the order was last modified (UTC).
	UpdatedAt time.Time
}
