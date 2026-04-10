// Package service (summary.go) implements the daily commission aggregation
// logic for the scheduler-service. Each day at midnight, the scheduler
// queries all commission records for the previous day, groups them by
// owner (merchant/agent/partner) and transaction type, and upserts the
// aggregated totals into the commission_daily_summary table.
//
// This pre-aggregation serves two purposes:
//  1. Performance: dashboard queries read from the summary table instead
//     of scanning millions of individual commission records.
//  2. Reporting: daily summaries provide a clean, pre-computed view of
//     commission data for exports and financial reconciliation.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farritpcz/richpayment/pkg/logger"
)

// ---------------------------------------------------------------------------
// SummaryService — aggregates commission data into daily summaries.
// ---------------------------------------------------------------------------

// SummaryService handles the daily aggregation of commission records into
// the commission_daily_summary table. It reads raw commission data from
// the commissions table, groups by owner + transaction type, and upserts
// the aggregated results.
type SummaryService struct {
	// pool is the PostgreSQL connection pool used for reading commission
	// data and upserting summary records.
	pool *pgxpool.Pool
}

// NewSummaryService constructs a new SummaryService with the given
// PostgreSQL connection pool.
//
// Parameters:
//   - pool: the pgx connection pool for database operations.
//
// Returns a ready-to-use SummaryService instance.
func NewSummaryService(pool *pgxpool.Pool) *SummaryService {
	return &SummaryService{
		pool: pool,
	}
}

// ---------------------------------------------------------------------------
// AggregateCommissions — aggregate a day's commissions into summaries.
// ---------------------------------------------------------------------------

// AggregateCommissions queries all commission records for the specified
// date, groups them by owner (system, agent, partner) and transaction type
// (deposit, withdrawal), and upserts the aggregated totals into the
// commission_daily_summary table.
//
// The aggregation uses a single SQL query with GROUP BY to compute the
// totals in the database, then performs an UPSERT (INSERT ... ON CONFLICT
// DO UPDATE) for each group to handle both initial inserts and re-runs
// (idempotent aggregation).
//
// Parameters:
//   - ctx: context for cancellation and database operations.
//   - date: the date to aggregate (truncated to midnight). For example,
//     passing 2026-04-09 00:00:00 aggregates commissions created between
//     2026-04-09 00:00:00 and 2026-04-10 00:00:00.
//
// Returns an error if the database query or upsert fails.
func (s *SummaryService) AggregateCommissions(ctx context.Context, date time.Time) error {
	// Truncate the date to midnight to ensure consistent range boundaries.
	summaryDate := date.Truncate(24 * time.Hour)

	// Calculate the date range: [summaryDate, summaryDate + 1 day).
	startTime := summaryDate
	endTime := summaryDate.AddDate(0, 0, 1)

	logger.Info("aggregating commissions",
		"date", summaryDate.Format("2006-01-02"),
		"start", startTime.Format(time.RFC3339),
		"end", endTime.Format(time.RFC3339),
	)

	// ---------------------------------------------------------------
	// Step 1: Query aggregated commission data grouped by owner and type.
	//
	// The query computes:
	//   - Total transaction count per group
	//   - Total transaction volume (sum of transaction amounts)
	//   - Total fee collected (sum of total_fee_amount)
	//   - Total commission paid out (sum of agent + partner amounts)
	//
	// The owner_type and owner_id are derived from the commission record:
	//   - If agent_id is not null, the agent is an owner (with agent_amount).
	//   - If partner_id is not null, the partner is an owner (with partner_amount).
	//   - The system is always an owner (with system_amount).
	//
	// We use UNION ALL to aggregate across all three owner types in a
	// single pass through the commissions table.
	// ---------------------------------------------------------------
	aggregationSQL := `
		WITH commission_data AS (
			SELECT
				transaction_type,
				transaction_id,
				merchant_id,
				total_fee_amount,
				system_amount,
				agent_id,
				agent_amount,
				partner_id,
				partner_amount,
				currency
			FROM commissions
			WHERE created_at >= $1 AND created_at < $2
		)
		-- System commissions (always present)
		SELECT
			'system' AS owner_type,
			'00000000-0000-0000-0000-000000000000' AS owner_id,
			transaction_type,
			currency,
			COUNT(*) AS tx_count,
			SUM(total_fee_amount) AS total_volume,
			SUM(total_fee_amount) AS total_fee,
			SUM(system_amount) AS total_commission
		FROM commission_data
		GROUP BY transaction_type, currency

		UNION ALL

		-- Agent commissions (only for records with an agent)
		SELECT
			'agent' AS owner_type,
			agent_id::text AS owner_id,
			transaction_type,
			currency,
			COUNT(*) AS tx_count,
			SUM(total_fee_amount) AS total_volume,
			SUM(total_fee_amount) AS total_fee,
			SUM(agent_amount) AS total_commission
		FROM commission_data
		WHERE agent_id IS NOT NULL
		GROUP BY agent_id, transaction_type, currency

		UNION ALL

		-- Partner commissions (only for records with a partner)
		SELECT
			'partner' AS owner_type,
			partner_id::text AS owner_id,
			transaction_type,
			currency,
			COUNT(*) AS tx_count,
			SUM(total_fee_amount) AS total_volume,
			SUM(total_fee_amount) AS total_fee,
			SUM(partner_amount) AS total_commission
		FROM commission_data
		WHERE partner_id IS NOT NULL
		GROUP BY partner_id, transaction_type, currency
	`

	// Execute the aggregation query.
	rows, err := s.pool.Query(ctx, aggregationSQL, startTime, endTime)
	if err != nil {
		return fmt.Errorf("query commission aggregation for %s: %w", summaryDate.Format("2006-01-02"), err)
	}
	defer rows.Close()

	// ---------------------------------------------------------------
	// Step 2: Upsert each aggregated group into commission_daily_summary.
	// ---------------------------------------------------------------
	upsertSQL := `
		INSERT INTO commission_daily_summary (
			summary_date, owner_type, owner_id, transaction_type,
			currency, total_tx_count, total_volume,
			total_fee, total_commission, created_at, updated_at
		) VALUES (
			$1, $2, $3::uuid, $4, $5, $6, $7, $8, $9, NOW(), NOW()
		)
		ON CONFLICT (summary_date, owner_type, owner_id, transaction_type, currency)
		DO UPDATE SET
			total_tx_count = EXCLUDED.total_tx_count,
			total_volume = EXCLUDED.total_volume,
			total_fee = EXCLUDED.total_fee,
			total_commission = EXCLUDED.total_commission,
			updated_at = NOW()
	`

	// Track the number of rows upserted for logging.
	upsertCount := 0

	// Iterate through the aggregation results.
	for rows.Next() {
		var (
			ownerType       string
			ownerID         string
			transactionType string
			currency        string
			txCount         int64
			totalVolume     float64
			totalFee        float64
			totalCommission float64
		)

		// Scan the aggregation row.
		if err := rows.Scan(
			&ownerType, &ownerID, &transactionType, &currency,
			&txCount, &totalVolume, &totalFee, &totalCommission,
		); err != nil {
			return fmt.Errorf("scan aggregation row: %w", err)
		}

		// Execute the upsert for this group.
		_, err := s.pool.Exec(ctx, upsertSQL,
			summaryDate, ownerType, ownerID, transactionType,
			currency, txCount, totalVolume, totalFee, totalCommission,
		)
		if err != nil {
			logger.Error("failed to upsert commission summary",
				"date", summaryDate.Format("2006-01-02"),
				"owner_type", ownerType,
				"owner_id", ownerID,
				"error", err,
			)
			return fmt.Errorf("upsert commission summary for %s/%s: %w", ownerType, ownerID, err)
		}

		upsertCount++
	}

	// Check for iteration errors.
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate aggregation rows: %w", err)
	}

	logger.Info("commission aggregation completed",
		"date", summaryDate.Format("2006-01-02"),
		"groups_upserted", upsertCount,
	)

	return nil
}
