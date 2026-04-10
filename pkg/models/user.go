// Package models defines the core domain types shared across all services in
// the RichPayment platform. This file contains the user-domain models: Admin,
// Merchant, Agent, and Partner.
package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Admin
// ---------------------------------------------------------------------------

// AdminStatus represents the current lifecycle state of an admin account.
// Admins progress through these states based on administrative actions.
type AdminStatus string

const (
	// AdminStatusActive indicates the admin can log in and perform actions.
	AdminStatusActive AdminStatus = "active"

	// AdminStatusSuspended indicates the admin is temporarily blocked.
	AdminStatusSuspended AdminStatus = "suspended"

	// AdminStatusDeleted indicates the admin has been soft-deleted.
	AdminStatusDeleted AdminStatus = "deleted"
)

// Admin represents a back-office administrator in the RichPayment system.
// Admins manage merchants, agents, partners, and system configuration.
// The RoleMask is a bitmask that encodes which permissions the admin holds.
type Admin struct {
	// ID is the unique identifier for the admin (UUID v4).
	ID uuid.UUID `json:"id"`

	// Email is the admin's login email address. Must be unique across all admins.
	Email string `json:"email"`

	// PasswordHash is the bcrypt hash of the admin's password.
	// Never serialised to JSON responses.
	PasswordHash string `json:"-"`

	// DisplayName is the human-readable name shown in the admin dashboard.
	DisplayName string `json:"display_name"`

	// RoleMask is a bitmask of permissions. Each bit represents a specific
	// capability (e.g. bit 0 = view merchants, bit 1 = manage merchants,
	// bit 2 = manage agents, bit 3 = manage finances, etc.).
	RoleMask int64 `json:"role_mask"`

	// Status indicates whether the admin account is active, suspended, or deleted.
	Status AdminStatus `json:"status"`

	// CreatedAt records when the admin account was created (UTC).
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt records when the admin account was last modified (UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Merchant
// ---------------------------------------------------------------------------

// MerchantStatus represents the current lifecycle state of a merchant account.
type MerchantStatus string

const (
	// MerchantStatusPending means the merchant is awaiting approval.
	MerchantStatusPending MerchantStatus = "pending"

	// MerchantStatusActive means the merchant can process transactions.
	MerchantStatusActive MerchantStatus = "active"

	// MerchantStatusSuspended means the merchant is temporarily blocked.
	MerchantStatusSuspended MerchantStatus = "suspended"

	// MerchantStatusDeleted means the merchant has been soft-deleted.
	MerchantStatusDeleted MerchantStatus = "deleted"
)

// Merchant represents a payment-accepting business in the RichPayment system.
// Each merchant has an API key for authentication and an HMAC secret for
// request signing. Merchants are typically associated with an Agent.
type Merchant struct {
	// ID is the unique identifier for the merchant (UUID v4).
	ID uuid.UUID `json:"id"`

	// Name is the merchant's business name.
	Name string `json:"name"`

	// Email is the merchant's contact email address.
	Email string `json:"email"`

	// APIKeyHash is the bcrypt hash of the merchant's API key.
	// The raw API key is only returned once at creation time.
	APIKeyHash string `json:"-"`

	// HMACSecret is the shared secret used to sign webhook payloads and
	// verify incoming API requests via HMAC-SHA256.
	HMACSecret string `json:"-"`

	// WebhookURL is the merchant's callback endpoint for order notifications.
	WebhookURL string `json:"webhook_url"`

	// AgentID is the UUID of the agent who manages this merchant.
	// Nullable — some merchants are managed directly by the system.
	AgentID *uuid.UUID `json:"agent_id,omitempty"`

	// DepositFeePct is the fee percentage charged on deposit transactions.
	// For example, 0.02 means 2%.
	DepositFeePct decimal.Decimal `json:"deposit_fee_pct"`

	// WithdrawalFeePct is the fee percentage charged on withdrawal transactions.
	WithdrawalFeePct decimal.Decimal `json:"withdrawal_fee_pct"`

	// DailyWithdrawalLimit is the maximum total withdrawal amount per day
	// in the merchant's base currency.
	DailyWithdrawalLimit decimal.Decimal `json:"daily_withdrawal_limit"`

	// Status indicates the merchant's current lifecycle state.
	Status MerchantStatus `json:"status"`

	// CreatedAt records when the merchant was created (UTC).
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt records when the merchant was last modified (UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Agent
// ---------------------------------------------------------------------------

// AgentStatus represents the current lifecycle state of an agent account.
type AgentStatus string

const (
	// AgentStatusActive indicates the agent is operational.
	AgentStatusActive AgentStatus = "active"

	// AgentStatusSuspended indicates the agent is temporarily blocked.
	AgentStatusSuspended AgentStatus = "suspended"

	// AgentStatusDeleted indicates the agent has been soft-deleted.
	AgentStatusDeleted AgentStatus = "deleted"
)

// Agent represents an intermediary who manages a portfolio of merchants.
// Agents earn commissions on transactions processed by their merchants.
// Each agent may be associated with a Partner.
type Agent struct {
	// ID is the unique identifier for the agent (UUID v4).
	ID uuid.UUID `json:"id"`

	// Name is the agent's display name.
	Name string `json:"name"`

	// Email is the agent's contact email address. Must be unique.
	Email string `json:"email"`

	// PasswordHash is the bcrypt hash of the agent's password.
	PasswordHash string `json:"-"`

	// PartnerID is the UUID of the partner this agent belongs to.
	// Nullable — some agents operate independently.
	PartnerID *uuid.UUID `json:"partner_id,omitempty"`

	// CommissionPct is the agent's commission percentage on merchant fees.
	// For example, 0.30 means the agent receives 30% of the merchant fee.
	CommissionPct decimal.Decimal `json:"commission_pct"`

	// Status indicates the agent's current lifecycle state.
	Status AgentStatus `json:"status"`

	// CreatedAt records when the agent account was created (UTC).
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt records when the agent account was last modified (UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Partner
// ---------------------------------------------------------------------------

// PartnerStatus represents the current lifecycle state of a partner account.
type PartnerStatus string

const (
	// PartnerStatusActive indicates the partner is operational.
	PartnerStatusActive PartnerStatus = "active"

	// PartnerStatusSuspended indicates the partner is temporarily blocked.
	PartnerStatusSuspended PartnerStatus = "suspended"

	// PartnerStatusDeleted indicates the partner has been soft-deleted.
	PartnerStatusDeleted PartnerStatus = "deleted"
)

// Partner represents a top-level entity that manages a network of agents.
// Partners earn commissions on transactions processed by agents in their
// network. They sit at the top of the commission hierarchy:
// Partner -> Agent -> Merchant.
type Partner struct {
	// ID is the unique identifier for the partner (UUID v4).
	ID uuid.UUID `json:"id"`

	// Name is the partner's business name.
	Name string `json:"name"`

	// Email is the partner's contact email address. Must be unique.
	Email string `json:"email"`

	// PasswordHash is the bcrypt hash of the partner's password.
	PasswordHash string `json:"-"`

	// CommissionPct is the partner's commission percentage on merchant fees
	// processed through their agents. For example, 0.10 means 10% of fees.
	CommissionPct decimal.Decimal `json:"commission_pct"`

	// Status indicates the partner's current lifecycle state.
	Status PartnerStatus `json:"status"`

	// CreatedAt records when the partner account was created (UTC).
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt records when the partner account was last modified (UTC).
	UpdatedAt time.Time `json:"updated_at"`
}
