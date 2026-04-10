// Package model defines the domain types used by the auth service, including
// the unified User struct (populated from admin/merchant/agent tables), the
// Session struct stored in Redis, and supporting type aliases for permissions,
// roles, and user types.
package model

import (
	"time"

	"github.com/google/uuid"
)

// Permission represents a single permission as a bitmask flag. Multiple
// permissions are combined using bitwise OR to form a role mask.
type Permission uint64

// Role is a human-readable role name (e.g. "super_admin", "operator").
type Role string

// UserType distinguishes between admin-panel users and other actor types so
// the auth service knows which database table to query.
type UserType string

const (
	// UserTypeAdmin represents a back-office administrator.
	UserTypeAdmin UserType = "admin"

	// UserTypeMerchant represents a merchant user.
	UserTypeMerchant UserType = "merchant"

	// UserTypeAgent represents an agent user.
	UserTypeAgent UserType = "agent"
)

// User represents a row from the appropriate user table (admins, merchants, or
// agents). The auth service maps each table's columns into this common struct
// so that the login flow can be shared across actor types.
type User struct {
	// ID is the unique identifier of the user (UUID v4).
	ID uuid.UUID

	// Email is the user's login email address.
	Email string

	// PasswordHash is the bcrypt hash of the user's password.
	PasswordHash string

	// TOTPSecret is the base32-encoded TOTP shared secret. Empty if the user
	// has not enrolled in two-factor authentication.
	TOTPSecret string

	// TOTPEnabled indicates whether the user has activated TOTP-based 2FA.
	TOTPEnabled bool

	// Role is the named role assigned to the user (e.g. "admin", "operator").
	Role Role

	// RoleMask is the effective permission bitmask. When non-zero it overrides
	// the default permissions for the user's Role.
	RoleMask Permission

	// FailedAttempts tracks consecutive failed login attempts. When this
	// reaches the threshold the account is temporarily locked.
	FailedAttempts int

	// LockedUntil is the timestamp until which the account is locked due to
	// excessive failed login attempts. Nil when the account is not locked.
	LockedUntil *time.Time

	// IsActive indicates whether the account is enabled. Disabled accounts
	// cannot log in regardless of credentials.
	IsActive bool

	// CreatedAt records when the user record was created (UTC).
	CreatedAt time.Time

	// UpdatedAt records when the user record was last modified (UTC).
	UpdatedAt time.Time
}

// Session represents an authenticated user session stored in Redis. It
// contains the minimum set of claims needed for downstream services to
// authorise requests without hitting the database.
type Session struct {
	// ID is the unique session identifier (UUID string), used as the Redis key suffix.
	ID string `json:"id"`

	// UserID is the UUID of the authenticated user.
	UserID uuid.UUID `json:"user_id"`

	// Email is the user's email, carried in the session for convenience.
	Email string `json:"email"`

	// UserType indicates the actor type (admin, merchant, agent).
	UserType UserType `json:"user_type"`

	// Role is the human-readable role name.
	Role Role `json:"role"`

	// RoleMask is the effective permission bitmask for authorisation checks.
	RoleMask Permission `json:"role_mask"`

	// CreatedAt is the timestamp when the session was created.
	CreatedAt time.Time `json:"created_at"`

	// ExpiresAt is the timestamp when the session expires and should be
	// considered invalid.
	ExpiresAt time.Time `json:"expires_at"`
}
