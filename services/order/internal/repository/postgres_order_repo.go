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

// PostgresOrderRepo is the PostgreSQL-backed implementation of the
// OrderRepository interface. It uses the pgxpool connection pool from the
// jackc/pgx driver for high-performance, production-grade database access.
type PostgresOrderRepo struct {
	// pool is the pgx connection pool shared across all repository calls.
	// The pool manages connection lifecycle, health checks, and concurrency.
	pool *pgxpool.Pool
}

// NewPostgresOrderRepo constructs a new PostgresOrderRepo with the given
// connection pool. The caller is responsible for opening and closing the pool.
func NewPostgresOrderRepo(pool *pgxpool.Pool) *PostgresOrderRepo {
	return &PostgresOrderRepo{pool: pool}
}

// Create inserts a new deposit order row into the deposit_orders table.
// All fields of the DepositOrder struct are persisted, including the
// pre-generated UUID, timestamps, and decimal monetary values.
// Returns an error if the INSERT fails (e.g. duplicate key, connection loss).
func (r *PostgresOrderRepo) Create(ctx context.Context, order *models.DepositOrder) error {
	// SQL query to insert a complete deposit order record.
	// We use numbered placeholders ($1..$N) as required by pgx.
	const query = `
		INSERT INTO deposit_orders (
			id, merchant_id, merchant_order_id, customer_name, customer_bank_code,
			requested_amount, adjusted_amount, actual_amount, fee_amount, net_amount,
			currency, bank_account_id, matched_by, matched_at, status,
			qr_payload, webhook_sent, webhook_attempts, expires_at,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15,
			$16, $17, $18, $19,
			$20, $21
		)
	`

	// Execute the insert with all order fields mapped positionally.
	_, err := r.pool.Exec(ctx, query,
		order.ID, order.MerchantID, order.MerchantOrderID, order.CustomerName, order.CustomerBankCode,
		order.RequestedAmount, order.AdjustedAmount, order.ActualAmount, order.FeeAmount, order.NetAmount,
		order.Currency, order.BankAccountID, order.MatchedBy, order.MatchedAt, order.Status,
		order.QRPayload, order.WebhookSent, order.WebhookAttempts, order.ExpiresAt,
		order.CreatedAt, order.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert deposit order: %w", err)
	}

	return nil
}

// GetByID fetches a single deposit order by its primary key (UUID).
// Scans every column from the deposit_orders table into the DepositOrder struct.
// Returns the shared ErrNotFound sentinel if no row matches.
func (r *PostgresOrderRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.DepositOrder, error) {
	// SQL query that selects all columns for a single order by ID.
	const query = `
		SELECT
			id, merchant_id, merchant_order_id, customer_name, customer_bank_code,
			requested_amount, adjusted_amount, actual_amount, fee_amount, net_amount,
			currency, bank_account_id, matched_by, matched_at, status,
			qr_payload, webhook_sent, webhook_attempts, expires_at,
			created_at, updated_at
		FROM deposit_orders
		WHERE id = $1
	`

	// Prepare the target struct to scan into.
	var order models.DepositOrder

	// QueryRow returns at most one row; Scan maps columns to struct fields.
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&order.ID, &order.MerchantID, &order.MerchantOrderID, &order.CustomerName, &order.CustomerBankCode,
		&order.RequestedAmount, &order.AdjustedAmount, &order.ActualAmount, &order.FeeAmount, &order.NetAmount,
		&order.Currency, &order.BankAccountID, &order.MatchedBy, &order.MatchedAt, &order.Status,
		&order.QRPayload, &order.WebhookSent, &order.WebhookAttempts, &order.ExpiresAt,
		&order.CreatedAt, &order.UpdatedAt,
	)
	if err != nil {
		// pgx.ErrNoRows means the UUID did not match any row.
		if err == pgx.ErrNoRows {
			return nil, errors.ErrNotFound
		}
		return nil, fmt.Errorf("get deposit order by id: %w", err)
	}

	return &order, nil
}

// UpdateStatus transitions an order to a new status and applies additional
// column updates provided in the fields map. The map keys are column names
// (snake_case) and the values are the new column values. The method builds
// a dynamic UPDATE SET clause to avoid separate methods per status transition.
//
// Example usage:
//
//	repo.UpdateStatus(ctx, orderID, models.OrderStatusCompleted, map[string]interface{}{
//	    "matched_by":    "sms",
//	    "actual_amount": actualAmt,
//	    "fee_amount":    feeAmt,
//	    "net_amount":    netAmt,
//	    "matched_at":    time.Now(),
//	})
func (r *PostgresOrderRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status models.OrderStatus, fields map[string]interface{}) error {
	// Start building the SET clause. Status and updated_at are always set.
	setClauses := []string{"status = $1", "updated_at = $2"}
	// args holds positional parameter values; first two are always status and now().
	args := []interface{}{status, time.Now().UTC()}

	// argIndex tracks the next placeholder number ($3, $4, ...).
	argIndex := 3

	// Iterate over the additional fields map and append each as a SET clause.
	// This allows callers to update arbitrary columns in a single round-trip.
	for col, val := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", col, argIndex))
		args = append(args, val)
		argIndex++
	}

	// The WHERE clause uses the final placeholder for the order ID.
	args = append(args, id)

	// Assemble the full UPDATE statement with all SET clauses joined by commas.
	query := fmt.Sprintf(
		"UPDATE deposit_orders SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "),
		argIndex,
	)

	// Execute the update. CommandTag tells us how many rows were affected.
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update deposit order status: %w", err)
	}

	// If no rows were affected, the order ID does not exist.
	if tag.RowsAffected() == 0 {
		return errors.ErrNotFound
	}

	return nil
}

// FindPendingByAmount searches for a single pending deposit order that belongs
// to a specific bank account and has the given adjusted amount. This is the
// primary lookup used by the amount-matching engine when an SMS notification
// arrives with a transfer amount. Only orders with status "pending" are
// considered. The query orders by created_at ASC so the oldest matching
// order is returned first (FIFO fairness).
func (r *PostgresOrderRepo) FindPendingByAmount(ctx context.Context, bankAccountID uuid.UUID, amount decimal.Decimal) (*models.DepositOrder, error) {
	// SQL selects the oldest pending order for the given bank account + amount.
	// LIMIT 1 ensures we return exactly one match (the oldest).
	const query = `
		SELECT
			id, merchant_id, merchant_order_id, customer_name, customer_bank_code,
			requested_amount, adjusted_amount, actual_amount, fee_amount, net_amount,
			currency, bank_account_id, matched_by, matched_at, status,
			qr_payload, webhook_sent, webhook_attempts, expires_at,
			created_at, updated_at
		FROM deposit_orders
		WHERE bank_account_id = $1
		  AND adjusted_amount = $2
		  AND status = 'pending'
		ORDER BY created_at ASC
		LIMIT 1
	`

	var order models.DepositOrder

	err := r.pool.QueryRow(ctx, query, bankAccountID, amount).Scan(
		&order.ID, &order.MerchantID, &order.MerchantOrderID, &order.CustomerName, &order.CustomerBankCode,
		&order.RequestedAmount, &order.AdjustedAmount, &order.ActualAmount, &order.FeeAmount, &order.NetAmount,
		&order.Currency, &order.BankAccountID, &order.MatchedBy, &order.MatchedAt, &order.Status,
		&order.QRPayload, &order.WebhookSent, &order.WebhookAttempts, &order.ExpiresAt,
		&order.CreatedAt, &order.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errors.ErrNotFound
		}
		return nil, fmt.Errorf("find pending order by amount: %w", err)
	}

	return &order, nil
}

// FindExpired returns all deposit orders that are still in "pending" status
// but whose expires_at timestamp is earlier than the supplied cutoff time.
// The expiry background worker calls this method every polling cycle to
// discover orders that should be transitioned to "expired" status.
// Returns an empty slice (not nil) when there are no expired orders.
func (r *PostgresOrderRepo) FindExpired(ctx context.Context, before time.Time) ([]models.DepositOrder, error) {
	// SQL selects all pending orders whose expiry has passed.
	// We order by expires_at ASC to process the oldest expirations first.
	const query = `
		SELECT
			id, merchant_id, merchant_order_id, customer_name, customer_bank_code,
			requested_amount, adjusted_amount, actual_amount, fee_amount, net_amount,
			currency, bank_account_id, matched_by, matched_at, status,
			qr_payload, webhook_sent, webhook_attempts, expires_at,
			created_at, updated_at
		FROM deposit_orders
		WHERE status = 'pending'
		  AND expires_at < $1
		ORDER BY expires_at ASC
	`

	rows, err := r.pool.Query(ctx, query, before)
	if err != nil {
		return nil, fmt.Errorf("find expired orders: %w", err)
	}
	defer rows.Close()

	// Collect all matching rows into a slice.
	var orders []models.DepositOrder
	for rows.Next() {
		var order models.DepositOrder
		if err := rows.Scan(
			&order.ID, &order.MerchantID, &order.MerchantOrderID, &order.CustomerName, &order.CustomerBankCode,
			&order.RequestedAmount, &order.AdjustedAmount, &order.ActualAmount, &order.FeeAmount, &order.NetAmount,
			&order.Currency, &order.BankAccountID, &order.MatchedBy, &order.MatchedAt, &order.Status,
			&order.QRPayload, &order.WebhookSent, &order.WebhookAttempts, &order.ExpiresAt,
			&order.CreatedAt, &order.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan expired order row: %w", err)
		}
		orders = append(orders, order)
	}

	// Check for any error that occurred during row iteration.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired order rows: %w", err)
	}

	return orders, nil
}
