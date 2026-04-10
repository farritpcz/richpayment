// Package database provides factory functions for creating pre-configured
// database and cache clients (PostgreSQL and Redis) used by all RichPayment
// microservices. This file contains the Redis client factory and distributed
// lock helpers used to prevent race conditions in concurrent wallet operations.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// -------------------------------------------------------------------------
// Redis client factory
// -------------------------------------------------------------------------

// NewRedisClient creates a new Redis client with a fixed pool size of 20
// connections and verifies connectivity with a PING command. It is used for
// caching, session storage, rate limiting, the emergency-freeze flag, and
// distributed locking for wallet balance operations.
//
// Parameters:
//   - ctx: context used for the initial PING health check.
//   - addr: Redis address in "host:port" format (e.g. "localhost:6379").
//   - password: Redis AUTH password (empty string for no auth).
//   - db: Redis database index (0-15).
//
// Returns:
//   - *redis.Client: a ready-to-use Redis client with a verified connection.
//   - error: non-nil if the connection or health check fails.
func NewRedisClient(ctx context.Context, addr, password string, db int) (*redis.Client, error) {
	// Create the client with connection pooling enabled.
	// PoolSize=20 provides enough concurrency for typical wallet-service load
	// without exhausting the Redis server's file descriptor limit.
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
		PoolSize: 20,
	})

	// Verify the Redis server is reachable before returning.
	// This catches misconfigurations (wrong host, wrong password) at startup
	// rather than at the first request, which is much easier to debug.
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return client, nil
}

// -------------------------------------------------------------------------
// Distributed lock constants
// -------------------------------------------------------------------------

// DefaultLockTTL is the default time-to-live for a distributed lock key.
// After this duration, the lock is automatically released even if the holder
// has not explicitly released it. This prevents deadlocks caused by crashed
// processes that never call ReleaseLock.
//
// 10 seconds is chosen because wallet operations (SELECT FOR UPDATE +
// UPDATE + INSERT ledger) typically complete in under 100ms, so 10s gives
// a 100x safety margin while still recovering quickly from crashes.
const DefaultLockTTL = 10 * time.Second

// ErrLockNotAcquired is returned when AcquireLock fails to obtain the
// distributed lock because another process currently holds it. Callers
// should retry after a short back-off or return a "too many requests" error
// to the client.
var ErrLockNotAcquired = errors.New("distributed lock: failed to acquire lock, another process holds it")

// -------------------------------------------------------------------------
// AcquireLock — distributed lock via Redis SETNX
// -------------------------------------------------------------------------

// AcquireLock attempts to acquire a distributed lock for the given key using
// the Redis SET NX (set-if-not-exists) command with a TTL. This implements
// the "single-instance Redis lock" pattern described in the Redis documentation.
//
// The lock key is set to a caller-provided value (typically a request ID or
// UUID) so that only the holder can release it, preventing accidental release
// by a different process.
//
// IMPORTANT: This lock is a "best effort" secondary defense. The primary
// defense against race conditions is the PostgreSQL SELECT ... FOR UPDATE
// row-level lock inside a serialisable transaction. The Redis lock reduces
// contention at the application level so that most concurrent requests fail
// fast without hitting the database.
//
// Usage pattern for wallet operations:
//
//	lockKey := fmt.Sprintf("wallet_lock:%s", walletID.String())
//	lockVal := requestID.String()
//	if err := database.AcquireLock(ctx, redisClient, lockKey, lockVal, database.DefaultLockTTL); err != nil {
//	    return fmt.Errorf("could not acquire wallet lock: %w", err)
//	}
//	defer database.ReleaseLock(ctx, redisClient, lockKey, lockVal)
//
// Parameters:
//   - ctx:    request-scoped context for cancellation and deadline propagation.
//   - client: the Redis client instance to use for the SETNX command.
//   - key:    the lock key (e.g. "wallet_lock:{wallet_id}").
//   - value:  a unique identifier for this lock holder (e.g. request UUID).
//             Used by ReleaseLock to ensure only the holder can release.
//   - ttl:    how long the lock is valid before auto-expiring. Use DefaultLockTTL
//             unless you have a specific reason to change it.
//
// Returns:
//   - error: nil if the lock was acquired, ErrLockNotAcquired if another
//            process holds the lock, or a wrapped Redis error on failure.
func AcquireLock(ctx context.Context, client *redis.Client, key, value string, ttl time.Duration) error {
	// SET key value NX EX ttl
	// NX = only set if key does not exist (atomic acquire)
	// EX = set expiration in seconds (auto-release on crash)
	//
	// This is an atomic operation: either we get the lock or we don't.
	// There is no TOCTOU gap between checking and setting.
	ok, err := client.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		// Redis communication error (network, timeout, etc.).
		// We treat this as a lock failure to be safe — if we can't talk to
		// Redis, we cannot guarantee mutual exclusion.
		return fmt.Errorf("distributed lock: redis SETNX failed for key %q: %w", key, err)
	}

	// ok=false means the key already existed, i.e. another process holds the lock.
	if !ok {
		return ErrLockNotAcquired
	}

	// Lock acquired successfully. The caller MUST call ReleaseLock when done
	// (typically via defer) to free the lock before the TTL expires.
	return nil
}

// -------------------------------------------------------------------------
// ReleaseLock — safe release via Lua script
// -------------------------------------------------------------------------

// releaseLockScript is a Lua script that atomically checks the lock value
// and deletes the key only if the value matches. This prevents a process
// from accidentally releasing a lock that was already expired and re-acquired
// by another process.
//
// Without this check, the following race condition is possible:
//   1. Process A acquires lock with value "A".
//   2. Process A takes too long; the TTL expires.
//   3. Process B acquires lock with value "B".
//   4. Process A finishes and calls DEL — this would delete B's lock!
//
// The Lua script makes the check-and-delete atomic within Redis, eliminating
// this race condition.
//
// KEYS[1] = the lock key
// ARGV[1] = the expected lock value (the holder's unique identifier)
// Returns 1 if the lock was released, 0 if the value did not match.
var releaseLockScript = redis.NewScript(`
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("DEL", KEYS[1])
	else
		return 0
	end
`)

// ReleaseLock releases a distributed lock previously acquired with AcquireLock.
// It uses a Lua script to atomically verify that the lock is still held by the
// caller (by comparing the stored value) before deleting the key. This prevents
// accidentally releasing a lock that was re-acquired by another process after
// the TTL expired.
//
// If the lock has already expired or was acquired by another holder, this
// method silently succeeds (no error) because the lock is no longer ours to
// release. This is intentional: the caller's critical section has already
// finished, so there is nothing to roll back.
//
// Parameters:
//   - ctx:    request-scoped context for cancellation and deadline propagation.
//   - client: the Redis client instance to use for the Lua script execution.
//   - key:    the lock key (must match the key used in AcquireLock).
//   - value:  the unique identifier that was used to acquire the lock. Only
//             if the stored value matches will the lock be deleted.
//
// Returns:
//   - error: nil on success (whether the lock was released or already gone),
//            or a wrapped Redis error on communication failure.
func ReleaseLock(ctx context.Context, client *redis.Client, key, value string) error {
	// Execute the Lua script atomically on the Redis server.
	// The script checks if the stored value matches our value before deleting.
	// This is safe even under high concurrency because Redis executes Lua
	// scripts atomically (no other commands interleave).
	_, err := releaseLockScript.Run(ctx, client, []string{key}, value).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		// redis.Nil is returned when the key doesn't exist (already expired).
		// Any other error is a genuine Redis communication failure.
		return fmt.Errorf("distributed lock: redis release script failed for key %q: %w", key, err)
	}

	// Lock released (or was already gone). Either way, we're done.
	return nil
}
