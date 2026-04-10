// Package main is the entry point for the withdrawal-service. It initialises
// the repository and service layers, constructs HTTP handlers, and launches
// an HTTP server on port 8085.
//
// The service manages the complete withdrawal lifecycle for the RichPayment
// platform. Merchants request withdrawals from their wallets, and admins
// approve or reject them. Approved withdrawals are then completed by the
// finance team after executing the bank transfer.
//
// API routes:
//
//	POST   /api/v1/withdrawals                  - Create a new withdrawal
//	GET    /api/v1/withdrawals/pending           - List pending withdrawals
//	GET    /api/v1/withdrawals/{id}              - Get withdrawal by ID
//	POST   /api/v1/withdrawals/{id}/approve      - Approve a withdrawal
//	POST   /api/v1/withdrawals/{id}/reject       - Reject a withdrawal
//	POST   /api/v1/withdrawals/{id}/complete     - Complete a withdrawal
//	GET    /health                               - Health check endpoint
//
// Graceful shutdown is handled via OS signals (SIGINT / SIGTERM).
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
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/middleware"
	"github.com/farritpcz/richpayment/services/withdrawal/internal/handler"
	"github.com/farritpcz/richpayment/services/withdrawal/internal/repository"
	"github.com/farritpcz/richpayment/services/withdrawal/internal/service"
)

func main() {
	// Obtain the default structured JSON logger for the service.
	log := logger.Default()
	log.Info("starting withdrawal-service")

	// ------------------------------------------------------------------
	// Load configuration from environment variables with sensible defaults.
	// ------------------------------------------------------------------
	port := config.Get("WITHDRAWAL_PORT", "8085")

	// ------------------------------------------------------------------
	// Construct the repository layer.
	// In production this would be a PostgreSQL-backed implementation.
	// For now we use the in-memory stub to allow the service to start
	// without external dependencies.
	// ------------------------------------------------------------------
	withdrawalRepo := repository.NewStubWithdrawalRepo()

	// ------------------------------------------------------------------
	// Construct stub clients for wallet, commission, and merchant services.
	// In production, these would be HTTP/gRPC clients connecting to the
	// respective microservices. The stubs provide sensible defaults for
	// local development and testing.
	// ------------------------------------------------------------------

	// walletClient handles balance checks, holds, releases, and debits.
	walletClient := &service.StubWalletClient{}

	// commissionClient records fee splits on completed withdrawals.
	commissionClient := &service.StubCommissionClient{}

	// merchantClient fetches merchant fee configuration and limits.
	merchantClient := &service.StubMerchantClient{}

	// ------------------------------------------------------------------
	// Construct the service layer.
	// The withdrawal service orchestrates the entire withdrawal lifecycle
	// by coordinating between the repository and external service clients.
	// ------------------------------------------------------------------
	withdrawalSvc := service.NewWithdrawalService(
		withdrawalRepo, walletClient, commissionClient, merchantClient,
	)

	// ------------------------------------------------------------------
	// Construct the HTTP handler and register routes.
	// ------------------------------------------------------------------

	// withdrawalHandler maps HTTP routes to service-layer methods.
	withdrawalHandler := handler.NewWithdrawalHandler(withdrawalSvc)

	// Build the HTTP router using the standard library ServeMux.
	mux := http.NewServeMux()

	// Register all withdrawal routes (create, get, list, approve, reject, complete).
	withdrawalHandler.Register(mux)

	// Health check endpoint — returns 200 OK with a JSON body.
	// Used by load balancers and orchestrators (e.g. Kubernetes) to verify
	// the service is alive and ready to accept traffic.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "withdrawal-service",
		})
	})

	// ------------------------------------------------------------------
	// Apply middleware and create the HTTP server.
	// ------------------------------------------------------------------

	// Wrap the mux with the panic recovery middleware so that unexpected
	// panics in handlers do not crash the entire service.
	httpHandler := middleware.Recovery(log)(mux)

	// Configure the HTTP server with sensible timeouts to prevent slow
	// clients from holding connections indefinitely.
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

	// Allow up to 10 seconds for in-flight HTTP requests to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", "error", err)
	}

	log.Info("withdrawal-service stopped")
}
