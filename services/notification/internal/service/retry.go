package service

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// -----------------------------------------------------------------
// Constants
// -----------------------------------------------------------------

const (
	// retryPollInterval is the frequency at which the background retry
	// worker checks the Redis sorted set for webhooks that are ready to be
	// retried. A 5-second interval provides a good balance between latency
	// and Redis load.
	retryPollInterval = 5 * time.Second

	// retryBatchSize is the maximum number of webhooks to dequeue from the
	// retry sorted set in a single polling cycle. This prevents a burst of
	// retries from overwhelming downstream merchant endpoints.
	retryBatchSize = 10
)

// -----------------------------------------------------------------
// RetryWorker
// -----------------------------------------------------------------

// RetryWorker is a background process that polls the Redis sorted set
// "webhook_retry_queue" for webhooks whose next_retry_at timestamp has
// arrived. It dequeues them and delegates re-delivery to the WebhookService.
type RetryWorker struct {
	// rdb is the Redis client used to read from the retry sorted set.
	rdb *redis.Client

	// webhook is the WebhookService instance that performs the actual
	// re-delivery attempt for each dequeued webhook.
	webhook *WebhookService

	// log is the structured logger for the retry worker.
	log *slog.Logger
}

// NewRetryWorker constructs a new RetryWorker with the given dependencies.
// The worker does not start automatically; call StartRetryWorker to begin
// polling.
func NewRetryWorker(rdb *redis.Client, webhook *WebhookService, log *slog.Logger) *RetryWorker {
	return &RetryWorker{
		rdb:     rdb,
		webhook: webhook,
		log:     log,
	}
}

// -----------------------------------------------------------------
// StartRetryWorker - background goroutine entry point
// -----------------------------------------------------------------

// StartRetryWorker launches a background goroutine that continuously polls
// the webhook retry queue. The goroutine runs until the provided context is
// cancelled, at which point it exits cleanly.
//
// This function returns immediately after spawning the goroutine. The caller
// should cancel the context during service shutdown to stop the worker.
func (w *RetryWorker) StartRetryWorker(ctx context.Context) {
	w.log.Info("starting webhook retry worker",
		"poll_interval", retryPollInterval.String(),
		"batch_size", retryBatchSize,
	)

	go func() {
		// Create a ticker that fires at the configured poll interval.
		ticker := time.NewTicker(retryPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// The parent context was cancelled, which means the service
				// is shutting down. Exit the goroutine cleanly.
				w.log.Info("webhook retry worker stopped")
				return

			case <-ticker.C:
				// Poll the retry queue for webhooks that are ready to retry.
				w.pollAndRetry(ctx)
			}
		}
	}()
}

// -----------------------------------------------------------------
// pollAndRetry - single poll cycle
// -----------------------------------------------------------------

// pollAndRetry performs a single poll cycle: it queries the Redis sorted set
// for all webhooks whose scheduled retry time (score) is at or before the
// current time, and attempts to re-deliver each one.
//
// Webhooks are removed from the sorted set before retrying to prevent
// duplicate delivery. If the retry fails, the WebhookService will re-enqueue
// the webhook with an updated score.
func (w *RetryWorker) pollAndRetry(ctx context.Context) {
	// Build the range query: fetch all members with score <= now.
	// The score represents the Unix timestamp at which the retry should fire.
	now := time.Now().UTC().Unix()

	// Use ZRANGEBYSCORE to get webhooks ready for retry, limited to the
	// configured batch size to avoid processing too many at once.
	members, err := w.rdb.ZRangeByScore(ctx, webhookRetryQueue, &redis.ZRangeBy{
		Min:   "-inf",
		Max:   strconv.FormatInt(now, 10),
		Count: retryBatchSize,
	}).Result()
	if err != nil {
		// Redis errors during polling are non-fatal; we simply log and
		// retry on the next tick.
		w.log.Error("failed to poll webhook retry queue",
			"error", err,
		)
		return
	}

	// No webhooks are due for retry at this moment.
	if len(members) == 0 {
		return
	}

	w.log.Info("found webhooks ready for retry",
		"count", len(members),
	)

	// Process each webhook that is ready for retry.
	for _, webhookKey := range members {
		// Remove the webhook from the sorted set first to prevent other
		// workers (in a multi-instance deployment) from picking it up
		// simultaneously. ZREM is atomic and returns the number of removed
		// members, so only the instance that successfully removes it will
		// proceed with the retry.
		removed, err := w.rdb.ZRem(ctx, webhookRetryQueue, webhookKey).Result()
		if err != nil {
			w.log.Error("failed to remove webhook from retry queue",
				"webhook_key", webhookKey,
				"error", err,
			)
			continue
		}

		// If another worker already removed this entry, skip it to avoid
		// duplicate delivery.
		if removed == 0 {
			w.log.Debug("webhook already claimed by another worker",
				"webhook_key", webhookKey,
			)
			continue
		}

		// Delegate the actual retry to the WebhookService. If delivery
		// fails again, the WebhookService will re-enqueue it with an
		// updated backoff delay.
		if err := w.webhook.RetryWebhook(ctx, webhookKey); err != nil {
			w.log.Warn("webhook retry attempt failed",
				"webhook_key", webhookKey,
				"error", err,
			)
			// The WebhookService has already handled re-enqueueing or
			// exhaustion, so we just log and continue.
		}
	}
}
