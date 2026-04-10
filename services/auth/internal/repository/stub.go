package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/farritpcz/richpayment/services/auth/internal/model"
)

// StubRepository is a no-op implementation of AuthRepository for compilation
// and early development. Replace with a PostgreSQL-backed implementation.
type StubRepository struct{}

// NewStubRepository returns a new StubRepository.
func NewStubRepository() *StubRepository {
	return &StubRepository{}
}

func (r *StubRepository) FindUserByEmail(_ context.Context, _ string, _ model.UserType) (*model.User, error) {
	return nil, nil // no users in stub
}

func (r *StubRepository) UpdateFailedAttempts(_ context.Context, _ uuid.UUID, _ model.UserType, _ int, _ *time.Time) error {
	return nil
}

func (r *StubRepository) ResetFailedAttempts(_ context.Context, _ uuid.UUID, _ model.UserType) error {
	return nil
}
