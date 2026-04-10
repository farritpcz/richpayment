// Package service contains the core business-logic layer for the auth service.
// This file implements the AuthService which handles login (password + TOTP),
// session creation/validation/destruction, account-locking on repeated failed
// login attempts, IP-based login rate limiting, session IP binding, session
// fingerprinting via User-Agent hash, and TOTP brute-force rate limiting.
//
// Security features overview:
//   - IP rate limiting: prevents credential stuffing by limiting login attempts
//     per source IP address (max 10/hour, tracked in Redis).
//   - Session IP binding: the client IP is stored in the session at creation
//     time and verified on every subsequent validation; mismatches trigger
//     session invalidation and an admin alert.
//   - Session fingerprinting: a SHA-256 hash of the User-Agent header is
//     stored at login; if it changes during the session, the session is
//     invalidated to detect cookie theft across browsers/devices.
//   - TOTP rate limiting: max 3 failed TOTP attempts per session; after 3
//     failures the session is locked for 5 minutes to mitigate brute-force
//     attacks against the 6-digit TOTP code space.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"github.com/farritpcz/richpayment/services/auth/internal/model"
	"github.com/farritpcz/richpayment/services/auth/internal/repository"
)

const (
	// maxFailedAttempts is the number of consecutive incorrect login attempts
	// allowed before the account is temporarily locked.
	maxFailedAttempts = 5

	// lockDuration is how long an account stays locked after exceeding the
	// maximum failed login attempts.
	lockDuration = 15 * time.Minute

	// sessionTTL is the Redis TTL for session keys, controlling how long a
	// session remains valid without renewal.
	sessionTTL = 24 * time.Hour

	// sessionPrefix is the Redis key namespace for session data.
	sessionPrefix = "session:"

	// loginRateLimitPrefix is the Redis key namespace for IP-based login
	// rate limiting. Keys follow the pattern "login_ratelimit:{ip}".
	loginRateLimitPrefix = "login_ratelimit:"

	// maxLoginAttemptsPerIP is the maximum number of login attempts allowed
	// from a single IP address within the loginRateLimitWindow. Once
	// exceeded, further login attempts from that IP are rejected with
	// HTTP 429 Too Many Requests.
	maxLoginAttemptsPerIP = 10

	// loginRateLimitWindow is the sliding window during which login
	// attempts are counted per IP address. The counter resets after this
	// duration elapses from the first attempt.
	loginRateLimitWindow = 1 * time.Hour

	// maxTOTPFailures is the maximum number of TOTP verification failures
	// allowed per session before the session is temporarily locked.
	// This prevents brute-force attacks against the 6-digit code space
	// (1,000,000 possibilities).
	maxTOTPFailures = 3

	// totpLockDuration is how long a session stays locked after exceeding
	// the maximum TOTP failure count. During this period, any TOTP
	// verification request is immediately rejected.
	totpLockDuration = 5 * time.Minute
)

// AuthService contains the core authentication business logic including
// login with password + optional TOTP verification, session management in
// Redis, account-locking after excessive failed attempts, IP-based rate
// limiting, session IP binding, User-Agent fingerprinting, and TOTP
// brute-force protection.
type AuthService struct {
	// repo provides data access for user lookup and failed-attempt tracking.
	repo repository.AuthRepository
	// redis is the Redis client used for session storage, rate limiting
	// counters, and retrieval.
	redis *redis.Client
	// totp handles TOTP code generation and validation for 2FA.
	totp *TOTPService
	// logger is used for security-relevant event logging (session
	// invalidation, rate limit hits, IP mismatches, etc.).
	logger *slog.Logger
}

// NewAuthService creates a new AuthService with all required dependencies.
// The logger parameter is used for security audit logging (IP mismatches,
// rate limit events, session invalidation alerts).
func NewAuthService(repo repository.AuthRepository, redisClient *redis.Client, totpSvc *TOTPService, logger *slog.Logger) *AuthService {
	return &AuthService{
		repo:   repo,
		redis:  redisClient,
		totp:   totpSvc,
		logger: logger,
	}
}

// Login authenticates a user by email/password, verifies the TOTP code if
// enabled, enforces IP-based rate limiting, and creates a new session in
// Redis with the client's IP and User-Agent fingerprint stored for later
// verification.
//
// Parameters:
//   - ctx: request context for cancellation and deadlines.
//   - email: the user's login email address.
//   - password: the plaintext password to verify against the stored hash.
//   - totpCode: the 6-digit TOTP code (empty string if 2FA not enabled).
//   - userType: distinguishes admin/merchant/agent for table routing.
//   - clientIP: the IP address of the client making the login request,
//     used for rate limiting and session binding.
//   - userAgent: the User-Agent header from the client's request, hashed
//     and stored for session fingerprinting.
//
// Returns:
//   - *model.Session on success (contains the session ID and metadata).
//   - An error with a descriptive message on failure (invalid credentials,
//     rate limited, account locked, etc.).
//   - retryAfter > 0 indicates the caller should return HTTP 429 with a
//     Retry-After header set to this many seconds.
func (s *AuthService) Login(ctx context.Context, email, password, totpCode string, userType model.UserType, clientIP, userAgent string) (*model.Session, int, error) {
	// -------------------------------------------------------------------
	// Step 1: IP-based login rate limiting.
	// Track failed login attempts per IP in Redis. Key pattern:
	// "login_ratelimit:{ip}". Max 10 attempts per IP per hour.
	// After the limit is reached, return an error with a retry-after hint.
	// -------------------------------------------------------------------
	rateLimitKey := loginRateLimitPrefix + clientIP
	attemptCount, err := s.redis.Incr(ctx, rateLimitKey).Result()
	if err != nil {
		// On Redis failure, log the error but allow the request through
		// to avoid a complete login outage when Redis is temporarily
		// unavailable. This is a deliberate availability-over-security
		// trade-off for the rate limiter only.
		s.logger.Error("ip rate limit redis error, allowing request",
			"error", err,
			"client_ip", clientIP,
		)
	} else {
		// Set the TTL on the first increment so the counter auto-expires
		// after the rate limit window elapses. Subsequent increments
		// within the window do not reset the TTL.
		if attemptCount == 1 {
			s.redis.Expire(ctx, rateLimitKey, loginRateLimitWindow)
		}
		// If the IP has exceeded the allowed number of attempts, reject
		// the login immediately without checking credentials. This
		// prevents credential stuffing and password spraying attacks.
		if attemptCount > int64(maxLoginAttemptsPerIP) {
			// Calculate the remaining TTL so the caller can set an
			// accurate Retry-After header for the client.
			ttl, _ := s.redis.TTL(ctx, rateLimitKey).Result()
			retryAfter := int(ttl.Seconds())
			if retryAfter <= 0 {
				retryAfter = int(loginRateLimitWindow.Seconds())
			}
			s.logger.Warn("login rate limit exceeded",
				"client_ip", clientIP,
				"attempts", attemptCount,
				"retry_after_seconds", retryAfter,
			)
			return nil, retryAfter, fmt.Errorf("too many login attempts, please try again later")
		}
	}

	// -------------------------------------------------------------------
	// Step 2: User lookup and basic validation.
	// -------------------------------------------------------------------
	user, err := s.repo.FindUserByEmail(ctx, email, userType)
	if err != nil {
		return nil, 0, fmt.Errorf("find user: %w", err)
	}
	if user == nil {
		return nil, 0, fmt.Errorf("invalid credentials")
	}

	if !user.IsActive {
		return nil, 0, fmt.Errorf("account is disabled")
	}

	// Check account lock (from too many failed password attempts).
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		return nil, 0, fmt.Errorf("account locked until %s", user.LockedUntil.Format(time.RFC3339))
	}

	// -------------------------------------------------------------------
	// Step 3: Password verification.
	// -------------------------------------------------------------------
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		if err := s.recordFailedAttempt(ctx, user, userType); err != nil {
			return nil, 0, fmt.Errorf("record failed attempt: %w", err)
		}
		return nil, 0, fmt.Errorf("invalid credentials")
	}

	// -------------------------------------------------------------------
	// Step 4: TOTP verification (if enabled).
	// -------------------------------------------------------------------
	if user.TOTPEnabled {
		if totpCode == "" {
			return nil, 0, fmt.Errorf("2FA code required")
		}
		if !s.totp.ValidateCode(user.TOTPSecret, totpCode) {
			if err := s.recordFailedAttempt(ctx, user, userType); err != nil {
				return nil, 0, fmt.Errorf("record failed attempt: %w", err)
			}
			return nil, 0, fmt.Errorf("invalid 2FA code")
		}
	}

	// -------------------------------------------------------------------
	// Step 5: Successful login — reset failed attempts and create session.
	// -------------------------------------------------------------------
	if err := s.repo.ResetFailedAttempts(ctx, user.ID, userType); err != nil {
		return nil, 0, fmt.Errorf("reset failed attempts: %w", err)
	}

	// On successful login, decrement the IP rate limit counter by 1 so
	// that legitimate users who occasionally mistype are not penalised
	// as heavily. We only decrement (never below 0).
	s.redis.Decr(ctx, rateLimitKey)

	// Resolve effective permission mask: prefer the user-level override,
	// fall back to the default for the user's role.
	roleMask := user.RoleMask
	if roleMask == 0 {
		roleMask = RolePermissions(user.Role)
	}

	// -------------------------------------------------------------------
	// Step 6: Create session with IP binding and User-Agent fingerprint.
	// The client IP and a SHA-256 hash of the User-Agent are stored in
	// the session so that every subsequent validation can verify that the
	// session is being used from the same network/device.
	// -------------------------------------------------------------------
	now := time.Now()
	sess := &model.Session{
		ID:            uuid.New().String(),
		UserID:        user.ID,
		Email:         user.Email,
		UserType:      userType,
		Role:          user.Role,
		RoleMask:      roleMask,
		CreatedAt:     now,
		ExpiresAt:     now.Add(sessionTTL),
		ClientIP:      clientIP,
		UserAgentHash: hashUserAgent(userAgent),
		TOTPFailures:  0,
	}

	if err := s.storeSession(ctx, sess); err != nil {
		return nil, 0, fmt.Errorf("store session: %w", err)
	}

	s.logger.Info("login successful",
		"user_id", user.ID.String(),
		"email", user.Email,
		"user_type", string(userType),
		"client_ip", clientIP,
		"session_id", sess.ID,
	)

	return sess, 0, nil
}

// ValidateSession retrieves and validates a session by its ID. In addition
// to checking expiration, it verifies the client's IP address and User-Agent
// fingerprint against the values stored at login time. A mismatch on either
// triggers immediate session invalidation and an admin security alert.
//
// Parameters:
//   - ctx: request context.
//   - sessionID: the session ID to validate.
//   - clientIP: the current request's IP address for IP binding check.
//   - userAgent: the current request's User-Agent header for fingerprint
//     comparison.
//
// Returns the session on success, or an error if the session is invalid,
// expired, or fails the security checks.
func (s *AuthService) ValidateSession(ctx context.Context, sessionID, clientIP, userAgent string) (*model.Session, error) {
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

	// Check session expiration.
	if time.Now().After(sess.ExpiresAt) {
		_ = s.redis.Del(ctx, key).Err()
		return nil, fmt.Errorf("session expired")
	}

	// -------------------------------------------------------------------
	// IP binding check: compare the request IP with the IP stored at
	// session creation. If they differ, this may indicate session
	// hijacking from a different network. Invalidate the session and
	// log a security alert for the admin.
	// -------------------------------------------------------------------
	if sess.ClientIP != "" && clientIP != "" && sess.ClientIP != clientIP {
		// Invalidate the compromised session immediately.
		_ = s.redis.Del(ctx, key).Err()
		// Log a security alert so administrators can investigate.
		s.logger.Error("SESSION_SECURITY_ALERT: IP mismatch detected, session invalidated",
			"session_id", sessionID,
			"user_id", sess.UserID.String(),
			"email", sess.Email,
			"expected_ip", sess.ClientIP,
			"actual_ip", clientIP,
			"action", "session_invalidated",
		)
		return nil, fmt.Errorf("session invalidated: IP address mismatch")
	}

	// -------------------------------------------------------------------
	// User-Agent fingerprint check: compare the SHA-256 hash of the
	// current User-Agent with the hash stored at login. A change
	// suggests the session cookie was stolen and used from a different
	// browser or device.
	// -------------------------------------------------------------------
	if sess.UserAgentHash != "" && userAgent != "" {
		currentHash := hashUserAgent(userAgent)
		if sess.UserAgentHash != currentHash {
			// Invalidate the compromised session immediately.
			_ = s.redis.Del(ctx, key).Err()
			// Log a security alert for admin investigation.
			s.logger.Error("SESSION_SECURITY_ALERT: User-Agent mismatch detected, session invalidated",
				"session_id", sessionID,
				"user_id", sess.UserID.String(),
				"email", sess.Email,
				"expected_ua_hash", sess.UserAgentHash,
				"actual_ua_hash", currentHash,
				"action", "session_invalidated",
			)
			return nil, fmt.Errorf("session invalidated: User-Agent mismatch")
		}
	}

	return &sess, nil
}

// ValidateTOTPForSession verifies a TOTP code within an existing session,
// enforcing per-session TOTP rate limiting. After maxTOTPFailures (3)
// consecutive failures, the session is locked for totpLockDuration (5
// minutes) to prevent brute-force enumeration of the 6-digit code space.
//
// Parameters:
//   - ctx: request context.
//   - sessionID: the session in which the TOTP code is being verified.
//   - totpSecret: the user's base32-encoded TOTP shared secret.
//   - totpCode: the 6-digit code provided by the user.
//
// Returns nil on success, or an error describing the failure (locked,
// invalid code, etc.).
func (s *AuthService) ValidateTOTPForSession(ctx context.Context, sessionID, totpSecret, totpCode string) error {
	key := sessionPrefix + sessionID

	// -------------------------------------------------------------------
	// Retrieve the current session from Redis.
	// -------------------------------------------------------------------
	data, err := s.redis.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("session not found or expired")
		}
		return fmt.Errorf("get session: %w", err)
	}

	var sess model.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return fmt.Errorf("unmarshal session: %w", err)
	}

	// -------------------------------------------------------------------
	// Check whether the session is currently locked due to excessive
	// TOTP failures. If locked and the lock has not yet expired, reject
	// the attempt immediately.
	// -------------------------------------------------------------------
	if sess.TOTPLockedUntil != nil && time.Now().Before(*sess.TOTPLockedUntil) {
		remaining := time.Until(*sess.TOTPLockedUntil).Seconds()
		s.logger.Warn("TOTP attempt rejected: session locked",
			"session_id", sessionID,
			"user_id", sess.UserID.String(),
			"locked_until", sess.TOTPLockedUntil.Format(time.RFC3339),
			"remaining_seconds", int(remaining),
		)
		return fmt.Errorf("session locked due to too many TOTP failures, try again in %d seconds", int(remaining))
	}

	// -------------------------------------------------------------------
	// Validate the TOTP code.
	// -------------------------------------------------------------------
	if s.totp.ValidateCode(totpSecret, totpCode) {
		// Success: reset the TOTP failure counter for this session.
		sess.TOTPFailures = 0
		sess.TOTPLockedUntil = nil
		if err := s.storeSession(ctx, &sess); err != nil {
			return fmt.Errorf("update session after TOTP success: %w", err)
		}
		return nil
	}

	// -------------------------------------------------------------------
	// TOTP verification failed — increment the failure counter.
	// -------------------------------------------------------------------
	sess.TOTPFailures++
	s.logger.Warn("TOTP verification failed",
		"session_id", sessionID,
		"user_id", sess.UserID.String(),
		"failure_count", sess.TOTPFailures,
		"max_failures", maxTOTPFailures,
	)

	// If the failure count reaches the threshold, lock the session.
	if sess.TOTPFailures >= maxTOTPFailures {
		lockUntil := time.Now().Add(totpLockDuration)
		sess.TOTPLockedUntil = &lockUntil
		s.logger.Error("TOTP rate limit exceeded: session locked",
			"session_id", sessionID,
			"user_id", sess.UserID.String(),
			"locked_until", lockUntil.Format(time.RFC3339),
			"lock_duration", totpLockDuration.String(),
		)
	}

	// Persist the updated failure count (and optional lock timestamp).
	if err := s.storeSession(ctx, &sess); err != nil {
		return fmt.Errorf("update session after TOTP failure: %w", err)
	}

	return fmt.Errorf("invalid 2FA code")
}

// Logout destroys a session by deleting its Redis key. This is called
// when the user explicitly logs out or when a session needs to be
// forcefully terminated (e.g. after a security event).
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

// SessionTTL returns the configured session time-to-live duration. This is
// exposed so that callers (e.g. the gateway middleware) can set cookie
// Max-Age attributes to match the session expiration.
func (s *AuthService) SessionTTL() time.Duration {
	return sessionTTL
}

// recordFailedAttempt increments the per-account failed login counter and
// locks the account if the threshold is reached. This is separate from the
// IP-based rate limiter — the account lock protects individual accounts,
// while the IP rate limiter protects against distributed attacks.
func (s *AuthService) recordFailedAttempt(ctx context.Context, user *model.User, userType model.UserType) error {
	attempts := user.FailedAttempts + 1

	var lockedUntil *time.Time
	if attempts >= maxFailedAttempts {
		t := time.Now().Add(lockDuration)
		lockedUntil = &t
	}

	return s.repo.UpdateFailedAttempts(ctx, user.ID, userType, attempts, lockedUntil)
}

// storeSession serialises the session to JSON and writes it to Redis with
// a TTL. This is used both for initial session creation and for updating
// session fields (e.g. TOTP failure counters).
func (s *AuthService) storeSession(ctx context.Context, sess *model.Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	key := sessionPrefix + sess.ID
	return s.redis.Set(ctx, key, data, sessionTTL).Err()
}

// hashUserAgent computes a SHA-256 hex digest of the given User-Agent
// string. This is stored in the session at login time and compared on
// every subsequent validation to detect session cookie theft across
// different browsers or devices. Using a hash avoids storing the
// (potentially long) raw User-Agent string in Redis while still
// enabling reliable comparison.
func hashUserAgent(ua string) string {
	h := sha256.Sum256([]byte(ua))
	return hex.EncodeToString(h[:])
}
