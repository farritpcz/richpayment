package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type OwnerType string

const (
	OwnerTypeMerchant OwnerType = "merchant"
	OwnerTypeAgent    OwnerType = "agent"
	OwnerTypePartner  OwnerType = "partner"
	OwnerTypeSystem   OwnerType = "system"
)

type Wallet struct {
	ID          uuid.UUID
	OwnerType   OwnerType
	OwnerID     uuid.UUID
	Currency    string
	Balance     decimal.Decimal
	HoldBalance decimal.Decimal
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type LedgerEntryType string

const (
	LedgerDepositCredit       LedgerEntryType = "deposit_credit"
	LedgerWithdrawalDebit     LedgerEntryType = "withdrawal_debit"
	LedgerWithdrawalHold      LedgerEntryType = "withdrawal_hold"
	LedgerWithdrawalRelease   LedgerEntryType = "withdrawal_release"
	LedgerFeeDebit            LedgerEntryType = "fee_debit"
	LedgerCommissionCredit    LedgerEntryType = "commission_credit"
	LedgerCommissionPayout    LedgerEntryType = "commission_payout_debit"
	LedgerAdjustment          LedgerEntryType = "adjustment"
)

type WalletLedger struct {
	ID            int64
	WalletID      uuid.UUID
	EntryType     LedgerEntryType
	ReferenceType string
	ReferenceID   uuid.UUID
	Amount        decimal.Decimal
	BalanceAfter  decimal.Decimal
	Description   string
	CreatedAt     time.Time
}
