// Package service — antispoof.go implements the anti-spoofing confidence
// scoring system for SMS-based deposit verification.
//
// SMS sender numbers can be spoofed by attackers who forge the sender ID
// (e.g. "KBANK") to trick the parser into crediting a merchant's wallet.
// This module assigns a numeric confidence score (0-100) to every incoming
// SMS based on multiple independent signals. The score determines whether
// the deposit should be auto-approved, delayed, or held for manual review.
//
// Scoring breakdown:
//   - Sender matches known bank number:      +30 points
//   - SMS format matches bank template:       +30 points
//   - Amount matches a pending order exactly:  +20 points
//   - Timestamp is within expected window:     +10 points
//   - No recent anomalies on this account:     +10 points
//   - Total maximum:                          100 points
//
// Decision thresholds:
//   - Score >= 80:  auto-approve (high confidence)
//   - Score 50-79:  delay 60 seconds then approve (medium confidence)
//   - Score < 50:   require manual admin approval (low confidence)
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/services/parser/internal/banks"
	"github.com/farritpcz/richpayment/services/parser/internal/repository"
)

// ------------------- Confidence Score Constants -------------------
// These constants define the point values for each signal in the
// anti-spoofing confidence scoring system. They are separated as
// constants so that they can be tuned without modifying logic.

const (
	// scoreSenderMatch is awarded when the SMS sender number matches
	// a known bank sender in the ValidSenders() list. This is the first
	// line of defence: if the sender is unknown, the SMS gets 0 here.
	scoreSenderMatch = 30

	// scoreFormatMatch is awarded when the SMS body matches the bank's
	// expected regex template exactly (i.e. parser.CanParse returns true
	// AND parser.Parse succeeds without error). A spoofed SMS often has
	// subtle formatting differences that cause the parse to fail.
	scoreFormatMatch = 30

	// scoreAmountMatch is awarded when the parsed amount exactly matches
	// a pending deposit order for the same bank account. This is a strong
	// signal because the attacker would need to know the exact adjusted
	// amount assigned to a real pending order.
	scoreAmountMatch = 20

	// scoreTimestampOK is awarded when the SMS timestamp (receivedAt) is
	// within the acceptable freshness window (maxSMSAge = 5 minutes).
	// Old or replayed SMS messages score 0 here.
	scoreTimestampOK = 10

	// scoreNoAnomalies is awarded when there are no recent rate anomalies
	// detected on the bank account (i.e. the account has NOT been flagged
	// by the anomaly detector in the last 5 minutes). If the account is
	// under a rate anomaly alert, this score is withheld.
	scoreNoAnomalies = 10
)

// ------------------- Decision Thresholds -------------------
// These thresholds determine the action taken based on the total
// confidence score. They can be overridden via AntiSpoofConfig.

const (
	// defaultAutoApproveThreshold is the minimum score for instant auto-approval.
	// SMS messages scoring at or above this threshold are considered safe.
	defaultAutoApproveThreshold = 80

	// defaultDelayApproveThreshold is the minimum score for delayed approval.
	// SMS messages scoring between this value and autoApproveThreshold are
	// held for a delay period (default 60s) before being approved.
	defaultDelayApproveThreshold = 50
)

// ------------------- Configuration -------------------

// AntiSpoofConfig holds tunable parameters for the anti-spoofing system.
// All fields have sensible defaults; callers only need to override what
// they want to customise.
type AntiSpoofConfig struct {
	// AutoApproveThreshold is the minimum confidence score for instant
	// auto-approval. Default: 80.
	AutoApproveThreshold int

	// DelayApproveThreshold is the minimum score for delayed approval.
	// Scores between this and AutoApproveThreshold trigger a delay.
	// Default: 50.
	DelayApproveThreshold int

	// DelayDuration is how long to hold a medium-confidence deposit in
	// the "pending_verification" state before auto-approving it.
	// Default: 60 seconds.
	DelayDuration time.Duration

	// ManualApprovalAmountThreshold is the amount (in THB) above which
	// deposits always require manual admin approval, regardless of
	// confidence score. Default: 50,000 THB.
	ManualApprovalAmountThreshold decimal.Decimal

	// VerificationDelay is the base delay applied to ALL deposits before
	// they transition from "pending_verification" to confirmed. This
	// gives time for cross-validation checks. Default: 30 seconds.
	VerificationDelay time.Duration
}

// DefaultAntiSpoofConfig returns an AntiSpoofConfig with production-safe
// default values. Callers can override individual fields after construction.
//
// Returns:
//   - A fully populated AntiSpoofConfig with default thresholds and durations.
func DefaultAntiSpoofConfig() AntiSpoofConfig {
	return AntiSpoofConfig{
		AutoApproveThreshold:          defaultAutoApproveThreshold,
		DelayApproveThreshold:         defaultDelayApproveThreshold,
		DelayDuration:                 60 * time.Second,
		ManualApprovalAmountThreshold: decimal.NewFromInt(50000),
		VerificationDelay:             30 * time.Second,
	}
}

// ------------------- Confidence Score Result -------------------

// ConfidenceResult holds the detailed breakdown of an SMS confidence
// evaluation. It includes the total score, per-signal scores, and the
// recommended action to take for this deposit.
type ConfidenceResult struct {
	// TotalScore is the sum of all individual signal scores (0-100).
	TotalScore int

	// SenderScore is the points awarded for sender number validation.
	SenderScore int

	// FormatScore is the points awarded for SMS format/template matching.
	FormatScore int

	// AmountScore is the points awarded for matching a pending order amount.
	AmountScore int

	// TimestampScore is the points awarded for timestamp freshness.
	TimestampScore int

	// AnomalyScore is the points awarded for absence of rate anomalies.
	AnomalyScore int

	// Action is the recommended action based on the total score and amount
	// thresholds. Possible values: "auto_approve", "delay_approve",
	// "manual_approval".
	Action string

	// RequiresManualApproval is true when the deposit amount exceeds the
	// manual approval threshold OR the confidence score is below the
	// delay approval threshold. When true, the deposit must be reviewed
	// by an admin before being completed.
	RequiresManualApproval bool

	// DelaySeconds is the number of seconds to hold the deposit in
	// "pending_verification" state before auto-approving. This is 0
	// for auto-approve, the configured delay for medium confidence,
	// and 0 for manual approval (since a human will decide).
	DelaySeconds int
}

// ------------------- Anti-Spoof Evaluator -------------------

// AntiSpoofEvaluator evaluates incoming SMS messages for spoofing risk
// by calculating a multi-signal confidence score. It coordinates between
// the bank parser registry, Redis (for pending orders and anomaly flags),
// and the configured thresholds to produce an actionable recommendation.
type AntiSpoofEvaluator struct {
	// config holds the tunable thresholds and timing parameters.
	config AntiSpoofConfig

	// rdb is the Redis client used to check pending orders and anomaly flags.
	rdb *redis.Client

	// orderMatcher queries Redis for pending orders to verify amount matches.
	orderMatcher repository.OrderMatcher

	// logger is the structured logger for anti-spoofing evaluations.
	logger *slog.Logger
}

// NewAntiSpoofEvaluator constructs an AntiSpoofEvaluator with all required
// dependencies.
//
// Parameters:
//   - config:       tunable thresholds and timing parameters.
//   - rdb:          Redis client for anomaly flag lookups.
//   - orderMatcher: Redis-backed order matcher for amount verification.
//   - logger:       structured logger for operational visibility.
//
// Returns:
//   - A fully initialised AntiSpoofEvaluator ready to evaluate SMS messages.
func NewAntiSpoofEvaluator(
	config AntiSpoofConfig,
	rdb *redis.Client,
	orderMatcher repository.OrderMatcher,
	logger *slog.Logger,
) *AntiSpoofEvaluator {
	return &AntiSpoofEvaluator{
		config:       config,
		rdb:          rdb,
		orderMatcher: orderMatcher,
		logger:       logger,
	}
}

// Evaluate calculates the anti-spoofing confidence score for an incoming SMS.
// It checks five independent signals and sums their scores to produce a
// total confidence value. Based on the total score and the deposit amount,
// it recommends one of three actions: auto-approve, delay-approve, or
// manual-approval.
//
// Parameters:
//   - ctx:           request-scoped context for Redis operations and cancellation.
//   - senderNumber:  the SMS sender phone number or alphanumeric ID.
//   - rawMessage:    the full, unmodified SMS body text.
//   - receivedAt:    when the SMS gateway received the message.
//   - parsedAmount:  the transaction amount extracted from the SMS by the bank parser.
//   - bankAccountID: the internal UUID of the bank account that received the transfer.
//   - parseSuccess:  true if the bank parser successfully extracted data from the SMS.
//
// Returns:
//   - A ConfidenceResult with detailed score breakdown and recommended action.
func (e *AntiSpoofEvaluator) Evaluate(
	ctx context.Context,
	senderNumber string,
	rawMessage string,
	receivedAt time.Time,
	parsedAmount decimal.Decimal,
	bankAccountID uuid.UUID,
	parseSuccess bool,
) *ConfidenceResult {
	result := &ConfidenceResult{}

	// --- Signal 1: Sender number validation ---
	// Check if the sender number is in the known bank senders list.
	// A spoofed SMS from an unknown number gets 0 points here.
	if banks.IsKnownSender(senderNumber) {
		result.SenderScore = scoreSenderMatch
		e.logger.Debug("anti-spoof: sender number is known",
			"sender", senderNumber,
			"score", scoreSenderMatch,
		)
	} else {
		e.logger.Warn("anti-spoof: sender number is NOT known",
			"sender", senderNumber,
			"score", 0,
		)
	}

	// --- Signal 2: SMS format/template match ---
	// If the bank parser could successfully parse the SMS (extracting amount,
	// sender, reference, and timestamp), the format matches the bank's template.
	// A spoofed SMS with incorrect formatting will fail parsing and score 0.
	if parseSuccess {
		result.FormatScore = scoreFormatMatch
		e.logger.Debug("anti-spoof: SMS format matches bank template",
			"score", scoreFormatMatch,
		)
	} else {
		e.logger.Warn("anti-spoof: SMS format does NOT match bank template",
			"score", 0,
		)
	}

	// --- Signal 3: Amount matches a pending order ---
	// Check if the parsed amount exactly matches a pending deposit order
	// for this bank account. An attacker would need to guess the exact
	// adjusted amount (which includes a random satang offset) to score here.
	if !parsedAmount.IsZero() {
		pendingOrder, err := e.orderMatcher.FindPendingOrder(ctx, bankAccountID, parsedAmount)
		if err == nil && pendingOrder != nil {
			result.AmountScore = scoreAmountMatch
			e.logger.Debug("anti-spoof: amount matches pending order",
				"amount", parsedAmount.String(),
				"order_id", pendingOrder.OrderID,
				"score", scoreAmountMatch,
			)
		} else {
			e.logger.Debug("anti-spoof: amount does NOT match any pending order",
				"amount", parsedAmount.String(),
				"score", 0,
			)
		}
	}

	// --- Signal 4: Timestamp freshness ---
	// Verify the SMS timestamp is recent (within maxSMSAge). This catches
	// replay attacks where an old bank notification is re-sent. The check
	// also rejects timestamps in the future (clock skew or forgery).
	age := time.Since(receivedAt)
	if age >= 0 && age <= maxSMSAge {
		result.TimestampScore = scoreTimestampOK
		e.logger.Debug("anti-spoof: timestamp is within acceptable window",
			"age", age.String(),
			"score", scoreTimestampOK,
		)
	} else {
		e.logger.Warn("anti-spoof: timestamp is outside acceptable window",
			"age", age.String(),
			"score", 0,
		)
	}

	// --- Signal 5: No recent anomalies on this bank account ---
	// Check Redis for a rate anomaly flag on this bank account. The anomaly
	// detector sets this flag when it detects an unusual SMS rate (e.g. >10
	// SMS per minute). If the flag exists, the account is under suspicion
	// and this signal scores 0.
	anomalyKey := fmt.Sprintf("sms_anomaly:%s", bankAccountID.String())
	anomalyExists, err := e.rdb.Exists(ctx, anomalyKey).Result()
	if err != nil {
		// If Redis fails, be conservative and withhold the score.
		e.logger.Error("anti-spoof: failed to check anomaly flag in Redis",
			"bank_account_id", bankAccountID,
			"error", err,
		)
	} else if anomalyExists == 0 {
		// No anomaly flag exists — the account is clean.
		result.AnomalyScore = scoreNoAnomalies
		e.logger.Debug("anti-spoof: no recent anomalies on account",
			"bank_account_id", bankAccountID,
			"score", scoreNoAnomalies,
		)
	} else {
		// Anomaly flag exists — withhold points.
		e.logger.Warn("anti-spoof: anomaly flag detected on account",
			"bank_account_id", bankAccountID,
			"score", 0,
		)
	}

	// --- Calculate total score ---
	result.TotalScore = result.SenderScore +
		result.FormatScore +
		result.AmountScore +
		result.TimestampScore +
		result.AnomalyScore

	// --- Determine action based on score and amount ---
	// High-value deposits always require manual approval regardless of score.
	if parsedAmount.GreaterThanOrEqual(e.config.ManualApprovalAmountThreshold) {
		result.Action = "manual_approval"
		result.RequiresManualApproval = true
		result.DelaySeconds = 0
		e.logger.Info("anti-spoof: high-value deposit requires manual approval",
			"amount", parsedAmount.String(),
			"threshold", e.config.ManualApprovalAmountThreshold.String(),
			"total_score", result.TotalScore,
		)
		return result
	}

	// Apply score-based decision thresholds.
	switch {
	case result.TotalScore >= e.config.AutoApproveThreshold:
		// High confidence — auto-approve after the base verification delay.
		result.Action = "auto_approve"
		result.RequiresManualApproval = false
		result.DelaySeconds = int(e.config.VerificationDelay.Seconds())
		e.logger.Info("anti-spoof: high confidence, auto-approve with verification delay",
			"total_score", result.TotalScore,
			"delay_seconds", result.DelaySeconds,
		)

	case result.TotalScore >= e.config.DelayApproveThreshold:
		// Medium confidence — delay longer before approving.
		result.Action = "delay_approve"
		result.RequiresManualApproval = false
		result.DelaySeconds = int(e.config.DelayDuration.Seconds())
		e.logger.Info("anti-spoof: medium confidence, delay before approval",
			"total_score", result.TotalScore,
			"delay_seconds", result.DelaySeconds,
		)

	default:
		// Low confidence — require manual admin approval.
		result.Action = "manual_approval"
		result.RequiresManualApproval = true
		result.DelaySeconds = 0
		e.logger.Warn("anti-spoof: low confidence, manual approval required",
			"total_score", result.TotalScore,
		)
	}

	return result
}

// StorePendingVerification saves a matched deposit into Redis with a
// "pending_verification" state. The deposit will remain in this state
// for the specified delay duration before being eligible for completion.
// This gives time for cross-validation and anomaly detection to flag
// suspicious transactions before they are finalised.
//
// The Redis key format is: "pending_verification:{sms_id}"
// The value is a JSON-serialisable struct containing all deposit details.
// A TTL is set so that stale entries are automatically cleaned up.
//
// Parameters:
//   - ctx:          request-scoped context for Redis operations.
//   - smsID:        the UUID of the SMS record in the database.
//   - orderID:      the UUID of the matched deposit order.
//   - bankAccountID: the internal bank account UUID.
//   - amount:       the parsed transfer amount.
//   - score:        the confidence score from the anti-spoof evaluation.
//   - delaySeconds: how many seconds to hold before allowing completion.
//
// Returns:
//   - An error if the Redis SET operation fails.
func (e *AntiSpoofEvaluator) StorePendingVerification(
	ctx context.Context,
	smsID uuid.UUID,
	orderID uuid.UUID,
	bankAccountID uuid.UUID,
	amount decimal.Decimal,
	score int,
	delaySeconds int,
) error {
	// Build the Redis key for this pending verification entry.
	key := fmt.Sprintf("pending_verification:%s", smsID.String())

	// Store a pipe-delimited value with all relevant deposit details.
	// Format: "order_id|bank_account_id|amount|score|created_at"
	// Using a simple string format instead of JSON to minimise dependencies
	// and keep Redis storage lightweight.
	value := fmt.Sprintf("%s|%s|%s|%d|%d",
		orderID.String(),
		bankAccountID.String(),
		amount.String(),
		score,
		time.Now().Unix(),
	)

	// Set with TTL = delay duration + 5 minute grace period.
	// The grace period ensures the key persists long enough for the
	// verification worker to process it, even if the worker is slightly delayed.
	ttl := time.Duration(delaySeconds)*time.Second + 5*time.Minute

	if err := e.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		e.logger.Error("failed to store pending verification in Redis",
			"sms_id", smsID,
			"order_id", orderID,
			"error", err,
		)
		return fmt.Errorf("store pending verification: %w", err)
	}

	e.logger.Info("deposit stored in pending_verification state",
		"sms_id", smsID,
		"order_id", orderID,
		"score", score,
		"delay_seconds", delaySeconds,
		"redis_key", key,
	)

	return nil
}
