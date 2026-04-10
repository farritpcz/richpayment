// Package handler provides the HTTP handlers for the telegram-service's
// internal API. These endpoints are called by other RichPayment services
// (not by external clients) to interact with the Telegram bot:
//
//   - POST /internal/telegram/webhook — alternative to long-polling; receives
//     Telegram updates via webhook.
//   - POST /internal/telegram/alert — sends a security alert to the Telegram
//     alert channel.
//   - GET  /healthz — health check endpoint for load balancers and k8s probes.
//
// All handlers follow the standard library http.HandlerFunc signature and
// are designed to be registered on an http.ServeMux.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/services/telegram/internal/service"
)

// ---------------------------------------------------------------------------
// TelegramHandler — groups all HTTP handlers for the telegram-service.
// ---------------------------------------------------------------------------

// TelegramHandler holds references to the service layer and provides
// HTTP handler methods for each API endpoint. It acts as the bridge
// between raw HTTP requests and the business logic in the service package.
type TelegramHandler struct {
	// botSvc is the Telegram bot service that processes updates, downloads
	// photos, and sends replies. Used by the webhook handler.
	botSvc *service.BotService

	// alertSvc is the security alert service that formats and sends
	// alert messages to the Telegram alert channel. Used by the alert handler.
	alertSvc *service.AlertService
}

// NewTelegramHandler constructs a new TelegramHandler with the given
// service dependencies.
//
// Parameters:
//   - botSvc: the Telegram bot lifecycle service.
//   - alertSvc: the security alert sending service.
//
// Returns a ready-to-use TelegramHandler instance.
func NewTelegramHandler(botSvc *service.BotService, alertSvc *service.AlertService) *TelegramHandler {
	return &TelegramHandler{
		botSvc:   botSvc,
		alertSvc: alertSvc,
	}
}

// ---------------------------------------------------------------------------
// RegisterRoutes — register all HTTP routes on the given ServeMux.
// ---------------------------------------------------------------------------

// RegisterRoutes registers all telegram-service HTTP endpoints on the
// provided ServeMux. This method should be called once during server setup
// to wire up the routing table.
//
// Parameters:
//   - mux: the HTTP serve mux to register routes on.
func (h *TelegramHandler) RegisterRoutes(mux *http.ServeMux) {
	// POST /internal/telegram/webhook — receive Telegram updates via webhook.
	// This is an alternative to long-polling and is used when the service is
	// deployed behind a public HTTPS endpoint with a registered webhook URL.
	mux.HandleFunc("POST /internal/telegram/webhook", h.HandleWebhook)

	// POST /internal/telegram/alert — send a security alert.
	// Called by other RichPayment services when a security event occurs
	// that requires immediate notification to the operations team.
	mux.HandleFunc("POST /internal/telegram/alert", h.HandleAlert)

	// GET /healthz — health check endpoint.
	// Returns a simple JSON response indicating the service is alive.
	// Used by Kubernetes liveness/readiness probes and load balancers.
	mux.HandleFunc("GET /healthz", h.HandleHealthz)
}

// ---------------------------------------------------------------------------
// HandleWebhook — process incoming Telegram webhook updates.
// ---------------------------------------------------------------------------

// alertRequest is the JSON body for POST /internal/telegram/alert.
// It contains the alert type and a free-form details map that varies
// by alert type (e.g. "ip" for login failures, "merchant_id" for freezes).
type alertRequest struct {
	// AlertType is the category of the security alert (e.g. "login_failed",
	// "emergency_freeze", "sms_spoofing"). Must match one of the defined
	// AlertType constants.
	AlertType string `json:"alert_type"`

	// Details is a key-value map of alert-specific information. Keys and
	// values vary by alert type. For example, a login_failed alert might
	// include "ip", "email", and "attempts" keys.
	Details map[string]string `json:"details"`
}

// HandleWebhook processes a single Telegram update received via webhook.
// This is the webhook counterpart to the long-polling approach in BotService.
// When the Telegram webhook is configured, Telegram sends a POST request
// with a JSON body containing a single TelegramUpdate for each new event.
//
// The handler decodes the update and delegates to BotService.HandleUpdate
// for processing (photo download, slip verification, reply sending).
//
// Parameters:
//   - w: the HTTP response writer.
//   - r: the incoming HTTP request containing the Telegram update.
func (h *TelegramHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// Decode the incoming Telegram update from the request body.
	var update service.TelegramUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		logger.Error("failed to decode webhook update", "error", err)
		// Return 200 OK even on decode errors to prevent Telegram from
		// retrying the webhook delivery. Invalid payloads should be
		// silently dropped.
		w.WriteHeader(http.StatusOK)
		return
	}

	logger.Info("received webhook update", "update_id", update.UpdateID)

	// Delegate the update to the bot service for processing.
	// Run in a goroutine so we can immediately respond to Telegram
	// with 200 OK and avoid webhook timeout errors.
	go h.botSvc.HandleUpdate(r.Context(), update)

	// Telegram expects a 200 OK response to acknowledge receipt of
	// the webhook. Any other status code triggers a retry.
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// HandleAlert — send a security alert via Telegram.
// ---------------------------------------------------------------------------

// HandleAlert receives a security alert request from another RichPayment
// service and forwards it to the Telegram alert channel. The request body
// contains the alert type and a details map that is formatted into a
// structured Telegram message.
//
// This endpoint is internal-only and should be protected by network-level
// access controls (e.g. only reachable from the internal service mesh).
//
// Parameters:
//   - w: the HTTP response writer.
//   - r: the incoming HTTP request containing the alert payload.
func (h *TelegramHandler) HandleAlert(w http.ResponseWriter, r *http.Request) {
	// Decode the alert request from the JSON body.
	var req alertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Error("failed to decode alert request", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Validate that the alert type is not empty.
	if req.AlertType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alert_type is required"})
		return
	}

	logger.Info("received alert request",
		"alert_type", req.AlertType,
	)

	// Send the alert to the Telegram channel via the alert service.
	if err := h.alertSvc.SendSecurityAlert(
		r.Context(),
		service.AlertType(req.AlertType),
		req.Details,
	); err != nil {
		logger.Error("failed to send security alert",
			"error", err,
			"alert_type", req.AlertType,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send alert"})
		return
	}

	// Return success response.
	writeJSON(w, http.StatusOK, map[string]string{"status": "alert_sent"})
}

// ---------------------------------------------------------------------------
// HandleHealthz — health check endpoint.
// ---------------------------------------------------------------------------

// HandleHealthz returns a simple JSON health check response. This endpoint
// is used by orchestrators (Kubernetes), load balancers, and monitoring
// systems to verify the telegram-service is alive and accepting requests.
//
// Parameters:
//   - w: the HTTP response writer.
//   - r: the incoming HTTP request (unused but required by HandlerFunc).
func (h *TelegramHandler) HandleHealthz(w http.ResponseWriter, _ *http.Request) {
	// Return a minimal JSON body with service name and status.
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "telegram-service",
	})
}

// ---------------------------------------------------------------------------
// writeJSON — helper to write a JSON response with a status code.
// ---------------------------------------------------------------------------

// writeJSON serialises the given value as JSON and writes it to the HTTP
// response with the specified status code. It sets the Content-Type header
// to application/json. Encoding errors are silently ignored because the
// response has already been partially written at that point.
//
// Parameters:
//   - w: the HTTP response writer.
//   - status: the HTTP status code to send.
//   - v: the value to serialise as JSON.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
