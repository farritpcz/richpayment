// Package service implements the business logic for the user-service.
// This file contains the AdminService which handles CRUD operations for
// admin accounts, including password hashing and role management.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/user/internal/repository"
)

// AdminService encapsulates the business logic for managing admin accounts.
// It validates inputs, hashes passwords, and delegates persistence to the
// repository layer.
type AdminService struct {
	// repo is the data access layer for all user-domain entities.
	repo repository.UserRepository
}

// NewAdminService constructs an AdminService with the given repository.
//
// Parameters:
//   - repo: the repository implementation for persisting admin records.
//
// Returns a pointer to a fully initialised AdminService.
func NewAdminService(repo repository.UserRepository) *AdminService {
	return &AdminService{repo: repo}
}

// CreateAdmin creates a new admin account with the provided details.
// It hashes the plaintext password using bcrypt before storing. The new
// admin starts in "active" status.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - email: the admin's login email address. Must be unique.
//   - password: the plaintext password to be hashed with bcrypt.
//   - displayName: the human-readable name shown in the dashboard.
//   - roleMask: bitmask of permissions assigned to this admin.
//
// Returns the created Admin model (with PasswordHash redacted in JSON)
// and nil error on success. Returns an error if password hashing fails
// or if the repository rejects the insert (e.g. duplicate email).
func (s *AdminService) CreateAdmin(
	ctx context.Context,
	email string,
	password string,
	displayName string,
	roleMask int64,
) (*models.Admin, error) {
	// Validate that required fields are not empty.
	if email == "" {
		return nil, errors.New("VALIDATION_ERROR", "email is required", 400)
	}
	if password == "" {
		return nil, errors.New("VALIDATION_ERROR", "password is required", 400)
	}
	if displayName == "" {
		return nil, errors.New("VALIDATION_ERROR", "display_name is required", 400)
	}

	// Hash the plaintext password using bcrypt with the default cost factor.
	// bcrypt automatically generates a random salt and includes it in the
	// output hash, so no separate salt storage is needed.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash admin password: %w", err)
	}

	// Build the admin model with a new UUID and current timestamps.
	now := time.Now().UTC()
	admin := &models.Admin{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: string(hash),
		DisplayName:  displayName,
		RoleMask:     roleMask,
		Status:       models.AdminStatusActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Persist the admin in the repository.
	if err := s.repo.CreateAdmin(ctx, admin); err != nil {
		return nil, fmt.Errorf("create admin in repository: %w", err)
	}

	logger.Info("admin created",
		"admin_id", admin.ID.String(),
		"email", admin.Email,
	)

	return admin, nil
}

// GetAdmin retrieves an admin by its unique identifier.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the admin to retrieve.
//
// Returns the Admin model and nil error on success.
// Returns ErrNotFound if no admin with the given ID exists.
func (s *AdminService) GetAdmin(ctx context.Context, id uuid.UUID) (*models.Admin, error) {
	// Delegate to the repository for the database lookup.
	admin, err := s.repo.GetAdminByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get admin: %w", err)
	}
	return admin, nil
}

// ListAdmins returns a paginated list of all admins in the system.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - page: the 1-based page number. Page 1 returns the first set of results.
//   - limit: the maximum number of admins per page.
//
// Returns a slice of Admin models, the total count of all admins, and nil error.
// The page parameter is converted to a zero-based offset internally.
func (s *AdminService) ListAdmins(ctx context.Context, page, limit int) ([]models.Admin, int, error) {
	// Convert 1-based page number to zero-based offset for the repository.
	// For example, page=1 with limit=20 gives offset=0, page=2 gives offset=20.
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	// Delegate the paginated query to the repository.
	admins, total, err := s.repo.ListAdmins(ctx, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list admins: %w", err)
	}

	return admins, total, nil
}

// UpdateAdmin applies partial updates to an existing admin record.
// This method accepts a dynamic map of field names to new values,
// which allows the HTTP handler to only send changed fields.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and tracing.
//   - id: the UUID of the admin to update.
//   - fields: a map of column names to new values. Supported fields include
//     "display_name" (string), "role_mask" (int64), "status" (AdminStatus),
//     and "email" (string).
//
// Returns nil on success, or an error if the admin is not found or the
// update fails.
func (s *AdminService) UpdateAdmin(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error {
	// Verify the admin exists before attempting the update.
	_, err := s.repo.GetAdminByID(ctx, id)
	if err != nil {
		return fmt.Errorf("admin not found for update: %w", err)
	}

	// Delegate the partial update to the repository.
	if err := s.repo.UpdateAdmin(ctx, id, fields); err != nil {
		return fmt.Errorf("update admin: %w", err)
	}

	logger.Info("admin updated",
		"admin_id", id.String(),
	)

	return nil
}
