// Package main is the entry point for the order-service. It initialises all
// infrastructure connections (PostgreSQL, Redis), constructs the service and
// repository layers, starts the background expiry worker, and launches an
// HTTP server on port 8083.
//
// The service exposes a simple JSON/HTTP API for deposit order operations:
//
//	POST   /api/v1/deposits          - Create a new deposit order
//	GET    /api/v1/deposits/{id}     - Retrieve an existing deposit order
//	POST   /api/v1/deposits/match    - Match an SMS notification to an order
//	POST   /api/v1/deposits/{id}/complete - Complete a matched deposit
//	GET    /health                   - Health check endpoint
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
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/config"
	"github.com/farritpcz/richpayment/pkg/database"
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/pkg/middleware"
	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/order/internal/repository"
	"github.com/farritpcz/richpayment/services/order/internal/service"
)

func main() {
	// Obtain the default structured JSON logger for the service.
	log := logger.Default()
	log.Info("starting order-service")

	// ------------------------------------------------------------------
	// Load configuration from environment variables with sensible defaults.
	// ------------------------------------------------------------------
	dbCfg := config.LoadDatabaseConfig()
	redisCfg := config.LoadRedisConfig()
	port := config.Get("ORDER_PORT", "8083")

	// orderExpiry controls how long a deposit order remains pending before
	// the timeout worker marks it as expired. Default: 15 minutes.
	orderExpiry := config.GetDuration("ORDER_EXPIRY", 15*time.Minute)

	// feePercent is the merchant deposit fee (e.g. "0.02" = 2%).
	feePercentStr := config.Get("DEPOSIT_FEE_PERCENT", "0.02")
	feePercent, err := decimal.NewFromString(feePercentStr)
	if err != nil {
		log.Error("invalid DEPOSIT_FEE_PERCENT", "value", feePercentStr, "error", err)
		os.Exit(1)
	}

	// matchStrategy controls the order-matching algorithm.
	// "unique_amount" (default) adjusts amounts for deterministic matching.
	// "time_based" matches by amount and picks the oldest pending order.
	matchStrategy := config.Get("MATCH_STRATEGY", "unique_amount")

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
	// Establish Redis connection.
	// ------------------------------------------------------------------
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisCfg.Addr(),
		Password: redisCfg.Password,
		DB:       redisCfg.DB,
	})

	// Verify Redis is reachable; log a warning but do not crash if it is
	// temporarily unavailable (it may come up after the service starts).
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Warn("redis not reachable at startup, continuing anyway", "error", err)
	} else {
		log.Info("connected to Redis")
	}

	// ------------------------------------------------------------------
	// Construct the repository and service layers.
	// ------------------------------------------------------------------

	// orderRepo is the PostgreSQL-backed implementation of OrderRepository.
	orderRepo := repository.NewPostgresOrderRepo(pgPool)

	// depositSvc handles deposit order creation, retrieval, and completion.
	depositSvc := service.NewDepositService(
		orderRepo, rdb, orderExpiry, feePercent, matchStrategy,
	)

	// matcherSvc handles pairing incoming bank notifications with pending orders.
	matcherSvc := service.NewMatcherService(orderRepo, rdb, matchStrategy)

	// ------------------------------------------------------------------
	// Start the background expiry worker.
	// It polls every 10 seconds for orders that have exceeded their deadline.
	// ------------------------------------------------------------------
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	expiryWorker := service.NewExpiryWorker(orderRepo, rdb, 10*time.Second)
	expiryWorker.StartExpiryWorker(appCtx)

	// ------------------------------------------------------------------
	// Build HTTP router using the standard library ServeMux.
	// ------------------------------------------------------------------
	mux := http.NewServeMux()

	// Health check endpoint — returns 200 OK with a JSON body.
	// Used by load balancers and orchestrators (e.g. Kubernetes) to verify
	// the service is alive and ready to accept traffic.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "order-service"})
	})

	// POST /api/v1/deposits — Create a new deposit order.
	// Accepts a JSON body with merchant_id, merchant_order_id, amount,
	// customer_name, and customer_bank fields. Returns the created order.
	mux.HandleFunc("POST /api/v1/deposits", func(w http.ResponseWriter, r *http.Request) {
		// Decode the incoming JSON request body into the create request struct.
		var req createDepositRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		// Parse the merchant UUID from the request.
		merchantID, err := uuid.Parse(req.MerchantID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid merchant_id"})
			return
		}

		// Parse the deposit amount as a decimal for precise monetary arithmetic.
		amount, err := decimal.NewFromString(req.Amount)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
			return
		}

		// Delegate to the deposit service to execute the full creation flow.
		order, err := depositSvc.CreateDepositOrder(
			r.Context(), merchantID, req.MerchantOrderID,
			amount, req.CustomerName, req.CustomerBank,
		)
		if err != nil {
			log.Error("create deposit order failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		// Return the created order as JSON with 201 Created status.
		writeJSON(w, http.StatusCreated, order)
	})

	// GET /api/v1/deposits/{id} — Retrieve a deposit order by ID.
	// The order UUID is extracted from the URL path.
	mux.HandleFunc("GET /api/v1/deposits/", func(w http.ResponseWriter, r *http.Request) {
		// Extract the order ID from the URL path (everything after the last slash).
		idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/deposits/")
		if idStr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing order id"})
			return
		}

		// Parse the order UUID.
		orderID, err := uuid.Parse(idStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid order id"})
			return
		}

		// Fetch the order from the service layer.
		order, err := depositSvc.GetDepositOrder(r.Context(), orderID)
		if err != nil {
			log.Error("get deposit order failed", "error", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found"})
			return
		}

		writeJSON(w, http.StatusOK, order)
	})

	// POST /api/v1/deposits/match — Match an SMS notification to an order.
	// Called by the parser-service when a bank SMS is received and parsed.
	mux.HandleFunc("POST /api/v1/deposits/match", func(w http.ResponseWriter, r *http.Request) {
		// Decode the match request JSON body.
		var req matchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		// Parse the bank account UUID.
		bankAccountID, err := uuid.Parse(req.BankAccountID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bank_account_id"})
			return
		}

		// Parse the amount from the SMS notification.
		amount, err := decimal.NewFromString(req.Amount)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
			return
		}

		// Parse the SMS timestamp (RFC3339 format).
		smsTimestamp, err := time.Parse(time.RFC3339, req.SMSTimestamp)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sms_timestamp"})
			return
		}

		// Attempt to match the SMS to a pending order.
		orderID, err := matcherSvc.MatchSMSToOrder(r.Context(), bankAccountID, amount, smsTimestamp)
		if err != nil {
			log.Warn("SMS match failed", "error", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no matching order found"})
			return
		}

		// Return the matched order ID.
		writeJSON(w, http.StatusOK, map[string]string{"order_id": orderID.String()})
	})

	// POST /api/v1/deposits/{id}/complete — Complete a matched deposit order.
	// Called after a successful match to finalise the deposit and trigger
	// settlement (fee calculation, wallet credit, webhook).
	mux.HandleFunc("POST /api/v1/deposits/complete", func(w http.ResponseWriter, r *http.Request) {
		// Decode the complete request JSON body.
		var req completeDepositRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		// Parse the order UUID.
		orderID, err := uuid.Parse(req.OrderID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid order_id"})
			return
		}

		// Parse the actual amount from the bank notification.
		actualAmount, err := decimal.NewFromString(req.ActualAmount)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid actual_amount"})
			return
		}

		// Delegate completion to the deposit service.
		if err := depositSvc.CompleteDeposit(r.Context(), orderID, models.MatchedBy(req.MatchedBy), actualAmount); err != nil {
			log.Error("complete deposit failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "completed"})
	})

	// ------------------------------------------------------------------
	// Apply middleware and create the HTTP server.
	// ------------------------------------------------------------------

	// Wrap the mux with the panic recovery middleware so that unexpected
	// panics in handlers do not crash the entire service.
	handler := middleware.Recovery(log)(mux)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
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

	// Cancel the application context to stop background workers (expiry worker).
	appCancel()

	// Allow up to 10 seconds for in-flight HTTP requests to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", "error", err)
	}

	// Close infrastructure connections.
	_ = rdb.Close()
	pgPool.Close()

	log.Info("order-service stopped")
}

// ---------------------------------------------------------------------------
// Request and response types for the HTTP API.
// ---------------------------------------------------------------------------

// createDepositRequest is the JSON body for POST /api/v1/deposits.
// It contains all the information needed to initiate a new deposit order.
type createDepositRequest struct {
	// MerchantID is the UUID of the merchant requesting the deposit.
	MerchantID string `json:"merchant_id"`

	// MerchantOrderID is the merchant's own reference ID for reconciliation.
	MerchantOrderID string `json:"merchant_order_id"`

	// Amount is the requested deposit amount in THB (as a decimal string).
	Amount string `json:"amount"`

	// CustomerName is the depositing customer's display name.
	CustomerName string `json:"customer_name"`

	// CustomerBank is the bank code of the customer's originating bank.
	CustomerBank string `json:"customer_bank"`
}

// matchRequest is the JSON body for POST /api/v1/deposits/match.
// It represents an incoming bank notification that needs to be paired
// with a pending deposit order.
type matchRequest struct {
	// BankAccountID is the UUID of the bank account that received the transfer.
	BankAccountID string `json:"bank_account_id"`

	// Amount is the transferred amount extracted from the SMS (decimal string).
	Amount string `json:"amount"`

	// SMSTimestamp is the time the SMS was received, in RFC3339 format.
	SMSTimestamp string `json:"sms_timestamp"`
}

// completeDepositRequest is the JSON body for POST /api/v1/deposits/complete.
// It provides the details needed to finalise a matched deposit order.
type completeDepositRequest struct {
	// OrderID is the UUID of the deposit order to complete.
	OrderID string `json:"order_id"`

	// MatchedBy indicates how the order was matched (e.g. "sms", "email", "slip").
	MatchedBy string `json:"matched_by"`

	// ActualAmount is the actual transfer amount from the bank notification.
	ActualAmount string `json:"actual_amount"`
}

// writeJSON is a helper that serialises a value as JSON and writes it to the
// HTTP response with the given status code. It sets the Content-Type header
// to application/json. Encoding errors are silently ignored because the
// response has already been partially written at that point.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
