// Package service (alert.go) implements the security alert sender for the
// telegram-service. When critical security events occur across the RichPayment
// platform (failed logins, API key revocations, emergency freezes, etc.),
// this service formats and sends alert messages to a designated Telegram
// security channel so the operations team is immediately notified.
//
// Alert types are predefined constants that map to specific formatting
// templates with appropriate severity indicators (emojis and labels).
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/farritpcz/richpayment/pkg/logger"
)

// ---------------------------------------------------------------------------
// Alert type constants.
// ---------------------------------------------------------------------------

// AlertType represents the category of a security alert. Each type maps to
// a specific formatting template and severity level. New alert types should
// be added here with corresponding formatting in FormatAlert.
type AlertType string

const (
	// AlertLoginFailed indicates one or more failed login attempts were
	// detected. Details typically include the IP address, user agent,
	// and number of consecutive failures.
	AlertLoginFailed AlertType = "login_failed"

	// AlertAPIKeyRevoked indicates a merchant's API key was revoked,
	// either manually by an admin or automatically by a security rule.
	AlertAPIKeyRevoked AlertType = "api_key_revoked"

	// AlertEmergencyFreeze indicates an emergency freeze was activated
	// on a merchant, bank account, or the entire system. All transactions
	// are paused until the freeze is lifted.
	AlertEmergencyFreeze AlertType = "emergency_freeze"

	// AlertWebhookExhausted indicates that all retry attempts for a
	// merchant webhook delivery have been exhausted. The merchant is not
	// receiving deposit/withdrawal notifications.
	AlertWebhookExhausted AlertType = "webhook_exhausted"

	// AlertSMSSpoofing indicates a potential SMS spoofing attack was
	// detected. This could mean someone is sending fake bank notification
	// SMS messages to manipulate the order matching system.
	AlertSMSSpoofing AlertType = "sms_spoofing"

	// AlertBankDisabled indicates a bank account was disabled, either
	// due to maintenance, fraud detection, or manual admin action.
	AlertBankDisabled AlertType = "bank_disabled"

	// AlertLargeWithdrawal indicates a withdrawal request exceeding the
	// configured threshold was submitted. This may require manual approval
	// from the operations team.
	AlertLargeWithdrawal AlertType = "large_withdrawal"
)

// ---------------------------------------------------------------------------
// AlertService — sends security alerts to Telegram.
// ---------------------------------------------------------------------------

// AlertService sends formatted security alert messages to a designated
// Telegram channel or group. It uses the Telegram Bot API's sendMessage
// endpoint with Markdown formatting for rich alert messages.
type AlertService struct {
	// token is the Telegram Bot API token for authentication.
	// This may be the same token as the main bot or a separate
	// alert-dedicated bot token.
	token string

	// alertChatID is the Telegram chat ID of the security alert channel.
	// All alert messages are sent to this channel. Typically a private
	// group with the operations/security team.
	alertChatID int64

	// httpClient is the HTTP client for Telegram API calls. Configured
	// with a reasonable timeout for non-interactive alert delivery.
	httpClient *http.Client
}

// NewAlertService constructs a new AlertService with the given Telegram
// bot token and target alert channel ID.
//
// Parameters:
//   - token: the Telegram Bot API token for sending messages.
//   - alertChatID: the Telegram chat ID of the alert channel.
//
// Returns a ready-to-use AlertService instance.
func NewAlertService(token string, alertChatID int64) *AlertService {
	return &AlertService{
		token:       token,
		alertChatID: alertChatID,
		httpClient: &http.Client{
			// 15-second timeout is sufficient for a simple sendMessage call.
			// Alerts are fire-and-forget; we log failures but do not retry.
			Timeout: 15 * time.Second,
		},
	}
}

// ---------------------------------------------------------------------------
// FormatAlert — format an alert message with severity and details.
// ---------------------------------------------------------------------------

// FormatAlert converts an alert type and its details map into a formatted
// Telegram Markdown message string. Each alert type has a specific template
// with an appropriate severity indicator and structured detail fields.
//
// Parameters:
//   - alertType: the category of the security alert.
//   - details: a key-value map of alert-specific details (e.g. "ip", "merchant_id").
//
// Returns a Markdown-formatted string ready to be sent via the Telegram API.
func FormatAlert(alertType AlertType, details map[string]string) string {
	// Determine the severity label and icon based on the alert type.
	// Critical alerts use a red icon; warnings use yellow; info uses blue.
	var severityIcon string
	var severityLabel string

	switch alertType {
	case AlertEmergencyFreeze, AlertSMSSpoofing:
		// Critical severity — immediate action required.
		severityIcon = "[CRITICAL]"
		severityLabel = "CRITICAL"
	case AlertLoginFailed, AlertAPIKeyRevoked, AlertBankDisabled, AlertLargeWithdrawal:
		// Warning severity — review required.
		severityIcon = "[WARNING]"
		severityLabel = "WARNING"
	case AlertWebhookExhausted:
		// Info severity — operational awareness.
		severityIcon = "[INFO]"
		severityLabel = "INFO"
	default:
		// Unknown alert type — treat as info.
		severityIcon = "[ALERT]"
		severityLabel = "ALERT"
	}

	// Build the formatted message using Markdown.
	var sb strings.Builder

	// Header line with severity and alert type.
	sb.WriteString(fmt.Sprintf("*%s %s: %s*\n\n", severityIcon, severityLabel, string(alertType)))

	// Timestamp line.
	sb.WriteString(fmt.Sprintf("Time: `%s`\n", time.Now().UTC().Format(time.RFC3339)))

	// Append all detail fields in a structured format.
	for key, value := range details {
		sb.WriteString(fmt.Sprintf("%s: `%s`\n", key, value))
	}

	// Footer with separator.
	sb.WriteString("\n---\n_RichPayment Security Alert_")

	return sb.String()
}

// ---------------------------------------------------------------------------
// SendSecurityAlert — send a formatted alert to the Telegram channel.
// ---------------------------------------------------------------------------

// SendSecurityAlert formats and sends a security alert message to the
// configured Telegram alert channel. It calls FormatAlert to build the
// message text, then sends it via the Telegram sendMessage API.
//
// This method is designed to be called from any service in the platform
// (via the telegram-service's internal HTTP API) whenever a security
// event occurs that the operations team needs to be aware of.
//
// Parameters:
//   - ctx: context for cancellation and deadline propagation.
//   - alertType: the category of the security alert.
//   - details: a key-value map of alert-specific details.
//
// Returns an error if the Telegram API call fails. Callers should log
// the error but should NOT block their own operations on alert delivery
// failures — alerts are best-effort notifications.
func (a *AlertService) SendSecurityAlert(ctx context.Context, alertType AlertType, details map[string]string) error {
	// Format the alert message using the appropriate template.
	message := FormatAlert(alertType, details)

	logger.Info("sending security alert",
		"alert_type", string(alertType),
		"chat_id", a.alertChatID,
	)

	// Build the Telegram sendMessage API payload.
	payload := map[string]interface{}{
		"chat_id":    a.alertChatID,
		"text":       message,
		"parse_mode": "Markdown",
	}

	// Marshal the payload to JSON bytes.
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal alert payload: %w", err)
	}

	// Construct the Telegram sendMessage API URL.
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", a.token)

	// Create the HTTP request with context for cancellation support.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create alert request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute the sendMessage request.
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send alert to telegram: %w", err)
	}
	defer resp.Body.Close()

	// Check for HTTP-level errors from the Telegram API.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram sendMessage returned status %d", resp.StatusCode)
	}

	logger.Info("security alert sent successfully",
		"alert_type", string(alertType),
		"chat_id", a.alertChatID,
	)

	return nil
}
