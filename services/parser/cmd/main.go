// Package main is the entry point for the parser-service.
//
// The parser-service is responsible for receiving bank SMS/Email notifications
// via webhooks, parsing them using bank-specific plugins, persisting the raw
// messages for audit, and matching them against pending deposit orders.
//
// Architecture overview:
//   - HTTP server on port 8086 (configurable via PARSER_PORT env var).
//   - POST /internal/sms  - Receives SMS webhooks from the SMS gateway.
//   - GET  /healthz       - Health check for load balancers and Kubernetes.
//   - Bank parser plugins are auto-registered via init() functions.
//   - PostgreSQL stores SMS records (sms_messages table).
//   - Redis is used for fast pending-order lookups.
//
// The service uses graceful shutdown: on SIGINT/SIGTERM it stops accepting
// new requests and waits up to 10 seconds for in-flight requests to complete.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/farritpcz/richpayment/pkg/config"
	"github.com/farritpcz/richpayment/pkg/logger"

	// Import the banks package to trigger init() registration of all bank
	// parsers. Without this blank import, no parsers would be available.
	_ "github.com/farritpcz/richpayment/services/parser/internal/banks"

	"github.com/farritpcz/richpayment/services/parser/internal/handler"
	"github.com/farritpcz/richpayment/services/parser/internal/repository"
	"github.com/farritpcz/richpayment/services/parser/internal/service"
)

func main() {
	// Initialise the structured logger. All log output is JSON-formatted
	// for compatibility with log aggregation systems (ELK, Loki, etc.).
	log := logger.Default()
	log.Info("starting parser-service")

	// --- Load configuration from environment variables ---
	// Database and Redis configs use shared helpers from the pkg/config package.
	dbCfg := config.LoadDatabaseConfig()
	redisCfg := config.LoadRedisConfig()
	port := config.Get("PARSER_PORT", "8086")

	// --- Connect to PostgreSQL ---
	// The connection pool is shared across all request handlers. We use a
	// short timeout for the initial connection to fail fast during deployment
	// if the database is unreachable.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pgPool, err := pgxpool.New(ctx, dbCfg.DSN())
	if err != nil {
		log.Error("failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	// Verify the database connection is alive.
	if err := pgPool.Ping(ctx); err != nil {
		log.Error("PostgreSQL ping failed", "error", err)
		os.Exit(1)
	}
	log.Info("connected to PostgreSQL")

	// --- Connect to Redis ---
	// Redis is used for fast lookups of pending deposit orders. If Redis
	// is temporarily down, SMS processing degrades gracefully: messages are
	// stored but marked as unmatched.
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisCfg.Addr(),
		Password: redisCfg.Password,
		DB:       redisCfg.DB,
		PoolSize: 20,
	})

	// Verify Redis connection. We log a warning instead of exiting because
	// the parser can still store SMS records without Redis.
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Warn("redis not reachable at startup, continuing anyway", "error", err)
	} else {
		log.Info("connected to Redis")
	}
	defer rdb.Close()

	// --- Build repositories ---
	// The SMS repository handles PostgreSQL persistence.
	// The order matcher handles Redis lookups for pending orders.
	smsRepo := repository.NewPostgresSMSRepository(pgPool)
	orderMatcher := repository.NewRedisOrderMatcher(rdb)

	// --- Configure bank account mappings ---
	// In production these mappings would be loaded from the database or a
	// configuration service. Each mapping associates an SMS sender number
	// with the internal bank account UUID that receives transfers.
	//
	// TODO: Replace these placeholder UUIDs with real values from the
	// bank_accounts table, or load them dynamically at startup.
	bankAccountMappings := []service.BankAccountMapping{
		{
			SenderNumber:  "+66868882888",
			BankAccountID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		},
		{
			SenderNumber:  "KBANK",
			BankAccountID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		},
		{
			SenderNumber:  "KBank",
			BankAccountID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		},
		{
			SenderNumber:  "+66218951111",
			BankAccountID: uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		},
		{
			SenderNumber:  "SCB",
			BankAccountID: uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		},
		{
			SenderNumber:  "SCBeasy",
			BankAccountID: uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		},
	}

	// --- Configure anti-spoofing security ---
	// Load anti-spoofing confidence scoring configuration with production defaults.
	// Score >= 80: auto-approve with 30s verification delay
	// Score 50-79: delay 60s then approve
	// Score < 50:  require manual admin approval
	// Deposits >= 50,000 THB: always require manual admin approval
	antiSpoofCfg := service.DefaultAntiSpoofConfig()

	// --- Configure anomaly detection ---
	// Load anomaly detection configuration with production defaults.
	// Rate limit: 10 SMS per bank account per minute.
	// Telegram alerts are optional — set TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID
	// environment variables to enable admin alerting.
	anomalyCfg := service.DefaultAnomalyConfig()
	anomalyCfg.TelegramBotToken = config.Get("TELEGRAM_BOT_TOKEN", "")
	anomalyCfg.TelegramChatID = config.Get("TELEGRAM_CHAT_ID", "")

	// --- Build the service layer ---
	// The parser service now includes anti-spoofing confidence scoring,
	// duplicate SMS detection, and SMS rate anomaly monitoring.
	parserSvc := service.NewParserService(
		smsRepo,
		orderMatcher,
		bankAccountMappings,
		log,
		rdb,
		antiSpoofCfg,
		anomalyCfg,
	)

	// --- Build HTTP handlers ---
	smsHandler := handler.NewSMSHandler(parserSvc, log)

	// --- Configure HTTP routes ---
	// We use the standard library's ServeMux. The parser-service has a
	// small API surface (two endpoints) so a third-party router is overkill.
	mux := http.NewServeMux()

	// POST /internal/sms - SMS webhook endpoint.
	// Called by the SMS gateway when a new bank notification arrives.
	mux.HandleFunc("/internal/sms", smsHandler.ReceiveSMS)

	// GET /healthz - Health check endpoint.
	// Used by Kubernetes probes and load balancers.
	mux.HandleFunc("/healthz", smsHandler.Health)

	// --- Create and start HTTP server ---
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,

		// ReadTimeout limits how long the server waits to read the full
		// request (headers + body). 15 seconds is generous for small
		// JSON webhook payloads.
		ReadTimeout: 15 * time.Second,

		// WriteTimeout limits how long the server waits for the handler
		// to write the response. This prevents slow clients from holding
		// connections indefinitely.
		WriteTimeout: 15 * time.Second,

		// IdleTimeout controls how long keep-alive connections remain open
		// when idle. 60 seconds balances connection reuse with resource usage.
		IdleTimeout: 60 * time.Second,
	}

	// Start the HTTP server in a background goroutine. The main goroutine
	// blocks on signal handling below.
	go func() {
		log.Info("listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// --- Graceful shutdown ---
	// Wait for SIGINT (Ctrl+C) or SIGTERM (Docker/Kubernetes stop signal).
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("shutting down", "signal", sig.String())

	// Give in-flight requests up to 10 seconds to complete before forcing
	// the server to stop. This prevents data loss during rolling deployments.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", "error", err)
	}

	// Close database and Redis connections after the server has stopped
	// accepting new requests, ensuring no in-flight queries are interrupted.
	pgPool.Close()
	_ = rdb.Close()

	log.Info("parser-service stopped")
}
