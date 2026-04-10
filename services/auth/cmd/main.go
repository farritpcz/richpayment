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

	"github.com/redis/go-redis/v9"

	"github.com/farritpcz/richpayment/services/auth/internal/handler"
	"github.com/farritpcz/richpayment/services/auth/internal/repository"
	"github.com/farritpcz/richpayment/services/auth/internal/service"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Redis client.
	redisAddr := envOrDefault("REDIS_ADDR", "localhost:6379")
	redisPassword := envOrDefault("REDIS_PASSWORD", "")
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       0,
		PoolSize: 20,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Warn("redis not reachable at startup, will retry on demand", "addr", redisAddr, "err", err)
	}
	defer rdb.Close()

	// Stub repository (replace with Postgres-backed implementation later).
	repo := repository.NewStubRepository()

	// Services.
	totpSvc := service.NewTOTPService()
	authSvc := service.NewAuthService(repo, rdb, totpSvc)

	// HTTP handler.
	authHandler := handler.NewAuthHandler(authSvc)
	mux := http.NewServeMux()
	authHandler.RegisterRoutes(mux)

	// Health check.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"status":"ok"}`)
	})

	port := envOrDefault("PORT", "8081")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Info("received signal, shutting down", "signal", sig)
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("server shutdown error", "err", err)
		}
	}()

	log.Info("auth-service starting", "port", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
	log.Info("auth-service stopped")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
