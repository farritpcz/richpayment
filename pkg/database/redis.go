package database

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// NewRedisClient creates a new Redis client with a fixed pool size of 20
// connections and verifies connectivity with a PING command. It is used for
// caching, session storage, rate limiting, and the emergency-freeze flag.
//
// Parameters:
//   - ctx: context used for the initial PING health check.
//   - addr: Redis address in "host:port" format.
//   - password: Redis AUTH password (empty string for no auth).
//   - db: Redis database index (0-15).
func NewRedisClient(ctx context.Context, addr, password string, db int) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
		PoolSize: 20,
	})

	// Verify the Redis server is reachable before returning.
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return client, nil
}
