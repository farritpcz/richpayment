// Package handler provides HTTP request handlers for the parser-service.
//
// The parser-service exposes a small internal HTTP API (not public-facing)
// that receives SMS webhooks from the SMS gateway and a health check
// endpoint for infrastructure monitoring. All endpoints return JSON using
// a consistent response envelope.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/farritpcz/richpayment/services/parser/internal/service"
)

// SMSHandler handles HTTP requests related to SMS processing.
// It delegates all business logic to the ParserService and focuses
// solely on HTTP concerns: request decoding, response encoding, and
// status code selection.
type SMSHandler struct {
	// svc is the core SMS processing service that runs the full pipeline
	// (validation, parsing, persistence, matching).
	svc *service.ParserService

	// logger is the structured logger for HTTP-layer events.
	logger *slog.Logger
}

// NewSMSHandler constructs an SMSHandler with the given dependencies.
//
// Parameters:
//   - svc:    the ParserService instance that handles SMS business logic.
//   - logger: structured logger for request/response logging.
//
// Returns:
//   - A fully initialised SMSHandler ready to be registered with an HTTP mux.
func NewSMSHandler(svc *service.ParserService, logger *slog.Logger) *SMSHandler {
	return &SMSHandler{
		svc:    svc,
		logger: logger,
	}
}

// smsWebhookRequest is the expected JSON body for the POST /internal/sms
// endpoint. The SMS gateway sends this payload when a new message arrives.
type smsWebhookRequest struct {
	// Sender is the phone number or alphanumeric ID of the SMS sender.
	// Examples: "+66868882888", "KBANK", "SCB"
	Sender string `json:"sender"`

	// Message is the full SMS body text, unmodified by the gateway.
	Message string `json:"message"`

	// ReceivedAt is the ISO 8601 timestamp of when the gateway received the SMS.
	// Used for replay-attack protection (rejected if older than 5 minutes).
	ReceivedAt string `json:"received_at"`
}

// apiResponse is the standard JSON response envelope used by all endpoints.
// It mirrors the gateway-service's APIResponse structure for consistency
// across the RichPayment platform.
type apiResponse struct {
	// Success indicates whether the request was processed without errors.
	Success bool `json:"success"`

	// Data contains the response payload when Success is true.
	Data any `json:"data,omitempty"`

	// Error contains a human-readable error message when Success is false.
	Error string `json:"error,omitempty"`
}

// smsResultData contains the detailed outcome of processing an SMS webhook.
// This struct is serialised as the "data" field in the API response.
type smsResultData struct {
	// Status is the high-level outcome: "matched", "unmatched", or "error".
	Status string `json:"status"`

	// SMSID is the UUID assigned to the persisted SMS record.
	SMSID string `json:"sms_id,omitempty"`

	// OrderID is the matched order UUID (only present when status is "matched").
	OrderID string `json:"order_id,omitempty"`

	// BankCode is the bank identifier extracted from the SMS.
	BankCode string `json:"bank_code,omitempty"`

	// Amount is the transaction amount as a string (decimal format).
	Amount string `json:"amount,omitempty"`

	// Message is a human-readable description of the processing outcome.
	Message string `json:"message"`
}

// ReceiveSMS handles POST /internal/sms requests from the SMS gateway.
//
// Request body (JSON):
//
//	{
//	  "sender":      "+66868882888",
//	  "message":     "รับเงิน 1,000.00 บ. จาก สมชาย xxx ...",
//	  "received_at": "2026-04-10T14:30:00+07:00"
//	}
//
// Response body (JSON):
//
//	{
//	  "success": true,
//	  "data": {
//	    "status":    "matched",
//	    "sms_id":    "uuid",
//	    "order_id":  "uuid",
//	    "bank_code": "KBANK",
//	    "amount":    "1000.00",
//	    "message":   "matched to order ..."
//	  }
//	}
//
// HTTP status codes:
//   - 200: SMS processed (matched, unmatched, or soft error).
//   - 400: Invalid request body or missing required fields.
//   - 500: Unexpected infrastructure failure (DB/Redis down).
func (h *SMSHandler) ReceiveSMS(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests. The SMS gateway should always POST.
	if r.Method != http.MethodPost {
		h.respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Decode the JSON request body from the SMS gateway.
	var req smsWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("invalid request body", "error", err)
		h.respondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate that all required fields are present and non-empty.
	// The gateway should always send all three fields; missing fields
	// indicate a misconfigured webhook.
	if req.Sender == "" {
		h.respondError(w, http.StatusBadRequest, "sender is required")
		return
	}
	if req.Message == "" {
		h.respondError(w, http.StatusBadRequest, "message is required")
		return
	}
	if req.ReceivedAt == "" {
		h.respondError(w, http.StatusBadRequest, "received_at is required")
		return
	}

	// Parse the received_at timestamp. Accept both RFC3339 (with timezone)
	// and a simpler format without timezone offset as a fallback, since
	// some SMS gateways may omit the timezone.
	receivedAt, err := time.Parse(time.RFC3339, req.ReceivedAt)
	if err != nil {
		// Fallback: try parsing without timezone offset.
		receivedAt, err = time.Parse("2006-01-02T15:04:05", req.ReceivedAt)
		if err != nil {
			h.logger.Warn("invalid received_at format",
				"received_at", req.ReceivedAt,
				"error", err,
			)
			h.respondError(w, http.StatusBadRequest, "invalid received_at format, expected RFC3339")
			return
		}
	}

	// Delegate to the service layer for the full processing pipeline.
	// The service handles: anti-spoofing, timestamp validation, parsing,
	// persistence, and order matching.
	result, err := h.svc.ProcessSMS(r.Context(), req.Sender, req.Message, receivedAt)
	if err != nil {
		// Infrastructure error (DB or Redis down). Return 500 so the
		// SMS gateway knows to retry delivery.
		h.logger.Error("ProcessSMS infrastructure error", "error", err)
		h.respondError(w, http.StatusInternalServerError, "internal processing error")
		return
	}

	// Build the response data from the service result.
	data := &smsResultData{
		Status:   string(result.Status),
		BankCode: result.BankCode,
		Message:  result.Message,
	}

	// Only include the SMS ID when it was assigned (i.e. the SMS was
	// persisted to the database). Early rejections (anti-spoofing, timestamp)
	// do not generate an SMS record.
	var zeroUUID uuid.UUID
	if result.SMSID != zeroUUID {
		data.SMSID = result.SMSID.String()
	}

	// Include the matched order ID only when a match was found.
	if result.OrderID != nil {
		data.OrderID = result.OrderID.String()
	}

	// Include the amount only when parsing was successful.
	if !result.Amount.IsZero() {
		data.Amount = result.Amount.String()
	}

	// Return 200 for all successfully processed requests, including
	// unmatched and soft-error outcomes. The "status" field in the
	// response body distinguishes between outcomes.
	h.respondOK(w, data)
}

// Health handles GET /healthz requests. It returns a simple JSON response
// indicating the service is running. Kubernetes liveness/readiness probes
// and load balancers use this endpoint to determine whether to route
// traffic to this instance.
//
// Response body (JSON):
//
//	{
//	  "success": true,
//	  "data": { "status": "ok", "service": "parser-service" }
//	}
func (h *SMSHandler) Health(w http.ResponseWriter, r *http.Request) {
	h.respondOK(w, map[string]string{
		"status":  "ok",
		"service": "parser-service",
	})
}

// respondOK writes a 200 JSON response with the standard success envelope.
// The data parameter is serialised into the "data" field of the envelope.
func (h *SMSHandler) respondOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(apiResponse{
		Success: true,
		Data:    data,
	})
}

// respondError writes an error JSON response with the given HTTP status code.
// The message is included in the "error" field of the response envelope.
func (h *SMSHandler) respondError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiResponse{
		Success: false,
		Error:   message,
	})
}
