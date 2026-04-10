// Package logger provides a thin wrapper around Go's structured logging (slog)
// with a pre-configured JSON handler that writes to stdout. It exposes a
// package-level default logger and convenience functions so that callers do not
// need to manage their own slog.Logger instances.
package logger

import (
	"log/slog"
	"os"
)

// defaultLogger is the package-level logger instance used by the convenience
// functions (Info, Error, Warn, Debug). It is initialised in init() and can
// be reconfigured at runtime via SetLevel.
var defaultLogger *slog.Logger

// init sets up the default logger with a JSON handler at INFO level.
// This runs automatically when the package is first imported.
func init() {
	defaultLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// Default returns the package-level *slog.Logger so callers can pass it to
// libraries or middleware that accept a logger dependency.
func Default() *slog.Logger { return defaultLogger }

// SetLevel replaces the default logger with a new one configured at the given
// log level. This is useful for enabling debug logging via a runtime flag or
// environment variable.
func SetLevel(level slog.Level) {
	defaultLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
}

// Info logs a message at INFO level using the default logger.
func Info(msg string, args ...any) { defaultLogger.Info(msg, args...) }

// Error logs a message at ERROR level using the default logger.
func Error(msg string, args ...any) { defaultLogger.Error(msg, args...) }

// Warn logs a message at WARN level using the default logger.
func Warn(msg string, args ...any) { defaultLogger.Warn(msg, args...) }

// Debug logs a message at DEBUG level using the default logger.
func Debug(msg string, args ...any) { defaultLogger.Debug(msg, args...) }
