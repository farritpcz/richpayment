// Package database provides factory functions for creating pre-configured
// database and cache clients (PostgreSQL and Redis) used by all RichPayment
// microservices.
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPostgresPool creates a new pgxpool.Pool with sensible default connection
// pool settings. It parses the DSN, applies pool limits, and verifies
// connectivity with a Ping before returning. The caller is responsible for
// closing the pool when it is no longer needed.
//
// Parameters:
//   - ctx: context used for the initial connection and ping.
//   - dsn: a PostgreSQL connection string (e.g. "postgres://user:pass@host:5432/db").
//   - maxConns: the upper bound on open connections in the pool.
func NewPostgresPool(ctx context.Context, dsn string, maxConns int) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Pool tuning: keep a minimum of 5 connections warm, recycle connections
	// after 30 minutes to avoid stale server-side state, and run periodic
	// health checks to evict broken connections proactively.
	config.MaxConns = int32(maxConns)
	config.MinConns = 5
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	// Verify the database is reachable before returning the pool.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}
