// Package router wires together all HTTP routes, handlers, and middleware for
// the gateway-api service into a single http.Handler ready to serve.
//
// INTER-SERVICE COMMUNICATION ARCHITECTURE:
// The gateway-api is the single public entry point for all merchant API calls.
// It does NOT contain any business logic or database access. Instead, it
// validates incoming requests and proxies them to the appropriate internal
// microservices:
//
//   Merchant -> gateway (:8080) -> order-service (:8083)       [deposits]
//                               -> withdrawal-service (:8085)  [withdrawals]
//                               -> wallet-service (:8084)      [balance queries]
//
// Each handler receives an httpclient.ServiceClient for its target service,
// injected through the router constructor. This allows the service URLs to be
// configured via environment variables (see cmd/main.go).
package router

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/farritpcz/richpayment/pkg/httpclient"
	"github.com/farritpcz/richpayment/services/gateway/internal/handler"
	"github.com/farritpcz/richpayment/services/gateway/internal/middleware"
)

// New creates and configures the HTTP router with all routes and middleware.
//
// Parameters:
//   - rdb: Redis client for rate limiting and freeze checks.
//   - logger: structured logger for request logging.
//   - orderClient: HTTP client for proxying deposit requests to order-service (:8083).
//   - walletClient: HTTP client for proxying balance queries to wallet-service (:8084).
//   - withdrawalClient: HTTP client for proxying withdrawal requests to withdrawal-service (:8085).
//
// The three service clients enable the gateway to forward requests to the
// appropriate backend services without containing any business logic itself.
func New(
	rdb *redis.Client,
	logger *slog.Logger,
	orderClient *httpclient.ServiceClient,
	walletClient *httpclient.ServiceClient,
	withdrawalClient *httpclient.ServiceClient,
) http.Handler {
	mux := http.NewServeMux()

	// --- Handlers ---
	// Each handler receives the HTTP client for its target internal service.
	// This dependency injection pattern makes it easy to swap service URLs
	// between environments (dev, staging, production) via environment variables.
	healthH := handler.NewHealthHandler()
	depositH := handler.NewDepositHandler(orderClient)          // Proxies to order-service (:8083)
	withdrawalH := handler.NewWithdrawalHandler(withdrawalClient) // Proxies to withdrawal-service (:8085)
	walletH := handler.NewWalletHandler(walletClient)            // Proxies to wallet-service (:8084)

	// --- Public routes ---
	mux.HandleFunc("GET /healthz", healthH.Health)

	// --- API v1 routes (protected by API key + signature) ---
	// These routes are the merchant-facing API. Each route validates the
	// request and forwards it to the corresponding internal service.
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("POST /api/v1/deposits", depositH.Create)       // -> order-service
	apiMux.HandleFunc("GET /api/v1/deposits/{id}", depositH.Get)       // -> order-service
	apiMux.HandleFunc("POST /api/v1/withdrawals", withdrawalH.Create)  // -> withdrawal-service
	apiMux.HandleFunc("GET /api/v1/withdrawals/{id}", withdrawalH.Get) // -> withdrawal-service
	apiMux.HandleFunc("GET /api/v1/wallet/balance", walletH.Balance)   // -> wallet-service

	// --- Middleware stack for API routes ---
	apiKeyAuth := middleware.NewAPIKeyAuth(logger)
	freezeCheck := middleware.NewFreezeCheck(rdb, logger)
	rateLimiter := middleware.NewRateLimiter(rdb, 100, 1*time.Minute, logger)

	// Chain: rate limit -> freeze check -> API key auth -> handler
	apiHandler := rateLimiter.Middleware(
		freezeCheck.Middleware(
			apiKeyAuth.Middleware(apiMux),
		),
	)

	mux.Handle("/api/", apiHandler)

	// --- Top-level middleware (applies to all routes) ---
	var top http.Handler = mux
	top = logRequests(logger, top)

	return top
}

// logRequests is a simple request logging middleware.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}
