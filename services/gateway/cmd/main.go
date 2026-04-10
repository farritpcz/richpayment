// Package main is the entrypoint for the gateway-api service binary. It
// initialises configuration, connects to Redis, builds the HTTP router with
// all middleware and routes, and starts a graceful-shutdown-aware HTTP server.
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
	"github.com/farritpcz/richpayment/services/gateway/internal/router"
)

// main initialises all dependencies and starts the gateway-api HTTP server.
func main() {
	log := logger.Default()
	log.Info("starting gateway-api service")

	// Load configuration.
	redisCfg := config.LoadRedisConfig()
	port := config.Get("GATEWAY_PORT", "8080")

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

	// Build router.
	handler := router.New(rdb, log)

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
