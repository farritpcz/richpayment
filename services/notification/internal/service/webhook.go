// Package service implements the core business logic for the notification service,
// including webhook delivery with retry semantics and Telegram alerting.
package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// -----------------------------------------------------------------
// Constants
// -----------------------------------------------------------------

const (
	// webhookTimeout is the per-attempt HTTP timeout when calling a merchant's
	// webhook endpoint. Merchants that take longer than this to respond will
	// be treated as a failed attempt.
	webhookTimeout = 10 * time.Second

	// maxWebhookAttempts is the maximum number of delivery attempts before
	// the webhook is considered exhausted and an admin alert is sent.
	maxWebhookAttempts = 5

	// webhookRetryQueue is the Redis sorted-set key used to schedule future
	// webhook retry attempts. The score is the Unix timestamp at which the
	// next retry should be attempted.
	webhookRetryQueue = "webhook_retry_queue"

	// webhookDataPrefix is the Redis hash-key prefix that stores the full
	// payload and metadata needed to re-attempt a webhook delivery.
	webhookDataPrefix = "webhook_data:"
)

// retryDelays defines the exponential back-off intervals between successive
// webhook delivery attempts. Each entry corresponds to the delay before
// attempt N+1 (i.e. index 0 = delay before the 2nd attempt, etc.).
// The progression is: 10s -> 30s -> 90s -> 270s -> 810s.
var retryDelays = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	90 * time.Second,
	270 * time.Second,
	810 * time.Second,
}

// -----------------------------------------------------------------
// WebhookService
// -----------------------------------------------------------------

// WebhookService is responsible for delivering signed HTTP POST webhooks to
// merchant endpoints. It supports automatic retry with exponential back-off
// and will escalate to a Telegram alert if all attempts are exhausted.
type WebhookService struct {
	// rdb is the Redis client used for persisting retry state and webhook
	// delivery logs.
	rdb *redis.Client

	// httpClient is a dedicated HTTP client with a fixed timeout for making
	// outbound webhook calls. Using a shared client allows connection reuse.
	httpClient *http.Client

	// telegram is used to send admin alerts when a webhook is exhausted
	// after all retry attempts have been consumed.
	telegram *TelegramService

	// log is the structured logger used throughout the webhook service.
	log *slog.Logger
}

// NewWebhookService constructs a new WebhookService with the provided
// dependencies. The Redis client is used for retry state management, and the
// TelegramService is used to send alerts on delivery exhaustion.
func NewWebhookService(rdb *redis.Client, telegram *TelegramService, log *slog.Logger) *WebhookService {
	return &WebhookService{
		rdb: rdb,
		httpClient: &http.Client{
			// Each individual webhook call is bounded by this timeout to
			// prevent hanging connections from blocking the retry pipeline.
			Timeout: webhookTimeout,
		},
		telegram: telegram,
		log:      log,
	}
}

// -----------------------------------------------------------------
// WebhookPayload
// -----------------------------------------------------------------

// WebhookPayload holds all data required to deliver (or re-deliver) a webhook.
// It is serialised to JSON and stored in Redis so the retry worker can pick
// it up later without needing access to the original database row.
type WebhookPayload struct {
	// MerchantID identifies the merchant that should receive the webhook.
	MerchantID string `json:"merchant_id"`

	// OrderID is the payment order that triggered this webhook notification.
	OrderID string `json:"order_id"`

	// WebhookURL is the merchant-configured endpoint to which the payload
	// will be POSTed.
	WebhookURL string `json:"webhook_url"`

	// WebhookSecret is the HMAC-SHA256 key used to sign the request body
	// so the merchant can verify authenticity.
	WebhookSecret string `json:"webhook_secret"`

	// Payload is the raw JSON body that will be sent to the merchant.
	Payload json.RawMessage `json:"payload"`

	// Attempt tracks how many delivery attempts have been made so far.
	// Starts at 0 for a brand-new webhook.
	Attempt int `json:"attempt"`

	// CreatedAt records when the webhook was first enqueued for delivery.
	CreatedAt time.Time `json:"created_at"`
}

// -----------------------------------------------------------------
// SendWebhook - primary entry point
// -----------------------------------------------------------------

// SendWebhook initiates delivery of a signed webhook to the merchant's
// endpoint. If the first attempt fails, the webhook is scheduled for retry
// with exponential back-off. On success the result is persisted in Redis.
//
// Parameters:
//   - ctx:           request-scoped context for cancellation / deadlines
//   - merchantID:    UUID of the merchant receiving the notification
//   - orderID:       UUID of the related payment order
//   - webhookURL:    the merchant's HTTPS callback endpoint
//   - webhookSecret: shared secret used to HMAC-sign the payload
//   - payload:       arbitrary JSON body to deliver
func (s *WebhookService) SendWebhook(
	ctx context.Context,
	merchantID, orderID, webhookURL, webhookSecret string,
	payload json.RawMessage,
) error {
	// Build the full webhook envelope that will be stored in Redis for
	// potential retry use.
	wp := &WebhookPayload{
		MerchantID:    merchantID,
		OrderID:       orderID,
		WebhookURL:    webhookURL,
		WebhookSecret: webhookSecret,
		Payload:       payload,
		Attempt:       0,
		CreatedAt:     time.Now().UTC(),
	}

	s.log.Info("sending webhook",
		"merchant_id", merchantID,
		"order_id", orderID,
		"url", webhookURL,
		"attempt", 1,
	)

	// Attempt the first delivery immediately.
	if err := s.deliver(ctx, wp); err != nil {
		s.log.Warn("webhook delivery failed, scheduling retry",
			"merchant_id", merchantID,
			"order_id", orderID,
			"attempt", 1,
			"error", err,
		)

		// Schedule the first retry using exponential back-off.
		return s.scheduleRetry(ctx, wp)
	}

	// First attempt succeeded - persist the success record.
	s.log.Info("webhook delivered successfully",
		"merchant_id", merchantID,
		"order_id", orderID,
		"attempt", 1,
	)
	return s.markDelivered(ctx, wp)
}

// -----------------------------------------------------------------
// deliver - performs a single HTTP POST attempt
// -----------------------------------------------------------------

// deliver sends a single HTTP POST request to the merchant's webhook URL.
// The request body is the raw JSON payload, signed with HMAC-SHA256. Two
// custom headers are attached for the merchant to verify authenticity:
//   - X-Webhook-Signature: hex-encoded HMAC-SHA256 of the body
//   - X-Webhook-Timestamp: Unix epoch second when the signature was created
//
// Returns nil if the merchant responded with a 2xx status code.
func (s *WebhookService) deliver(ctx context.Context, wp *WebhookPayload) error {
	// Capture the exact moment of signing so the merchant can detect
	// replay attacks by checking the timestamp freshness.
	timestamp := time.Now().UTC().Unix()
	timestampStr := strconv.FormatInt(timestamp, 10)

	// Build the HMAC-SHA256 signature over the raw payload bytes.
	// The merchant should compute the same signature server-side and
	// compare it to X-Webhook-Signature.
	signature := s.signPayload(wp.Payload, wp.WebhookSecret, timestampStr)

	// Construct the outgoing HTTP request with a context-aware timeout.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wp.WebhookURL, bytes.NewReader(wp.Payload))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}

	// Set required headers so the merchant can identify and verify the call.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", signature)
	req.Header.Set("X-Webhook-Timestamp", timestampStr)
	req.Header.Set("X-Webhook-ID", uuid.New().String())

	// Execute the HTTP call. The client has a 10-second timeout to prevent
	// slow merchant endpoints from blocking our delivery pipeline.
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook POST failed: %w", err)
	}
	defer resp.Body.Close()

	// Drain and discard the body to allow connection reuse by the HTTP
	// transport pool.
	_, _ = io.Copy(io.Discard, resp.Body)

	// Any non-2xx status is treated as a delivery failure.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	return nil
}

// -----------------------------------------------------------------
// signPayload - HMAC-SHA256 signature generation
// -----------------------------------------------------------------

// signPayload computes the HMAC-SHA256 signature of the webhook body. The
// signed message is "{timestamp}.{body}" which binds the signature to a
// specific point in time, preventing replay attacks.
//
// Parameters:
//   - body:      raw JSON payload bytes
//   - secret:    the merchant's webhook secret key
//   - timestamp: Unix epoch second as a string
//
// Returns the hex-encoded HMAC-SHA256 digest.
func (s *WebhookService) signPayload(body []byte, secret, timestamp string) string {
	// Construct the canonical signing string: "timestamp.body".
	// This approach is similar to Stripe's webhook signature scheme.
	message := timestamp + "." + string(body)

	// Create the HMAC-SHA256 MAC using the merchant's secret.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))

	// Return the hex-encoded digest for easy transport in an HTTP header.
	return hex.EncodeToString(mac.Sum(nil))
}

// -----------------------------------------------------------------
// scheduleRetry - enqueue a failed webhook for future retry
// -----------------------------------------------------------------

// scheduleRetry persists the webhook payload in Redis and adds it to the
// retry sorted set with a score equal to the next retry timestamp. The
// retry worker will pick it up when the scheduled time arrives.
//
// If the maximum number of attempts has been reached, the webhook is marked
// as exhausted and a Telegram alert is sent to the admin group.
func (s *WebhookService) scheduleRetry(ctx context.Context, wp *WebhookPayload) error {
	// Increment the attempt counter before scheduling the next try.
	wp.Attempt++

	// Check whether we have exhausted all allowed retries.
	if wp.Attempt >= maxWebhookAttempts {
		s.log.Error("webhook exhausted all retries",
			"merchant_id", wp.MerchantID,
			"order_id", wp.OrderID,
			"attempts", wp.Attempt,
		)

		// Mark the webhook as permanently failed in Redis.
		_ = s.markExhausted(ctx, wp)

		// Escalate to operations via Telegram so a human can investigate.
		alertDetails := fmt.Sprintf(
			"merchant=%s order=%s url=%s attempts=%d",
			wp.MerchantID, wp.OrderID, wp.WebhookURL, wp.Attempt,
		)
		if s.telegram != nil {
			_ = s.telegram.SendSecurityAlert(ctx, "webhook_exhausted", alertDetails)
		}

		return fmt.Errorf("webhook exhausted after %d attempts", wp.Attempt)
	}

	// Determine when the next retry should fire based on exponential
	// back-off schedule.
	delay := retryDelays[wp.Attempt-1]
	nextRetryAt := time.Now().UTC().Add(delay)

	// Generate a unique key for this webhook delivery attempt.
	webhookKey := fmt.Sprintf("%s:%s", wp.MerchantID, wp.OrderID)
	dataKey := webhookDataPrefix + webhookKey

	// Serialise the full webhook payload to JSON for Redis storage.
	data, err := json.Marshal(wp)
	if err != nil {
		return fmt.Errorf("marshal webhook payload for retry: %w", err)
	}

	// Store the payload data in a Redis hash so the retry worker has
	// everything it needs to re-attempt delivery.
	if err := s.rdb.Set(ctx, dataKey, data, 24*time.Hour).Err(); err != nil {
		return fmt.Errorf("store webhook retry data in redis: %w", err)
	}

	// Add the webhook to the retry sorted set. The score is the Unix
	// timestamp at which the retry should be attempted.
	if err := s.rdb.ZAdd(ctx, webhookRetryQueue, redis.Z{
		Score:  float64(nextRetryAt.Unix()),
		Member: webhookKey,
	}).Err(); err != nil {
		return fmt.Errorf("enqueue webhook retry in sorted set: %w", err)
	}

	s.log.Info("webhook retry scheduled",
		"merchant_id", wp.MerchantID,
		"order_id", wp.OrderID,
		"attempt", wp.Attempt,
		"next_retry_at", nextRetryAt.Format(time.RFC3339),
		"delay", delay.String(),
	)

	return nil
}

// -----------------------------------------------------------------
// RetryWebhook - called by the retry worker
// -----------------------------------------------------------------

// RetryWebhook is called by the background retry worker to re-attempt
// delivery of a previously failed webhook. It loads the stored payload from
// Redis, attempts delivery, and either marks it as delivered or schedules
// the next retry.
func (s *WebhookService) RetryWebhook(ctx context.Context, webhookKey string) error {
	// Build the Redis key where the webhook payload data is stored.
	dataKey := webhookDataPrefix + webhookKey

	// Load the stored webhook payload from Redis.
	data, err := s.rdb.Get(ctx, dataKey).Bytes()
	if err != nil {
		return fmt.Errorf("load webhook retry data from redis key %s: %w", dataKey, err)
	}

	// Deserialise the webhook payload.
	var wp WebhookPayload
	if err := json.Unmarshal(data, &wp); err != nil {
		return fmt.Errorf("unmarshal webhook retry payload: %w", err)
	}

	s.log.Info("retrying webhook delivery",
		"merchant_id", wp.MerchantID,
		"order_id", wp.OrderID,
		"attempt", wp.Attempt+1,
	)

	// Attempt delivery.
	if err := s.deliver(ctx, &wp); err != nil {
		s.log.Warn("webhook retry failed",
			"merchant_id", wp.MerchantID,
			"order_id", wp.OrderID,
			"attempt", wp.Attempt+1,
			"error", err,
		)

		// Schedule the next retry (or mark as exhausted).
		return s.scheduleRetry(ctx, &wp)
	}

	// Delivery succeeded on this retry attempt.
	s.log.Info("webhook delivered on retry",
		"merchant_id", wp.MerchantID,
		"order_id", wp.OrderID,
		"attempt", wp.Attempt+1,
	)

	// Clean up the retry data from Redis now that delivery succeeded.
	_ = s.rdb.Del(ctx, dataKey).Err()

	return s.markDelivered(ctx, &wp)
}

// -----------------------------------------------------------------
// markDelivered - record a successful delivery
// -----------------------------------------------------------------

// markDelivered persists a record of successful webhook delivery in Redis.
// This allows other services to query whether a webhook was delivered and
// serves as an audit trail.
func (s *WebhookService) markDelivered(ctx context.Context, wp *WebhookPayload) error {
	// Build a delivery receipt with relevant metadata.
	deliveryKey := fmt.Sprintf("webhook_delivered:%s:%s", wp.MerchantID, wp.OrderID)

	record := map[string]interface{}{
		"merchant_id":  wp.MerchantID,
		"order_id":     wp.OrderID,
		"webhook_url":  wp.WebhookURL,
		"attempts":     wp.Attempt + 1,
		"delivered_at": time.Now().UTC().Format(time.RFC3339),
		"status":       "delivered",
	}

	// Store delivery receipt in Redis with a 7-day TTL for audit purposes.
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal delivery record: %w", err)
	}

	if err := s.rdb.Set(ctx, deliveryKey, data, 7*24*time.Hour).Err(); err != nil {
		return fmt.Errorf("store delivery record in redis: %w", err)
	}

	return nil
}

// -----------------------------------------------------------------
// markExhausted - record a permanently failed delivery
// -----------------------------------------------------------------

// markExhausted persists a record indicating that all retry attempts have
// been consumed and the webhook could not be delivered. This record is kept
// for 30 days so operations can investigate and potentially trigger a manual
// re-delivery.
func (s *WebhookService) markExhausted(ctx context.Context, wp *WebhookPayload) error {
	// Build a failure receipt with relevant metadata.
	exhaustedKey := fmt.Sprintf("webhook_exhausted:%s:%s", wp.MerchantID, wp.OrderID)

	record := map[string]interface{}{
		"merchant_id":  wp.MerchantID,
		"order_id":     wp.OrderID,
		"webhook_url":  wp.WebhookURL,
		"attempts":     wp.Attempt,
		"exhausted_at": time.Now().UTC().Format(time.RFC3339),
		"status":       "exhausted",
	}

	// Store exhaustion record in Redis with a 30-day TTL for investigation.
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal exhaustion record: %w", err)
	}

	if err := s.rdb.Set(ctx, exhaustedKey, data, 30*24*time.Hour).Err(); err != nil {
		return fmt.Errorf("store exhaustion record in redis: %w", err)
	}

	// Remove the retry data key since no more retries will be attempted.
	webhookKey := fmt.Sprintf("%s:%s", wp.MerchantID, wp.OrderID)
	_ = s.rdb.Del(ctx, webhookDataPrefix+webhookKey).Err()

	return nil
}
