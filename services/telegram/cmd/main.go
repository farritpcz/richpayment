// Package main is the entry point for the telegram-service. It initialises
// all dependencies (EasySlip client, repositories, services), starts the
// Telegram bot long-polling loop in a background goroutine, and launches
// an HTTP server on port 8090 for the internal API (webhook, alerts, health).
//
// The service exposes these internal HTTP endpoints:
//
//   POST /internal/telegram/webhook  - Receive Telegram updates via webhook
//   POST /internal/telegram/alert    - Send a security alert to Telegram
//   GET  /healthz                    - Health check for load balancers/k8s
//
// Graceful shutdown is handled via OS signals (SIGINT / SIGTERM). When a
// shutdown signal is received, the bot polling loop is cancelled, in-flight
// HTTP requests are drained, and the service exits cleanly.
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
	"github.com/farritpcz/richpayment/services/telegram/internal/easyslip"
	"github.com/farritpcz/richpayment/services/telegram/internal/handler"
	"github.com/farritpcz/richpayment/services/telegram/internal/repository"
	"github.com/farritpcz/richpayment/services/telegram/internal/service"
)

func main() {
	// Obtain the default structured JSON logger for the service.
	log := logger.Default()
	log.Info("starting telegram-service")

	// ------------------------------------------------------------------
	// Load configuration from environment variables.
	// ------------------------------------------------------------------

	// port is the HTTP server listen port. Defaults to 8090.
	port := config.Get("TELEGRAM_PORT", "8090")

	// telegramToken is the Telegram Bot API token used to authenticate
	// all bot API calls (polling, sending messages, downloading files).
	// This is a required configuration — the service will start but the
	// bot will not function without it.
	telegramToken := config.Get("TELEGRAM_BOT_TOKEN", "")
	if telegramToken == "" {
		log.Warn("TELEGRAM_BOT_TOKEN is not set; bot polling will not work")
	}

	// easySlipAPIKey is the API key for the EasySlip slip verification
	// service. Required for slip photo verification.
	easySlipAPIKey := config.Get("EASYSLIP_API_KEY", "")
	if easySlipAPIKey == "" {
		log.Warn("EASYSLIP_API_KEY is not set; slip verification will not work")
	}

	// alertChatID is the Telegram chat ID of the security alert channel.
	// All security alerts are sent to this channel. Defaults to 0 (disabled).
	alertChatID := int64(config.GetInt("TELEGRAM_ALERT_CHAT_ID", 0))
	if alertChatID == 0 {
		log.Warn("TELEGRAM_ALERT_CHAT_ID is not set; security alerts will not be sent")
	}

	// botPollingEnabled controls whether the bot starts long-polling for
	// Telegram updates. Set to "false" if using webhook mode instead.
	botPollingEnabled := config.Get("BOT_POLLING_ENABLED", "true")

	// ------------------------------------------------------------------
	// Construct infrastructure clients.
	// ------------------------------------------------------------------

	// easySlipClient is the HTTP client for the EasySlip API. It handles
	// slip image verification and returns parsed transaction data.
	easySlipClient := easyslip.NewClient(easySlipAPIKey)

	// ------------------------------------------------------------------
	// Construct repositories.
	// ------------------------------------------------------------------

	// slipRepo is the persistence layer for slip verification records.
	// Using a stub implementation for development; replace with PostgreSQL
	// implementation in production.
	slipRepo := repository.NewStubSlipRepository()

	// ------------------------------------------------------------------
	// Construct service layer.
	// ------------------------------------------------------------------

	// slipSvc orchestrates the slip verification pipeline: hashing,
	// duplicate detection, EasySlip API calls, and result storage.
	slipSvc := service.NewSlipService(easySlipClient, slipRepo)

	// alertSvc sends formatted security alert messages to the designated
	// Telegram alert channel.
	alertSvc := service.NewAlertService(telegramToken, alertChatID)

	// botSvc manages the Telegram bot lifecycle: polling for updates,
	// routing them to handlers, downloading photos, and sending replies.
	botSvc := service.NewBotService(telegramToken, slipSvc, alertSvc)

	// ------------------------------------------------------------------
	// Start the Telegram bot polling loop in a background goroutine.
	// The loop runs until appCtx is cancelled during shutdown.
	// ------------------------------------------------------------------
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	if botPollingEnabled == "true" && telegramToken != "" {
		go botSvc.StartBot(appCtx)
		log.Info("telegram bot polling started")
	} else {
		log.Info("telegram bot polling disabled (using webhook mode or token not set)")
	}

	// ------------------------------------------------------------------
	// Build HTTP router using the standard library ServeMux.
	// ------------------------------------------------------------------
	mux := http.NewServeMux()

	// Register the telegram handler routes (webhook, alert, healthz).
	telegramHandler := handler.NewTelegramHandler(botSvc, alertSvc)
	telegramHandler.RegisterRoutes(mux)

	// Fallback health endpoint at /health for backward compatibility
	// with older monitoring configurations.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "telegram-service",
		})
	})

	// ------------------------------------------------------------------
	// Apply middleware and create the HTTP server.
	// ------------------------------------------------------------------

	// Wrap the mux with the panic recovery middleware so that unexpected
	// panics in handlers do not crash the entire service.
	httpHandler := middleware.Recovery(log)(mux)

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

	// Cancel the application context to stop the bot polling loop and
	// any other background goroutines.
	appCancel()

	// Allow up to 10 seconds for in-flight HTTP requests to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", "error", err)
	}

	log.Info("telegram-service stopped")
}
