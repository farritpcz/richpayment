package handler

import (
	"net/http"
)

// HealthHandler provides health check endpoints.
type HealthHandler struct{}

// NewHealthHandler creates a new HealthHandler.
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Health returns the service health status.
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	respondOK(w, map[string]string{
		"status":  "ok",
		"service": "gateway-api",
	})
}
