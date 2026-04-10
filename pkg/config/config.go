// Package config provides helpers for reading typed configuration values from
// environment variables with sensible defaults. It also defines configuration
// structs for the external dependencies used across all RichPayment services
// (PostgreSQL, Redis, NATS).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Get returns an environment variable or a default value.
func Get(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// MustGet returns an environment variable or panics if not set.
func MustGet(key string) string {
	val := os.Getenv(key)
	if val == "" {
		panic(fmt.Sprintf("required environment variable %s is not set", key))
	}
	return val
}

// GetInt returns an environment variable as int or a default value.
func GetInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

// GetBool returns an environment variable as bool or a default value.
func GetBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			return b
		}
	}
	return defaultVal
}

// GetDuration returns an environment variable as time.Duration or a default value.
func GetDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}

// DatabaseConfig holds PostgreSQL connection configuration.
type DatabaseConfig struct {
	// Host is the database server hostname or IP address.
	Host string
	// Port is the TCP port the database listens on (default 5432).
	Port int
	// User is the database role used to authenticate.
	User string
	// Password is the authentication password for the database role.
	Password string
	// DBName is the name of the PostgreSQL database to connect to.
	DBName string
	// SSLMode controls the SSL negotiation mode (e.g. "disable", "require").
	SSLMode string
	// MaxConns is the maximum number of connections in the pool.
	MaxConns int
}

// DSN returns the PostgreSQL connection string.
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.DBName, c.SSLMode,
	)
}

// LoadDatabaseConfig loads database configuration from environment variables.
func LoadDatabaseConfig() *DatabaseConfig {
	return &DatabaseConfig{
		Host:     Get("DB_HOST", "localhost"),
		Port:     GetInt("DB_PORT", 5432),
		User:     Get("DB_USER", "richpayment"),
		Password: Get("DB_PASSWORD", "richpayment_dev"),
		DBName:   Get("DB_NAME", "richpayment"),
		SSLMode:  Get("DB_SSLMODE", "disable"),
		MaxConns: GetInt("DB_MAX_CONNS", 25),
	}
}

// RedisConfig holds Redis connection configuration.
type RedisConfig struct {
	// Host is the Redis server hostname or IP address.
	Host string
	// Port is the TCP port Redis listens on (default 6379).
	Port int
	// Password is the Redis AUTH password (empty for no auth).
	Password string
	// DB is the Redis database index (0-15).
	DB int
}

// Addr returns the Redis address string.
func (c *RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// LoadRedisConfig loads Redis configuration from environment variables.
func LoadRedisConfig() *RedisConfig {
	return &RedisConfig{
		Host:     Get("REDIS_HOST", "localhost"),
		Port:     GetInt("REDIS_PORT", 6379),
		Password: Get("REDIS_PASSWORD", "richpayment_dev"),
		DB:       GetInt("REDIS_DB", 0),
	}
}

// NATSConfig holds NATS connection configuration.
type NATSConfig struct {
	// URL is the NATS server URL (e.g. "nats://localhost:4222").
	URL string
	// Token is the authentication token for the NATS connection (empty for no auth).
	Token string
}

// LoadNATSConfig loads NATS configuration from environment variables.
func LoadNATSConfig() *NATSConfig {
	return &NATSConfig{
		URL:   Get("NATS_URL", "nats://localhost:4222"),
		Token: Get("NATS_TOKEN", ""),
	}
}
