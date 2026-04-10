// Package main is the entry point for the scheduler-service. It initialises
// the PostgreSQL connection, constructs the service layer (partition manager,
// archive service, commission summary service), starts the cron scheduler
// in a background goroutine, and launches an HTTP server on port 8091 for
// the internal management API.
//
// The service exposes these internal HTTP endpoints:
//
//   POST /internal/scheduler/run/{job}  - Manually trigger a named cron job
//   GET  /internal/scheduler/status     - View all jobs and their schedules
//   GET  /healthz                       - Health check for load balancers/k8s
//
// Graceful shutdown is handled via OS signals (SIGINT / SIGTERM). When a
// shutdown signal is received, the cron scheduler is cancelled, in-flight
// HTTP requests are drained, and the database pool is closed.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/farritpcz/richpayment/pkg/config"
	"github.com/farritpcz/richpayment/pkg/database"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/middleware"
	"github.com/farritpcz/richpayment/services/scheduler/internal/handler"
	"github.com/farritpcz/richpayment/services/scheduler/internal/service"
)

func main() {
	// Obtain the default structured JSON logger for the service.
	log := logger.Default()
	log.Info("starting scheduler-service")

	// ------------------------------------------------------------------
	// Load configuration from environment variables.
	// ------------------------------------------------------------------

	// port is the HTTP server listen port. Defaults to 8091.
	port := config.Get("SCHEDULER_PORT", "8091")

	// dbCfg holds the PostgreSQL connection parameters loaded from
	// environment variables (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD,
	// DB_NAME, DB_MAX_CONNS).
	dbCfg := config.LoadDatabaseConfig()

	// archiveDir is the local directory where pg_dump archive files are
	// stored before manual off-server transfer.
	archiveDir := config.Get("ARCHIVE_DIR", "/var/lib/richpayment/archives")

	// retentionMonths is the number of months of data to keep in the
	// live database. Partitions older than this are archived and dropped.
	retentionMonths := config.GetInt("RETENTION_MONTHS", 3)

	// ------------------------------------------------------------------
	// Establish PostgreSQL connection pool.
	// ------------------------------------------------------------------
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pgPool, err := database.NewPostgresPool(ctx, dbCfg.DSN(), dbCfg.MaxConns)
	if err != nil {
		log.Error("failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pgPool.Close()
	log.Info("connected to PostgreSQL")

	// ------------------------------------------------------------------
	// Construct the service layer.
	// ------------------------------------------------------------------

	// partitionSvc manages PostgreSQL range partitions: checking for
	// missing partitions and creating them automatically.
	partitionSvc := service.NewPartitionService(pgPool)

	// archiveSvc handles archival of old partitions: pg_dump export,
	// partition detach, and drop.
	archiveSvc := service.NewArchiveService(pgPool, archiveDir, dbCfg.DSN(), retentionMonths)

	// summarySvc aggregates daily commission records into the
	// commission_daily_summary table for dashboards and reporting.
	summarySvc := service.NewSummaryService(pgPool)

	// cronScheduler manages all periodic background jobs with their
	// schedules and executes them at the configured times.
	cronScheduler := service.NewCronScheduler(partitionSvc, archiveSvc, summarySvc)

	// ------------------------------------------------------------------
	// Start the cron scheduler in a background goroutine.
	// The scheduler polls every 30 seconds for due jobs and executes
	// them in separate goroutines. It runs until appCtx is cancelled.
	// ------------------------------------------------------------------
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	go cronScheduler.StartScheduler(appCtx)
	log.Info("cron scheduler started")

	// ------------------------------------------------------------------
	// Build HTTP router using the standard library ServeMux.
	// ------------------------------------------------------------------
	mux := http.NewServeMux()

	// Register the scheduler handler routes (run, status, healthz).
	schedulerHandler := handler.NewSchedulerHandler(cronScheduler)
	schedulerHandler.RegisterRoutes(mux)

	// Fallback health endpoint at /health for backward compatibility
	// with older monitoring configurations.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "scheduler-service",
		})
	})

	// ------------------------------------------------------------------
	// Apply middleware and create the HTTP server.
	// ------------------------------------------------------------------

	// Wrap the mux with the panic recovery middleware so that unexpected
	// panics in handlers do not crash the entire service.
	httpHandler := middleware.Recovery(log)(mux)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      httpHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ------------------------------------------------------------------
	// Start the HTTP server in a background goroutine.
	// ------------------------------------------------------------------
	go func() {
		log.Info("listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// ------------------------------------------------------------------
	// Wait for shutdown signal (SIGINT or SIGTERM).
	// ------------------------------------------------------------------
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("shutting down", "signal", sig.String())

	// Cancel the application context to stop the cron scheduler and
	// any currently executing jobs.
	appCancel()

	// Allow up to 10 seconds for in-flight HTTP requests to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", "error", err)
	}

	// Close the PostgreSQL connection pool.
	pgPool.Close()

	log.Info("scheduler-service stopped")
}
