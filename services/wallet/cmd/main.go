// Package main is the entry point for the wallet-service. It wires together
// the configuration, database pool, repository, service, and HTTP handler
// layers, then starts an HTTP server on the configured port (default 8084).
//
// The server supports graceful shutdown: on SIGINT or SIGTERM, in-flight
// requests are given up to 10 seconds to complete before the process exits.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/farritpcz/richpayment/pkg/config"
	"github.com/farritpcz/richpayment/pkg/database"
	"github.com/farritpcz/richpayment/pkg/middleware"
	"github.com/farritpcz/richpayment/services/wallet/internal/handler"
	"github.com/farritpcz/richpayment/services/wallet/internal/repository"
	"github.com/farritpcz/richpayment/services/wallet/internal/service"
)

func main() {
	// ---------------------------------------------------------------
	// 1. Logger setup
	// ---------------------------------------------------------------
	// Create a structured JSON logger that writes to stdout. This is
	// the canonical logger for the entire process; all subsystems
	// should use it (or the pkg/logger helpers which wrap slog).
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(log)

	// ---------------------------------------------------------------
	// 2. Root context with cancellation
	// ---------------------------------------------------------------
	// The root context is cancelled when the application receives a
	// termination signal. All long-running operations should use this
	// context (or a child derived from it) so they stop promptly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---------------------------------------------------------------
	// 3. Database connection pool
	// ---------------------------------------------------------------
	// Load the PostgreSQL connection settings from environment
	// variables (with sensible defaults for local development) and
	// create a connection pool. The pool is shared across all
	// repository instances.
	dbCfg := config.LoadDatabaseConfig()
	pool, err := database.NewPostgresPool(ctx, dbCfg.DSN(), dbCfg.MaxConns)
	if err != nil {
		log.Error("failed to connect to PostgreSQL", "err", err, "dsn", dbCfg.DSN())
		os.Exit(1)
	}
	defer pool.Close()
	log.Info("connected to PostgreSQL",
		"host", dbCfg.Host,
		"port", dbCfg.Port,
		"db", dbCfg.DBName,
	)

	// ---------------------------------------------------------------
	// 4. Repository layer
	// ---------------------------------------------------------------
	// The PostgresWalletRepo implements the WalletRepository interface
	// and provides all data-access operations for wallets and ledger
	// entries.
	walletRepo := repository.NewPostgresWalletRepo(pool)

	// ---------------------------------------------------------------
	// 5. Service layer
	// ---------------------------------------------------------------
	// The WalletService encapsulates all business rules: optimistic
	// locking, balance validation, ledger entry creation, etc. It
	// depends only on the repository interface, not on pgx directly.
	walletSvc := service.NewWalletService(walletRepo)

	// ---------------------------------------------------------------
	// 6. HTTP handler and routing
	// ---------------------------------------------------------------
	// The WalletHandler translates HTTP requests/responses into
	// service-layer calls and back.
	walletHandler := handler.NewWalletHandler(walletSvc)

	// Create the top-level ServeMux and register all wallet routes.
	mux := http.NewServeMux()
	walletHandler.RegisterRoutes(mux)

	// Health-check endpoint used by load balancers and orchestrators
	// (e.g. Kubernetes readiness probes) to verify the service is alive.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// ---------------------------------------------------------------
	// 7. HTTP server configuration
	// ---------------------------------------------------------------
	// The port defaults to 8084 but can be overridden via the PORT
	// environment variable.
	port := config.Get("PORT", "8084")
	srv := &http.Server{
		Addr: ":" + port,

		// Wrap the mux with the recovery middleware so that panics in
		// handlers are caught, logged, and converted to 500 responses
		// instead of crashing the process.
		Handler: middleware.Recovery(log)(mux),

		// Timeout settings prevent slow clients from holding
		// connections open indefinitely.
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ---------------------------------------------------------------
	// 8. Graceful shutdown
	// ---------------------------------------------------------------
	// A separate goroutine listens for SIGINT (Ctrl+C) and SIGTERM
	// (sent by Docker/K8s on stop). When a signal arrives, the root
	// context is cancelled and the HTTP server is shut down gracefully,
	// allowing in-flight requests up to 10 seconds to finish.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Info("received shutdown signal", "signal", sig)

		// Cancel the root context so background work stops.
		cancel()

		// Give in-flight HTTP requests time to complete.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("HTTP server shutdown error", "err", err)
		}
	}()

	// ---------------------------------------------------------------
	// 9. Start listening
	// ---------------------------------------------------------------
	log.Info("wallet-service starting", "port", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("HTTP server fatal error", "err", err)
		os.Exit(1)
	}
	log.Info("wallet-service stopped gracefully")
}
