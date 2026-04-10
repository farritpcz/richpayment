// Package service implements the core business logic for the scheduler-service.
// This file contains the cron scheduler that registers and runs all periodic
// background jobs: daily counter resets, commission aggregation, partition
// management, and data archival.
//
// The scheduler uses time-based polling instead of a third-party cron library
// to minimise dependencies. Each job has a defined schedule, and the scheduler
// checks once per minute whether any jobs are due to run.
package service

import (
	"context"
	"sync"
	"time"

	"github.com/farritpcz/richpayment/pkg/logger"
)

// ---------------------------------------------------------------------------
// JobFunc — the function signature for scheduled jobs.
// ---------------------------------------------------------------------------

// JobFunc represents a scheduled job function that accepts a context and
// returns an error. All scheduled jobs must conform to this signature.
// The context is used for cancellation and deadline propagation.
type JobFunc func(ctx context.Context) error

// ---------------------------------------------------------------------------
// ScheduledJob — metadata and schedule for a single cron job.
// ---------------------------------------------------------------------------

// ScheduledJob represents a single scheduled job with its name, execution
// function, schedule parameters, and last/next run timestamps. The scheduler
// uses these fields to determine when each job should fire.
type ScheduledJob struct {
	// Name is the human-readable identifier for this job (e.g. "reset_daily_counters").
	// Used for logging and the status API endpoint.
	Name string

	// Fn is the function that executes the job's business logic.
	// It receives a context for cancellation support.
	Fn JobFunc

	// Hour is the hour of day (0-23) when the job should run.
	// Combined with Minute, this defines the daily trigger time.
	Hour int

	// Minute is the minute of the hour (0-59) when the job should run.
	// Combined with Hour, this defines the daily trigger time.
	Minute int

	// IntervalHours defines the interval in hours between runs for
	// hourly jobs. If zero, the job runs daily at the specified Hour:Minute.
	// If non-zero, the job runs every IntervalHours hours.
	IntervalHours int

	// IntervalDays defines the interval in days between runs for
	// multi-day jobs (e.g. weekly partition creation). If zero, the job
	// is daily or hourly (depending on IntervalHours).
	IntervalDays int

	// LastRun records the timestamp of the last successful execution.
	// Used to calculate when the next run is due.
	LastRun time.Time

	// NextRun is the calculated timestamp of the next scheduled execution.
	// Updated after each run or when the scheduler starts.
	NextRun time.Time

	// Running indicates whether this job is currently executing.
	// Used to prevent overlapping executions of the same job.
	Running bool
}

// ---------------------------------------------------------------------------
// CronScheduler — manages and executes all scheduled jobs.
// ---------------------------------------------------------------------------

// CronScheduler manages the lifecycle of all scheduled jobs. It holds
// references to the individual service instances (partition, archive,
// summary) and maintains the list of registered jobs with their schedules.
type CronScheduler struct {
	// partitionSvc handles PostgreSQL partition management: checking for
	// missing partitions and creating future ones.
	partitionSvc *PartitionService

	// archiveSvc handles data archival: dumping old partitions to
	// compressed files and detaching them from the main tables.
	archiveSvc *ArchiveService

	// summarySvc handles daily commission aggregation: querying raw
	// commission records and upserting into the daily summary table.
	summarySvc *SummaryService

	// jobs is the list of all registered scheduled jobs. Protected by
	// the mu mutex for concurrent access from the scheduler loop and
	// the status HTTP endpoint.
	jobs []*ScheduledJob

	// mu protects concurrent access to the jobs slice (reading job status
	// from the HTTP handler while the scheduler loop updates timestamps).
	mu sync.RWMutex
}

// NewCronScheduler constructs a new CronScheduler with the given service
// dependencies. It does NOT start the scheduler — call StartScheduler
// to begin the polling loop.
//
// Parameters:
//   - partitionSvc: the partition management service.
//   - archiveSvc: the data archival service.
//   - summarySvc: the daily commission aggregation service.
//
// Returns a ready-to-configure CronScheduler instance.
func NewCronScheduler(
	partitionSvc *PartitionService,
	archiveSvc *ArchiveService,
	summarySvc *SummaryService,
) *CronScheduler {
	return &CronScheduler{
		partitionSvc: partitionSvc,
		archiveSvc:   archiveSvc,
		summarySvc:   summarySvc,
		jobs:         make([]*ScheduledJob, 0),
	}
}

// ---------------------------------------------------------------------------
// registerJobs — set up all cron jobs with their schedules.
// ---------------------------------------------------------------------------

// registerJobs creates and registers all scheduled jobs with their
// configured schedules. This is called once during StartScheduler before
// the polling loop begins.
//
// Current job schedule:
//   - Every midnight (00:00): ResetDailyCounters, AggregateCommissions
//   - Every hour: CheckPartitions
//   - Every day at 02:00: ArchiveOldData
//   - Every 7 days: CreateFuturePartitions
func (s *CronScheduler) registerJobs() {
	now := time.Now()

	// ---------------------------------------------------------------
	// Job: ResetDailyCounters
	// Runs at midnight (00:00) every day.
	// Resets daily rate-limit counters, withdrawal totals, and other
	// per-day accumulators in Redis and PostgreSQL.
	// ---------------------------------------------------------------
	s.jobs = append(s.jobs, &ScheduledJob{
		Name:    "reset_daily_counters",
		Fn:      s.resetDailyCounters,
		Hour:    0,
		Minute:  0,
		NextRun: calculateNextDailyRun(now, 0, 0),
	})

	// ---------------------------------------------------------------
	// Job: AggregateCommissions
	// Runs at midnight (00:00) every day.
	// Aggregates the previous day's commission records into the
	// commission_daily_summary table for reporting and dashboards.
	// ---------------------------------------------------------------
	s.jobs = append(s.jobs, &ScheduledJob{
		Name:    "aggregate_commissions",
		Fn:      s.aggregateCommissions,
		Hour:    0,
		Minute:  0,
		NextRun: calculateNextDailyRun(now, 0, 0),
	})

	// ---------------------------------------------------------------
	// Job: CheckPartitions
	// Runs every hour.
	// Checks whether next month's PostgreSQL partitions exist and
	// creates them if missing. Hourly frequency ensures partitions
	// are created promptly even if a previous check failed.
	// ---------------------------------------------------------------
	s.jobs = append(s.jobs, &ScheduledJob{
		Name:          "check_partitions",
		Fn:            s.checkPartitions,
		IntervalHours: 1,
		NextRun:       now.Add(1 * time.Hour).Truncate(time.Hour),
	})

	// ---------------------------------------------------------------
	// Job: ArchiveOldData
	// Runs at 02:00 every day.
	// Archives partitions older than 3 months: pg_dump to compressed
	// file, then detach and drop the partition.
	// ---------------------------------------------------------------
	s.jobs = append(s.jobs, &ScheduledJob{
		Name:    "archive_old_data",
		Fn:      s.archiveOldData,
		Hour:    2,
		Minute:  0,
		NextRun: calculateNextDailyRun(now, 2, 0),
	})

	// ---------------------------------------------------------------
	// Job: CreateFuturePartitions
	// Runs every 7 days.
	// Proactively creates partitions for several months ahead to
	// ensure partition availability well before they are needed.
	// ---------------------------------------------------------------
	s.jobs = append(s.jobs, &ScheduledJob{
		Name:         "create_future_partitions",
		Fn:           s.createFuturePartitions,
		IntervalDays: 7,
		NextRun:      calculateNextDailyRun(now, 3, 0),
	})

	logger.Info("registered scheduled jobs", "count", len(s.jobs))
}

// ---------------------------------------------------------------------------
// StartScheduler — begin the cron polling loop.
// ---------------------------------------------------------------------------

// StartScheduler registers all jobs and starts the main scheduling loop.
// It checks every 30 seconds whether any job is due to run and executes
// due jobs in separate goroutines to avoid blocking the scheduler.
//
// The method blocks until the context is cancelled (typically on shutdown).
// Each job execution is protected against overlapping runs — if a job is
// still running when its next scheduled time arrives, it is skipped.
//
// Parameters:
//   - ctx: context for cancellation. When cancelled, the loop exits.
func (s *CronScheduler) StartScheduler(ctx context.Context) {
	// Register all jobs with their schedules.
	s.registerJobs()

	logger.Info("starting cron scheduler")

	// Create a ticker that fires every 30 seconds to check for due jobs.
	// 30 seconds provides a good balance between responsiveness and
	// CPU overhead. Jobs will run within 30 seconds of their scheduled time.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Main scheduler loop. Runs until the context is cancelled.
	for {
		select {
		case <-ctx.Done():
			// Context cancelled — stop the scheduler.
			logger.Info("cron scheduler stopped", "reason", ctx.Err())
			return

		case now := <-ticker.C:
			// Check each registered job to see if it is due to run.
			s.mu.RLock()
			jobsToRun := make([]*ScheduledJob, 0)
			for _, job := range s.jobs {
				// Skip jobs that are already running (prevent overlapping).
				if job.Running {
					continue
				}
				// Check if the current time is at or past the job's next run time.
				if now.Equal(job.NextRun) || now.After(job.NextRun) {
					jobsToRun = append(jobsToRun, job)
				}
			}
			s.mu.RUnlock()

			// Execute each due job in a separate goroutine.
			for _, job := range jobsToRun {
				go s.executeJob(ctx, job)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// executeJob — run a single job and update its schedule.
// ---------------------------------------------------------------------------

// executeJob runs a single scheduled job, updates its running status, and
// calculates the next run time after completion. Errors from the job
// function are logged but do not affect the scheduler or other jobs.
//
// Parameters:
//   - ctx: context for cancellation.
//   - job: the scheduled job to execute.
func (s *CronScheduler) executeJob(ctx context.Context, job *ScheduledJob) {
	// Mark the job as running to prevent overlapping executions.
	s.mu.Lock()
	job.Running = true
	s.mu.Unlock()

	logger.Info("executing scheduled job", "job", job.Name)
	startTime := time.Now()

	// Execute the job function.
	err := job.Fn(ctx)

	// Calculate execution duration for logging.
	duration := time.Since(startTime)

	// Update the job's timestamps and running status.
	s.mu.Lock()
	job.Running = false
	job.LastRun = startTime

	// Calculate the next run time based on the job's schedule type.
	now := time.Now()
	if job.IntervalHours > 0 {
		// Hourly interval job: next run is IntervalHours from now.
		job.NextRun = now.Add(time.Duration(job.IntervalHours) * time.Hour).Truncate(time.Hour)
	} else if job.IntervalDays > 0 {
		// Multi-day interval job: next run is IntervalDays from now.
		job.NextRun = now.AddDate(0, 0, job.IntervalDays)
		job.NextRun = time.Date(
			job.NextRun.Year(), job.NextRun.Month(), job.NextRun.Day(),
			job.Hour, job.Minute, 0, 0, job.NextRun.Location(),
		)
	} else {
		// Daily job: next run is tomorrow at the configured time.
		job.NextRun = calculateNextDailyRun(now, job.Hour, job.Minute)
	}
	s.mu.Unlock()

	// Log the result.
	if err != nil {
		logger.Error("scheduled job failed",
			"job", job.Name,
			"duration", duration.String(),
			"error", err,
		)
	} else {
		logger.Info("scheduled job completed",
			"job", job.Name,
			"duration", duration.String(),
			"next_run", job.NextRun.Format(time.RFC3339),
		)
	}
}

// ---------------------------------------------------------------------------
// Job wrapper functions — delegates to the appropriate service.
// ---------------------------------------------------------------------------

// resetDailyCounters resets all daily rate-limit counters and accumulators.
// This is a placeholder that logs the action; in production it would clear
// Redis keys and PostgreSQL daily totals.
func (s *CronScheduler) resetDailyCounters(ctx context.Context) error {
	logger.Info("resetting daily counters")
	// TODO: Clear Redis daily counter keys (e.g. daily_withdrawal:{merchant_id}).
	// TODO: Reset daily_withdrawal_used in the merchants table.
	_ = ctx
	return nil
}

// aggregateCommissions delegates to the SummaryService to aggregate the
// previous day's commission records into daily summaries.
func (s *CronScheduler) aggregateCommissions(ctx context.Context) error {
	// Aggregate commissions for yesterday (the most recently completed day).
	yesterday := time.Now().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	return s.summarySvc.AggregateCommissions(ctx, yesterday)
}

// checkPartitions delegates to the PartitionService to verify that
// next month's partitions exist and creates them if missing.
func (s *CronScheduler) checkPartitions(ctx context.Context) error {
	return s.partitionSvc.CheckPartitions(ctx)
}

// archiveOldData delegates to the ArchiveService to archive partitions
// older than 3 months.
func (s *CronScheduler) archiveOldData(ctx context.Context) error {
	return s.archiveSvc.ArchiveOldPartitions(ctx)
}

// createFuturePartitions delegates to the PartitionService to proactively
// create partitions for several months in the future.
func (s *CronScheduler) createFuturePartitions(ctx context.Context) error {
	return s.partitionSvc.CreateFuturePartitions(ctx)
}

// ---------------------------------------------------------------------------
// GetJobs — return a snapshot of all jobs for the status endpoint.
// ---------------------------------------------------------------------------

// JobStatus is a read-only snapshot of a scheduled job's current state.
// Used by the HTTP status endpoint to report job schedules and last runs.
type JobStatus struct {
	// Name is the job's human-readable identifier.
	Name string `json:"name"`

	// LastRun is the timestamp of the last execution (zero if never run).
	LastRun *time.Time `json:"last_run"`

	// NextRun is the calculated timestamp of the next scheduled execution.
	NextRun time.Time `json:"next_run"`

	// Running indicates whether the job is currently executing.
	Running bool `json:"running"`
}

// GetJobs returns a snapshot of all registered jobs and their current status.
// This is called by the HTTP status endpoint to report job schedules.
// The snapshot is taken under a read lock to ensure consistency.
//
// Returns a slice of JobStatus structs, one per registered job.
func (s *CronScheduler) GetJobs() []JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build a snapshot slice. We copy data out of the locked structure
	// so the caller can use the result without holding the lock.
	result := make([]JobStatus, len(s.jobs))
	for i, job := range s.jobs {
		var lastRun *time.Time
		if !job.LastRun.IsZero() {
			t := job.LastRun
			lastRun = &t
		}
		result[i] = JobStatus{
			Name:    job.Name,
			LastRun: lastRun,
			NextRun: job.NextRun,
			Running: job.Running,
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// RunJobByName — manually trigger a job by name.
// ---------------------------------------------------------------------------

// RunJobByName finds a registered job by name and executes it immediately
// in a goroutine, regardless of its schedule. This is called by the manual
// trigger HTTP endpoint to allow operators to run jobs on demand.
//
// Parameters:
//   - ctx: context for the job execution.
//   - name: the name of the job to run (e.g. "check_partitions").
//
// Returns true if the job was found and started, false if not found or
// already running.
func (s *CronScheduler) RunJobByName(ctx context.Context, name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Search for the job by name.
	for _, job := range s.jobs {
		if job.Name == name {
			// Check if the job is already running.
			if job.Running {
				logger.Warn("job is already running, skipping manual trigger",
					"job", name,
				)
				return false
			}

			// Execute the job in a goroutine.
			logger.Info("manually triggering job", "job", name)
			go s.executeJob(ctx, job)
			return true
		}
	}

	// Job not found.
	logger.Warn("job not found for manual trigger", "job", name)
	return false
}

// ---------------------------------------------------------------------------
// Helper: calculateNextDailyRun.
// ---------------------------------------------------------------------------

// calculateNextDailyRun computes the next occurrence of a specific time-of-day
// (hour:minute) after the given reference time. If the target time has already
// passed today, the function returns tomorrow at the target time.
//
// Parameters:
//   - after: the reference time to calculate from.
//   - hour: the target hour (0-23).
//   - minute: the target minute (0-59).
//
// Returns the next time.Time at the specified hour:minute.
func calculateNextDailyRun(after time.Time, hour, minute int) time.Time {
	// Build today's target time.
	next := time.Date(
		after.Year(), after.Month(), after.Day(),
		hour, minute, 0, 0, after.Location(),
	)

	// If the target time has already passed today, advance to tomorrow.
	if next.Before(after) || next.Equal(after) {
		next = next.AddDate(0, 0, 1)
	}

	return next
}
