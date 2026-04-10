package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// -----------------------------------------------------------------
// Alert severity levels
// -----------------------------------------------------------------

// AlertSeverity represents the severity level of a security alert.
// Higher severity levels trigger more aggressive notification channels
// (e.g. phone calls in addition to messages).
type AlertSeverity string

const (
	// AlertSeverityCritical indicates an event that requires immediate human
	// intervention, such as an emergency freeze or large withdrawal.
	AlertSeverityCritical AlertSeverity = "CRITICAL"

	// AlertSeverityHigh indicates a serious security event that should be
	// investigated promptly, such as repeated login failures.
	AlertSeverityHigh AlertSeverity = "HIGH"

	// AlertSeverityMedium indicates a notable security event that warrants
	// attention but is not immediately dangerous.
	AlertSeverityMedium AlertSeverity = "MEDIUM"

	// AlertSeverityLow indicates an informational security event that is
	// logged for audit purposes.
	AlertSeverityLow AlertSeverity = "LOW"
)

// -----------------------------------------------------------------
// Known alert event types
// -----------------------------------------------------------------

const (
	// EventLoginFailed is triggered when an authentication attempt fails,
	// which may indicate a brute-force attack.
	EventLoginFailed = "login_failed"

	// EventAPIKeyRevoked is triggered when an API key is revoked, either
	// manually by an admin or automatically by the security system.
	EventAPIKeyRevoked = "api_key_revoked"

	// EventEmergencyFreeze is triggered when the system enters an emergency
	// freeze state, halting all financial operations.
	EventEmergencyFreeze = "emergency_freeze"

	// EventWebhookExhausted is triggered when a webhook delivery has failed
	// all retry attempts and cannot reach the merchant.
	EventWebhookExhausted = "webhook_exhausted"

	// EventSMSSpoofing is triggered when the SMS parser detects a potential
	// spoofing attempt in an incoming bank notification.
	EventSMSSpoofing = "sms_spoofing"

	// EventBankAccountDisabled is triggered when a bank account is disabled
	// due to suspicious activity or administrative action.
	EventBankAccountDisabled = "bank_account_disabled"

	// EventLargeWithdrawal is triggered when a withdrawal exceeds the
	// configured threshold for large transaction monitoring.
	EventLargeWithdrawal = "large_withdrawal"
)

// alertSeverityMap maps each known event type to its default severity level.
// This determines the visual urgency of the Telegram message and can be used
// to filter or route alerts to different channels.
var alertSeverityMap = map[string]AlertSeverity{
	EventLoginFailed:         AlertSeverityHigh,
	EventAPIKeyRevoked:       AlertSeverityHigh,
	EventEmergencyFreeze:     AlertSeverityCritical,
	EventWebhookExhausted:    AlertSeverityMedium,
	EventSMSSpoofing:         AlertSeverityCritical,
	EventBankAccountDisabled: AlertSeverityHigh,
	EventLargeWithdrawal:     AlertSeverityCritical,
}

// -----------------------------------------------------------------
// Telegram Bot API constants
// -----------------------------------------------------------------

const (
	// telegramAPIBase is the base URL for the Telegram Bot API. The bot
	// token is appended as a path segment.
	telegramAPIBase = "https://api.telegram.org/bot"

	// telegramTimeout is the HTTP timeout for calls to the Telegram API.
	// Telegram usually responds quickly, but we allow a generous timeout
	// to handle occasional network hiccups.
	telegramTimeout = 15 * time.Second
)

// -----------------------------------------------------------------
// TelegramService
// -----------------------------------------------------------------

// TelegramService sends messages to Telegram chats via the Bot API.
// It is primarily used for delivering security and operational alerts to
// the admin team, but can also be used for general notifications.
type TelegramService struct {
	// botToken is the Telegram Bot API token used to authenticate requests.
	// This token is obtained from @BotFather when creating the bot.
	botToken string

	// adminChatID is the Telegram chat (group or channel) where security
	// alerts are delivered. All security events are routed here.
	adminChatID string

	// httpClient is a dedicated HTTP client for Telegram API calls with
	// an appropriate timeout.
	httpClient *http.Client

	// log is the structured logger for the Telegram service.
	log *slog.Logger
}

// NewTelegramService constructs a new TelegramService with the given bot
// token and admin chat ID. The bot token authenticates with the Telegram API,
// and the admin chat ID determines where security alerts are sent.
func NewTelegramService(botToken, adminChatID string, log *slog.Logger) *TelegramService {
	return &TelegramService{
		botToken:    botToken,
		adminChatID: adminChatID,
		httpClient: &http.Client{
			Timeout: telegramTimeout,
		},
		log: log,
	}
}

// -----------------------------------------------------------------
// telegramSendMessageRequest
// -----------------------------------------------------------------

// telegramSendMessageRequest is the JSON body sent to the Telegram
// Bot API's sendMessage endpoint. It mirrors the required subset of
// the API's request schema.
type telegramSendMessageRequest struct {
	// ChatID is the unique identifier for the target chat or username of
	// the target channel (in the format @channelusername).
	ChatID string `json:"chat_id"`

	// Text is the message text to be sent. Supports Telegram's MarkdownV2
	// and HTML parse modes when ParseMode is set.
	Text string `json:"text"`

	// ParseMode controls how the text is rendered. Supported values are
	// "MarkdownV2", "HTML", or empty for plain text.
	ParseMode string `json:"parse_mode,omitempty"`
}

// -----------------------------------------------------------------
// SendAlert - send a message to a specific chat
// -----------------------------------------------------------------

// SendAlert delivers a plain-text message to the specified Telegram chat.
// This is the low-level method that other alert functions build upon.
//
// Parameters:
//   - ctx:     request-scoped context for cancellation / deadlines
//   - chatID:  the Telegram chat ID (numeric string or @channel name)
//   - message: the text content to send
func (t *TelegramService) SendAlert(ctx context.Context, chatID, message string) error {
	// Validate inputs to avoid sending empty or misdirected messages.
	if chatID == "" {
		return fmt.Errorf("telegram chat_id is empty")
	}
	if message == "" {
		return fmt.Errorf("telegram message is empty")
	}

	// If the bot token is not configured, log the alert locally but do not
	// return an error. This allows the service to run in development without
	// a Telegram bot.
	if t.botToken == "" {
		t.log.Warn("telegram bot token not configured, alert logged locally",
			"chat_id", chatID,
			"message", message,
		)
		return nil
	}

	// Build the sendMessage API request payload.
	reqBody := telegramSendMessageRequest{
		ChatID: chatID,
		Text:   message,
	}

	// Serialise the request body to JSON.
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal telegram request: %w", err)
	}

	// Construct the full API URL: https://api.telegram.org/bot<token>/sendMessage
	apiURL := telegramAPIBase + t.botToken + "/sendMessage"

	// Build the HTTP request with the caller's context so the call can be
	// cancelled if the parent operation times out.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute the API call.
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram API call failed: %w", err)
	}
	defer resp.Body.Close()

	// Telegram returns 200 on success. Any other status indicates an error
	// (e.g. invalid token, chat not found, rate limited).
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	t.log.Info("telegram alert sent",
		"chat_id", chatID,
		"message_length", len(message),
	)

	return nil
}

// -----------------------------------------------------------------
// SendSecurityAlert - formatted security alert to admin group
// -----------------------------------------------------------------

// SendSecurityAlert formats and sends a structured security alert to the
// configured admin Telegram group. The message includes the severity level
// (derived from the event type), event name, details, and a UTC timestamp.
//
// Alert format: "[ALERT] {severity} | {event} | {details} | {timestamp}"
//
// Parameters:
//   - ctx:     request-scoped context for cancellation / deadlines
//   - event:   the type of security event (e.g. "login_failed", "emergency_freeze")
//   - details: human-readable description of what happened
func (t *TelegramService) SendSecurityAlert(ctx context.Context, event, details string) error {
	// Look up the severity for this event type, defaulting to MEDIUM for
	// any unknown event types that may be added in the future.
	severity, ok := alertSeverityMap[event]
	if !ok {
		severity = AlertSeverityMedium
	}

	// Format the alert message with a consistent structure that is easy
	// to parse both visually and programmatically.
	timestamp := time.Now().UTC().Format(time.RFC3339)
	message := fmt.Sprintf("[ALERT] %s | %s | %s | %s", severity, event, details, timestamp)

	t.log.Info("sending security alert",
		"event", event,
		"severity", string(severity),
		"details", details,
	)

	// Deliver the formatted message to the admin chat group.
	return t.SendAlert(ctx, t.adminChatID, message)
}
