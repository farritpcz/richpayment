// Package main is the entrypoint for the gateway-api service binary. It
// initialises configuration, connects to Redis, creates HTTP clients for
// internal service-to-service communication, builds the HTTP router with
// all middleware and routes, and starts a graceful-shutdown-aware HTTP server.
//
// INTER-SERVICE COMMUNICATION SETUP:
// The gateway-api is the public entry point for merchants. It does not contain
// business logic — instead it proxies requests to internal services:
//
//   gateway (:8080) --> order-service      (ORDER_SERVICE_URL,      default :8083)
//   gateway (:8080) --> wallet-service     (WALLET_SERVICE_URL,     default :8084)
//   gateway (:8080) --> withdrawal-service (WITHDRAWAL_SERVICE_URL, default :8085)
//
// Service URLs are configured via environment variables so that the same binary
// works in local development (localhost), Docker Compose (service names), and
// Kubernetes (ClusterIP services).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/farritpcz/richpayment/pkg/config"
	"github.com/farritpcz/richpayment/pkg/httpclient"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/services/gateway/internal/router"
)

// main initialises all dependencies and starts the gateway-api HTTP server.
func main() {
	log := logger.Default()
	log.Info("starting gateway-api service")

	// Load configuration.
	redisCfg := config.LoadRedisConfig()
	port := config.Get("GATEWAY_PORT", "8080")

	// ------------------------------------------------------------------
	// Load internal service URLs from environment variables.
	//
	// These URLs define where the gateway forwards requests. In local
	// development, services run on localhost with different ports. In
	// Docker Compose or Kubernetes, these would be service hostnames.
	//
	// ORDER_SERVICE_URL:      The order-service handles deposit lifecycle.
	// WALLET_SERVICE_URL:     The wallet-service manages balances and ledger.
	// WITHDRAWAL_SERVICE_URL: The withdrawal-service handles payout lifecycle.
	// ------------------------------------------------------------------
	orderServiceURL := config.Get("ORDER_SERVICE_URL", "http://localhost:8083")
	walletServiceURL := config.Get("WALLET_SERVICE_URL", "http://localhost:8084")
	withdrawalServiceURL := config.Get("WITHDRAWAL_SERVICE_URL", "http://localhost:8085")

	log.Info("configured upstream service URLs",
		"order_service", orderServiceURL,
		"wallet_service", walletServiceURL,
		"withdrawal_service", withdrawalServiceURL,
	)

	// ------------------------------------------------------------------
	// Create HTTP clients for inter-service communication.
	//
	// Each client targets a single internal service. The 10-second timeout
	// is generous enough for database-backed operations in the target
	// services but short enough to fail fast if a service is down.
	//
	// These clients are passed to the handlers via the router constructor,
	// enabling dependency injection and easy testing.
	// ------------------------------------------------------------------
	orderClient := httpclient.New(orderServiceURL, 10*time.Second)
	walletClient := httpclient.New(walletServiceURL, 10*time.Second)
	withdrawalClient := httpclient.New(withdrawalServiceURL, 10*time.Second)

	// Connect to Redis.
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisCfg.Addr(),
		Password: redisCfg.Password,
		DB:       redisCfg.DB,
	})

	// Verify Redis connection.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Warn("redis not reachable at startup, continuing anyway", "error", err)
	}

	// Build router, passing the inter-service HTTP clients so handlers
	// can proxy requests to the appropriate backend services.
	handler := router.New(rdb, log, orderClient, walletClient, withdrawalClient)

	// Create HTTP server.
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in background.
	go func() {
		log.Info("listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("shutting down", "signal", sig.String())

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", "error", err)
	}

	_ = rdb.Close()
	log.Info("gateway-api stopped")
}
