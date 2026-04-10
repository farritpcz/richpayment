// Package handler provides the HTTP handlers for the scheduler-service's
// internal API. These endpoints allow operators and monitoring systems to
// interact with the scheduler:
//
//   POST /internal/scheduler/run/{job}  - Manually trigger a named job
//   GET  /internal/scheduler/status     - View all jobs and their next run times
//   GET  /healthz                       - Health check for load balancers/k8s
//
// All handlers follow the standard library http.HandlerFunc signature and
// are registered on an http.ServeMux.
package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/services/scheduler/internal/service"
)

// ---------------------------------------------------------------------------
// SchedulerHandler — groups all HTTP handlers for the scheduler-service.
// ---------------------------------------------------------------------------

// SchedulerHandler holds a reference to the CronScheduler and provides
// HTTP handler methods for each API endpoint. It bridges raw HTTP requests
// and the scheduler's job management functionality.
type SchedulerHandler struct {
	// scheduler is the cron scheduler that manages all registered jobs.
	// Used to query job status and trigger manual job execution.
	scheduler *service.CronScheduler
}

// NewSchedulerHandler constructs a new SchedulerHandler with the given
// CronScheduler dependency.
//
// Parameters:
//   - scheduler: the cron scheduler that manages all jobs.
//
// Returns a ready-to-use SchedulerHandler instance.
func NewSchedulerHandler(scheduler *service.CronScheduler) *SchedulerHandler {
	return &SchedulerHandler{
		scheduler: scheduler,
	}
}

// ---------------------------------------------------------------------------
// RegisterRoutes — register all HTTP routes on the given ServeMux.
// ---------------------------------------------------------------------------

// RegisterRoutes registers all scheduler-service HTTP endpoints on the
// provided ServeMux. This method should be called once during server setup
// to wire up the routing table.
//
// Parameters:
//   - mux: the HTTP serve mux to register routes on.
func (h *SchedulerHandler) RegisterRoutes(mux *http.ServeMux) {
	// POST /internal/scheduler/run/ — manually trigger a job by name.
	// The job name is extracted from the URL path suffix.
	// Example: POST /internal/scheduler/run/check_partitions
	mux.HandleFunc("POST /internal/scheduler/run/", h.HandleRunJob)

	// GET /internal/scheduler/status — show all jobs and their next run times.
	// Returns a JSON array of job status objects.
	mux.HandleFunc("GET /internal/scheduler/status", h.HandleStatus)

	// GET /healthz — health check endpoint.
	// Returns a simple JSON response indicating the service is alive.
	mux.HandleFunc("GET /healthz", h.HandleHealthz)
}

// ---------------------------------------------------------------------------
// HandleRunJob — manually trigger a scheduled job by name.
// ---------------------------------------------------------------------------

// HandleRunJob extracts a job name from the URL path and triggers it
// immediately, regardless of its normal schedule. This allows operators
// to run jobs on demand for testing, recovery, or manual intervention.
//
// The job name is the last segment of the URL path:
//   POST /internal/scheduler/run/{job_name}
//
// If the job is found and not already running, it is started in a background
// goroutine. The response indicates whether the job was triggered.
//
// Parameters:
//   - w: the HTTP response writer.
//   - r: the incoming HTTP request.
func (h *SchedulerHandler) HandleRunJob(w http.ResponseWriter, r *http.Request) {
	// Extract the job name from the URL path.
	// The path format is: /internal/scheduler/run/{job_name}
	jobName := strings.TrimPrefix(r.URL.Path, "/internal/scheduler/run/")
	jobName = strings.TrimSpace(jobName)

	// Validate that a job name was provided.
	if jobName == "" {
		logger.Warn("manual job trigger with empty job name")
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "job name is required in URL path",
		})
		return
	}

	logger.Info("manual job trigger requested", "job", jobName)

	// Attempt to find and run the job by name.
	started := h.scheduler.RunJobByName(r.Context(), jobName)
	if !started {
		// Job was not found or is already running.
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "job not found or already running",
			"job":   jobName,
		})
		return
	}

	// Job was found and started successfully.
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "job_triggered",
		"job":    jobName,
	})
}

// ---------------------------------------------------------------------------
// HandleStatus — show all jobs and their current state.
// ---------------------------------------------------------------------------

// HandleStatus returns a JSON array of all registered jobs and their
// current state, including last run time, next run time, and whether
// each job is currently executing. This endpoint is used by operators
// and monitoring dashboards to track the scheduler's activity.
//
// Parameters:
//   - w: the HTTP response writer.
//   - r: the incoming HTTP request (unused but required by HandlerFunc).
func (h *SchedulerHandler) HandleStatus(w http.ResponseWriter, _ *http.Request) {
	// Get a snapshot of all job statuses from the scheduler.
	jobs := h.scheduler.GetJobs()

	// Return the job status list as JSON.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"service": "scheduler-service",
		"jobs":    jobs,
	})
}

// ---------------------------------------------------------------------------
// HandleHealthz — health check endpoint.
// ---------------------------------------------------------------------------

// HandleHealthz returns a simple JSON health check response. This endpoint
// is used by orchestrators (Kubernetes), load balancers, and monitoring
// systems to verify the scheduler-service is alive and accepting requests.
//
// Parameters:
//   - w: the HTTP response writer.
//   - r: the incoming HTTP request (unused but required by HandlerFunc).
func (h *SchedulerHandler) HandleHealthz(w http.ResponseWriter, _ *http.Request) {
	// Return a minimal JSON body with service name and status.
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "scheduler-service",
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
