package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type TransactionType string

const (
	TransactionTypeDeposit    TransactionType = "deposit"
	TransactionTypeWithdrawal TransactionType = "withdrawal"
)

type Commission struct {
	ID              uuid.UUID
	TransactionType TransactionType
	TransactionID   uuid.UUID
	MerchantID      uuid.UUID
	TotalFeeAmount  decimal.Decimal
	SystemAmount    decimal.Decimal
	AgentID         *uuid.UUID
	AgentAmount     decimal.Decimal
	PartnerID       *uuid.UUID
	PartnerAmount   decimal.Decimal
	MerchantFeePct  decimal.Decimal
	AgentPct        decimal.Decimal
	PartnerPct      decimal.Decimal
	Currency        string
	CreatedAt       time.Time
}

type CommissionDailySummary struct {
	ID              int64
	SummaryDate     time.Time
	OwnerType       OwnerType
	OwnerID         uuid.UUID
	TransactionType TransactionType
	Currency        string
	TotalTxCount    int
	TotalVolume     decimal.Decimal
	TotalFee        decimal.Decimal
	TotalCommission decimal.Decimal
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
