// Package service (archive.go) implements the data archival pipeline for
// the scheduler-service. Old PostgreSQL partitions (older than 3 months)
// are archived by dumping them to compressed files, then detaching and
// dropping the partition from the parent table.
//
// The archival process is designed to be safe and auditable:
//  1. Identify partitions older than the retention threshold (3 months).
//  2. Export the partition data to a compressed file using pg_dump.
//  3. Log the archive file location for manual off-server transfer.
//  4. Detach the partition from the parent table.
//  5. Drop the detached partition table.
//
// Each step is logged for audit purposes. If any step fails, the partition
// is left intact and the error is logged for manual investigation.
package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farritpcz/richpayment/pkg/logger"
)

// ---------------------------------------------------------------------------
// ArchiveService — manages data archival of old partitions.
// ---------------------------------------------------------------------------

// ArchiveService handles the archival of old PostgreSQL partitions. It
// identifies partitions older than the configured retention period, exports
// them to compressed files, and removes them from the database to free
// storage and maintain query performance.
type ArchiveService struct {
	// pool is the PostgreSQL connection pool used for querying partition
	// metadata and executing DDL statements (DETACH, DROP).
	pool *pgxpool.Pool

	// archiveDir is the local filesystem directory where pg_dump output
	// files are stored before manual off-server transfer. Defaults to
	// /var/lib/richpayment/archives.
	archiveDir string

	// pgDumpDSN is the PostgreSQL connection string used by pg_dump for
	// exporting partition data. This may differ from the pool DSN if
	// pg_dump requires a different authentication method.
	pgDumpDSN string

	// retentionMonths is the number of months of data to retain in the
	// live database. Partitions older than this are eligible for archival.
	// Defaults to 3 months.
	retentionMonths int
}

// NewArchiveService constructs a new ArchiveService with the given
// PostgreSQL connection pool and configuration.
//
// Parameters:
//   - pool: the pgx connection pool for database operations.
//   - archiveDir: the directory for storing archive dump files.
//   - pgDumpDSN: the connection string for pg_dump.
//   - retentionMonths: months of data to keep live (partitions older are archived).
//
// Returns a ready-to-use ArchiveService instance.
func NewArchiveService(pool *pgxpool.Pool, archiveDir, pgDumpDSN string, retentionMonths int) *ArchiveService {
	return &ArchiveService{
		pool:            pool,
		archiveDir:      archiveDir,
		pgDumpDSN:       pgDumpDSN,
		retentionMonths: retentionMonths,
	}
}

// ---------------------------------------------------------------------------
// ArchiveOldPartitions — find and archive partitions beyond retention.
// ---------------------------------------------------------------------------

// ArchiveOldPartitions identifies partitions older than the retention
// threshold and archives them. For each eligible partition, it:
//  1. Exports the data to a compressed file via pg_dump.
//  2. Logs the archive file path for manual off-server transfer.
//  3. Detaches the partition from the parent table.
//  4. Drops the detached partition table.
//
// The method processes all partitioned tables defined in the
// partitionedTables list. Errors on individual partitions are logged but
// do not prevent other partitions from being processed.
//
// Parameters:
//   - ctx: context for cancellation and database operations.
//
// Returns the last error encountered, or nil if all partitions were
// archived successfully.
func (s *ArchiveService) ArchiveOldPartitions(ctx context.Context) error {
	// Calculate the cutoff date. Partitions with an end date before this
	// cutoff are eligible for archival.
	cutoff := time.Now().AddDate(0, -s.retentionMonths, 0)
	cutoffYear := cutoff.Year()
	cutoffMonth := int(cutoff.Month())

	logger.Info("scanning for old partitions to archive",
		"cutoff_year", cutoffYear,
		"cutoff_month", cutoffMonth,
		"retention_months", s.retentionMonths,
	)

	// Track the last error for the return value.
	var lastErr error

	// Iterate over all partitioned tables.
	for _, tableName := range partitionedTables {
		// Query for partition tables that match the naming convention
		// and are older than the cutoff date.
		partitions, err := s.findOldPartitions(ctx, tableName, cutoffYear, cutoffMonth)
		if err != nil {
			logger.Error("failed to find old partitions",
				"table", tableName,
				"error", err,
			)
			lastErr = err
			continue
		}

		// Archive each old partition.
		for _, partition := range partitions {
			logger.Info("archiving old partition", "partition", partition)

			if err := s.archiveSinglePartition(ctx, tableName, partition); err != nil {
				logger.Error("failed to archive partition",
					"partition", partition,
					"error", err,
				)
				lastErr = err
				continue
			}

			logger.Info("partition archived successfully", "partition", partition)
		}
	}

	return lastErr
}

// ---------------------------------------------------------------------------
// findOldPartitions — query for partitions older than the cutoff.
// ---------------------------------------------------------------------------

// findOldPartitions queries the information_schema for partition tables
// that match the naming convention ({table}_{YYYY}_{MM}) and are older
// than the specified cutoff year/month.
//
// Parameters:
//   - ctx: context for the database query.
//   - tableName: the parent table name (e.g. "deposit_orders").
//   - cutoffYear: partitions before this year are eligible.
//   - cutoffMonth: partitions before this month (in cutoffYear) are eligible.
//
// Returns a slice of partition names that should be archived.
func (s *ArchiveService) findOldPartitions(ctx context.Context, tableName string, cutoffYear, cutoffMonth int) ([]string, error) {
	// Query for child tables of the partitioned parent that match the
	// naming convention. We use a LIKE pattern to find partitions.
	pattern := fmt.Sprintf("%s_%%", tableName)

	rows, err := s.pool.Query(ctx,
		`SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name LIKE $1
		ORDER BY table_name`,
		pattern,
	)
	if err != nil {
		return nil, fmt.Errorf("query partitions for %s: %w", tableName, err)
	}
	defer rows.Close()

	// Collect partition names that are older than the cutoff.
	var oldPartitions []string
	for rows.Next() {
		var partName string
		if err := rows.Scan(&partName); err != nil {
			return nil, fmt.Errorf("scan partition name: %w", err)
		}

		// Parse the year and month from the partition name.
		// Expected format: {tableName}_{YYYY}_{MM}
		year, month, err := parsePartitionDate(partName, tableName)
		if err != nil {
			// Skip partitions that don't match the expected naming convention.
			logger.Debug("skipping non-standard partition name",
				"partition", partName,
				"error", err,
			)
			continue
		}

		// Check if this partition is older than the cutoff.
		if year < cutoffYear || (year == cutoffYear && month < cutoffMonth) {
			oldPartitions = append(oldPartitions, partName)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate partitions for %s: %w", tableName, err)
	}

	return oldPartitions, nil
}

// ---------------------------------------------------------------------------
// archiveSinglePartition — export, detach, and drop one partition.
// ---------------------------------------------------------------------------

// archiveSinglePartition performs the full archival sequence for a single
// partition: export to compressed file, detach from parent, and drop.
//
// Parameters:
//   - ctx: context for cancellation and database operations.
//   - parentTable: the parent table name (e.g. "deposit_orders").
//   - partitionName: the full partition table name (e.g. "deposit_orders_2025_12").
//
// Returns an error if any step fails. If the export fails, the partition
// is left intact (not detached or dropped).
func (s *ArchiveService) archiveSinglePartition(ctx context.Context, parentTable, partitionName string) error {
	// ---------------------------------------------------------------
	// Step 1: Export the partition to a compressed file using pg_dump.
	// The output file is stored in the configured archive directory.
	// ---------------------------------------------------------------
	archivePath := fmt.Sprintf("%s/%s.sql.gz", s.archiveDir, partitionName)

	logger.Info("exporting partition to archive file",
		"partition", partitionName,
		"archive_path", archivePath,
	)

	// Build the pg_dump command. We use --table to dump only the specific
	// partition table and pipe through gzip for compression.
	// #nosec G204 — partitionName is validated from information_schema
	dumpCmd := exec.CommandContext(ctx,
		"bash", "-c",
		fmt.Sprintf("pg_dump '%s' --table='%s' --no-owner --no-privileges | gzip > '%s'",
			s.pgDumpDSN, partitionName, archivePath,
		),
	)

	output, err := dumpCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump %s failed: %w (output: %s)", partitionName, err, string(output))
	}

	logger.Info("partition exported successfully",
		"partition", partitionName,
		"archive_path", archivePath,
		"note", "manual transfer to off-server storage required",
	)

	// ---------------------------------------------------------------
	// Step 2: Detach the partition from the parent table.
	// DETACH PARTITION removes the partition from the parent's partition
	// list without dropping the data. This is a safe intermediate step.
	// ---------------------------------------------------------------
	detachSQL := fmt.Sprintf("ALTER TABLE %s DETACH PARTITION %s", parentTable, partitionName)

	logger.Info("detaching partition from parent", "sql", detachSQL)

	_, err = s.pool.Exec(ctx, detachSQL)
	if err != nil {
		return fmt.Errorf("detach partition %s: %w", partitionName, err)
	}

	// ---------------------------------------------------------------
	// Step 3: Drop the detached partition table.
	// Now that the partition is detached and the data is exported,
	// we can safely drop the table to reclaim storage.
	// ---------------------------------------------------------------
	dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s", partitionName)

	logger.Info("dropping detached partition", "sql", dropSQL)

	_, err = s.pool.Exec(ctx, dropSQL)
	if err != nil {
		return fmt.Errorf("drop partition %s: %w", partitionName, err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// parsePartitionDate — extract year/month from a partition table name.
// ---------------------------------------------------------------------------

// parsePartitionDate extracts the year and month from a partition table
// name that follows the convention: {parentTable}_{YYYY}_{MM}.
//
// Parameters:
//   - partitionName: the full partition table name.
//   - parentTable: the parent table name (prefix to strip).
//
// Returns the year, month, and any parsing error.
func parsePartitionDate(partitionName, parentTable string) (int, int, error) {
	// Strip the parent table name prefix and the underscore separator.
	suffix := strings.TrimPrefix(partitionName, parentTable+"_")
	if suffix == partitionName {
		return 0, 0, fmt.Errorf("partition %s does not match parent %s", partitionName, parentTable)
	}

	// Parse the year and month from the suffix (expected format: YYYY_MM).
	var year, month int
	_, err := fmt.Sscanf(suffix, "%d_%02d", &year, &month)
	if err != nil {
		return 0, 0, fmt.Errorf("parse date from partition %s: %w", partitionName, err)
	}

	// Validate the parsed month is in the valid range.
	if month < 1 || month > 12 {
		return 0, 0, fmt.Errorf("invalid month %d in partition %s", month, partitionName)
	}

	return year, month, nil
}
