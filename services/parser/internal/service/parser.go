// Package service contains the core business logic for the parser-service.
//
// The primary workflow is SMS processing: an incoming bank notification SMS
// goes through duplicate detection, anti-spoofing validation, rate anomaly
// checking, timestamp checks, message parsing, confidence scoring, database
// persistence, order matching, and pending verification storage. Each step
// is clearly separated and documented to make the flow auditable and testable.
//
// SECURITY MEASURES (anti-SMS-spoofing):
//
// 1. DUPLICATE SMS DETECTION — Every SMS is hashed (SHA-256 of body + sender
//    + timestamp) and checked in Redis before processing. Prevents replay attacks.
//
// 2. SMS RATE ANOMALY DETECTION — Tracks SMS count per bank account per minute.
//    If >10 SMS arrive in 1 minute, flags the account as suspicious, alerts admin
//    via Telegram, and reduces confidence score for subsequent messages.
//
// 3. ANTI-SPOOFING CONFIDENCE SCORE — Multi-signal scoring system (0-100 points)
//    that evaluates sender validity, format match, amount match, timestamp
//    freshness, and anomaly status. Score determines action:
//      >= 80: auto-approve with 30s verification delay
//      50-79: delay 60s then approve
//      < 50:  require manual admin approval
//
// 4. DEPOSIT VERIFICATION DELAY — All matched deposits are stored in a
//    "pending_verification" state in Redis before being completed. This gives
//    time for cross-validation checks.
//
// 5. AMOUNT THRESHOLD FOR MANUAL APPROVAL — Deposits above a configurable
//    threshold (default 50,000 THB) always require manual admin approval,
//    regardless of confidence score.
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

// maxSMSAge is the maximum allowed age of an SMS message. If the time
// between when the SMS gateway received the message and the current time
// exceeds this threshold, the SMS is rejected. This prevents replay attacks
// where an attacker resends old bank notifications to trigger duplicate
// credits. Five minutes is generous enough to handle normal gateway delays
// but tight enough to block replays.
const maxSMSAge = 5 * time.Minute

// SMSMatchStatus represents the outcome of processing a single SMS message.
// It is returned to the HTTP handler so it can produce an appropriate response.
type SMSMatchStatus string

const (
	// SMSStatusMatched indicates the SMS was successfully matched to a
	// pending deposit order. The deposit may still be in "pending_verification"
	// state depending on the confidence score.
	SMSStatusMatched SMSMatchStatus = "matched"

	// SMSStatusUnmatched indicates the SMS was valid and parsed, but no
	// pending order was found for the extracted amount and bank account.
	SMSStatusUnmatched SMSMatchStatus = "unmatched"

	// SMSStatusError indicates a processing error occurred (parse failure,
	// database error, duplicate detected, etc.).
	SMSStatusError SMSMatchStatus = "error"

	// SMSStatusDuplicate indicates the SMS was detected as a duplicate
	// (same body + sender + timestamp already processed). Separated from
	// SMSStatusError for clearer metrics and logging.
	SMSStatusDuplicate SMSMatchStatus = "duplicate"

	// SMSStatusPendingVerification indicates the SMS was matched but is
	// being held in a verification delay before completion. This status
	// is used when the confidence score triggers a delay or when the
	// deposit amount exceeds the manual approval threshold.
	SMSStatusPendingVerification SMSMatchStatus = "pending_verification"

	// SMSStatusManualApproval indicates the deposit requires manual
	// admin review before it can be completed. Triggered by low confidence
	// score or high deposit amount.
	SMSStatusManualApproval SMSMatchStatus = "manual_approval"

	// SMSStatusAnomalyDetected indicates a rate anomaly was found on
	// the bank account. The SMS is still processed but flagged.
	SMSStatusAnomalyDetected SMSMatchStatus = "anomaly_detected"
)

// ProcessSMSResult holds the outcome of processing a single SMS message.
// The handler uses this to build its HTTP response.
type ProcessSMSResult struct {
	// Status is the high-level outcome: matched, unmatched, error, duplicate,
	// pending_verification, or manual_approval.
	Status SMSMatchStatus

	// SMSID is the UUID assigned to the persisted SMS record.
	// Useful for tracing and support investigations.
	SMSID uuid.UUID

	// OrderID is populated only when Status == SMSStatusMatched or
	// SMSStatusPendingVerification. It contains the UUID of the deposit
	// order that was matched.
	OrderID *uuid.UUID

	// BankCode is the short bank identifier extracted from the SMS.
	BankCode string

	// Amount is the transaction amount extracted from the SMS.
	Amount decimal.Decimal

	// Message provides a human-readable description of the outcome,
	// useful for logging and debugging.
	Message string

	// ConfidenceScore is the anti-spoofing confidence score (0-100) assigned
	// to this SMS. Only populated after the confidence evaluation step.
	ConfidenceScore int

	// RequiresManualApproval is true when the deposit needs admin review.
	// This can be triggered by a low confidence score or a high deposit amount.
	RequiresManualApproval bool

	// VerificationDelaySeconds is the number of seconds the deposit is held
	// in "pending_verification" before auto-completion. Zero means no delay
	// (manual approval or error).
	VerificationDelaySeconds int

	// AnomalyDetected is true when an SMS rate anomaly was detected on the
	// bank account during processing of this SMS. The SMS is still processed
	// but the confidence score is reduced.
	AnomalyDetected bool
}

// BankAccountMapping maps a bank sender phone number to an internal bank
// account UUID. The parser-service uses this to determine which of the
// merchant's bank accounts received the transfer. In production this would
// be loaded from a database or configuration service; here it is injected
// at construction time.
type BankAccountMapping struct {
	// SenderNumber is the phone number or alphanumeric sender ID (e.g. "KBANK").
	SenderNumber string

	// BankAccountID is the internal UUID of the bank account associated
	// with this sender number.
	BankAccountID uuid.UUID
}

// ParserService orchestrates the full SMS processing pipeline: duplicate
// detection, anti-spoofing validation, anomaly checking, parsing, persistence,
// order matching, confidence scoring, and pending verification storage.
// It is the central business-logic component of the parser-service.
type ParserService struct {
	// smsRepo handles persistent storage of SMS messages in PostgreSQL.
	smsRepo repository.SMSRepository

	// orderMatcher queries Redis for pending orders to match against.
	orderMatcher repository.OrderMatcher

	// accountMap provides sender-number-to-bank-account-ID lookups.
	// Keyed by normalised sender number string.
	accountMap map[string]uuid.UUID

	// logger is the structured logger for this service instance.
	logger *slog.Logger

	// antiSpoof is the confidence scoring evaluator that determines the
	// trust level of each incoming SMS and the appropriate action to take.
	antiSpoof *AntiSpoofEvaluator

	// anomalyDetector provides duplicate SMS detection and SMS rate
	// anomaly monitoring via Redis.
	anomalyDetector *AnomalyDetector

	// rdb is the Redis client used for pending verification storage.
	// Shared with antiSpoof and anomalyDetector but kept as a direct
	// reference for operations specific to the parser pipeline.
	rdb *redis.Client
}

// NewParserService constructs a ParserService with all required dependencies,
// including the anti-spoofing evaluator and anomaly detector.
//
// Parameters:
//   - smsRepo:         the repository for persisting SMS records to PostgreSQL.
//   - orderMatcher:    the Redis-backed matcher for pending deposit orders.
//   - mappings:        the list of sender-number-to-bank-account-ID mappings.
//   - logger:          structured logger for operational visibility.
//   - rdb:             Redis client for anti-spoofing, anomaly detection, and
//                      pending verification storage.
//   - antiSpoofCfg:    configuration for the anti-spoofing confidence scoring system.
//   - anomalyCfg:      configuration for the anomaly detection system (rate limit,
//                      Telegram credentials).
//
// Returns:
//   - A fully initialised ParserService ready to process SMS messages with
//     all security measures active.
func NewParserService(
	smsRepo repository.SMSRepository,
	orderMatcher repository.OrderMatcher,
	mappings []BankAccountMapping,
	logger *slog.Logger,
	rdb *redis.Client,
	antiSpoofCfg AntiSpoofConfig,
	anomalyCfg AnomalyConfig,
) *ParserService {
	// Build a lookup map from the slice for O(1) access during processing.
	accountMap := make(map[string]uuid.UUID, len(mappings))
	for _, m := range mappings {
		accountMap[m.SenderNumber] = m.BankAccountID
	}

	// Create the anti-spoofing evaluator with its own logger context.
	antiSpoof := NewAntiSpoofEvaluator(
		antiSpoofCfg,
		rdb,
		orderMatcher,
		logger.With("component", "antispoof"),
	)

	// Create the anomaly detector with its own logger context.
	anomalyDetector := NewAnomalyDetector(
		rdb,
		anomalyCfg,
		logger.With("component", "anomaly"),
	)

	return &ParserService{
		smsRepo:         smsRepo,
		orderMatcher:    orderMatcher,
		accountMap:      accountMap,
		logger:          logger,
		antiSpoof:       antiSpoof,
		anomalyDetector: anomalyDetector,
		rdb:             rdb,
	}
}

// ProcessSMS executes the full SMS processing pipeline with anti-spoofing
// security measures. This is the primary entry point called by the HTTP
// handler when an SMS webhook arrives.
//
// The security-enhanced pipeline has twelve clearly defined steps:
//
//  1.  DUPLICATE DETECTION: hash the SMS and check Redis to prevent replay.
//  2.  ANTI-SPOOFING: verify the sender number is a known bank sender.
//  3.  TIMESTAMP VALIDATION: ensure the SMS is not too old (replay protection).
//  4.  PARSER LOOKUP: find the correct bank parser via CanParse().
//  5.  MESSAGE PARSING: extract amount, sender name, reference, timestamp.
//  6.  BANK ACCOUNT IDENTIFICATION: map sender number to internal bank account.
//  7.  RATE ANOMALY CHECK: track SMS rate per account and flag anomalies.
//  8.  PERSISTENCE: store the SMS in the sms_messages table.
//  9.  ORDER MATCHING: query Redis for a pending order with matching amount.
//  10. CONFIDENCE SCORING: evaluate anti-spoofing score (0-100 points).
//  11. ACTION DETERMINATION: auto-approve / delay / manual approval.
//  12. PENDING VERIFICATION: store in Redis with appropriate delay.
//
// Parameters:
//   - ctx:          request-scoped context for cancellation and tracing.
//   - senderNumber: the phone number or sender ID that delivered the SMS.
//   - rawMessage:   the full, unmodified SMS body text.
//   - receivedAt:   when the SMS gateway received the message.
//
// Returns:
//   - A ProcessSMSResult describing the outcome including confidence score.
//   - An error only for unexpected infrastructure failures (DB/Redis down).
func (s *ParserService) ProcessSMS(
	ctx context.Context,
	senderNumber string,
	rawMessage string,
	receivedAt time.Time,
) (*ProcessSMSResult, error) {

	// --- Step 1: Duplicate SMS Detection ---
	// Hash the full SMS body + sender + timestamp and check Redis.
	// This prevents the same SMS from being processed twice, blocking
	// replay attacks where an attacker re-sends a captured bank notification.
	isDuplicate, err := s.anomalyDetector.IsDuplicate(ctx, senderNumber, rawMessage, receivedAt)
	if err != nil {
		// Redis failure during duplicate check — log and continue processing.
		// We prefer to risk processing a duplicate over rejecting a legitimate SMS.
		s.logger.Error("duplicate check failed, continuing processing",
			"sender", senderNumber,
			"error", err,
		)
	} else if isDuplicate {
		// The same SMS has already been processed — reject immediately.
		s.logger.Warn("DUPLICATE SMS REJECTED",
			"sender", senderNumber,
			"received_at", receivedAt,
		)
		return &ProcessSMSResult{
			Status:  SMSStatusDuplicate,
			Message: "duplicate SMS detected: this message has already been processed",
		}, nil
	}

	// --- Step 2: Anti-spoofing validation ---
	// Check that the sender number belongs to a known bank. This prevents
	// attackers from sending fake bank-formatted SMSes from random numbers.
	if !banks.IsKnownSender(senderNumber) {
		s.logger.Warn("anti-spoofing: unknown sender number rejected",
			"sender", senderNumber,
		)
		return &ProcessSMSResult{
			Status:  SMSStatusError,
			Message: fmt.Sprintf("unknown sender number: %s", senderNumber),
		}, nil
	}

	// --- Step 3: Timestamp validation ---
	// Reject SMSes that are too old. This prevents replay attacks where an
	// attacker resends a previously captured bank notification SMS to
	// trigger a duplicate credit. The 5-minute window accounts for normal
	// SMS gateway delivery delays.
	age := time.Since(receivedAt)
	if age > maxSMSAge {
		s.logger.Warn("timestamp validation: SMS too old",
			"sender", senderNumber,
			"received_at", receivedAt,
			"age", age.String(),
		)
		return &ProcessSMSResult{
			Status:  SMSStatusError,
			Message: fmt.Sprintf("SMS too old: received %s ago (max %s)", age.Round(time.Second), maxSMSAge),
		}, nil
	}

	// Also reject SMSes with timestamps in the future (clock skew protection).
	// Allow up to 1 minute of clock skew to handle minor time differences
	// between the SMS gateway and our server.
	if receivedAt.After(time.Now().Add(1 * time.Minute)) {
		s.logger.Warn("timestamp validation: SMS from the future",
			"sender", senderNumber,
			"received_at", receivedAt,
		)
		return &ProcessSMSResult{
			Status:  SMSStatusError,
			Message: "SMS received_at is in the future",
		}, nil
	}

	// --- Step 4: Find matching parser ---
	// Iterate registered bank parsers to find one that recognises this SMS.
	// The parser is found by checking CanParse() which validates both the
	// sender number and message body keywords.
	parser := banks.FindParser(senderNumber, rawMessage)
	if parser == nil {
		s.logger.Warn("no parser found for SMS",
			"sender", senderNumber,
			"message_preview", truncate(rawMessage, 80),
		)
		return &ProcessSMSResult{
			Status:  SMSStatusError,
			Message: "no parser found for this SMS format",
		}, nil
	}

	// --- Step 5: Parse the SMS ---
	// Extract structured transaction data (amount, sender, reference, time).
	// If parsing fails, the SMS format does not match the bank's template,
	// which is a negative signal for the confidence score.
	parsed, parseErr := parser.Parse(rawMessage)
	parseSuccess := parseErr == nil

	if parseErr != nil {
		s.logger.Error("SMS parse failed",
			"sender", senderNumber,
			"bank", parser.BankCode(),
			"error", parseErr,
		)
		return &ProcessSMSResult{
			Status:   SMSStatusError,
			BankCode: parser.BankCode(),
			Message:  fmt.Sprintf("parse error: %v", parseErr),
		}, nil
	}

	s.logger.Info("SMS parsed successfully",
		"bank", parsed.BankCode,
		"amount", parsed.Amount.String(),
		"sender_name", parsed.SenderName,
		"reference", parsed.Reference,
	)

	// --- Step 6: Identify bank account ---
	// Map the SMS sender number to the internal bank account UUID. This tells
	// us which of the merchant's bank accounts received the money.
	bankAccountID, ok := s.accountMap[senderNumber]
	if !ok {
		// Sender number is known to a parser but not mapped to a bank account.
		// This is a configuration issue, not a security issue.
		s.logger.Error("sender number not mapped to bank account",
			"sender", senderNumber,
			"bank", parsed.BankCode,
		)
		return &ProcessSMSResult{
			Status:   SMSStatusError,
			BankCode: parsed.BankCode,
			Amount:   parsed.Amount,
			Message:  fmt.Sprintf("no bank account mapping for sender %s", senderNumber),
		}, nil
	}

	// --- Step 7: Rate Anomaly Detection ---
	// Track SMS count per bank account per minute. If more than 10 SMS
	// arrive in 1 minute for the same account, flag as suspicious.
	// The anomaly flag is stored in Redis and checked by the confidence
	// scorer to reduce the trust score for this and subsequent messages.
	anomalyDetected := false
	isAnomaly, anomalyErr := s.anomalyDetector.CheckRateAnomaly(ctx, bankAccountID, senderNumber)
	if anomalyErr != nil {
		// Redis failure during anomaly check — log and continue.
		// We don't want Redis failures to block SMS processing.
		s.logger.Error("rate anomaly check failed, continuing processing",
			"bank_account_id", bankAccountID,
			"error", anomalyErr,
		)
	} else if isAnomaly {
		anomalyDetected = true
		s.logger.Warn("SMS rate anomaly detected on bank account",
			"bank_account_id", bankAccountID,
			"sender", senderNumber,
		)
	}

	// --- Step 8: Persist SMS to database ---
	// Store the SMS record before attempting order matching. This ensures we
	// have an audit trail even if the match step fails. The status is set to
	// "pending" initially and will be updated based on the confidence score.
	smsID := uuid.New()
	smsRecord := &repository.SMSMessage{
		ID:            smsID,
		BankAccountID: bankAccountID,
		BankCode:      parsed.BankCode,
		SenderNumber:  senderNumber,
		Amount:        parsed.Amount,
		SenderName:    parsed.SenderName,
		Reference:     parsed.Reference,
		RawMessage:    rawMessage,
		Status:        "pending", // Will be updated based on confidence score below.
		ReceivedAt:    receivedAt,
		CreatedAt:     time.Now(),
	}

	if err := s.smsRepo.Store(ctx, smsRecord); err != nil {
		s.logger.Error("failed to store SMS record",
			"sms_id", smsID,
			"error", err,
		)
		return nil, fmt.Errorf("store SMS: %w", err)
	}

	// --- Step 9: Try to match with pending orders ---
	// Query Redis for a pending deposit order that matches this bank account
	// and exact amount. The order-service pre-computes unique adjusted amounts
	// to prevent ambiguous matches.
	pendingOrder, err := s.orderMatcher.FindPendingOrder(ctx, bankAccountID, parsed.Amount)
	if err != nil {
		s.logger.Error("order matching query failed",
			"sms_id", smsID,
			"bank_account_id", bankAccountID,
			"amount", parsed.Amount.String(),
			"error", err,
		)
		// Don't fail the whole request - mark as unmatched and continue.
		// The order can be manually matched later.
		return &ProcessSMSResult{
			Status:   SMSStatusUnmatched,
			SMSID:    smsID,
			BankCode: parsed.BankCode,
			Amount:   parsed.Amount,
			Message:  "order matching failed, marked as unmatched",
		}, nil
	}

	// --- Step 10: Anti-Spoofing Confidence Scoring ---
	// Evaluate the SMS against five independent signals to produce a
	// confidence score (0-100). This is the core anti-spoofing mechanism.
	// The score determines whether the deposit is auto-approved, delayed,
	// or requires manual admin approval.
	confidenceResult := s.antiSpoof.Evaluate(
		ctx,
		senderNumber,
		rawMessage,
		receivedAt,
		parsed.Amount,
		bankAccountID,
		parseSuccess,
	)

	s.logger.Info("anti-spoofing confidence score calculated",
		"sms_id", smsID,
		"total_score", confidenceResult.TotalScore,
		"sender_score", confidenceResult.SenderScore,
		"format_score", confidenceResult.FormatScore,
		"amount_score", confidenceResult.AmountScore,
		"timestamp_score", confidenceResult.TimestampScore,
		"anomaly_score", confidenceResult.AnomalyScore,
		"action", confidenceResult.Action,
		"requires_manual_approval", confidenceResult.RequiresManualApproval,
		"delay_seconds", confidenceResult.DelaySeconds,
	)

	// --- Step 11: Handle matched order with confidence-based action ---
	if pendingOrder != nil {
		s.logger.Info("SMS matched to pending order",
			"sms_id", smsID,
			"order_id", pendingOrder.OrderID,
			"amount", parsed.Amount.String(),
			"confidence_score", confidenceResult.TotalScore,
			"action", confidenceResult.Action,
		)

		// --- Step 12: Store in pending verification ---
		// All matched deposits go through a verification delay before being
		// completed. The delay duration depends on the confidence score:
		//   - High confidence (>= 80): 30-second base delay (VerificationDelay)
		//   - Medium confidence (50-79): 60-second delay (DelayDuration)
		//   - Low confidence (< 50): manual approval required (no auto-delay)
		//   - High amount (>= 50,000 THB): manual approval required
		if confidenceResult.RequiresManualApproval {
			// Deposit requires manual admin approval. Store in pending
			// verification with delay=0 (admin will decide).
			storeErr := s.antiSpoof.StorePendingVerification(
				ctx,
				smsID,
				pendingOrder.OrderID,
				bankAccountID,
				parsed.Amount,
				confidenceResult.TotalScore,
				0, // No auto-delay for manual approval.
			)
			if storeErr != nil {
				s.logger.Error("failed to store pending verification for manual approval",
					"sms_id", smsID,
					"order_id", pendingOrder.OrderID,
					"error", storeErr,
				)
			}

			return &ProcessSMSResult{
				Status:                   SMSStatusManualApproval,
				SMSID:                    smsID,
				OrderID:                  &pendingOrder.OrderID,
				BankCode:                 parsed.BankCode,
				Amount:                   parsed.Amount,
				Message:                  fmt.Sprintf("matched order %s requires manual approval (score: %d, action: %s)", pendingOrder.OrderID, confidenceResult.TotalScore, confidenceResult.Action),
				ConfidenceScore:          confidenceResult.TotalScore,
				RequiresManualApproval:   true,
				VerificationDelaySeconds: 0,
				AnomalyDetected:          anomalyDetected,
			}, nil
		}

		// Deposit can be auto-approved after the verification delay.
		// Store in pending verification with the appropriate delay.
		storeErr := s.antiSpoof.StorePendingVerification(
			ctx,
			smsID,
			pendingOrder.OrderID,
			bankAccountID,
			parsed.Amount,
			confidenceResult.TotalScore,
			confidenceResult.DelaySeconds,
		)
		if storeErr != nil {
			s.logger.Error("failed to store pending verification",
				"sms_id", smsID,
				"order_id", pendingOrder.OrderID,
				"error", storeErr,
			)
			// Continue — the deposit will still be matched but without
			// the pending verification record in Redis.
		}

		return &ProcessSMSResult{
			Status:                   SMSStatusPendingVerification,
			SMSID:                    smsID,
			OrderID:                  &pendingOrder.OrderID,
			BankCode:                 parsed.BankCode,
			Amount:                   parsed.Amount,
			Message:                  fmt.Sprintf("matched order %s, pending verification (score: %d, delay: %ds)", pendingOrder.OrderID, confidenceResult.TotalScore, confidenceResult.DelaySeconds),
			ConfidenceScore:          confidenceResult.TotalScore,
			RequiresManualApproval:   false,
			VerificationDelaySeconds: confidenceResult.DelaySeconds,
			AnomalyDetected:          anomalyDetected,
		}, nil
	}

	// --- No match found — mark as unmatched ---
	// The SMS is valid and parsed, but no pending order matches. This can
	// happen when a customer transfers money before creating an order, or
	// when the order has already expired. The unmatched SMS is available
	// for manual reconciliation.
	s.logger.Info("SMS unmatched - no pending order found",
		"sms_id", smsID,
		"bank_account_id", bankAccountID,
		"amount", parsed.Amount.String(),
		"confidence_score", confidenceResult.TotalScore,
	)

	return &ProcessSMSResult{
		Status:          SMSStatusUnmatched,
		SMSID:           smsID,
		BankCode:        parsed.BankCode,
		Amount:          parsed.Amount,
		Message:         "no matching pending order found",
		ConfidenceScore: confidenceResult.TotalScore,
		AnomalyDetected: anomalyDetected,
	}, nil
}

// truncate shortens a string to maxLen characters, appending "..." if
// truncation occurs. This is used for safe logging of potentially long
// SMS message bodies without flooding log storage.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
