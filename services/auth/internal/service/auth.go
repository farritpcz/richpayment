package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"github.com/farritpcz/richpayment/services/auth/internal/model"
	"github.com/farritpcz/richpayment/services/auth/internal/repository"
)

const (
	maxFailedAttempts = 5
	lockDuration      = 15 * time.Minute
	sessionTTL        = 24 * time.Hour
	sessionPrefix     = "session:"
)

// AuthService contains the core authentication business logic.
type AuthService struct {
	repo  repository.AuthRepository
	redis *redis.Client
	totp  *TOTPService
}

// NewAuthService creates a new AuthService.
func NewAuthService(repo repository.AuthRepository, redisClient *redis.Client, totpSvc *TOTPService) *AuthService {
	return &AuthService{
		repo:  repo,
		redis: redisClient,
		totp:  totpSvc,
	}
}

// Login authenticates a user by email/password, verifies the TOTP code if enabled,
// and creates a new session in Redis.
func (s *AuthService) Login(ctx context.Context, email, password, totpCode string, userType model.UserType) (*model.Session, error) {
	user, err := s.repo.FindUserByEmail(ctx, email, userType)
	if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	if !user.IsActive {
		return nil, fmt.Errorf("account is disabled")
	}

	// Check account lock.
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		return nil, fmt.Errorf("account locked until %s", user.LockedUntil.Format(time.RFC3339))
	}

	// Verify password.
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		if err := s.recordFailedAttempt(ctx, user, userType); err != nil {
			return nil, fmt.Errorf("record failed attempt: %w", err)
		}
		return nil, fmt.Errorf("invalid credentials")
	}

	// Verify TOTP if enabled.
	if user.TOTPEnabled {
		if totpCode == "" {
			return nil, fmt.Errorf("2FA code required")
		}
		if !s.totp.ValidateCode(user.TOTPSecret, totpCode) {
			if err := s.recordFailedAttempt(ctx, user, userType); err != nil {
				return nil, fmt.Errorf("record failed attempt: %w", err)
			}
			return nil, fmt.Errorf("invalid 2FA code")
		}
	}

	// Successful login: reset failed attempts.
	if err := s.repo.ResetFailedAttempts(ctx, user.ID, userType); err != nil {
		return nil, fmt.Errorf("reset failed attempts: %w", err)
	}

	// Resolve effective permission mask: prefer the user-level override,
	// fall back to the default for the user's role.
	roleMask := user.RoleMask
	if roleMask == 0 {
		roleMask = RolePermissions(user.Role)
	}

	// Create session.
	now := time.Now()
	sess := &model.Session{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Email:     user.Email,
		UserType:  userType,
		Role:      user.Role,
		RoleMask:  roleMask,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}

	if err := s.storeSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("store session: %w", err)
	}

	return sess, nil
}

// ValidateSession retrieves and validates a session by its ID.
func (s *AuthService) ValidateSession(ctx context.Context, sessionID string) (*model.Session, error) {
	key := sessionPrefix + sessionID
	data, err := s.redis.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("session not found or expired")
		}
		return nil, fmt.Errorf("get session: %w", err)
	}

	var sess model.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}

	if time.Now().After(sess.ExpiresAt) {
		_ = s.redis.Del(ctx, key).Err()
		return nil, fmt.Errorf("session expired")
	}

	return &sess, nil
}

// Logout destroys a session.
func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	key := sessionPrefix + sessionID
	deleted, err := s.redis.Del(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if deleted == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

// recordFailedAttempt increments the counter and locks the account if the
// threshold is reached.
func (s *AuthService) recordFailedAttempt(ctx context.Context, user *model.User, userType model.UserType) error {
	attempts := user.FailedAttempts + 1

	var lockedUntil *time.Time
	if attempts >= maxFailedAttempts {
		t := time.Now().Add(lockDuration)
		lockedUntil = &t
	}

	return s.repo.UpdateFailedAttempts(ctx, user.ID, userType, attempts, lockedUntil)
}

// storeSession serialises the session to JSON and writes it to Redis with a TTL.
func (s *AuthService) storeSession(ctx context.Context, sess *model.Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	key := sessionPrefix + sess.ID
	return s.redis.Set(ctx, key, data, sessionTTL).Err()
}
