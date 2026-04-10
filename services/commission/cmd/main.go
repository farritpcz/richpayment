// Package main is the entry point for the commission-service.
//
// The commission-service is responsible for:
//   - Calculating how transaction fees are split among system, agent, and partner.
//   - Recording commission records in the database and crediting wallets.
//   - Aggregating daily and monthly commission summaries for reporting.
//
// It listens on port 8088 (configurable via COMMISSION_PORT) and exposes
// both internal (service-to-service) and external (admin dashboard) endpoints.
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
	"github.com/farritpcz/richpayment/services/commission/internal/handler"
	"github.com/farritpcz/richpayment/services/commission/internal/repository"
	"github.com/farritpcz/richpayment/services/commission/internal/service"
)

func main() {
	// Initialise the structured JSON logger used throughout the service.
	log := logger.Default()
	log.Info("starting commission-service")

	// -----------------------------------------------------------------------
	// Configuration
	// -----------------------------------------------------------------------

	// Load database and Redis configuration from environment variables.
	// Defaults are provided for local development environments.
	dbCfg := config.LoadDatabaseConfig()
	redisCfg := config.LoadRedisConfig()

	// COMMISSION_PORT controls which TCP port the HTTP server binds to.
	// Defaults to 8088 so that it does not collide with other services.
	port := config.Get("COMMISSION_PORT", "8088")

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

	// Redis is used for caching daily summaries and as a fast lookup layer
	// to avoid hitting the database on every summary request.
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisCfg.Addr(),
		Password: redisCfg.Password,
		DB:       redisCfg.DB,
	})

	// Verify Redis connection. We warn but do not exit because the service
	// can still function (with degraded caching) if Redis is unavailable.
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
	repo := repository.NewCommissionRepository(pool, rdb)

	// Create the service layer that contains business logic.
	calcSvc := service.NewCalculator(repo, log)
	aggSvc := service.NewAggregator(repo, rdb, log)

	// Create the HTTP handler that wires routes to the service layer.
	commissionHandler := handler.NewCommissionHandler(calcSvc, aggSvc, log)

	// -----------------------------------------------------------------------
	// HTTP server
	// -----------------------------------------------------------------------

	// Build the HTTP mux with all commission-service routes.
	mux := commissionHandler.RegisterRoutes()

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
	log.Info("commission-service stopped")
}
