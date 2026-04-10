// Package repository defines the data-access interface for the auth service
// and provides implementations (currently a stub; a PostgreSQL-backed
// implementation will replace it in production).
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/farritpcz/richpayment/services/auth/internal/model"
)

// AuthRepository defines the data-access contract for the auth service.
// Implementations may back this with PostgreSQL, an in-memory store, etc.
type AuthRepository interface {
	// FindUserByEmail looks up a user by email and user type.
	FindUserByEmail(ctx context.Context, email string, userType model.UserType) (*model.User, error)

	// UpdateFailedAttempts sets the failed-login counter (and optional lock time) for a user.
	UpdateFailedAttempts(ctx context.Context, userID uuid.UUID, userType model.UserType, count int, lockedUntil *time.Time) error

	// ResetFailedAttempts zeroes out the counter after a successful login.
	ResetFailedAttempts(ctx context.Context, userID uuid.UUID, userType model.UserType) error
}
