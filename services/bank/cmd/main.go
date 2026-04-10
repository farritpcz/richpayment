// Package main is the entry point for the bank-service.
//
// The bank-service is responsible for:
//   - Managing a pool of bank accounts used for receiving deposits.
//   - Selecting the optimal account for each deposit (round-robin by priority).
//   - Monitoring daily receiving volumes and auto-switching accounts at limits.
//   - Processing fund transfers from pool accounts to holding (treasury) accounts.
//
// It listens on port 8089 (configurable via BANK_PORT) and exposes both
// internal (service-to-service) and external (admin dashboard) endpoints.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/farritpcz/richpayment/pkg/config"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/services/bank/internal/handler"
	"github.com/farritpcz/richpayment/services/bank/internal/repository"
	"github.com/farritpcz/richpayment/services/bank/internal/service"
)

func main() {
	// Initialise the structured JSON logger used throughout the service.
	log := logger.Default()
	log.Info("starting bank-service")

	// -----------------------------------------------------------------------
	// Configuration
	// -----------------------------------------------------------------------

	// Load database and Redis configuration from environment variables.
	// Defaults are provided for local development environments.
	dbCfg := config.LoadDatabaseConfig()
	redisCfg := config.LoadRedisConfig()

	// BANK_PORT controls which TCP port the HTTP server binds to.
	// Defaults to 8089 so that it does not collide with other services.
	port := config.Get("BANK_PORT", "8089")

	// -----------------------------------------------------------------------
	// Database connection
	// -----------------------------------------------------------------------

	// Create a connection pool to PostgreSQL. The pool manages connections
	// automatically, opening new ones on demand and reusing idle ones.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(dbCfg.DSN())
	if err != nil {
		log.Error("failed to parse database DSN", "error", err)
		os.Exit(1)
	}

	// Tune pool sizes based on environment configuration.
	poolCfg.MaxConns = int32(dbCfg.MaxConns)
	poolCfg.MinConns = 5
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Error("failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Verify that the database is reachable before starting the server.
	if err := pool.Ping(ctx); err != nil {
		log.Error("database ping failed", "error", err)
		os.Exit(1)
	}
	log.Info("connected to PostgreSQL")

	// -----------------------------------------------------------------------
	// Redis connection
	// -----------------------------------------------------------------------

	// Redis is used for caching account pool sorted sets and daily receiving
	// counters. It provides fast reads for the SelectAccount path.
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisCfg.Addr(),
		Password: redisCfg.Password,
		DB:       redisCfg.DB,
	})

	// Verify Redis connection. We warn but do not exit because the service
	// can still function (with degraded performance) if Redis is unavailable.
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	if err := rdb.Ping(rctx).Err(); err != nil {
		log.Warn("redis not reachable at startup, continuing anyway", "error", err)
	} else {
		log.Info("connected to Redis")
	}

	// -----------------------------------------------------------------------
	// Dependency wiring
	// -----------------------------------------------------------------------

	// Create the repository layer (database + cache access).
	repo := repository.NewBankRepository(pool, rdb)

	// Create the service layer that contains business logic.
	// Note: Pool must be created first because Monitor depends on it
	// for triggering auto-switch when limits are reached.
	poolSvc := service.NewPool(repo, rdb, log)
	monitorSvc := service.NewMonitor(repo, rdb, poolSvc, log)
	transferSvc := service.NewTransferService(repo, log)

	// Create the HTTP handler that wires routes to the service layer.
	bankHandler := handler.NewBankHandler(poolSvc, monitorSvc, transferSvc, log)

	// -----------------------------------------------------------------------
	// HTTP server
	// -----------------------------------------------------------------------

	// Build the HTTP mux with all bank-service routes.
	mux := bankHandler.RegisterRoutes()

	// Create the HTTP server with sensible timeouts to prevent slow-loris
	// and other resource exhaustion attacks.
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start the server in a background goroutine so that main can block
	// on the shutdown signal.
	go func() {
		log.Info("listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// -----------------------------------------------------------------------
	// Graceful shutdown
	// -----------------------------------------------------------------------

	// Block until we receive SIGINT (Ctrl+C) or SIGTERM (Docker stop).
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("shutting down", "signal", sig.String())

	// Give in-flight requests up to 10 seconds to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", "error", err)
	}

	// Close external connections cleanly.
	_ = rdb.Close()
	pool.Close()
	log.Info("bank-service stopped")
}
