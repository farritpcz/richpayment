// This file provides an authenticated HTTP client for internal service-to-service
// communication within the RichPayment microservices architecture. It wraps the
// standard ServiceClient with automatic HMAC-SHA256 authentication headers and
// retry-with-backoff logic.
//
// # Why This Exists
//
// The base ServiceClient (client.go) provides plain HTTP calls with no
// authentication. In a zero-trust architecture, every inter-service call must
// be authenticated to prevent lateral movement after a service compromise.
//
// This InternalServiceClient automatically adds three authentication headers
// to every outgoing request:
//
//   - X-Internal-Service:   identifies the calling service
//   - X-Internal-Timestamp: current Unix timestamp for freshness validation
//   - X-Internal-Signature: HMAC-SHA256(secret, "timestamp.service_name.path")
//
// The receiving service validates these headers using the InternalAuth middleware
// (see pkg/middleware/internal_auth.go).
//
// # Retry Strategy
//
// Transient failures (network errors, 502/503/504 responses) are retried up to
// 3 times with exponential backoff (1s, 2s, 4s). This improves resilience
// against brief network blips and rolling deployments without overwhelming
// a struggling target service.
//
// # Usage
//
//	client := httpclient.NewInternalClient(
//	    "http://localhost:8084",
//	    "order-service",
//	    os.Getenv("INTERNAL_API_SECRET"),
//	)
//	err := client.Post(ctx, "/wallet/credit", creditReq, &resp)
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/farritpcz/richpayment/pkg/crypto"
)

// defaultTimeout is the maximum duration for a single HTTP request attempt.
// This includes connection setup, sending the request, and reading the full
// response. 10 seconds is generous for internal calls on a local network
// while still preventing indefinite hangs.
const defaultTimeout = 10 * time.Second

// maxRetries is the maximum number of times a request will be retried after
// a transient failure. Combined with exponential backoff, 3 retries cover
// up to ~7 seconds of outage (1s + 2s + 4s) before giving up.
const maxRetries = 3

// initialBackoff is the delay before the first retry attempt. Subsequent
// retries double this value (exponential backoff): 1s, 2s, 4s.
const initialBackoff = 1 * time.Second

// InternalServiceClient is an authenticated HTTP client for making calls to
// other RichPayment internal services. It automatically signs every request
// with HMAC-SHA256 headers and retries transient failures with exponential
// backoff.
//
// Each instance is configured for a specific target service (baseURL) and
// identifies itself with a service name. For example, the order-service
// would create separate InternalServiceClient instances for each service
// it calls (wallet, commission, notification, etc.).
type InternalServiceClient struct {
	// baseURL is the scheme + host + port of the target service.
	// Example: "http://localhost:8084" for wallet-service.
	baseURL string

	// serviceName is the name of the calling service, sent in the
	// X-Internal-Service header. This is used by the receiving service's
	// InternalAuth middleware and ServiceACL middleware to identify and
	// authorize the caller.
	// Example: "order-service", "withdrawal-service", "gateway-api"
	serviceName string

	// secret is the shared HMAC signing key used to compute the
	// X-Internal-Signature header. Must match the INTERNAL_API_SECRET
	// environment variable on the receiving service.
	secret string

	// httpClient is the underlying Go HTTP client with a configured timeout.
	// The timeout applies to each individual request attempt (not the total
	// time including retries).
	httpClient *http.Client
}

// NewInternalClient creates a new authenticated InternalServiceClient.
//
// Parameters:
//   - baseURL: the root URL of the target service (e.g., "http://localhost:8084").
//   - serviceName: the name of THIS service (the caller), sent in X-Internal-Service.
//     Must match the name expected by the target's ServiceACL rules.
//   - secret: the shared HMAC signing key. Should be loaded from the
//     INTERNAL_API_SECRET environment variable.
//
// Returns a ready-to-use *InternalServiceClient with a 10-second default timeout
// per request attempt and 3 retries with exponential backoff.
func NewInternalClient(baseURL, serviceName, secret string) *InternalServiceClient {
	return &InternalServiceClient{
		baseURL:     baseURL,
		serviceName: serviceName,
		secret:      secret,
		httpClient: &http.Client{
			// Each individual request attempt has a 10-second timeout.
			// Retried attempts each get their own fresh timeout.
			Timeout: defaultTimeout,
		},
	}
}

// Get sends an authenticated HTTP GET request to the specified path on the
// target service and decodes the JSON response into result.
//
// The request is automatically signed with HMAC-SHA256 authentication headers
// and retried up to 3 times on transient failures.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and deadline propagation.
//   - path: the URL path on the target service (e.g., "/wallet/balance").
//   - result: a pointer to the struct where the JSON response will be decoded.
//     Pass nil to discard the response body.
//
// Returns nil on success (HTTP 2xx), or an error with status and body details.
func (c *InternalServiceClient) Get(ctx context.Context, path string, result interface{}) error {
	return c.doWithRetry(ctx, http.MethodGet, path, nil, result)
}

// Post sends an authenticated HTTP POST request with a JSON-encoded body to
// the specified path on the target service and decodes the JSON response.
//
// This is the primary method for write operations between services. Examples:
//   - order-service -> wallet-service: POST /wallet/credit
//   - order-service -> commission-service: POST /internal/commission/calculate
//   - withdrawal-service -> wallet-service: POST /wallet/debit
//
// Parameters:
//   - ctx: request-scoped context for cancellation and deadline propagation.
//   - path: the URL path on the target service.
//   - body: the request payload; will be marshalled to JSON.
//   - result: a pointer for JSON response decoding; pass nil to discard.
//
// Returns nil on success (HTTP 2xx), or an error with status and body details.
func (c *InternalServiceClient) Post(ctx context.Context, path string, body interface{}, result interface{}) error {
	return c.doWithRetry(ctx, http.MethodPost, path, body, result)
}

// Put sends an authenticated HTTP PUT request with a JSON-encoded body to
// the specified path on the target service and decodes the JSON response.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and deadline propagation.
//   - path: the URL path on the target service.
//   - body: the request payload; will be marshalled to JSON.
//   - result: a pointer for JSON response decoding; pass nil to discard.
//
// Returns nil on success (HTTP 2xx), or an error with status and body details.
func (c *InternalServiceClient) Put(ctx context.Context, path string, body interface{}, result interface{}) error {
	return c.doWithRetry(ctx, http.MethodPut, path, body, result)
}

// Delete sends an authenticated HTTP DELETE request to the specified path on
// the target service and decodes the JSON response.
//
// Parameters:
//   - ctx: request-scoped context for cancellation and deadline propagation.
//   - path: the URL path on the target service.
//   - result: a pointer for JSON response decoding; pass nil to discard.
//
// Returns nil on success (HTTP 2xx), or an error with status and body details.
func (c *InternalServiceClient) Delete(ctx context.Context, path string, result interface{}) error {
	return c.doWithRetry(ctx, http.MethodDelete, path, nil, result)
}

// doWithRetry executes an HTTP request with automatic retry and exponential
// backoff for transient failures. It is the internal workhorse method used
// by all public HTTP methods (Get, Post, Put, Delete).
//
// Retry logic:
//   - Retries on network errors (connection refused, DNS failure, timeout).
//   - Retries on HTTP 502 (Bad Gateway), 503 (Service Unavailable),
//     504 (Gateway Timeout) — these indicate the target is temporarily down.
//   - Does NOT retry on 4xx errors (client errors) — these indicate a bug
//     in the caller, not a transient issue.
//   - Does NOT retry on 5xx errors other than 502/503/504 — a 500 Internal
//     Server Error is unlikely to resolve on retry.
//   - Backoff schedule: 1s, 2s, 4s (exponential, doubling each attempt).
//   - Respects context cancellation — if ctx is cancelled, stops retrying.
//
// Parameters:
//   - ctx: context for cancellation.
//   - method: HTTP method (GET, POST, PUT, DELETE).
//   - path: URL path on the target service.
//   - body: request payload (nil for GET/DELETE).
//   - result: pointer for JSON response decoding.
//
// Returns the error from the last attempt if all retries are exhausted.
func (c *InternalServiceClient) doWithRetry(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	var lastErr error

	// Attempt the request up to maxRetries+1 times (1 initial + 3 retries).
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// If this is a retry (not the first attempt), wait with exponential backoff
		// before trying again. This prevents overwhelming a struggling service.
		if attempt > 0 {
			backoff := initialBackoff * time.Duration(1<<uint(attempt-1)) // 1s, 2s, 4s
			select {
			case <-ctx.Done():
				// Context was cancelled while waiting — stop retrying.
				return fmt.Errorf("internal_client: context cancelled during retry backoff: %w", ctx.Err())
			case <-time.After(backoff):
				// Backoff period elapsed — proceed with the retry.
			}
		}

		// Execute the actual HTTP request with authentication headers.
		lastErr = c.doRequest(ctx, method, path, body, result)
		if lastErr == nil {
			// Request succeeded — no need to retry.
			return nil
		}

		// Determine if the error is retryable. Only retry on transient failures
		// (network errors and specific 5xx status codes).
		if !isRetryable(lastErr) {
			// Non-retryable error (e.g., 400, 401, 404) — fail immediately.
			return lastErr
		}
	}

	// All retry attempts exhausted — return the error from the last attempt.
	return fmt.Errorf("internal_client: all %d retries exhausted: %w", maxRetries, lastErr)
}

// doRequest executes a single HTTP request with authentication headers.
// This method handles:
//  1. JSON marshalling of the request body.
//  2. Building the full URL from baseURL + path.
//  3. Adding HMAC-SHA256 authentication headers.
//  4. Sending the request and reading the response.
//  5. Checking for non-2xx status codes.
//  6. JSON unmarshalling of the response body.
//
// Parameters:
//   - ctx: context for cancellation and timeout.
//   - method: HTTP method string.
//   - path: URL path on the target service.
//   - body: request payload to marshal as JSON (nil for bodiless requests).
//   - result: pointer for JSON response decoding (nil to discard).
//
// Returns nil on HTTP 2xx, or an error with status code and body context.
func (c *InternalServiceClient) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	// ---------------------------------------------------------------
	// 1. Marshal request body (if provided) to JSON bytes.
	// ---------------------------------------------------------------
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("internal_client: marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	// ---------------------------------------------------------------
	// 2. Build the full URL and create the HTTP request.
	// ---------------------------------------------------------------
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("internal_client: create %s request: %w", method, err)
	}

	// Set Content-Type for requests with a body so the receiving service
	// knows to parse JSON.
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// ---------------------------------------------------------------
	// 3. Add internal authentication headers.
	// These headers are validated by the InternalAuth middleware on the
	// receiving service (see pkg/middleware/internal_auth.go).
	// ---------------------------------------------------------------
	c.signRequest(req, path)

	// ---------------------------------------------------------------
	// 4. Send the request and process the response.
	// ---------------------------------------------------------------
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network-level errors: connection refused, DNS failure, timeout, etc.
		return fmt.Errorf("internal_client: %s %s: %w", method, url, err)
	}
	// Always close the response body to return the TCP connection to the pool.
	defer resp.Body.Close()

	// Read the full response body for error reporting and JSON decoding.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("internal_client: read response body: %w", err)
	}

	// ---------------------------------------------------------------
	// 5. Check for non-2xx HTTP status codes.
	// ---------------------------------------------------------------
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyPreview := string(respBody)
		if len(bodyPreview) > 512 {
			bodyPreview = bodyPreview[:512] + "..."
		}
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("internal_client: %s %s returned status %d: %s", method, url, resp.StatusCode, bodyPreview),
		}
	}

	// ---------------------------------------------------------------
	// 6. Decode the JSON response body into the caller's result struct.
	// ---------------------------------------------------------------
	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("internal_client: decode response from %s: %w", url, err)
		}
	}

	return nil
}

// signRequest adds the three internal authentication headers to an outgoing
// HTTP request. These headers form the HMAC-SHA256 authentication protocol
// used by the RichPayment internal service mesh.
//
// The signature is computed over the string "timestamp.service_name.request_path"
// to bind the signature to a specific point in time, a specific caller, and
// a specific endpoint. This prevents:
//   - Replay attacks (timestamp changes every second)
//   - Service impersonation (service name is part of the signed message)
//   - Endpoint redirection (path is part of the signed message)
//
// Parameters:
//   - req: the HTTP request to sign (headers are added in-place).
//   - path: the URL path being called (e.g., "/wallet/credit").
func (c *InternalServiceClient) signRequest(req *http.Request, path string) {
	// Get the current Unix timestamp as a string.
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Build the message to sign: "timestamp.service_name.request_path"
	// Example: "1712700000.order-service./wallet/credit"
	message := fmt.Sprintf("%s.%s.%s", timestamp, c.serviceName, path)

	// Compute the HMAC-SHA256 signature using the shared secret.
	// The crypto.HMACSign function returns a lowercase hex-encoded string.
	signature := crypto.HMACSign([]byte(message), []byte(c.secret))

	// Set the three authentication headers on the outgoing request.
	req.Header.Set("X-Internal-Service", c.serviceName)
	req.Header.Set("X-Internal-Timestamp", timestamp)
	req.Header.Set("X-Internal-Signature", signature)
}

// HTTPError represents an HTTP response with a non-2xx status code. It carries
// the status code so that retry logic can determine if the error is retryable
// (e.g., 503 Service Unavailable) or permanent (e.g., 400 Bad Request).
type HTTPError struct {
	// StatusCode is the HTTP status code from the response (e.g., 401, 503).
	StatusCode int

	// Message is a human-readable description including the URL, status code,
	// and a preview of the response body.
	Message string
}

// Error implements the error interface for HTTPError, returning the
// human-readable message that includes the HTTP status code and response body.
func (e *HTTPError) Error() string {
	return e.Message
}

// isRetryable determines whether an error from doRequest should trigger a retry.
// Only transient failures are retried:
//   - Network errors (connection refused, DNS failure, timeout) — these are
//     plain errors without an HTTPError wrapper.
//   - HTTP 502 (Bad Gateway) — upstream proxy got a bad response.
//   - HTTP 503 (Service Unavailable) — target is temporarily overloaded.
//   - HTTP 504 (Gateway Timeout) — upstream proxy timed out.
//
// All other errors (including 4xx client errors and 500 internal server errors)
// are considered permanent and are NOT retried.
//
// Parameters:
//   - err: the error returned by doRequest.
//
// Returns true if the error is transient and the request should be retried.
func isRetryable(err error) bool {
	// If the error is an HTTPError, check the status code.
	if httpErr, ok := err.(*HTTPError); ok {
		switch httpErr.StatusCode {
		case http.StatusBadGateway, // 502
			http.StatusServiceUnavailable, // 503
			http.StatusGatewayTimeout:     // 504
			return true
		default:
			// 4xx errors and other 5xx errors are not retryable.
			return false
		}
	}
	// Non-HTTP errors (network failures, timeouts) are retryable.
	return true
}
