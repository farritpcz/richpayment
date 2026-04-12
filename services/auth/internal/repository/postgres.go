// Package repository (postgres.go) provides the PostgreSQL-backed
// implementation of the AuthRepository interface. It queries the appropriate
// user table (admins, merchants, or agents) based on the UserType parameter,
// mapping each table's columns into the common model.User struct so that the
// login flow can be shared across all actor types.
//
// DESIGN DECISIONS:
//
//   - Table routing: A helper function tableForUserType returns the correct
//     table name based on model.UserType. This avoids dynamic SQL string
//     concatenation in the main query methods and keeps the switch logic in
//     one place. The table name is injected via fmt.Sprintf (safe because
//     the value comes from a controlled enum, not user input).
//
//   - TOTP handling: The totp_secret_enc column is BYTEA containing
//     AES-encrypted TOTP secrets. We scan the raw bytes and encode them as
//     hex for the model.User.TOTPSecret field. If the column is NULL the
//     user has not enrolled in 2FA, so TOTPEnabled is set to false.
//
//   - RoleMask: Only the admins table has a role_mask column. For merchants
//     and agents, RoleMask is set to 0 (no special permissions).
//
//   - Error wrapping: All errors are wrapped with fmt.Errorf using the %w
//     verb so callers can use errors.Is/As for programmatic inspection while
//     still getting human-readable context in logs.
package repository

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farritpcz/richpayment/services/auth/internal/model"
)

// -------------------------------------------------------------------------
// Compile-time interface assertion
// -------------------------------------------------------------------------

// Ensure PostgresAuthRepo satisfies the AuthRepository interface at compile
// time. If any method signature drifts, the build will fail immediately
// rather than at runtime.
var _ AuthRepository = (*PostgresAuthRepo)(nil)

// -------------------------------------------------------------------------
// PostgresAuthRepo – concrete implementation
// -------------------------------------------------------------------------

// PostgresAuthRepo implements AuthRepository using a PostgreSQL connection
// pool (pgxpool.Pool). All queries use parameterised statements ($1, $2, …)
// to prevent SQL injection. The struct is safe for concurrent use because
// pgxpool.Pool handles connection multiplexing internally.
type PostgresAuthRepo struct {
	// pool is the pgx connection pool shared across all repository calls.
	// It is created once at application startup and closed on shutdown.
	pool *pgxpool.Pool
}

// NewPostgresAuthRepo constructs a new PostgresAuthRepo. The caller owns
// the pool's lifecycle and is responsible for calling pool.Close() when
// the application shuts down.
//
// Parameters:
//   - pool: an already-initialised pgxpool.Pool connected to the target database.
//
// Returns:
//   - *PostgresAuthRepo: a ready-to-use repository instance.
func NewPostgresAuthRepo(pool *pgxpool.Pool) *PostgresAuthRepo {
	return &PostgresAuthRepo{pool: pool}
}

// -------------------------------------------------------------------------
// Helper: tableForUserType
// -------------------------------------------------------------------------

// tableForUserType maps a model.UserType to the corresponding PostgreSQL
// table name. This centralises the routing logic so that individual query
// methods do not need their own switch statements.
//
// The returned value is always one of the three hard-coded table names
// ("admins", "merchants", "agents"), so it is safe to interpolate into SQL
// via fmt.Sprintf — no risk of SQL injection.
//
// Parameters:
//   - userType: the actor type (admin, merchant, agent).
//
// Returns:
//   - string: the table name.
//   - error:  a descriptive error if the userType is not recognised.
func tableForUserType(userType model.UserType) (string, error) {
	switch userType {
	case model.UserTypeAdmin:
		return "admins", nil
	case model.UserTypeMerchant:
		return "merchants", nil
	case model.UserTypeAgent:
		return "agents", nil
	default:
		return "", fmt.Errorf("unknown user type: %q", userType)
	}
}

// -------------------------------------------------------------------------
// FindUserByEmail
// -------------------------------------------------------------------------

// FindUserByEmail looks up a user by email address in the table that
// corresponds to the given userType. It maps the table-specific columns
// into the common model.User struct.
//
// Column mapping:
//   - id               → User.ID
//   - email            → User.Email
//   - password_hash    → User.PasswordHash
//   - totp_secret_enc  → User.TOTPSecret (hex-encoded; NULL → empty string)
//   - (totp_secret_enc IS NOT NULL) → User.TOTPEnabled
//   - role_mask        → User.RoleMask (admins only; 0 for merchants/agents)
//   - is_active        → User.IsActive
//   - locked_until     → User.LockedUntil (nullable)
//   - failed_attempts  → User.FailedAttempts
//   - created_at       → User.CreatedAt
//   - updated_at       → User.UpdatedAt
//
// Parameters:
//   - ctx:      request-scoped context for cancellation and deadline propagation.
//   - email:    the user's login email address (case-sensitive match).
//   - userType: determines which table to query (admin, merchant, agent).
//
// Returns:
//   - *model.User: the matched user record, or nil if not found.
//   - error:       nil when the user is not found (returns nil, nil),
//                  or a wrapped DB/mapping error on failure.
func (r *PostgresAuthRepo) FindUserByEmail(ctx context.Context, email string, userType model.UserType) (*model.User, error) {
	// Resolve the target table from the user type.
	table, err := tableForUserType(userType)
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}

	// Build the user struct that we will populate from the query result.
	var user model.User

	// totpSecretRaw holds the raw BYTEA value from the totp_secret_enc
	// column. It is nullable — a nil slice means 2FA is not enrolled.
	var totpSecretRaw []byte

	// lockedUntil is a nullable TIMESTAMPTZ that we scan into a *time.Time.
	var lockedUntil *time.Time

	// The query and scanning differ slightly between admins (which have a
	// role_mask column) and merchants/agents (which do not). We handle
	// this with a switch on the userType to keep the SQL explicit and
	// easy to audit.
	switch userType {
	case model.UserTypeAdmin:
		// ---------------------------------------------------------------
		// ADMIN QUERY: includes the role_mask (BIGINT) column which maps
		// to the RoleMask (Permission = uint64) field on model.User.
		// ---------------------------------------------------------------
		query := fmt.Sprintf(`
			SELECT id, email, password_hash, totp_secret_enc,
			       role_mask, is_active, locked_until,
			       failed_attempts, created_at, updated_at
			FROM %s
			WHERE email = $1
		`, table)

		// roleMask is scanned as int64 (BIGINT) and then cast to
		// model.Permission (uint64). This is safe because permission
		// bitmasks are always non-negative.
		var roleMask int64

		err = r.pool.QueryRow(ctx, query, email).Scan(
			&user.ID,          // id (UUID)
			&user.Email,       // email (TEXT)
			&user.PasswordHash, // password_hash (TEXT)
			&totpSecretRaw,    // totp_secret_enc (BYTEA, nullable)
			&roleMask,         // role_mask (BIGINT)
			&user.IsActive,    // is_active (BOOLEAN)
			&lockedUntil,      // locked_until (TIMESTAMPTZ, nullable)
			&user.FailedAttempts, // failed_attempts (SMALLINT)
			&user.CreatedAt,   // created_at (TIMESTAMPTZ)
			&user.UpdatedAt,   // updated_at (TIMESTAMPTZ)
		)

		// Cast the scanned BIGINT to the Permission type.
		user.RoleMask = model.Permission(roleMask)

	default:
		// ---------------------------------------------------------------
		// MERCHANT / AGENT QUERY: these tables do not have a role_mask
		// column. RoleMask is set to 0 (no special permissions).
		// ---------------------------------------------------------------
		query := fmt.Sprintf(`
			SELECT id, email, password_hash, totp_secret_enc,
			       is_active, locked_until,
			       failed_attempts, created_at, updated_at
			FROM %s
			WHERE email = $1
		`, table)

		err = r.pool.QueryRow(ctx, query, email).Scan(
			&user.ID,          // id (UUID)
			&user.Email,       // email (TEXT)
			&user.PasswordHash, // password_hash (TEXT)
			&totpSecretRaw,    // totp_secret_enc (BYTEA, nullable)
			&user.IsActive,    // is_active (BOOLEAN)
			&lockedUntil,      // locked_until (TIMESTAMPTZ, nullable)
			&user.FailedAttempts, // failed_attempts (SMALLINT)
			&user.CreatedAt,   // created_at (TIMESTAMPTZ)
			&user.UpdatedAt,   // updated_at (TIMESTAMPTZ)
		)

		// Merchants and agents have no role_mask; default to zero.
		user.RoleMask = 0
	}

	// Handle query errors.
	if err != nil {
		// pgx.ErrNoRows means no user with this email exists in the
		// target table. We return (nil, nil) following the convention
		// established by the interface: a nil user with no error signals
		// "not found" to the caller without requiring a sentinel error.
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("find user by email (table=%s): %w", table, err)
	}

	// ---------------------------------------------------------------
	// Post-scan field mapping
	// ---------------------------------------------------------------

	// Map the nullable locked_until timestamp.
	user.LockedUntil = lockedUntil

	// Map the TOTP secret. If the column is NULL (user has not enrolled
	// in 2FA), totpSecretRaw will be nil and TOTPEnabled stays false.
	// Otherwise we hex-encode the encrypted bytes so the service layer
	// can pass them to the decryption/verification logic.
	if totpSecretRaw != nil {
		user.TOTPSecret = hex.EncodeToString(totpSecretRaw)
		user.TOTPEnabled = true
	} else {
		user.TOTPSecret = ""
		user.TOTPEnabled = false
	}

	// Set the Role field to the user type string (e.g. "admin") as a
	// reasonable default. The actual named role (e.g. "super_admin",
	// "operator") would come from a roles table or a dedicated column
	// in a future iteration.
	user.Role = model.Role(userType)

	return &user, nil
}

// -------------------------------------------------------------------------
// UpdateFailedAttempts
// -------------------------------------------------------------------------

// UpdateFailedAttempts sets the failed-login counter and optional account
// lock timestamp for the specified user. This is called by the login flow
// each time a login attempt fails:
//
//   - count is incremented by the service layer.
//   - lockedUntil is set to a future timestamp when count reaches the
//     brute-force threshold (e.g. 5 failures → lock for 15 minutes).
//
// The update targets the correct table based on userType and uses the
// user's UUID as the WHERE predicate.
//
// Parameters:
//   - ctx:         request-scoped context.
//   - userID:      the UUID of the user whose counter should be updated.
//   - userType:    determines which table to update (admin, merchant, agent).
//   - count:       the new value for the failed_attempts column.
//   - lockedUntil: the timestamp until which the account is locked. Pass nil
//                  to clear the lock (e.g. when count is below the threshold).
//
// Returns:
//   - error: nil on success, or a wrapped DB error on failure.
func (r *PostgresAuthRepo) UpdateFailedAttempts(ctx context.Context, userID uuid.UUID, userType model.UserType, count int, lockedUntil *time.Time) error {
	// Resolve the target table from the user type.
	table, err := tableForUserType(userType)
	if err != nil {
		return fmt.Errorf("update failed attempts: %w", err)
	}

	// SQL: update the failed_attempts counter and locked_until timestamp.
	// The updated_at column is refreshed to now() so that audit queries
	// can detect recent account-lockout events.
	query := fmt.Sprintf(`
		UPDATE %s
		SET failed_attempts = $1,
		    locked_until    = $2,
		    updated_at      = now()
		WHERE id = $3
	`, table)

	// Execute the update. We do not check RowsAffected because the caller
	// has already verified the user exists (via FindUserByEmail). If the
	// user was deleted between the lookup and this call, silently updating
	// zero rows is acceptable — the user no longer exists.
	_, err = r.pool.Exec(ctx, query, count, lockedUntil, userID)
	if err != nil {
		return fmt.Errorf("update failed attempts (table=%s, user=%s): %w", table, userID, err)
	}

	return nil
}

// -------------------------------------------------------------------------
// ResetFailedAttempts
// -------------------------------------------------------------------------

// ResetFailedAttempts zeroes out the failed-login counter and clears any
// account lock after a successful login. This ensures that a legitimate
// user is not penalised for past failed attempts once they prove ownership
// of the account by providing correct credentials.
//
// Parameters:
//   - ctx:      request-scoped context.
//   - userID:   the UUID of the user whose counter should be reset.
//   - userType: determines which table to update (admin, merchant, agent).
//
// Returns:
//   - error: nil on success, or a wrapped DB error on failure.
func (r *PostgresAuthRepo) ResetFailedAttempts(ctx context.Context, userID uuid.UUID, userType model.UserType) error {
	// Resolve the target table from the user type.
	table, err := tableForUserType(userType)
	if err != nil {
		return fmt.Errorf("reset failed attempts: %w", err)
	}

	// SQL: reset the counter to zero and clear the lock timestamp.
	// Setting locked_until to NULL ensures that the user is no longer
	// locked out, even if the previous lock had not yet expired.
	// The updated_at column is refreshed to now() for auditability.
	query := fmt.Sprintf(`
		UPDATE %s
		SET failed_attempts = 0,
		    locked_until    = NULL,
		    updated_at      = now()
		WHERE id = $1
	`, table)

	// Execute the reset. As with UpdateFailedAttempts, we do not check
	// RowsAffected — if the user was concurrently deleted, a zero-row
	// update is harmless.
	_, err = r.pool.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("reset failed attempts (table=%s, user=%s): %w", table, userID, err)
	}

	return nil
}
