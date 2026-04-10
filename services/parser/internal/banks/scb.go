package banks

// scb.go implements the BankParser interface for SCB (Siam Commercial Bank).
//
// SCB sends SMS notifications for incoming transfers in the following format:
//
//   "บช. xx1234 รับโอน 1,000.00 บ. จาก สมชาย วันที่ 10/04/26 เวลา 14:30"
//
// Translation:
//   "Acct. xx1234 received transfer 1,000.00 THB from Somchai date 10/04/26 time 14:30"
//
// The key difference from KBANK is the leading account fragment ("บช. xx1234")
// and the use of "รับโอน" (received transfer) instead of "รับเงิน" (received money).

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// scbThaiPattern matches the SCB Thai-language SMS format.
// Capture groups:
//   1. Partial account number (e.g. "xx1234")
//   2. Amount with commas and decimals (e.g. "1,000.00")
//   3. Sender name (e.g. "สมชาย")
//   4. Date in DD/MM/YY format (e.g. "10/04/26")
//   5. Time in HH:MM format (e.g. "14:30")
var scbThaiPattern = regexp.MustCompile(
	`บช\.\s*(\S+)\s+รับโอน\s+([\d,]+\.\d{2})\s*บ\.\s*จาก\s+(.+?)\s+วันที่\s+(\d{2}/\d{2}/\d{2})\s+เวลา\s+(\d{2}:\d{2})`,
)

// scbEngPattern matches an optional English-language SCB SMS format.
// Some SCB accounts configured for English may receive messages like:
//   "Acct xx1234 transfer received THB 1,000.00 from SOMCHAI on 10/04/26 at 14:30"
// Capture groups:
//   1. Partial account number (e.g. "xx1234")
//   2. Amount with commas and decimals (e.g. "1,000.00")
//   3. Sender name (e.g. "SOMCHAI")
//   4. Date in DD/MM/YY format (e.g. "10/04/26")
//   5. Time in HH:MM format (e.g. "14:30")
var scbEngPattern = regexp.MustCompile(
	`(?i)Acct\s+(\S+)\s+transfer\s+received\s+THB\s+([\d,]+\.\d{2})\s+from\s+(.+?)\s+on\s+(\d{2}/\d{2}/\d{2})\s+at\s+(\d{2}:\d{2})`,
)

// scbSenders is the list of known SCB SMS sender phone numbers.
// Siam Commercial Bank uses these short-codes and alphanumeric sender IDs
// to deliver transaction notification SMSes in Thailand.
var scbSenders = []string{
	"+66218951111", // Primary SCB notification sender
	"SCB",          // Alphanumeric sender ID
	"SCBeasy",      // Sender ID used by SCB Easy app notifications
}

// SCBParser implements BankParser for Siam Commercial Bank (SCB).
// It handles both Thai and English SMS formats using dedicated regex patterns.
type SCBParser struct{}

// init registers the SCB parser with the global registry at program start.
// This ensures SCB support is available without manual wiring in main().
func init() {
	RegisterParser(&SCBParser{})
}

// BankCode returns "SCB", the standard short identifier for Siam Commercial Bank.
// This code is persisted in every parsed transaction originating from SCB
// and is used for bank account lookups and reporting.
func (p *SCBParser) BankCode() string {
	return "SCB"
}

// CanParse returns true if the SMS appears to be an SCB transfer notification.
// It verifies both the sender number and the message content contain
// SCB-specific markers.
//
// Parameters:
//   - senderNumber: the originating phone number or alphanumeric sender ID.
//   - message:      the full SMS body text.
//
// Returns:
//   - true if this parser should handle the SMS.
func (p *SCBParser) CanParse(senderNumber string, message string) bool {
	// Step 1: Verify the sender is a known SCB sender.
	senderMatch := false
	for _, s := range scbSenders {
		if strings.EqualFold(senderNumber, s) {
			senderMatch = true
			break
		}
	}
	if !senderMatch {
		return false
	}

	// Step 2: Check for SCB-specific keywords in the message body.
	// Thai: "รับโอน" means "received transfer".
	// English: "transfer received" is the SCB English equivalent.
	lowerMsg := strings.ToLower(message)
	return strings.Contains(message, "รับโอน") || strings.Contains(lowerMsg, "transfer received")
}

// Parse extracts structured transaction data from an SCB SMS message.
// It tries the Thai regex first, then falls back to the English regex.
// The function assumes CanParse() has already returned true.
//
// Parameters:
//   - message: the full SMS body text.
//
// Returns:
//   - A populated ParsedTransaction on success.
//   - An error if neither regex matches or if amount/date parsing fails.
func (p *SCBParser) Parse(message string) (*ParsedTransaction, error) {
	// Try Thai format first (overwhelmingly more common in Thailand).
	if matches := scbThaiPattern.FindStringSubmatch(message); matches != nil {
		return p.buildTransaction(matches, message)
	}

	// Fall back to English format for accounts set to English language.
	if matches := scbEngPattern.FindStringSubmatch(message); matches != nil {
		return p.buildTransaction(matches, message)
	}

	// No regex matched - the SMS format is not recognised.
	return nil, fmt.Errorf("scb: unable to parse message, no regex match")
}

// buildTransaction constructs a ParsedTransaction from regex capture groups.
// Both the Thai and English patterns use the same capture group layout:
//   [1] account fragment, [2] amount, [3] sender name, [4] date, [5] time
//
// The account fragment (e.g. "xx1234") is used as the transaction reference
// since SCB does not include a separate reference number in the SMS.
//
// Parameters:
//   - matches: the regex submatch slice (len >= 6).
//   - rawMsg:  the original unmodified SMS text.
//
// Returns:
//   - A populated ParsedTransaction on success.
//   - An error if amount parsing or date parsing fails.
func (p *SCBParser) buildTransaction(matches []string, rawMsg string) (*ParsedTransaction, error) {
	// The partial account number serves as our reference since SCB does not
	// include a dedicated reference ID in the SMS body.
	reference := strings.TrimSpace(matches[1])

	// Parse the amount by removing comma thousands-separators.
	// Example: "1,000.00" -> "1000.00" -> decimal.Decimal
	amountStr := strings.ReplaceAll(matches[2], ",", "")
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return nil, fmt.Errorf("scb: invalid amount %q: %w", matches[2], err)
	}

	// Extract and clean the sender name.
	senderName := strings.TrimSpace(matches[3])

	// Parse the timestamp from DD/MM/YY HH:MM format.
	// SCB uses CE two-digit years in SMS (e.g. "26" = 2026).
	dateTimeStr := matches[4] + " " + matches[5]
	ts, err := time.Parse("02/01/06 15:04", dateTimeStr)
	if err != nil {
		return nil, fmt.Errorf("scb: invalid timestamp %q: %w", dateTimeStr, err)
	}

	// Localise to Bangkok timezone (ICT, UTC+7) since all SCB timestamps
	// are in Thai local time.
	bangkokLoc, _ := time.LoadLocation("Asia/Bangkok")
	ts = time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), 0, 0, bangkokLoc)

	return &ParsedTransaction{
		BankCode:   p.BankCode(),
		Amount:     amount,
		Reference:  reference,
		SenderName: senderName,
		Timestamp:  ts,
		RawMessage: rawMsg,
	}, nil
}

// ValidSenders returns the list of phone numbers and sender IDs that SCB
// uses to deliver transaction notification SMSes. Used by the anti-spoofing
// layer to filter out messages from unrecognised senders.
func (p *SCBParser) ValidSenders() []string {
	return scbSenders
}
