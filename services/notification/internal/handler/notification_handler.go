// Package handler implements the HTTP request handlers for the notification
// service. All handlers follow the standard net/http handler signature and
// use JSON request/response envelopes.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/farritpcz/richpayment/services/notification/internal/service"
)

// -----------------------------------------------------------------
// APIResponse - standard JSON envelope
// -----------------------------------------------------------------

// APIResponse is the standard JSON envelope used for all HTTP responses
// from the notification service. It mirrors the response format used by
// other RichPayment microservices for consistency.
type APIResponse struct {
	// Success indicates whether the request was processed without errors.
	Success bool `json:"success"`

	// Data contains the response payload on success. It is omitted from
	// the JSON output when nil.
	Data any `json:"data,omitempty"`

	// Error contains a human-readable error message on failure. It is
	// omitted from the JSON output when empty.
	Error string `json:"error,omitempty"`

	// Code is a machine-readable error code that callers can use for
	// programmatic error handling. It is omitted on success.
	Code string `json:"code,omitempty"`
}

// -----------------------------------------------------------------
// JSON response helpers
// -----------------------------------------------------------------

// writeJSON marshals the given value as JSON and writes it to the response
// writer with the specified HTTP status code. The Content-Type header is
// set to application/json.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// respondOK sends a 200 OK response with the given data wrapped in the
// standard API envelope.
func respondOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
	})
}

// respondError sends an error response with the given HTTP status code,
// machine-readable error code, and human-readable message.
func respondError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, APIResponse{
		Success: false,
		Error:   message,
		Code:    code,
	})
}

// -----------------------------------------------------------------
// NotificationHandler
// -----------------------------------------------------------------

// NotificationHandler groups the HTTP handlers for the notification service.
// It delegates business logic to the WebhookService and TelegramService,
// handling only HTTP concerns (parsing, validation, response formatting).
type NotificationHandler struct {
	// webhook is the service that handles webhook delivery with retry.
	webhook *service.WebhookService

	// telegram is the service that sends Telegram alerts.
	telegram *service.TelegramService

	// log is the structured logger for request-level logging.
	log *slog.Logger
}

// NewNotificationHandler creates a new NotificationHandler with the provided
// service dependencies.
func NewNotificationHandler(
	webhook *service.WebhookService,
	telegram *service.TelegramService,
	log *slog.Logger,
) *NotificationHandler {
	return &NotificationHandler{
		webhook:  webhook,
		telegram: telegram,
		log:      log,
	}
}

// -----------------------------------------------------------------
// Request types
// -----------------------------------------------------------------

// SendWebhookRequest is the expected JSON body for the webhook send endpoint.
// It contains all the information needed to deliver a signed webhook to a
// merchant's callback URL.
type SendWebhookRequest struct {
	// MerchantID is the UUID of the merchant that should receive the webhook.
	MerchantID string `json:"merchant_id"`

	// OrderID is the UUID of the payment order that triggered the notification.
	OrderID string `json:"order_id"`

	// WebhookURL is the merchant-configured HTTPS callback endpoint.
	WebhookURL string `json:"webhook_url"`

	// WebhookSecret is the shared HMAC-SHA256 key used to sign the payload.
	WebhookSecret string `json:"webhook_secret"`

	// Payload is the arbitrary JSON body that will be delivered to the merchant.
	Payload json.RawMessage `json:"payload"`
}

// SendAlertRequest is the expected JSON body for the alert send endpoint.
// It specifies the target Telegram chat and the message content.
type SendAlertRequest struct {
	// ChatID is the Telegram chat ID where the alert should be sent.
	// Can be a numeric ID or a @channel username.
	ChatID string `json:"chat_id"`

	// Message is the text content of the alert to be sent.
	Message string `json:"message"`

	// Event is an optional security event type (e.g. "login_failed").
	// When set, the message is formatted as a security alert with severity.
	Event string `json:"event,omitempty"`

	// Details is additional context for security alerts. Only used when
	// Event is specified.
	Details string `json:"details,omitempty"`
}

// -----------------------------------------------------------------
// POST /internal/webhook/send
// -----------------------------------------------------------------

// SendWebhook handles POST /internal/webhook/send. It validates the request
// body and delegates to the WebhookService for delivery. This endpoint is
// internal-only and should not be exposed to external traffic.
func (h *NotificationHandler) SendWebhook(w http.ResponseWriter, r *http.Request) {
	// Decode the JSON request body into the expected structure.
	var req SendWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Warn("invalid webhook request body",
			"error", err,
			"remote", r.RemoteAddr,
		)
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "invalid JSON request body")
		return
	}

	// Validate that all required fields are present. Missing fields would
	// cause confusing errors downstream.
	if req.MerchantID == "" || req.OrderID == "" || req.WebhookURL == "" || req.WebhookSecret == "" {
		respondError(w, http.StatusBadRequest, "MISSING_FIELDS", "merchant_id, order_id, webhook_url, and webhook_secret are required")
		return
	}

	// Validate that a payload was provided.
	if len(req.Payload) == 0 {
		respondError(w, http.StatusBadRequest, "MISSING_PAYLOAD", "payload is required")
		return
	}

	h.log.Info("webhook send requested",
		"merchant_id", req.MerchantID,
		"order_id", req.OrderID,
		"webhook_url", req.WebhookURL,
	)

	// Delegate to the webhook service for delivery (and potential retries).
	// This call may return immediately if the first attempt succeeds, or
	// schedule a background retry if it fails.
	if err := h.webhook.SendWebhook(
		r.Context(),
		req.MerchantID,
		req.OrderID,
		req.WebhookURL,
		req.WebhookSecret,
		req.Payload,
	); err != nil {
		// Even if the first attempt failed, the webhook has been scheduled
		// for retry. We return 202 Accepted to indicate the request was
		// accepted for processing.
		h.log.Warn("initial webhook delivery failed, retries scheduled",
			"merchant_id", req.MerchantID,
			"order_id", req.OrderID,
			"error", err,
		)
		writeJSON(w, http.StatusAccepted, APIResponse{
			Success: true,
			Data: map[string]string{
				"status":  "retry_scheduled",
				"message": "initial delivery failed, retries have been scheduled",
			},
		})
		return
	}

	// Webhook was delivered successfully on the first attempt.
	respondOK(w, map[string]string{
		"status":  "delivered",
		"message": "webhook delivered successfully",
	})
}

// -----------------------------------------------------------------
// POST /internal/alert/send
// -----------------------------------------------------------------

// SendAlert handles POST /internal/alert/send. It validates the request body
// and sends either a plain alert or a formatted security alert via Telegram.
// This endpoint is internal-only.
func (h *NotificationHandler) SendAlert(w http.ResponseWriter, r *http.Request) {
	// Decode the JSON request body.
	var req SendAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Warn("invalid alert request body",
			"error", err,
			"remote", r.RemoteAddr,
		)
		respondError(w, http.StatusBadRequest, "INVALID_BODY", "invalid JSON request body")
		return
	}

	// If an event type is specified, this is a security alert that gets
	// special formatting and is routed to the admin group.
	if req.Event != "" {
		h.log.Info("security alert requested",
			"event", req.Event,
			"details", req.Details,
		)

		if err := h.telegram.SendSecurityAlert(r.Context(), req.Event, req.Details); err != nil {
			h.log.Error("failed to send security alert",
				"event", req.Event,
				"error", err,
			)
			respondError(w, http.StatusInternalServerError, "ALERT_FAILED", "failed to send security alert")
			return
		}

		respondOK(w, map[string]string{
			"status":  "sent",
			"message": "security alert sent successfully",
		})
		return
	}

	// Plain alert: validate that chat_id and message are provided.
	if req.ChatID == "" || req.Message == "" {
		respondError(w, http.StatusBadRequest, "MISSING_FIELDS", "chat_id and message are required (or provide event + details for security alerts)")
		return
	}

	h.log.Info("plain alert requested",
		"chat_id", req.ChatID,
	)

	// Send the plain alert message to the specified Telegram chat.
	if err := h.telegram.SendAlert(r.Context(), req.ChatID, req.Message); err != nil {
		h.log.Error("failed to send telegram alert",
			"chat_id", req.ChatID,
			"error", err,
		)
		respondError(w, http.StatusInternalServerError, "ALERT_FAILED", "failed to send telegram alert")
		return
	}

	respondOK(w, map[string]string{
		"status":  "sent",
		"message": "alert sent successfully",
	})
}

// -----------------------------------------------------------------
// GET /healthz
// -----------------------------------------------------------------

// Healthz handles GET /healthz. It returns a simple JSON response indicating
// that the notification service is running and ready to accept requests.
// This endpoint is used by load balancers and orchestrators for health checks.
func (h *NotificationHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	respondOK(w, map[string]string{
		"status":  "ok",
		"service": "notification-service",
	})
}
