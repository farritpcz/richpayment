// Package main is the entry point for the notification service. It wires
// together all dependencies (Redis, Telegram, HTTP server, retry worker)
// and starts the HTTP server on port 8087.
//
// The notification service is responsible for:
//   - Delivering signed webhooks to merchant endpoints with automatic retry
//   - Sending security and operational alerts via Telegram
//   - Running a background retry worker for failed webhook deliveries
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
	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/services/notification/internal/handler"
	"github.com/farritpcz/richpayment/services/notification/internal/service"
)

func main() {
	// Initialise the structured JSON logger that all components will share.
	log := logger.Default()
	log.Info("starting notification service")

	// ---------------------------------------------------------------
	// Load configuration from environment variables.
	// ---------------------------------------------------------------

	// Redis configuration for retry queue state management.
	redisCfg := config.LoadRedisConfig()

	// HTTP server port; defaults to 8087 for the notification service.
	port := config.Get("NOTIFICATION_PORT", "8087")

	// Telegram Bot API token obtained from @BotFather. If empty, Telegram
	// alerts will be logged locally instead of sent.
	telegramBotToken := config.Get("TELEGRAM_BOT_TOKEN", "")

	// Telegram chat ID for the admin security alert group. All security
	// events are delivered to this chat.
	telegramAdminChatID := config.Get("TELEGRAM_ADMIN_CHAT_ID", "")

	// ---------------------------------------------------------------
	// Connect to Redis.
	// ---------------------------------------------------------------

	// Redis is used for:
	//   1. Storing webhook retry payloads (key-value)
	//   2. Maintaining the retry sorted set (scheduled queue)
	//   3. Recording delivery/exhaustion audit logs
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisCfg.Addr(),
		Password: redisCfg.Password,
		DB:       redisCfg.DB,
	})

	// Verify Redis connectivity at startup. We warn but do not abort
	// because Redis may become available shortly after the service starts
	// (e.g. during container orchestration).
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Warn("redis not reachable at startup, continuing anyway",
			"error", err,
		)
	}

	// ---------------------------------------------------------------
	// Initialise services.
	// ---------------------------------------------------------------

	// TelegramService handles all Telegram Bot API interactions.
	telegramSvc := service.NewTelegramService(telegramBotToken, telegramAdminChatID, log)

	// WebhookService handles signed webhook delivery with retry semantics.
	webhookSvc := service.NewWebhookService(rdb, telegramSvc, log)

	// ---------------------------------------------------------------
	// Start the background retry worker.
	// ---------------------------------------------------------------

	// Create a cancellable context for the retry worker. Cancelling this
	// context will gracefully stop the worker during shutdown.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	// The retry worker polls the Redis sorted set every 5 seconds for
	// webhooks that are ready for re-delivery.
	retryWorker := service.NewRetryWorker(rdb, webhookSvc, log)
	retryWorker.StartRetryWorker(workerCtx)

	// ---------------------------------------------------------------
	// Set up HTTP routes.
	// ---------------------------------------------------------------

	// Create the notification handler that translates HTTP requests into
	// service calls.
	notifHandler := handler.NewNotificationHandler(webhookSvc, telegramSvc, log)

	// Build the HTTP router using Go 1.22+ enhanced ServeMux patterns.
	mux := http.NewServeMux()

	// POST /internal/webhook/send - Trigger webhook delivery to a merchant.
	// This is an internal endpoint called by other RichPayment services
	// (e.g. order service) when a payment event needs to be notified.
	mux.HandleFunc("POST /internal/webhook/send", notifHandler.SendWebhook)

	// POST /internal/alert/send - Send a Telegram alert (plain or security).
	// This is an internal endpoint for other services to trigger alerts.
	mux.HandleFunc("POST /internal/alert/send", notifHandler.SendAlert)

	// GET /healthz - Health check endpoint for load balancers and
	// container orchestrators (e.g. Kubernetes liveness probes).
	mux.HandleFunc("GET /healthz", notifHandler.Healthz)

	// Wrap the mux with request logging middleware for observability.
	var topHandler http.Handler = mux
	topHandler = logRequests(log, topHandler)

	// ---------------------------------------------------------------
	// Create and start the HTTP server.
	// ---------------------------------------------------------------

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: topHandler,

		// ReadTimeout limits how long the server waits for the client to
		// send the full request (headers + body).
		ReadTimeout: 15 * time.Second,

		// WriteTimeout limits how long the server waits for the handler
		// to produce a response.
		WriteTimeout: 15 * time.Second,

		// IdleTimeout limits how long keep-alive connections are held open
		// between requests.
		IdleTimeout: 60 * time.Second,
	}

	// Start the HTTP server in a background goroutine so the main
	// goroutine can wait for shutdown signals.
	go func() {
		log.Info("listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// ---------------------------------------------------------------
	// Wait for shutdown signal (SIGINT or SIGTERM).
	// ---------------------------------------------------------------

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("shutting down", "signal", sig.String())

	// Cancel the retry worker context first so it stops polling Redis
	// before we close the Redis connection.
	workerCancel()

	// Give the HTTP server up to 10 seconds to finish in-flight requests
	// before forcefully closing connections.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", "error", err)
	}

	// Close the Redis connection pool.
	_ = rdb.Close()

	log.Info("notification service stopped")
}

// logRequests is a simple HTTP middleware that logs every request with its
// method, path, duration, and remote address. This provides basic
// observability without requiring a full tracing framework.
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
