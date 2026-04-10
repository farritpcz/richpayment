package router

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/farritpcz/richpayment/services/gateway/internal/handler"
	"github.com/farritpcz/richpayment/services/gateway/internal/middleware"
)

// New creates and configures the HTTP router with all routes and middleware.
func New(rdb *redis.Client, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// --- Handlers ---
	healthH := handler.NewHealthHandler()
	depositH := handler.NewDepositHandler()
	withdrawalH := handler.NewWithdrawalHandler()
	walletH := handler.NewWalletHandler()

	// --- Public routes ---
	mux.HandleFunc("GET /healthz", healthH.Health)

	// --- API v1 routes (protected by API key + signature) ---
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("POST /api/v1/deposits", depositH.Create)
	apiMux.HandleFunc("GET /api/v1/deposits/{id}", depositH.Get)
	apiMux.HandleFunc("POST /api/v1/withdrawals", withdrawalH.Create)
	apiMux.HandleFunc("GET /api/v1/withdrawals/{id}", withdrawalH.Get)
	apiMux.HandleFunc("GET /api/v1/wallet/balance", walletH.Balance)

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
