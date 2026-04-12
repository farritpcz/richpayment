// Package repository defines the data access interfaces and implementations
// for the withdrawal-service. This file provides the PostgreSQL-backed
// implementation of the WithdrawalRepository interface using the pgx/v5
// driver and connection pool.
//
// The withdrawals table is range-partitioned by created_at for efficient
// time-based queries and automated data lifecycle management. Most queries
// include created_at in their WHERE clause to enable partition pruning,
// except GetByID which relies on PostgreSQL scanning all partitions since
// the caller may not know the creation date.
//
// IMPORTANT: The domain model (models.Withdrawal) uses a single DestDetails
// JSON string to represent destination information, while the database stores
// destination fields in separate encrypted columns (bank_code,
// account_number_enc, account_name, usdt_address_enc, usdt_network). This
// implementation currently skips those encrypted columns during Create and
// GetByID. A future migration will reconcile this mapping when the encryption
// layer is wired in.
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/models"
)

// ---------------------------------------------------------------------------
// Compile-time interface assertion
// ---------------------------------------------------------------------------

// Compile-time assertion: PostgresWithdrawalRepo must fully implement the
// WithdrawalRepository interface. If any method signature changes or is
// missing, the build will fail here with a clear message.
var _ WithdrawalRepository = (*PostgresWithdrawalRepo)(nil)

// ---------------------------------------------------------------------------
// PostgresWithdrawalRepo — production PostgreSQL implementation
// ---------------------------------------------------------------------------

// PostgresWithdrawalRepo is the PostgreSQL-backed implementation of the
// WithdrawalRepository interface. It uses the pgxpool connection pool from
// the jackc/pgx driver for high-performance, production-grade database
// access. All monetary values are stored as NUMERIC via the shopspring/decimal
// library to avoid floating-point precision issues.
type PostgresWithdrawalRepo struct {
	// pool is the pgx connection pool shared across all repository calls.
	// The pool manages connection lifecycle, health checks, idle timeouts,
	// and concurrency limits. The caller (main or DI container) is
	// responsible for opening and closing the pool.
	pool *pgxpool.Pool
}

// NewPostgresWithdrawalRepo constructs a new PostgresWithdrawalRepo with the
// given connection pool. The caller is responsible for opening the pool before
// passing it here and closing it on application shutdown.
//
// Usage:
//
//	pool, _ := pgxpool.New(ctx, databaseURL)
//	repo := repository.NewPostgresWithdrawalRepo(pool)
func NewPostgresWithdrawalRepo(pool *pgxpool.Pool) *PostgresWithdrawalRepo {
	return &PostgresWithdrawalRepo{pool: pool}
}

// ---------------------------------------------------------------------------
// Create — insert a new withdrawal record
// ---------------------------------------------------------------------------

// Create inserts a new withdrawal row into the withdrawals table. The
// withdrawal.ID must be pre-generated (UUID v4) by the caller before
// calling this method. The created_at and updated_at timestamps should
// also be set by the caller (typically to time.Now().UTC()).
//
// NOTE: The encrypted destination columns (bank_code, account_number_enc,
// account_name, usdt_address_enc, usdt_network) are NOT populated in this
// implementation. The model's DestType is mapped to the destination_type
// column. A future update will handle DestDetails -> encrypted columns
// mapping once the encryption module is integrated.
//
// Returns an error if the INSERT fails (e.g. duplicate primary key,
// constraint violation, connection loss).
func (r *PostgresWithdrawalRepo) Create(ctx context.Context, w *models.Withdrawal) error {
	// SQL query to insert core withdrawal fields into the partitioned table.
	// The primary key is (id, created_at) due to range partitioning, so both
	// columns must be present in the INSERT. We use numbered placeholders
	// ($1..$N) as required by the pgx driver.
	const query = `
		INSERT INTO withdrawals (
			id, merchant_id, amount, fee_amount, net_amount, currency,
			destination_type, status,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8,
			$9, $10
		)
	`

	// Execute the insert with all withdrawal fields mapped positionally.
	// Each placeholder corresponds to a struct field in order.
	_, err := r.pool.Exec(ctx, query,
		w.ID,           // $1  — unique withdrawal identifier (UUID v4)
		w.MerchantID,   // $2  — merchant requesting the withdrawal
		w.Amount,       // $3  — gross withdrawal amount (NUMERIC)
		w.FeeAmount,    // $4  — calculated fee amount (NUMERIC)
		w.NetAmount,    // $5  — net amount after fee deduction (NUMERIC)
		w.Currency,     // $6  — ISO 4217 currency code (e.g. "THB")
		w.DestType,     // $7  — destination type ("bank", "promptpay")
		w.Status,       // $8  — initial status (typically "pending")
		w.CreatedAt,    // $9  — creation timestamp (UTC)
		w.UpdatedAt,    // $10 — last modification timestamp (UTC)
	)
	if err != nil {
		// Wrap the pgx error with context for easier debugging in logs.
		return fmt.Errorf("insert withdrawal: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// GetByID — fetch a single withdrawal by UUID
// ---------------------------------------------------------------------------

// GetByID retrieves a single withdrawal record by its unique identifier.
// Since the withdrawals table is partitioned by created_at and the caller
// may not know the creation date, the query uses only WHERE id = $1 which
// causes PostgreSQL to scan across all partitions. This is acceptable for
// single-row lookups by primary key but would not scale for bulk queries.
//
// Returns (nil, ErrNotFound) when no row matches the given id.
// Returns (nil, error) on any other database failure.
func (r *PostgresWithdrawalRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Withdrawal, error) {
	// SQL query that selects all available columns for a single withdrawal.
	// We map DB column names to the corresponding model fields:
	//   - destination_type      -> DestType
	//   - approved_by_admin_id  -> ApprovedBy
	//   - transfer_reference    -> TransferRef
	//   - transfer_proof_url    -> ProofURL
	//   - rejection_reason      -> RejectionReason
	//
	// NOTE: Encrypted columns (bank_code, account_number_enc, account_name,
	// usdt_address_enc, usdt_network) are intentionally excluded until the
	// encryption layer is integrated.
	const query = `
		SELECT
			id, merchant_id, amount, fee_amount, net_amount, currency,
			destination_type, status,
			approved_by_admin_id, approved_at, completed_at, rejection_reason,
			transfer_proof_url, transfer_reference,
			created_at, updated_at
		FROM withdrawals
		WHERE id = $1
	`

	// Prepare the target struct to scan column values into.
	var w models.Withdrawal

	// QueryRow returns at most one row; Scan maps columns to struct fields
	// in the exact order they appear in the SELECT clause.
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&w.ID,              // id                  -> ID
		&w.MerchantID,      // merchant_id         -> MerchantID
		&w.Amount,          // amount              -> Amount
		&w.FeeAmount,       // fee_amount          -> FeeAmount
		&w.NetAmount,       // net_amount          -> NetAmount
		&w.Currency,        // currency            -> Currency
		&w.DestType,        // destination_type    -> DestType
		&w.Status,          // status              -> Status
		&w.ApprovedBy,      // approved_by_admin_id -> ApprovedBy (nullable)
		&w.ApprovedAt,      // approved_at         -> ApprovedAt (nullable)
		&w.CompletedAt,     // completed_at        -> CompletedAt (nullable)
		&w.RejectionReason, // rejection_reason    -> RejectionReason
		&w.ProofURL,        // transfer_proof_url  -> ProofURL
		&w.TransferRef,     // transfer_reference  -> TransferRef
		&w.CreatedAt,       // created_at          -> CreatedAt
		&w.UpdatedAt,       // updated_at          -> UpdatedAt
	)
	if err != nil {
		// pgx.ErrNoRows is returned when the UUID does not match any row
		// across all partitions. Map it to our domain sentinel error.
		if err == pgx.ErrNoRows {
			return nil, errors.ErrNotFound
		}
		// Wrap unexpected errors with context for log analysis.
		return nil, fmt.Errorf("get withdrawal by id: %w", err)
	}

	return &w, nil
}

// ---------------------------------------------------------------------------
// UpdateStatus — transition withdrawal status with dynamic field updates
// ---------------------------------------------------------------------------

// UpdateStatus transitions a withdrawal to a new status and applies any
// additional column updates described in the fields map. The method builds
// a dynamic UPDATE SET clause to avoid separate methods for each status
// transition (approve, reject, complete, fail).
//
// The fields map uses logical key names (not raw DB column names) which are
// translated internally to the actual column names. Supported keys:
//
//   - "approved_by"      -> approved_by_admin_id (UUID pointer)
//   - "approved_at"      -> approved_at          (time pointer)
//   - "rejected_at"      -> rejected_at          — NOTE: not in DB schema,
//     but included for future compatibility
//   - "rejection_reason" -> rejection_reason      (string)
//   - "transfer_ref"     -> transfer_reference    (string)
//   - "proof_url"        -> transfer_proof_url    (string)
//   - "completed_at"     -> completed_at          (time pointer)
//   - "fee_amount"       -> fee_amount            (decimal)
//   - "net_amount"       -> net_amount            (decimal)
//
// The method always sets status and updated_at regardless of what is in
// the fields map. Returns ErrNotFound if no row matches the given id.
func (r *PostgresWithdrawalRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status models.WithdrawalStatus, fields map[string]interface{}) error {
	// Start building the SET clause. Status and updated_at are always
	// included in every status transition to maintain audit consistency.
	setClauses := []string{"status = $1", "updated_at = $2"}

	// args holds positional parameter values. The first two slots are
	// always the new status and the current UTC timestamp.
	args := []interface{}{status, time.Now().UTC()}

	// argIndex tracks the next available placeholder number ($3, $4, ...).
	// We start at 3 because $1 and $2 are already taken.
	argIndex := 3

	// columnMap translates logical field names (used by the service layer)
	// to actual database column names. This decouples the service from the
	// physical schema and allows column renames without changing callers.
	columnMap := map[string]string{
		"approved_by":      "approved_by_admin_id", // UUID of approving admin
		"approved_at":      "approved_at",          // approval timestamp
		"rejected_at":      "rejected_at",          // rejection timestamp (future)
		"rejection_reason": "rejection_reason",     // human-readable rejection text
		"transfer_ref":     "transfer_reference",   // bank transfer reference number
		"proof_url":        "transfer_proof_url",   // URL to transfer proof document
		"completed_at":     "completed_at",         // completion timestamp
		"fee_amount":       "fee_amount",           // withdrawal fee (decimal)
		"net_amount":       "net_amount",           // net amount after fees (decimal)
	}

	// Iterate over the additional fields map and append each as a SET
	// clause with a numbered placeholder. This builds the dynamic portion
	// of the UPDATE statement in a single pass.
	for key, val := range fields {
		// Look up the actual DB column name from our mapping table.
		// If the key is not in the map, use it as-is (allows direct
		// column names as a fallback for flexibility).
		colName, ok := columnMap[key]
		if !ok {
			colName = key
		}

		// Append "column = $N" to the SET clause list.
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", colName, argIndex))
		// Append the corresponding value to the args slice.
		args = append(args, val)
		// Advance to the next placeholder number.
		argIndex++
	}

	// The WHERE clause uses the final placeholder for the withdrawal ID.
	// This ensures the ID placeholder number is always correct regardless
	// of how many dynamic fields were added above.
	args = append(args, id)

	// Assemble the full UPDATE statement by joining all SET clauses with
	// commas. The WHERE clause references the last placeholder ($argIndex).
	query := fmt.Sprintf(
		"UPDATE withdrawals SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "),
		argIndex,
	)

	// Execute the update. The CommandTag tells us how many rows were affected.
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		// Wrap the error with context for debugging and log correlation.
		return fmt.Errorf("update withdrawal status: %w", err)
	}

	// If no rows were affected, the withdrawal ID does not exist in any
	// partition. Return our domain sentinel error so the service layer
	// can respond with a proper 404.
	if tag.RowsAffected() == 0 {
		return errors.ErrNotFound
	}

	return nil
}

// ---------------------------------------------------------------------------
// ListPending — paginated list of pending withdrawals
// ---------------------------------------------------------------------------

// ListPending returns all withdrawals with status "pending", ordered by
// created_at ascending (oldest first, FIFO fairness). Supports pagination
// via offset and limit parameters. Also returns the total count of pending
// withdrawals for building pagination metadata in API responses.
//
// The method executes two queries:
//  1. COUNT(*) to get the total number of pending withdrawals.
//  2. SELECT with LIMIT/OFFSET to fetch the requested page.
//
// This two-query approach is simpler than window functions and avoids
// scanning unnecessary columns in the count query.
//
// Returns (nil, 0, nil) when there are no pending withdrawals.
func (r *PostgresWithdrawalRepo) ListPending(ctx context.Context, offset, limit int) ([]models.Withdrawal, int, error) {
	// -----------------------------------------------------------------------
	// Step 1: Count total pending withdrawals for pagination metadata.
	// This query benefits from an index on (status) or (status, created_at).
	// -----------------------------------------------------------------------
	const countQuery = `
		SELECT COUNT(*)
		FROM withdrawals
		WHERE status = 'pending'
	`

	var total int

	// Execute the count query and scan the single integer result.
	err := r.pool.QueryRow(ctx, countQuery).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count pending withdrawals: %w", err)
	}

	// If there are no pending withdrawals at all, short-circuit and avoid
	// the second query entirely for efficiency.
	if total == 0 {
		return nil, 0, nil
	}

	// -----------------------------------------------------------------------
	// Step 2: Fetch the requested page of pending withdrawals.
	// ORDER BY created_at ASC ensures oldest requests are processed first.
	// -----------------------------------------------------------------------
	const listQuery = `
		SELECT
			id, merchant_id, amount, fee_amount, net_amount, currency,
			destination_type, status,
			approved_by_admin_id, approved_at, completed_at, rejection_reason,
			transfer_proof_url, transfer_reference,
			created_at, updated_at
		FROM withdrawals
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1 OFFSET $2
	`

	// Execute the paginated query with limit and offset as parameters.
	rows, err := r.pool.Query(ctx, listQuery, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list pending withdrawals: %w", err)
	}
	// Always close the rows cursor when done to release the connection
	// back to the pool, even if we encounter an error during iteration.
	defer rows.Close()

	// Pre-allocate the slice with the expected capacity to minimise
	// memory allocations during the scan loop.
	withdrawals := make([]models.Withdrawal, 0, limit)

	// Iterate over each row returned by the query and scan columns
	// into a Withdrawal struct. The column order must exactly match
	// the SELECT clause above.
	for rows.Next() {
		var w models.Withdrawal
		if err := rows.Scan(
			&w.ID,              // id
			&w.MerchantID,      // merchant_id
			&w.Amount,          // amount
			&w.FeeAmount,       // fee_amount
			&w.NetAmount,       // net_amount
			&w.Currency,        // currency
			&w.DestType,        // destination_type
			&w.Status,          // status
			&w.ApprovedBy,      // approved_by_admin_id (nullable)
			&w.ApprovedAt,      // approved_at (nullable)
			&w.CompletedAt,     // completed_at (nullable)
			&w.RejectionReason, // rejection_reason
			&w.ProofURL,        // transfer_proof_url
			&w.TransferRef,     // transfer_reference
			&w.CreatedAt,       // created_at
			&w.UpdatedAt,       // updated_at
		); err != nil {
			// Return the error immediately; any partially collected
			// withdrawals are discarded to avoid returning incomplete data.
			return nil, 0, fmt.Errorf("scan pending withdrawal row: %w", err)
		}

		// Append the successfully scanned withdrawal to our result slice.
		withdrawals = append(withdrawals, w)
	}

	// Check for any error that occurred during row iteration (e.g.
	// network interruption, context cancellation mid-stream).
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate pending withdrawal rows: %w", err)
	}

	return withdrawals, total, nil
}

// ---------------------------------------------------------------------------
// SumDailyWithdrawals — daily withdrawal total for limit enforcement
// ---------------------------------------------------------------------------

// SumDailyWithdrawals calculates the total gross withdrawal amount for a
// specific merchant on the given calendar date (UTC). This sum is used by
// the service layer to enforce per-merchant daily withdrawal limits before
// accepting new withdrawal requests.
//
// Only withdrawals with status "pending", "approved", or "completed" are
// included in the sum. Rejected and failed withdrawals are excluded because
// their held funds have been (or will be) released back to the merchant's
// wallet and should not count against the daily limit.
//
// The date range is calculated as [dayStart, dayEnd) where:
//   - dayStart = date truncated to midnight UTC
//   - dayEnd   = dayStart + 24 hours
//
// COALESCE ensures we return 0 (not NULL) when there are no matching rows.
//
// This query benefits from a composite index on
// (merchant_id, created_at, status) for efficient partition pruning and
// filtering.
func (r *PostgresWithdrawalRepo) SumDailyWithdrawals(ctx context.Context, merchantID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	// SQL aggregation query that sums the amount column for all qualifying
	// withdrawals within the target day. COALESCE converts NULL (no rows)
	// to 0 so we always get a valid decimal result.
	const query = `
		SELECT COALESCE(SUM(amount), 0)
		FROM withdrawals
		WHERE merchant_id = $1
		  AND created_at >= $2
		  AND created_at < $3
		  AND status IN ('pending', 'approved', 'completed')
	`

	// Calculate the start and end boundaries of the target day in UTC.
	// dayStart is midnight at the beginning of the date.
	// dayEnd is midnight at the beginning of the next date.
	// The range [dayStart, dayEnd) captures exactly one calendar day.
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)

	// Execute the aggregation query and scan the COALESCE'd result.
	var sum decimal.Decimal
	err := r.pool.QueryRow(ctx, query, merchantID, dayStart, dayEnd).Scan(&sum)
	if err != nil {
		// Wrap the error with context for log analysis and debugging.
		return decimal.Zero, fmt.Errorf("sum daily withdrawals: %w", err)
	}

	return sum, nil
}
