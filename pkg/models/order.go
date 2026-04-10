package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusMatched   OrderStatus = "matched"
	OrderStatusCompleted OrderStatus = "completed"
	OrderStatusExpired   OrderStatus = "expired"
	OrderStatusFailed    OrderStatus = "failed"
	OrderStatusCancelled OrderStatus = "cancelled"
)

type MatchedBy string

const (
	MatchedBySMS   MatchedBy = "sms"
	MatchedByEmail MatchedBy = "email"
	MatchedBySlip  MatchedBy = "slip"
)

type DepositOrder struct {
	ID                uuid.UUID
	MerchantID        uuid.UUID
	MerchantOrderID   string
	CustomerName      string
	CustomerBankCode  string
	RequestedAmount   decimal.Decimal
	AdjustedAmount    decimal.Decimal
	ActualAmount      decimal.Decimal
	FeeAmount         decimal.Decimal
	NetAmount         decimal.Decimal
	Currency          string
	BankAccountID     uuid.UUID
	MatchedBy         MatchedBy
	MatchedAt         *time.Time
	Status            OrderStatus
	QRPayload         string
	WebhookSent       bool
	WebhookAttempts   int
	ExpiresAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
