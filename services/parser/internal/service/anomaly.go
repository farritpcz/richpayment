// Package service — anomaly.go implements SMS rate anomaly detection and
// duplicate SMS detection for the parser-service.
//
// This module provides two critical security mechanisms:
//
// 1. DUPLICATE SMS DETECTION:
//    Each incoming SMS is hashed (SHA-256 of body + sender + timestamp) and
//    checked against Redis before processing. If the same hash already exists,
//    the SMS is rejected as a duplicate. This prevents replay attacks where
//    an attacker re-sends a previously captured bank notification.
//
// 2. SMS RATE ANOMALY DETECTION:
//    Tracks the number of SMS messages received per bank account per minute
//    using a Redis counter with a 60-second TTL. If more than the configured
//    threshold (default: 10) SMS messages arrive in a single minute for the
//    same bank account, the system:
//      a) Sets a "sms_anomaly:{bank_account_id}" flag in Redis (5-minute TTL).
//      b) Sends an alert to the admin via Telegram.
//      c) The anomaly flag is checked by the AntiSpoofEvaluator to withhold
//         confidence points from subsequent SMS messages on the flagged account.
//
// Both mechanisms use Redis for fast, atomic operations with automatic expiry
// so that stale data is cleaned up without manual intervention.
package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ------------------- Anomaly Detection Constants -------------------

const (
	// defaultRateLimit is the maximum number of SMS messages allowed per
	// bank account per minute before triggering a rate anomaly alert.
	// 10 SMS/minute is generous for legitimate bank notifications (a real
	// bank account rarely receives more than 2-3 per minute) but catches
	// obvious flooding attacks.
	defaultRateLimit = 10

	// rateLimitWindow is the time window for SMS rate counting.
	// Each bank account gets a Redis counter that expires after this duration.
	rateLimitWindow = 1 * time.Minute

	// anomalyFlagTTL is how long the anomaly flag remains active after
	// a rate anomaly is detected. During this period, the AntiSpoofEvaluator
	// withholds the "no anomalies" confidence points (+10) from all SMS
	// messages on the flagged bank account.
	anomalyFlagTTL = 5 * time.Minute

	// duplicateKeyTTL is how long a duplicate SMS hash is retained in Redis.
	// After this period, the same SMS content could theoretically be processed
	// again. 24 hours is sufficient because the maxSMSAge check (5 minutes)
	// already rejects old messages; this is a belt-and-suspenders defence.
	duplicateKeyTTL = 24 * time.Hour
)

// ------------------- Configuration -------------------

// AnomalyConfig holds tunable parameters for the anomaly detection system.
// All fields have sensible defaults; callers only need to override what
// they want to customise.
type AnomalyConfig struct {
	// RateLimit is the maximum SMS messages per bank account per minute.
	// Exceeding this triggers a rate anomaly alert. Default: 10.
	RateLimit int64

	// TelegramBotToken is the Telegram Bot API token used to send admin
	// alerts when anomalies are detected. If empty, Telegram alerts are
	// disabled (alerts are still logged).
	TelegramBotToken string

	// TelegramChatID is the Telegram chat or group ID where anomaly alerts
	// are sent. Must be set alongside TelegramBotToken.
	TelegramChatID string
}

// DefaultAnomalyConfig returns an AnomalyConfig with production-safe default
// values. Telegram credentials must be provided separately.
//
// Returns:
//   - A fully populated AnomalyConfig with default rate limit.
func DefaultAnomalyConfig() AnomalyConfig {
	return AnomalyConfig{
		RateLimit: defaultRateLimit,
	}
}

// ------------------- Anomaly Detector -------------------

// AnomalyDetector provides duplicate SMS detection and SMS rate anomaly
// monitoring. It uses Redis as the backing store for hash lookups and
// rate counters, and optionally sends Telegram alerts to admins when
// anomalies are detected.
type AnomalyDetector struct {
	// rdb is the Redis client for hash lookups, rate counters, and anomaly flags.
	rdb *redis.Client

	// config holds the tunable rate limit and Telegram credentials.
	config AnomalyConfig

	// logger is the structured logger for anomaly detection events.
	logger *slog.Logger
}

// NewAnomalyDetector constructs an AnomalyDetector with all required
// dependencies.
//
// Parameters:
//   - rdb:    Redis client for all anomaly detection operations.
//   - config: tunable parameters (rate limit, Telegram credentials).
//   - logger: structured logger for operational visibility.
//
// Returns:
//   - A fully initialised AnomalyDetector ready to check SMS messages.
func NewAnomalyDetector(
	rdb *redis.Client,
	config AnomalyConfig,
	logger *slog.Logger,
) *AnomalyDetector {
	return &AnomalyDetector{
		rdb:    rdb,
		config: config,
		logger: logger,
	}
}

// ------------------- Duplicate SMS Detection -------------------

// IsDuplicate checks whether an SMS with the same content has already been
// processed. It computes a SHA-256 hash of the SMS body, sender number, and
// timestamp, then checks Redis for the existence of this hash.
//
// If the hash does NOT exist, it is atomically set with a TTL to mark the
// SMS as "seen". This is done in a single SETNX (SET if Not eXists) call
// to prevent race conditions when multiple SMS webhook requests arrive
// simultaneously.
//
// Parameters:
//   - ctx:          request-scoped context for Redis operations.
//   - senderNumber: the SMS sender phone number or alphanumeric ID.
//   - rawMessage:   the full, unmodified SMS body text.
//   - receivedAt:   when the SMS gateway received the message.
//
// Returns:
//   - true if the SMS has already been processed (duplicate detected).
//   - false if the SMS is new and has been marked as "seen".
//   - An error if the Redis operation fails.
func (d *AnomalyDetector) IsDuplicate(
	ctx context.Context,
	senderNumber string,
	rawMessage string,
	receivedAt time.Time,
) (bool, error) {
	// Build the hash input by concatenating sender + message + timestamp.
	// Using a fixed delimiter ("|") to prevent collisions between fields
	// (e.g. sender="A|B" + message="C" vs sender="A" + message="B|C").
	hashInput := fmt.Sprintf("%s|%s|%d", senderNumber, rawMessage, receivedAt.UnixNano())

	// Compute SHA-256 hash of the input string.
	hash := sha256.Sum256([]byte(hashInput))
	hashHex := fmt.Sprintf("%x", hash)

	// Build the Redis key for this SMS hash.
	// Prefix with "sms_dup:" to namespace it away from other Redis keys.
	key := fmt.Sprintf("sms_dup:%s", hashHex)

	// Attempt to set the key only if it does not already exist (SETNX).
	// If the key already exists, SetNX returns false (meaning duplicate).
	// If the key is new, SetNX returns true and sets the TTL.
	wasSet, err := d.rdb.SetNX(ctx, key, "1", duplicateKeyTTL).Result()
	if err != nil {
		d.logger.Error("duplicate check: Redis SETNX failed",
			"key", key,
			"error", err,
		)
		return false, fmt.Errorf("duplicate check redis setnx: %w", err)
	}

	if !wasSet {
		// The key already existed — this is a duplicate SMS.
		d.logger.Warn("duplicate SMS detected and rejected",
			"sender", senderNumber,
			"hash", hashHex,
			"received_at", receivedAt,
		)
		return true, nil
	}

	// The key was newly created — this is a fresh SMS.
	d.logger.Debug("duplicate check passed, SMS is new",
		"sender", senderNumber,
		"hash", hashHex,
	)
	return false, nil
}

// ------------------- SMS Rate Anomaly Detection -------------------

// CheckRateAnomaly tracks the SMS count per bank account per minute and
// flags accounts that exceed the configured rate limit. When a rate anomaly
// is detected, it:
//   1. Sets a "sms_anomaly:{bank_account_id}" flag in Redis with a 5-minute TTL.
//   2. Sends a Telegram alert to the admin (if configured).
//   3. Returns true to indicate the anomaly.
//
// The rate counter uses a Redis key with a 60-second TTL that auto-expires,
// so counters reset every minute without manual cleanup.
//
// Parameters:
//   - ctx:           request-scoped context for Redis operations.
//   - bankAccountID: the internal UUID of the bank account receiving SMS messages.
//   - senderNumber:  the SMS sender number (included in the alert for context).
//
// Returns:
//   - true if a rate anomaly was detected (threshold exceeded).
//   - false if the rate is within normal bounds.
//   - An error if a Redis operation fails.
func (d *AnomalyDetector) CheckRateAnomaly(
	ctx context.Context,
	bankAccountID uuid.UUID,
	senderNumber string,
) (bool, error) {
	// Build the Redis key for the per-minute rate counter.
	// Format: "sms_rate:{bank_account_id}" with a 60-second TTL.
	rateKey := fmt.Sprintf("sms_rate:%s", bankAccountID.String())

	// Atomically increment the counter and check the new value.
	// INCR creates the key with value 1 if it doesn't exist.
	count, err := d.rdb.Incr(ctx, rateKey).Result()
	if err != nil {
		d.logger.Error("rate anomaly check: Redis INCR failed",
			"key", rateKey,
			"error", err,
		)
		return false, fmt.Errorf("rate anomaly redis incr: %w", err)
	}

	// If this is the first increment (count == 1), set the TTL.
	// We only set the TTL on the first increment to avoid resetting it
	// on subsequent increments within the same window.
	if count == 1 {
		d.rdb.Expire(ctx, rateKey, rateLimitWindow)
	}

	d.logger.Debug("SMS rate counter incremented",
		"bank_account_id", bankAccountID,
		"current_count", count,
		"limit", d.config.RateLimit,
	)

	// Check if the rate exceeds the configured threshold.
	if count > d.config.RateLimit {
		// --- Rate anomaly detected! ---
		d.logger.Warn("SMS RATE ANOMALY DETECTED",
			"bank_account_id", bankAccountID,
			"sender", senderNumber,
			"count_in_window", count,
			"limit", d.config.RateLimit,
		)

		// Set the anomaly flag in Redis so the AntiSpoofEvaluator can
		// detect it and withhold confidence points.
		anomalyKey := fmt.Sprintf("sms_anomaly:%s", bankAccountID.String())
		if err := d.rdb.Set(ctx, anomalyKey, fmt.Sprintf("rate_exceeded:%d", count), anomalyFlagTTL).Err(); err != nil {
			d.logger.Error("failed to set anomaly flag in Redis",
				"key", anomalyKey,
				"error", err,
			)
			// Don't fail the whole check — the anomaly was still detected.
		}

		// Send Telegram alert to admin (non-blocking, best-effort).
		go d.sendTelegramAlert(bankAccountID, senderNumber, count)

		return true, nil
	}

	return false, nil
}

// ------------------- Telegram Alerting -------------------

// sendTelegramAlert sends an anomaly alert message to the configured Telegram
// chat. This is a fire-and-forget operation: failures are logged but do not
// propagate to the caller. The function is designed to be called in a
// goroutine to avoid blocking SMS processing.
//
// Parameters:
//   - bankAccountID: the bank account that triggered the anomaly.
//   - senderNumber:  the SMS sender number for context.
//   - smsCount:      the number of SMS messages received in the current window.
func (d *AnomalyDetector) sendTelegramAlert(
	bankAccountID uuid.UUID,
	senderNumber string,
	smsCount int64,
) {
	// Skip if Telegram is not configured.
	if d.config.TelegramBotToken == "" || d.config.TelegramChatID == "" {
		d.logger.Debug("Telegram alert skipped: bot token or chat ID not configured")
		return
	}

	// Build the alert message with relevant context for the admin.
	message := fmt.Sprintf(
		"[ALERT] SMS Rate Anomaly Detected\n"+
			"Bank Account: %s\n"+
			"Sender: %s\n"+
			"SMS Count (1 min): %d\n"+
			"Threshold: %d\n"+
			"Time: %s\n"+
			"Action: Account flagged for %s, confidence score reduced.",
		bankAccountID.String(),
		senderNumber,
		smsCount,
		d.config.RateLimit,
		time.Now().Format(time.RFC3339),
		anomalyFlagTTL.String(),
	)

	// Build the Telegram Bot API URL.
	// API docs: https://core.telegram.org/bots/api#sendmessage
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", d.config.TelegramBotToken)

	// Make the HTTP POST request to the Telegram Bot API.
	// Use url.Values for form-encoded body (simpler than JSON for this use case).
	resp, err := http.PostForm(apiURL, url.Values{
		"chat_id": {d.config.TelegramChatID},
		"text":    {message},
	})
	if err != nil {
		d.logger.Error("failed to send Telegram anomaly alert",
			"bank_account_id", bankAccountID,
			"error", err,
		)
		return
	}
	defer resp.Body.Close()

	// Check for non-200 status codes.
	if resp.StatusCode != http.StatusOK {
		d.logger.Error("Telegram API returned non-200 status",
			"bank_account_id", bankAccountID,
			"status_code", resp.StatusCode,
		)
		return
	}

	d.logger.Info("Telegram anomaly alert sent successfully",
		"bank_account_id", bankAccountID,
		"sms_count", smsCount,
	)
}
