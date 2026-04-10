// Package models defines the core domain types shared across all services in
// the RichPayment platform. This file contains the wallet and ledger domain
// models used for balance tracking and double-entry bookkeeping.
package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// OwnerType identifies the kind of entity that owns a wallet. Each actor in
// the commission hierarchy (merchant, agent, partner) has its own wallet, and
// there is a special "system" wallet for platform-level funds.
type OwnerType string

const (
	// OwnerTypeMerchant indicates the wallet belongs to a merchant.
	OwnerTypeMerchant OwnerType = "merchant"

	// OwnerTypeAgent indicates the wallet belongs to an agent.
	OwnerTypeAgent OwnerType = "agent"

	// OwnerTypePartner indicates the wallet belongs to a partner.
	OwnerTypePartner OwnerType = "partner"

	// OwnerTypeSystem indicates the wallet belongs to the platform itself,
	// used to collect the system's share of fees.
	OwnerTypeSystem OwnerType = "system"
)

// Wallet represents a currency balance held by a merchant, agent, partner, or
// the system. It supports an optimistic-locking Version field to prevent
// concurrent updates from producing inconsistent balances.
type Wallet struct {
	// ID is the unique identifier for this wallet (UUID v4).
	ID uuid.UUID

	// OwnerType indicates who owns the wallet (merchant, agent, partner, system).
	OwnerType OwnerType

	// OwnerID is the UUID of the owning entity in its respective table.
	OwnerID uuid.UUID

	// Currency is the ISO 4217 currency code (e.g. "THB").
	Currency string

	// Balance is the total wallet balance including held funds.
	Balance decimal.Decimal

	// HoldBalance is the portion of Balance that is reserved (e.g. for
	// pending withdrawals). The available balance is Balance - HoldBalance.
	HoldBalance decimal.Decimal

	// Version is an optimistic-lock counter incremented on every balance
	// update. UPDATE statements include a WHERE version = ? clause to detect
	// concurrent modifications.
	Version int64

	// CreatedAt records when the wallet was created (UTC).
	CreatedAt time.Time

	// UpdatedAt records when the wallet was last modified (UTC).
	UpdatedAt time.Time
}

// LedgerEntryType categorises individual wallet ledger entries. Every balance
// change is recorded as a ledger row for auditability and reconciliation.
type LedgerEntryType string

const (
	// LedgerDepositCredit records funds added to a wallet after a deposit settles.
	LedgerDepositCredit LedgerEntryType = "deposit_credit"

	// LedgerWithdrawalDebit records the final deduction when a withdrawal completes.
	LedgerWithdrawalDebit LedgerEntryType = "withdrawal_debit"

	// LedgerWithdrawalHold records funds moved from available balance to hold
	// when a withdrawal is first created (pending approval).
	LedgerWithdrawalHold LedgerEntryType = "withdrawal_hold"

	// LedgerWithdrawalRelease records funds returned from hold to available
	// balance when a withdrawal is rejected or fails.
	LedgerWithdrawalRelease LedgerEntryType = "withdrawal_release"

	// LedgerFeeDebit records a platform fee deducted from the merchant's wallet.
	LedgerFeeDebit LedgerEntryType = "fee_debit"

	// LedgerCommissionCredit records commission earnings credited to an agent's
	// or partner's wallet.
	LedgerCommissionCredit LedgerEntryType = "commission_credit"

	// LedgerCommissionPayout records a commission payout debit from the agent's
	// or partner's wallet when they withdraw their earnings.
	LedgerCommissionPayout LedgerEntryType = "commission_payout_debit"

	// LedgerAdjustment records a manual balance adjustment made by an admin
	// for correction or reconciliation purposes.
	LedgerAdjustment LedgerEntryType = "adjustment"
)

// WalletLedger is an append-only audit log entry that records every change to
// a wallet's balance. Each row captures what happened, why (via reference),
// and the resulting balance after the operation.
type WalletLedger struct {
	// ID is the auto-incrementing primary key for ordering entries.
	ID int64

	// WalletID is the UUID of the wallet this entry belongs to.
	WalletID uuid.UUID

	// EntryType categorises the ledger entry (deposit, withdrawal, fee, etc.).
	EntryType LedgerEntryType

	// ReferenceType identifies the source entity (e.g. "deposit_order",
	// "withdrawal", "commission").
	ReferenceType string

	// ReferenceID is the UUID of the source entity that triggered this entry.
	ReferenceID uuid.UUID

	// Amount is the signed value of the balance change (positive for credits,
	// negative for debits).
	Amount decimal.Decimal

	// BalanceAfter is the wallet's total balance immediately after this entry
	// was applied.
	BalanceAfter decimal.Decimal

	// Description is a human-readable note explaining the entry.
	Description string

	// CreatedAt records when the ledger entry was created (UTC).
	CreatedAt time.Time
}
