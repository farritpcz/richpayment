// Package service (partition.go) implements PostgreSQL range partition
// management for the scheduler-service. Time-based partitioning is used
// across the RichPayment platform to keep table sizes manageable, improve
// query performance via partition pruning, and enable efficient data archival.
//
// Partitions are created on a monthly basis. The naming convention is:
//   {table_name}_{YYYY}_{MM}
//
// For example: deposit_orders_2026_04 covers April 2026.
//
// The partitioned tables in the RichPayment platform are:
//   - deposit_orders
//   - wallet_ledger
//   - withdrawals
//   - commissions
//   - sms_messages
//   - slip_verifications
//   - audit_logs
//   - webhook_deliveries
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farritpcz/richpayment/pkg/logger"
)

// ---------------------------------------------------------------------------
// partitionedTables — the list of tables that require monthly partitions.
// ---------------------------------------------------------------------------

// partitionedTables is the authoritative list of all PostgreSQL tables that
// use range partitioning by created_at timestamp. When adding a new
// partitioned table to the schema, add its name to this slice so the
// scheduler automatically manages its partitions.
var partitionedTables = []string{
	"deposit_orders",
	"wallet_ledger",
	"withdrawals",
	"commissions",
	"sms_messages",
	"slip_verifications",
	"audit_logs",
	"webhook_deliveries",
}

// ---------------------------------------------------------------------------
// PartitionService — manages PostgreSQL table partitions.
// ---------------------------------------------------------------------------

// PartitionService handles the creation and management of PostgreSQL range
// partitions. It connects to the database via a pgx connection pool and
// executes DDL statements to create partition tables as needed.
type PartitionService struct {
	// pool is the PostgreSQL connection pool used for executing DDL
	// statements (CREATE TABLE, partition management queries).
	pool *pgxpool.Pool
}

// NewPartitionService constructs a new PartitionService with the given
// PostgreSQL connection pool.
//
// Parameters:
//   - pool: the pgx connection pool for database operations.
//
// Returns a ready-to-use PartitionService instance.
func NewPartitionService(pool *pgxpool.Pool) *PartitionService {
	return &PartitionService{
		pool: pool,
	}
}

// ---------------------------------------------------------------------------
// CheckPartitions — verify and create missing partitions for next month.
// ---------------------------------------------------------------------------

// CheckPartitions verifies that partitions exist for the next month across
// all partitioned tables. If any partitions are missing, they are created
// automatically. This method is designed to be called hourly by the cron
// scheduler to ensure partitions are always available before they are needed.
//
// The method iterates over all tables in the partitionedTables list and
// checks whether next month's partition exists. Missing partitions are
// created via CreatePartition.
//
// Parameters:
//   - ctx: context for cancellation and database operations.
//
// Returns an error if any partition creation fails. Partial failures are
// logged but do not prevent other partitions from being created.
func (s *PartitionService) CheckPartitions(ctx context.Context) error {
	// Calculate next month's year and month.
	now := time.Now()
	nextMonth := now.AddDate(0, 1, 0)
	year := nextMonth.Year()
	month := int(nextMonth.Month())

	logger.Info("checking partitions for next month",
		"year", year,
		"month", month,
		"table_count", len(partitionedTables),
	)

	// Track whether any partition creation failed.
	var lastErr error

	// Iterate over all partitioned tables and ensure next month's
	// partition exists.
	for _, tableName := range partitionedTables {
		// Check if the partition already exists by querying the
		// information_schema for a table with the expected partition name.
		partitionName := fmt.Sprintf("%s_%d_%02d", tableName, year, month)
		exists, err := s.partitionExists(ctx, partitionName)
		if err != nil {
			logger.Error("failed to check partition existence",
				"partition", partitionName,
				"error", err,
			)
			lastErr = err
			continue
		}

		if exists {
			logger.Debug("partition already exists", "partition", partitionName)
			continue
		}

		// Partition does not exist — create it.
		logger.Info("creating missing partition", "partition", partitionName)
		if err := s.CreatePartition(ctx, tableName, year, month); err != nil {
			logger.Error("failed to create partition",
				"partition", partitionName,
				"error", err,
			)
			lastErr = err
			continue
		}

		logger.Info("partition created successfully", "partition", partitionName)
	}

	return lastErr
}

// ---------------------------------------------------------------------------
// CreatePartition — create a single monthly partition for a table.
// ---------------------------------------------------------------------------

// CreatePartition creates a new monthly range partition for the specified
// table. The partition covers the date range [start_of_month, start_of_next_month)
// using PostgreSQL's PARTITION OF ... FOR VALUES FROM ... TO syntax.
//
// The partition naming convention is: {tableName}_{year}_{month:02d}
// For example: deposit_orders_2026_04
//
// Parameters:
//   - ctx: context for the database operation.
//   - tableName: the parent table name (e.g. "deposit_orders").
//   - year: the partition year (e.g. 2026).
//   - month: the partition month (1-12).
//
// Returns an error if the DDL statement fails (e.g. partition already exists,
// parent table does not exist, or permission denied).
func (s *PartitionService) CreatePartition(ctx context.Context, tableName string, year, month int) error {
	// Calculate the start and end dates for the partition range.
	// Start: first day of the target month at 00:00:00 UTC.
	// End: first day of the following month at 00:00:00 UTC.
	startDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	// Build the partition name following the naming convention.
	partitionName := fmt.Sprintf("%s_%d_%02d", tableName, year, month)

	// Build the CREATE TABLE ... PARTITION OF DDL statement.
	// The FOR VALUES FROM ... TO clause defines the date range using
	// half-open intervals: [startDate, endDate).
	sql := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
		partitionName,
		tableName,
		startDate.Format("2006-01-02"),
		endDate.Format("2006-01-02"),
	)

	logger.Info("executing partition DDL",
		"sql", sql,
		"partition", partitionName,
	)

	// Execute the DDL statement.
	_, err := s.pool.Exec(ctx, sql)
	if err != nil {
		return fmt.Errorf("create partition %s: %w", partitionName, err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// CreateFuturePartitions — proactively create partitions for months ahead.
// ---------------------------------------------------------------------------

// CreateFuturePartitions proactively creates partitions for the next 3 months
// across all partitioned tables. This ensures partitions are available well
// in advance, even if the hourly CheckPartitions job experiences temporary
// failures.
//
// Parameters:
//   - ctx: context for cancellation and database operations.
//
// Returns an error if any partition creation fails.
func (s *PartitionService) CreateFuturePartitions(ctx context.Context) error {
	now := time.Now()

	logger.Info("creating future partitions for next 3 months")

	// Track the last error encountered during partition creation.
	var lastErr error

	// Create partitions for each of the next 3 months.
	for monthOffset := 1; monthOffset <= 3; monthOffset++ {
		// Calculate the target month.
		targetDate := now.AddDate(0, monthOffset, 0)
		year := targetDate.Year()
		month := int(targetDate.Month())

		// Create partitions for all tables for this target month.
		for _, tableName := range partitionedTables {
			partitionName := fmt.Sprintf("%s_%d_%02d", tableName, year, month)

			// Check if partition already exists to avoid unnecessary DDL.
			exists, err := s.partitionExists(ctx, partitionName)
			if err != nil {
				logger.Error("failed to check partition existence",
					"partition", partitionName,
					"error", err,
				)
				lastErr = err
				continue
			}

			if exists {
				continue
			}

			// Create the missing partition.
			if err := s.CreatePartition(ctx, tableName, year, month); err != nil {
				logger.Error("failed to create future partition",
					"partition", partitionName,
					"error", err,
				)
				lastErr = err
				continue
			}

			logger.Info("future partition created", "partition", partitionName)
		}
	}

	return lastErr
}

// ---------------------------------------------------------------------------
// partitionExists — check if a partition table exists in the database.
// ---------------------------------------------------------------------------

// partitionExists checks whether a table (partition) with the given name
// exists in the database by querying the information_schema.tables view.
//
// Parameters:
//   - ctx: context for the database query.
//   - partitionName: the full partition table name (e.g. "deposit_orders_2026_04").
//
// Returns true if the table exists, false otherwise.
func (s *PartitionService) partitionExists(ctx context.Context, partitionName string) (bool, error) {
	// Query the information_schema to check for table existence.
	// This is database-portable and does not require superuser privileges.
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = $1 AND table_schema = 'public'
		)`,
		partitionName,
	).Scan(&exists)

	if err != nil {
		return false, fmt.Errorf("check partition exists %s: %w", partitionName, err)
	}

	return exists, nil
}
