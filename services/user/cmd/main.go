// Package main is the entry point for the user-service. It initialises the
// repository and service layers, constructs HTTP handlers, and launches an
// HTTP server on port 8082.
//
// The service exposes a JSON/HTTP API for managing the four user-domain
// entities in the RichPayment platform:
//
//   - Admins:    back-office administrators with role-based permissions.
//   - Merchants: payment-accepting businesses with API keys and fee settings.
//   - Agents:    intermediaries who manage merchant portfolios.
//   - Partners:  top-level entities who manage agent networks.
//
// API routes:
//
//	POST/GET       /api/v1/admins
//	GET/PUT        /api/v1/admins/{id}
//	POST/GET       /api/v1/merchants
//	GET/PUT        /api/v1/merchants/{id}
//	POST           /api/v1/merchants/{id}/revoke-key
//	POST/GET       /api/v1/agents
//	GET/PUT        /api/v1/agents/{id}
//	POST/GET       /api/v1/partners
//	GET/PUT        /api/v1/partners/{id}
//	GET            /health
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
	"github.com/farritpcz/richpayment/pkg/database"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/middleware"
	"github.com/farritpcz/richpayment/services/user/internal/handler"
	"github.com/farritpcz/richpayment/services/user/internal/repository"
	"github.com/farritpcz/richpayment/services/user/internal/service"
)

func main() {
	// Obtain the default structured JSON logger for the service.
	log := logger.Default()
	log.Info("starting user-service")

	// ------------------------------------------------------------------
	// Load configuration from environment variables with sensible defaults.
	// ------------------------------------------------------------------
	port := config.Get("USER_PORT", "8082")

	// ------------------------------------------------------------------
	// Construct the repository layer.
	// หากมี DATABASE_URL จะใช้ PostgreSQL-backed implementation จริง
	// ไม่มีก็ fallback ใช้ in-memory stub สำหรับ development
	// ------------------------------------------------------------------
	var userRepo repository.UserRepository

	dbDSN := config.Get("DATABASE_URL", "")
	if dbDSN != "" {
		// เชื่อมต่อ PostgreSQL ด้วย connection pool (max 20 connections)
		pool, err := database.NewPostgresPool(context.Background(), dbDSN, 20)
		if err != nil {
			log.Error("failed to connect to PostgreSQL", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		log.Info("connected to PostgreSQL for user-service")
		userRepo = repository.NewPostgresUserRepo(pool)
	} else {
		log.Warn("DATABASE_URL not set, using in-memory stub repository")
		userRepo = repository.NewStubUserRepo()
	}

	// ------------------------------------------------------------------
	// Construct the service layer.
	// Each service handles the business logic for one entity type and
	// shares the same repository instance.
	// ------------------------------------------------------------------

	// adminSvc manages admin CRUD operations including password hashing.
	adminSvc := service.NewAdminService(userRepo)

	// merchantSvc manages merchant CRUD, API key generation, and rotation.
	merchantSvc := service.NewMerchantService(userRepo)

	// agentSvc manages agent CRUD operations.
	agentSvc := service.NewAgentService(userRepo)

	// partnerSvc manages partner CRUD operations.
	partnerSvc := service.NewPartnerService(userRepo)

	// ------------------------------------------------------------------
	// Construct the HTTP handler and register routes.
	// ------------------------------------------------------------------

	// userHandler wires together all four services and exposes them
	// as HTTP endpoints following RESTful conventions.
	userHandler := handler.NewUserHandler(adminSvc, merchantSvc, agentSvc, partnerSvc)

	// Build the HTTP router using the standard library ServeMux.
	mux := http.NewServeMux()

	// Register all CRUD routes for admins, merchants, agents, and partners.
	userHandler.Register(mux)

	// Health check endpoint — returns 200 OK with a JSON body.
	// Used by load balancers and orchestrators (e.g. Kubernetes) to verify
	// the service is alive and ready to accept traffic.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "user-service",
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

	log.Info("user-service stopped")
}
