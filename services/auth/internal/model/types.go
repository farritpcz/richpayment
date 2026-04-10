package model

import (
	"time"

	"github.com/google/uuid"
)

// Permission represents a single permission as a bitmask flag.
type Permission uint64

// Role is a human-readable role name.
type Role string

// UserType distinguishes between admin-panel users and other actor types.
type UserType string

const (
	UserTypeAdmin    UserType = "admin"
	UserTypeMerchant UserType = "merchant"
	UserTypeAgent    UserType = "agent"
)

// User represents a row from the appropriate user table.
type User struct {
	ID             uuid.UUID
	Email          string
	PasswordHash   string
	TOTPSecret     string
	TOTPEnabled    bool
	Role           Role
	RoleMask       Permission
	FailedAttempts int
	LockedUntil    *time.Time
	IsActive       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Session represents an authenticated user session stored in Redis.
type Session struct {
	ID        string     `json:"id"`
	UserID    uuid.UUID  `json:"user_id"`
	Email     string     `json:"email"`
	UserType  UserType   `json:"user_type"`
	Role      Role       `json:"role"`
	RoleMask  Permission `json:"role_mask"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
}
