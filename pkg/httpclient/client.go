// Package httpclient provides a reusable HTTP client for internal
// service-to-service communication within the RichPayment microservices
// architecture.
//
// # Architecture Overview
//
// RichPayment is composed of multiple independent services (gateway, order,
// wallet, withdrawal, commission, notification, etc.) that communicate over
// HTTP on a private network. Each service runs on its own port:
//
//   - gateway-api:          :8080 (public entry point for merchants)
//   - auth-service:         :8081
//   - user-service:         :8082
//   - order-service:        :8083 (deposit order lifecycle)
//   - wallet-service:       :8084 (balance management, credits, debits)
//   - withdrawal-service:   :8085 (withdrawal lifecycle)
//   - commission-service:   :8086 (fee split calculation and recording)
//   - bank-service:         :8087
//   - parser-service:       :8088
//   - telegram-service:     :8089
//   - notification-service: :8090 (webhook + alert delivery)
//   - scheduler-service:    :8091
//
// This client abstracts the low-level HTTP details (JSON marshalling,
// timeout handling, error propagation) so that callers can focus on the
// business logic of inter-service calls.
//
// # Usage
//
// Create a ServiceClient pointing at a target service's base URL, then
// use Post() or Get() to make JSON-over-HTTP requests:
//
//	walletClient := httpclient.New("http://localhost:8084", 5*time.Second)
//	err := walletClient.Post(ctx, "/wallet/credit", creditReq, &resp)
//
// All errors returned include the HTTP status code context so the caller
// can decide whether to retry, fail fast, or log and continue.
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ServiceClient makes HTTP calls to other internal RichPayment services.
// It is designed for service-to-service (east-west) traffic on the private
// network. All communication uses JSON encoding over plain HTTP.
//
// Each ServiceClient instance targets a single service. For example, the
// order-service would create separate ServiceClient instances for the
// wallet-service, commission-service, and notification-service.
type ServiceClient struct {
	// baseURL is the scheme + host + port of the target service.
	// Example: "http://localhost:8084" for the wallet-service.
	baseURL string

	// httpClient is the underlying Go HTTP client with a configured timeout.
	// The timeout applies to the entire request lifecycle (connect + headers
	// + body read).
	httpClient *http.Client
}

// New creates a ServiceClient pointing at the given base URL.
//
// Parameters:
//   - baseURL: the root URL of the target service (e.g. "http://localhost:8084").
//   - timeout: the maximum duration for each HTTP request. A sensible default
//     for internal calls is 5-10 seconds.
//
// Returns a ready-to-use *ServiceClient.
func New(baseURL string, timeout time.Duration) *ServiceClient {
	return &ServiceClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Post sends an HTTP POST request with a JSON-encoded body to the specified
// path on the target service and decodes the JSON response into result.
//
// This is the primary method for write operations in inter-service
// communication. For example:
//   - gateway -> order-service: POST /api/v1/deposits (create deposit)
//   - order-service -> wallet-service: POST /wallet/credit (credit merchant)
//   - order-service -> commission-service: POST /internal/commission/calculate
//   - order-service -> notification-service: POST /internal/webhook/send
//
// Parameters:
//   - ctx: request-scoped context for cancellation and deadline propagation.
//     If the caller's context is cancelled, the HTTP request is aborted.
//   - path: the URL path on the target service (e.g. "/wallet/credit").
//   - body: the request payload; will be marshalled to JSON.
//   - result: a pointer to the struct where the JSON response will be decoded.
//     Pass nil if the response body should be ignored.
//
// Returns nil on success (HTTP 2xx). Returns an error containing the HTTP
// status code and response body for non-2xx responses or network failures.
func (c *ServiceClient) Post(ctx context.Context, path string, body interface{}, result interface{}) error {
	// Marshal the request body to JSON bytes.
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("httpclient: marshal request body: %w", err)
	}

	// Build the full URL by combining the base URL with the path.
	// Example: "http://localhost:8084" + "/wallet/credit" = "http://localhost:8084/wallet/credit"
	url := c.baseURL + path

	// Create a new HTTP POST request with the JSON body and the caller's context.
	// The context ensures the request is cancelled if the parent operation times out.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("httpclient: create POST request: %w", err)
	}

	// Set Content-Type so the receiving service knows to parse JSON.
	req.Header.Set("Content-Type", "application/json")

	// Execute the HTTP request against the target service.
	return c.doRequest(req, result)
}

// Get sends an HTTP GET request to the specified path on the target service
// and decodes the JSON response into result.
//
// This is the primary method for read operations in inter-service
// communication. For example:
//   - gateway -> order-service: GET /api/v1/deposits/{id} (fetch deposit)
//   - gateway -> wallet-service: GET /wallet/balance?owner_type=merchant&...
//   - withdrawal-service -> wallet-service: GET /wallet/balance (check balance)
//
// Parameters:
//   - ctx: request-scoped context for cancellation and deadline propagation.
//   - path: the URL path (may include query string) on the target service.
//   - result: a pointer to the struct where the JSON response will be decoded.
//
// Returns nil on success (HTTP 2xx). Returns an error for non-2xx responses.
func (c *ServiceClient) Get(ctx context.Context, path string, result interface{}) error {
	// Build the full URL by combining the base URL with the path.
	url := c.baseURL + path

	// Create a new HTTP GET request with the caller's context.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("httpclient: create GET request: %w", err)
	}

	// Execute the HTTP request against the target service.
	return c.doRequest(req, result)
}

// doRequest executes the given HTTP request, checks the response status code,
// and optionally decodes the JSON response body into the result pointer.
//
// This is the internal workhorse method shared by Post() and Get(). It handles:
//  1. Sending the request via the underlying http.Client.
//  2. Reading and closing the response body (preventing resource leaks).
//  3. Checking for non-2xx status codes and returning descriptive errors.
//  4. Decoding the JSON response body into the caller's result struct.
//
// Parameters:
//   - req: the fully constructed *http.Request to execute.
//   - result: the target for JSON decoding; pass nil to discard the body.
//
// Returns nil on HTTP 2xx success, or an error with status and body details.
func (c *ServiceClient) doRequest(req *http.Request, result interface{}) error {
	// Send the HTTP request to the target service.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network-level errors: connection refused, DNS failure, timeout, etc.
		// These indicate the target service is unreachable.
		return fmt.Errorf("httpclient: %s %s: %w", req.Method, req.URL.String(), err)
	}
	// Always close the response body to release the underlying TCP connection
	// back to the connection pool. Failing to close causes connection leaks.
	defer resp.Body.Close()

	// Read the full response body. We read eagerly so that we can include
	// the body in error messages for non-2xx responses, and so we can
	// decode it into the result struct on success.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("httpclient: read response body: %w", err)
	}

	// Check for non-2xx HTTP status codes. Any status outside the 200-299
	// range indicates the target service rejected the request.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Include the status code and (truncated) response body in the error
		// so the caller has enough context for logging and debugging.
		bodyPreview := string(respBody)
		if len(bodyPreview) > 512 {
			bodyPreview = bodyPreview[:512] + "..."
		}
		return fmt.Errorf("httpclient: %s %s returned status %d: %s",
			req.Method, req.URL.String(), resp.StatusCode, bodyPreview)
	}

	// If the caller provided a result pointer, decode the JSON response body.
	// If result is nil, the caller does not need the response data.
	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("httpclient: decode response from %s: %w", req.URL.String(), err)
		}
	}

	return nil
}
