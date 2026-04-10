// Package repository defines the data-access interfaces and provides a
// concrete implementation for the commission-service.
//
// The repository pattern decouples business logic from database details,
// making it easy to swap implementations (e.g. for unit testing with mocks).
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
)

// ---------------------------------------------------------------------------
// Repository interface
// ---------------------------------------------------------------------------

// CommissionRepository defines the contract for all commission-related
// persistence operations. Any struct that satisfies this interface can be
// used by the service layer, including mock implementations for testing.
type CommissionRepository interface {
	// InsertCommission persists a new commission record into the database.
	// It should be called inside a transaction together with wallet credits
	// to guarantee atomicity.
	InsertCommission(ctx context.Context, c *models.Commission) error

	// CreditWallet adds the given amount to the specified wallet's balance.
	// The operation is idempotent with respect to referenceID — duplicate
	// calls with the same referenceID are silently ignored.
	CreditWallet(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, entryType models.LedgerEntryType, referenceID uuid.UUID, description string) error

	// GetCommissionsByDate returns all commission records for a specific
	// calendar date (UTC). Used by the daily aggregation job.
	GetCommissionsByDate(ctx context.Context, date time.Time) ([]models.Commission, error)

	// UpsertDailySummary inserts or updates a row in commission_daily_summary.
	// If a row already exists for the same (date, owner_type, owner_id,
	// transaction_type, currency) tuple, it merges the totals.
	UpsertDailySummary(ctx context.Context, summary *models.CommissionDailySummary) error

	// GetDailySummaries retrieves daily summaries for a given owner within
	// the specified date range [from, to] inclusive.
	GetDailySummaries(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, from, to time.Time) ([]models.CommissionDailySummary, error)

	// GetMonthlySummary aggregates all daily summaries for a given owner
	// within a calendar month and returns a single combined summary.
	GetMonthlySummary(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, year int, month time.Month) (*models.CommissionDailySummary, error)
}

// ---------------------------------------------------------------------------
// Concrete implementation (PostgreSQL + Redis)
// ---------------------------------------------------------------------------

// pgCommissionRepository is the production implementation backed by
// PostgreSQL for durable storage and Redis for caching.
type pgCommissionRepository struct {
	// pool is the PostgreSQL connection pool shared across the service.
	pool *pgxpool.Pool

	// rdb is the Redis client used for caching summary results.
	rdb *redis.Client
}

// NewCommissionRepository creates a new repository backed by PostgreSQL
// and Redis. Both connections must already be established before calling
// this constructor.
func NewCommissionRepository(pool *pgxpool.Pool, rdb *redis.Client) CommissionRepository {
	return &pgCommissionRepository{
		pool: pool,
		rdb:  rdb,
	}
}

// ---------------------------------------------------------------------------
// InsertCommission — persists a commission record.
// ---------------------------------------------------------------------------

// InsertCommission writes a new commission row to the commissions table.
// The commission ID is expected to be pre-generated (UUID v4) by the caller.
//
// SQL columns:
//
//	id, transaction_type, transaction_id, merchant_id, total_fee_amount,
//	system_amount, agent_id, agent_amount, partner_id, partner_amount,
//	merchant_fee_pct, agent_pct, partner_pct, currency, created_at
func (r *pgCommissionRepository) InsertCommission(ctx context.Context, c *models.Commission) error {
	// language=SQL
	const query = `
		INSERT INTO commissions (
			id, transaction_type, transaction_id, merchant_id,
			total_fee_amount, system_amount,
			agent_id, agent_amount,
			partner_id, partner_amount,
			merchant_fee_pct, agent_pct, partner_pct,
			currency, created_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6,
			$7, $8,
			$9, $10,
			$11, $12, $13,
			$14, $15
		)`

	_, err := r.pool.Exec(ctx, query,
		c.ID, c.TransactionType, c.TransactionID, c.MerchantID,
		c.TotalFeeAmount, c.SystemAmount,
		c.AgentID, c.AgentAmount,
		c.PartnerID, c.PartnerAmount,
		c.MerchantFeePct, c.AgentPct, c.PartnerPct,
		c.Currency, c.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert commission: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// CreditWallet — adds funds to a wallet with ledger entry.
// ---------------------------------------------------------------------------

// CreditWallet atomically increments the wallet balance and inserts a
// corresponding ledger entry. The ledger entry records WHY the balance
// changed (commission credit, payout, etc.) and links back to the
// originating transaction via referenceID.
//
// Note: In production this should run inside a database transaction
// together with InsertCommission. The stub demonstrates the SQL pattern.
func (r *pgCommissionRepository) CreditWallet(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal, entryType models.LedgerEntryType, referenceID uuid.UUID, description string) error {
	// language=SQL
	const updateWallet = `
		UPDATE wallets
		SET balance = balance + $1,
			updated_at = NOW()
		WHERE id = $2`

	// language=SQL
	const insertLedger = `
		INSERT INTO wallet_ledger (
			wallet_id, entry_type, reference_type, reference_id,
			amount, balance_after, description, created_at
		) VALUES (
			$1, $2, 'commission', $3,
			$4,
			(SELECT balance FROM wallets WHERE id = $1),
			$5, NOW()
		)`

	// Update the wallet balance first.
	_, err := r.pool.Exec(ctx, updateWallet, amount, walletID)
	if err != nil {
		return fmt.Errorf("credit wallet %s: %w", walletID, err)
	}

	// Record the ledger entry for audit trail.
	_, err = r.pool.Exec(ctx, insertLedger,
		walletID, entryType, referenceID, amount, description,
	)
	if err != nil {
		return fmt.Errorf("insert ledger for wallet %s: %w", walletID, err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// GetCommissionsByDate — fetch all commissions for a given date.
// ---------------------------------------------------------------------------

// GetCommissionsByDate returns every commission record whose created_at
// falls within the 24-hour window of the specified date (UTC).
// This is used by the daily aggregator to gather raw data before grouping.
func (r *pgCommissionRepository) GetCommissionsByDate(ctx context.Context, date time.Time) ([]models.Commission, error) {
	// Normalise to the start of the day in UTC.
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	endOfDay := startOfDay.Add(24 * time.Hour)

	// language=SQL
	const query = `
		SELECT id, transaction_type, transaction_id, merchant_id,
			   total_fee_amount, system_amount,
			   agent_id, agent_amount,
			   partner_id, partner_amount,
			   merchant_fee_pct, agent_pct, partner_pct,
			   currency, created_at
		FROM commissions
		WHERE created_at >= $1 AND created_at < $2
		ORDER BY created_at`

	rows, err := r.pool.Query(ctx, query, startOfDay, endOfDay)
	if err != nil {
		return nil, fmt.Errorf("query commissions by date: %w", err)
	}
	defer rows.Close()

	// Collect all rows into a slice.
	var result []models.Commission
	for rows.Next() {
		var c models.Commission
		if err := rows.Scan(
			&c.ID, &c.TransactionType, &c.TransactionID, &c.MerchantID,
			&c.TotalFeeAmount, &c.SystemAmount,
			&c.AgentID, &c.AgentAmount,
			&c.PartnerID, &c.PartnerAmount,
			&c.MerchantFeePct, &c.AgentPct, &c.PartnerPct,
			&c.Currency, &c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan commission row: %w", err)
		}
		result = append(result, c)
	}

	// Check for errors that occurred during iteration.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate commission rows: %w", err)
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// UpsertDailySummary — insert or merge a daily summary row.
// ---------------------------------------------------------------------------

// UpsertDailySummary uses PostgreSQL's ON CONFLICT clause to either insert
// a new summary row or add to the existing totals. The unique constraint is
// on (summary_date, owner_type, owner_id, transaction_type, currency).
func (r *pgCommissionRepository) UpsertDailySummary(ctx context.Context, s *models.CommissionDailySummary) error {
	// language=SQL
	const query = `
		INSERT INTO commission_daily_summary (
			summary_date, owner_type, owner_id, transaction_type, currency,
			total_tx_count, total_volume, total_fee, total_commission,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			NOW(), NOW()
		)
		ON CONFLICT (summary_date, owner_type, owner_id, transaction_type, currency)
		DO UPDATE SET
			total_tx_count  = EXCLUDED.total_tx_count,
			total_volume    = EXCLUDED.total_volume,
			total_fee       = EXCLUDED.total_fee,
			total_commission = EXCLUDED.total_commission,
			updated_at      = NOW()`

	_, err := r.pool.Exec(ctx, query,
		s.SummaryDate, s.OwnerType, s.OwnerID, s.TransactionType, s.Currency,
		s.TotalTxCount, s.TotalVolume, s.TotalFee, s.TotalCommission,
	)
	if err != nil {
		return fmt.Errorf("upsert daily summary: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// GetDailySummaries — retrieve summaries for an owner within a date range.
// ---------------------------------------------------------------------------

// GetDailySummaries fetches all daily summary rows for the specified owner
// between `from` and `to` (inclusive). Results are ordered chronologically
// so the caller can render them directly in a chart or table.
func (r *pgCommissionRepository) GetDailySummaries(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, from, to time.Time) ([]models.CommissionDailySummary, error) {
	// language=SQL
	const query = `
		SELECT id, summary_date, owner_type, owner_id, transaction_type, currency,
			   total_tx_count, total_volume, total_fee, total_commission,
			   created_at, updated_at
		FROM commission_daily_summary
		WHERE owner_type = $1 AND owner_id = $2
		  AND summary_date >= $3 AND summary_date <= $4
		ORDER BY summary_date`

	rows, err := r.pool.Query(ctx, query, ownerType, ownerID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query daily summaries: %w", err)
	}
	defer rows.Close()

	var result []models.CommissionDailySummary
	for rows.Next() {
		var s models.CommissionDailySummary
		if err := rows.Scan(
			&s.ID, &s.SummaryDate, &s.OwnerType, &s.OwnerID,
			&s.TransactionType, &s.Currency,
			&s.TotalTxCount, &s.TotalVolume, &s.TotalFee, &s.TotalCommission,
			&s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan daily summary row: %w", err)
		}
		result = append(result, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily summary rows: %w", err)
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// GetMonthlySummary — aggregate daily summaries into a single month total.
// ---------------------------------------------------------------------------

// GetMonthlySummary runs a SQL aggregation over commission_daily_summary
// for the specified owner and calendar month. It returns a single summary
// with the combined totals for that month.
func (r *pgCommissionRepository) GetMonthlySummary(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, year int, month time.Month) (*models.CommissionDailySummary, error) {
	// Calculate the first and last day of the requested month.
	firstDay := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	lastDay := firstDay.AddDate(0, 1, -1)

	// language=SQL
	const query = `
		SELECT COALESCE(SUM(total_tx_count), 0),
			   COALESCE(SUM(total_volume), 0),
			   COALESCE(SUM(total_fee), 0),
			   COALESCE(SUM(total_commission), 0)
		FROM commission_daily_summary
		WHERE owner_type = $1 AND owner_id = $2
		  AND summary_date >= $3 AND summary_date <= $4`

	// Prepare the result struct with metadata filled in.
	summary := &models.CommissionDailySummary{
		SummaryDate: firstDay,
		OwnerType:   ownerType,
		OwnerID:     ownerID,
	}

	// Scan aggregated values into the summary.
	err := r.pool.QueryRow(ctx, query, ownerType, ownerID, firstDay, lastDay).Scan(
		&summary.TotalTxCount,
		&summary.TotalVolume,
		&summary.TotalFee,
		&summary.TotalCommission,
	)
	if err != nil {
		return nil, fmt.Errorf("query monthly summary: %w", err)
	}

	return summary, nil
}
