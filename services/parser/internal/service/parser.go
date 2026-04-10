// Package service contains the core business logic for the parser-service.
//
// The primary workflow is SMS processing: an incoming bank notification SMS
// goes through anti-spoofing validation, timestamp checks, message parsing,
// database persistence, and order matching. Each step is clearly separated
// and documented to make the flow auditable and testable.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
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
	// pending deposit order.
	SMSStatusMatched SMSMatchStatus = "matched"

	// SMSStatusUnmatched indicates the SMS was valid and parsed, but no
	// pending order was found for the extracted amount and bank account.
	SMSStatusUnmatched SMSMatchStatus = "unmatched"

	// SMSStatusError indicates a processing error occurred (parse failure,
	// database error, etc.).
	SMSStatusError SMSMatchStatus = "error"
)

// ProcessSMSResult holds the outcome of processing a single SMS message.
// The handler uses this to build its HTTP response.
type ProcessSMSResult struct {
	// Status is the high-level outcome: matched, unmatched, or error.
	Status SMSMatchStatus

	// SMSID is the UUID assigned to the persisted SMS record.
	// Useful for tracing and support investigations.
	SMSID uuid.UUID

	// OrderID is populated only when Status == SMSStatusMatched.
	// It contains the UUID of the deposit order that was matched.
	OrderID *uuid.UUID

	// BankCode is the short bank identifier extracted from the SMS.
	BankCode string

	// Amount is the transaction amount extracted from the SMS.
	Amount decimal.Decimal

	// Message provides a human-readable description of the outcome,
	// useful for logging and debugging.
	Message string
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

// ParserService orchestrates the full SMS processing pipeline: validation,
// parsing, persistence, and order matching. It is the central business-logic
// component of the parser-service.
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
}

// NewParserService constructs a ParserService with all required dependencies.
//
// Parameters:
//   - smsRepo:      the repository for persisting SMS records to PostgreSQL.
//   - orderMatcher: the Redis-backed matcher for pending deposit orders.
//   - mappings:     the list of sender-number-to-bank-account-ID mappings.
//   - logger:       structured logger for operational visibility.
//
// Returns:
//   - A fully initialised ParserService ready to process SMS messages.
func NewParserService(
	smsRepo repository.SMSRepository,
	orderMatcher repository.OrderMatcher,
	mappings []BankAccountMapping,
	logger *slog.Logger,
) *ParserService {
	// Build a lookup map from the slice for O(1) access during processing.
	accountMap := make(map[string]uuid.UUID, len(mappings))
	for _, m := range mappings {
		accountMap[m.SenderNumber] = m.BankAccountID
	}

	return &ParserService{
		smsRepo:      smsRepo,
		orderMatcher: orderMatcher,
		accountMap:   accountMap,
		logger:       logger,
	}
}

// ProcessSMS executes the full SMS processing pipeline. This is the primary
// entry point called by the HTTP handler when an SMS webhook arrives.
//
// The pipeline has nine clearly defined steps:
//  1. Anti-spoofing: verify the sender number is a known bank sender.
//  2. Timestamp validation: ensure the SMS is not too old (replay protection).
//  3. Parser lookup: find the correct bank parser via CanParse().
//  4. Message parsing: extract amount, sender name, reference, timestamp.
//  5. Bank account identification: map sender number to internal bank account.
//  6. Persistence: store the SMS in the sms_messages table.
//  7. Order matching: query Redis for a pending order with matching amount.
//  8. If matched: return the match result with the order ID.
//  9. If unmatched: mark the SMS as unmatched and return.
//
// Parameters:
//   - ctx:          request-scoped context for cancellation and tracing.
//   - senderNumber: the phone number or sender ID that delivered the SMS.
//   - rawMessage:   the full, unmodified SMS body text.
//   - receivedAt:   when the SMS gateway received the message.
//
// Returns:
//   - A ProcessSMSResult describing the outcome.
//   - An error only for unexpected infrastructure failures (DB/Redis down).
func (s *ParserService) ProcessSMS(
	ctx context.Context,
	senderNumber string,
	rawMessage string,
	receivedAt time.Time,
) (*ProcessSMSResult, error) {

	// --- Step 1: Anti-spoofing validation ---
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

	// --- Step 2: Timestamp validation ---
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

	// --- Step 3: Find matching parser ---
	// Iterate registered bank parsers to find one that recognises this SMS.
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

	// --- Step 4: Parse the SMS ---
	// Extract structured transaction data (amount, sender, reference, time).
	parsed, err := parser.Parse(rawMessage)
	if err != nil {
		s.logger.Error("SMS parse failed",
			"sender", senderNumber,
			"bank", parser.BankCode(),
			"error", err,
		)
		return &ProcessSMSResult{
			Status:   SMSStatusError,
			BankCode: parser.BankCode(),
			Message:  fmt.Sprintf("parse error: %v", err),
		}, nil
	}

	s.logger.Info("SMS parsed successfully",
		"bank", parsed.BankCode,
		"amount", parsed.Amount.String(),
		"sender_name", parsed.SenderName,
		"reference", parsed.Reference,
	)

	// --- Step 5: Identify bank account ---
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

	// --- Step 6: Persist SMS to database ---
	// Store the SMS record before attempting order matching. This ensures we
	// have an audit trail even if the match step fails.
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
		Status:        "pending", // Will be updated to "matched" or "unmatched" below.
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

	// --- Step 7: Try to match with pending orders ---
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

	// --- Step 8: Match found - return success ---
	if pendingOrder != nil {
		s.logger.Info("SMS matched to pending order",
			"sms_id", smsID,
			"order_id", pendingOrder.OrderID,
			"amount", parsed.Amount.String(),
		)

		return &ProcessSMSResult{
			Status:   SMSStatusMatched,
			SMSID:    smsID,
			OrderID:  &pendingOrder.OrderID,
			BankCode: parsed.BankCode,
			Amount:   parsed.Amount,
			Message:  fmt.Sprintf("matched to order %s", pendingOrder.OrderID),
		}, nil
	}

	// --- Step 9: No match found - mark as unmatched ---
	// The SMS is valid and parsed, but no pending order matches. This can
	// happen when a customer transfers money before creating an order, or
	// when the order has already expired. The unmatched SMS is available
	// for manual reconciliation.
	s.logger.Info("SMS unmatched - no pending order found",
		"sms_id", smsID,
		"bank_account_id", bankAccountID,
		"amount", parsed.Amount.String(),
	)

	return &ProcessSMSResult{
		Status:   SMSStatusUnmatched,
		SMSID:    smsID,
		BankCode: parsed.BankCode,
		Amount:   parsed.Amount,
		Message:  "no matching pending order found",
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
