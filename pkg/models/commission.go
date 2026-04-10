// Package models defines the core domain types shared across all services in
// the RichPayment platform. This file contains the commission domain models
// that track how transaction fees are split between the system, agents, and
// partners.
package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// TransactionType distinguishes between deposit and withdrawal transactions
// for commission calculation purposes.
type TransactionType string

const (
	// TransactionTypeDeposit is a deposit (money-in) transaction.
	TransactionTypeDeposit TransactionType = "deposit"

	// TransactionTypeWithdrawal is a withdrawal (money-out) transaction.
	TransactionTypeWithdrawal TransactionType = "withdrawal"
)

// Commission records the fee breakdown for a single transaction. When a deposit
// or withdrawal completes, the platform fee is split among the system, the
// merchant's agent (if any), and the agent's partner (if any) according to
// their configured percentages.
type Commission struct {
	// ID is the unique identifier for this commission record (UUID v4).
	ID uuid.UUID

	// TransactionType indicates whether this commission came from a deposit
	// or a withdrawal.
	TransactionType TransactionType

	// TransactionID is the UUID of the deposit order or withdrawal that
	// generated this commission.
	TransactionID uuid.UUID

	// MerchantID is the UUID of the merchant whose transaction triggered the fee.
	MerchantID uuid.UUID

	// TotalFeeAmount is the gross fee charged to the merchant for this
	// transaction (before splitting).
	TotalFeeAmount decimal.Decimal

	// SystemAmount is the portion of the fee retained by the platform.
	SystemAmount decimal.Decimal

	// AgentID is the UUID of the agent who manages the merchant. Nil if the
	// merchant has no agent.
	AgentID *uuid.UUID

	// AgentAmount is the agent's share of the fee.
	AgentAmount decimal.Decimal

	// PartnerID is the UUID of the partner associated with the agent. Nil if
	// the agent has no partner.
	PartnerID *uuid.UUID

	// PartnerAmount is the partner's share of the fee.
	PartnerAmount decimal.Decimal

	// MerchantFeePct is the fee percentage applied to the merchant's
	// transaction (snapshotted at the time of the transaction for audit).
	MerchantFeePct decimal.Decimal

	// AgentPct is the agent's commission percentage at the time of the
	// transaction.
	AgentPct decimal.Decimal

	// PartnerPct is the partner's commission percentage at the time of the
	// transaction.
	PartnerPct decimal.Decimal

	// Currency is the ISO 4217 currency code (e.g. "THB").
	Currency string

	// CreatedAt records when the commission was recorded (UTC).
	CreatedAt time.Time
}

// CommissionDailySummary is a pre-aggregated daily roll-up of commissions for
// a specific owner. It is used by the reporting dashboard to display daily
// totals without scanning every individual commission row.
type CommissionDailySummary struct {
	// ID is the auto-incrementing primary key.
	ID int64

	// SummaryDate is the calendar date (UTC) that this summary covers.
	SummaryDate time.Time

	// OwnerType identifies who the summary is for (merchant, agent, partner, system).
	OwnerType OwnerType

	// OwnerID is the UUID of the entity this summary belongs to.
	OwnerID uuid.UUID

	// TransactionType indicates whether this summary covers deposits or withdrawals.
	TransactionType TransactionType

	// Currency is the ISO 4217 currency code.
	Currency string

	// TotalTxCount is the number of transactions for this owner on this date.
	TotalTxCount int

	// TotalVolume is the aggregate transaction volume (sum of amounts).
	TotalVolume decimal.Decimal

	// TotalFee is the aggregate fee amount collected.
	TotalFee decimal.Decimal

	// TotalCommission is the aggregate commission earned by this owner.
	TotalCommission decimal.Decimal

	// CreatedAt records when the summary row was first created (UTC).
	CreatedAt time.Time

	// UpdatedAt records when the summary row was last recalculated (UTC).
	UpdatedAt time.Time
}
