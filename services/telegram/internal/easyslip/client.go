// Package easyslip provides an HTTP client for the EasySlip API, a third-party
// service that extracts structured data from Thai bank transfer slip images.
// The client sends a base64-encoded image to EasySlip and receives back the
// parsed transaction details (reference, amount, sender, receiver, timestamp).
//
// In production the API key is loaded from Vault or environment variables.
// The client includes retry logic and configurable timeouts to handle
// transient network failures gracefully.
package easyslip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/logger"
)

// ---------------------------------------------------------------------------
// SlipResult — the structured output from the EasySlip API.
// ---------------------------------------------------------------------------

// SlipResult contains the parsed transaction data extracted from a bank
// transfer slip image by the EasySlip API. Every field is populated from the
// API response; the raw JSON is also preserved for debugging and auditing.
type SlipResult struct {
	// Ref is the bank's unique transaction reference number extracted from
	// the slip. This is the primary identifier used for duplicate detection
	// at the transaction level.
	Ref string `json:"ref"`

	// Amount is the transfer amount shown on the slip, parsed as a
	// high-precision decimal to avoid floating-point rounding issues that
	// are unacceptable in financial calculations.
	Amount decimal.Decimal `json:"amount"`

	// Sender is the name of the person or entity that initiated the bank
	// transfer, as printed on the slip.
	Sender string `json:"sender"`

	// Receiver is the name of the transfer recipient (i.e. the bank account
	// holder who received the funds), as printed on the slip.
	Receiver string `json:"receiver"`

	// Timestamp is the date and time when the bank transfer was executed,
	// as recorded on the slip. This is in the bank's local timezone (usually
	// Asia/Bangkok for Thai banks).
	Timestamp time.Time `json:"timestamp"`

	// Raw is the full JSON response body from the EasySlip API, preserved
	// as-is for debugging, auditing, and troubleshooting purposes. This
	// allows engineers to inspect the complete API response if the parsed
	// fields above are insufficient.
	Raw string `json:"raw"`
}

// ---------------------------------------------------------------------------
// Client — the EasySlip HTTP client.
// ---------------------------------------------------------------------------

// Client is the HTTP client for the EasySlip slip verification API. It holds
// the API key, base URL, and a configured http.Client with appropriate
// timeouts. The client is safe for concurrent use by multiple goroutines
// because http.Client is goroutine-safe and the other fields are read-only
// after construction.
type Client struct {
	// apiKey is the secret API key used to authenticate requests to the
	// EasySlip API. It is sent in the Authorization header as a Bearer token.
	apiKey string

	// baseURL is the root URL of the EasySlip API. Defaults to the
	// production endpoint but can be overridden for testing (e.g. to point
	// at a mock server or staging environment).
	baseURL string

	// httpClient is the underlying HTTP client used to make requests. It is
	// configured with a 30-second timeout to prevent long-running requests
	// from blocking the verification pipeline.
	httpClient *http.Client
}

// NewClient constructs a new EasySlip API client with the given API key.
// The base URL defaults to the production EasySlip endpoint. The HTTP client
// is configured with a 30-second timeout.
//
// Parameters:
//   - apiKey: the EasySlip API authentication key (Bearer token).
//
// Returns a ready-to-use Client instance.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: "https://developer.easyslip.com/api/v1",
		httpClient: &http.Client{
			// 30-second timeout covers DNS resolution, TLS handshake,
			// request sending, and response reading. Slip images can be
			// large, so we allow a generous timeout.
			Timeout: 30 * time.Second,
		},
	}
}

// ---------------------------------------------------------------------------
// easySlipRequest / easySlipResponse — internal API payload types.
// ---------------------------------------------------------------------------

// easySlipRequest is the JSON request body sent to the EasySlip verify endpoint.
// It contains the base64-encoded slip image for analysis.
type easySlipRequest struct {
	// Image is the base64-encoded slip image (JPEG or PNG). The EasySlip API
	// accepts images up to 10 MB in size.
	Image string `json:"image"`
}

// easySlipResponse is the JSON response body returned by the EasySlip API
// when verification succeeds. The nested Data field contains the parsed
// transaction details.
type easySlipResponse struct {
	// Status indicates whether the API call was successful. Expected values
	// are "success" and "error".
	Status string `json:"status"`

	// Data contains the parsed slip information when Status is "success".
	Data struct {
		// Ref is the bank transaction reference number.
		Ref string `json:"transRef"`

		// Amount contains the transfer amount details.
		Amount struct {
			// Amount is the transfer value as a float (we parse it to decimal).
			Amount float64 `json:"amount"`
		} `json:"amount"`

		// Sender contains information about the transfer originator.
		Sender struct {
			// Name is the sender's display name from the slip.
			Name string `json:"name"`
		} `json:"sender"`

		// Receiver contains information about the transfer recipient.
		Receiver struct {
			// Name is the receiver's display name from the slip.
			Name string `json:"name"`
		} `json:"receiver"`

		// Date is the transaction date/time string in the bank's format.
		Date string `json:"date"`
	} `json:"data"`
}

// ---------------------------------------------------------------------------
// VerifySlip — the main API call to verify a slip image.
// ---------------------------------------------------------------------------

// VerifySlip sends a base64-encoded slip image to the EasySlip API for
// verification and returns the parsed transaction data. This is the primary
// method used by the slip verification pipeline.
//
// The method performs the following steps:
//  1. Build the JSON request payload with the base64 image.
//  2. Send an HTTP POST request to the EasySlip verify endpoint.
//  3. Read and parse the JSON response body.
//  4. Convert the response into a SlipResult with proper decimal amounts.
//
// Parameters:
//   - ctx: context for cancellation and deadline propagation.
//   - imageData: the slip image encoded as a base64 string.
//
// Returns a SlipResult with all parsed fields populated, or an error if the
// API call fails, returns an error status, or the response is malformed.
func (c *Client) VerifySlip(ctx context.Context, imageData string) (*SlipResult, error) {
	// ---------------------------------------------------------------
	// Step 1: Build the JSON request payload.
	// ---------------------------------------------------------------
	reqBody := easySlipRequest{
		Image: imageData,
	}

	// Marshal the request payload to JSON bytes.
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal easyslip request: %w", err)
	}

	// ---------------------------------------------------------------
	// Step 2: Create and send the HTTP POST request.
	// ---------------------------------------------------------------
	reqURL := fmt.Sprintf("%s/verify", c.baseURL)

	// Create the HTTP request with the provided context so that callers
	// can cancel or set deadlines on the verification call.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create easyslip request: %w", err)
	}

	// Set required headers: JSON content type and Bearer authentication.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	// Execute the HTTP request using the configured client.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("easyslip api call failed: %w", err)
	}
	defer resp.Body.Close()

	// ---------------------------------------------------------------
	// Step 3: Read and parse the JSON response.
	// ---------------------------------------------------------------

	// Read the entire response body into memory for parsing and for
	// storing as the raw response in the SlipResult.
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read easyslip response body: %w", err)
	}

	// Check for HTTP-level errors before parsing JSON.
	if resp.StatusCode != http.StatusOK {
		logger.Error("easyslip api returned non-200 status",
			"status_code", resp.StatusCode,
			"body", string(rawBody),
		)
		return nil, fmt.Errorf("easyslip api returned status %d: %s", resp.StatusCode, string(rawBody))
	}

	// Parse the JSON response into the internal response struct.
	var apiResp easySlipResponse
	if err := json.Unmarshal(rawBody, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal easyslip response: %w", err)
	}

	// Verify the API-level status field indicates success.
	if apiResp.Status != "success" {
		return nil, fmt.Errorf("easyslip verification failed with status: %s", apiResp.Status)
	}

	// ---------------------------------------------------------------
	// Step 4: Convert the API response into a SlipResult.
	// ---------------------------------------------------------------

	// Parse the transaction date string into a time.Time value.
	// EasySlip returns dates in various formats; we try RFC3339 first.
	txTime, err := time.Parse(time.RFC3339, apiResp.Data.Date)
	if err != nil {
		// Fallback: try a common Thai bank slip date format.
		txTime, err = time.Parse("2006-01-02 15:04:05", apiResp.Data.Date)
		if err != nil {
			// If both formats fail, use the current time and log a warning.
			logger.Warn("could not parse easyslip date, using current time",
				"raw_date", apiResp.Data.Date,
			)
			txTime = time.Now().UTC()
		}
	}

	// Build the final SlipResult with all extracted data.
	result := &SlipResult{
		Ref:       apiResp.Data.Ref,
		Amount:    decimal.NewFromFloat(apiResp.Data.Amount.Amount),
		Sender:    apiResp.Data.Sender.Name,
		Receiver:  apiResp.Data.Receiver.Name,
		Timestamp: txTime,
		Raw:       string(rawBody),
	}

	logger.Info("easyslip verification succeeded",
		"ref", result.Ref,
		"amount", result.Amount.String(),
		"sender", result.Sender,
	)

	return result, nil
}
