package banks

// kbank.go implements the BankParser interface for KBANK (Kasikorn Bank).
//
// Kasikorn Bank sends SMS notifications in two primary formats:
//
// Thai format:
//   "รับเงิน 1,000.00 บ. จาก สมชาย xxx เข้า xxx-x-x1234-x เวลา 10/04/26 14:30"
//
// English format:
//   "Received THB 1,000.00 from SOMCHAI xxx to xxx-x-x1234-x at 10/04/26 14:30"
//
// Both formats share the same structure: an amount, a sender name, a partial
// account number, and a timestamp. The parser uses two separate regex patterns
// (one per language) and tries each in order.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// kbankThaiPattern matches the Thai-language KBANK SMS format.
// Capture groups:
//   1. Amount with commas and decimals (e.g. "1,000.00")
//   2. Sender name (e.g. "สมชาย xxx")
//   3. Partial account number (e.g. "xxx-x-x1234-x")
//   4. Date in DD/MM/YY format (e.g. "10/04/26")
//   5. Time in HH:MM format (e.g. "14:30")
var kbankThaiPattern = regexp.MustCompile(
	`รับเงิน\s+([\d,]+\.\d{2})\s*บ\.\s*จาก\s+(.+?)\s+เข้า\s+(\S+)\s+เวลา\s+(\d{2}/\d{2}/\d{2})\s+(\d{2}:\d{2})`,
)

// kbankEngPattern matches the English-language KBANK SMS format.
// Capture groups:
//   1. Amount with commas and decimals (e.g. "1,000.00")
//   2. Sender name (e.g. "SOMCHAI xxx")
//   3. Partial account number (e.g. "xxx-x-x1234-x")
//   4. Date in DD/MM/YY format (e.g. "10/04/26")
//   5. Time in HH:MM format (e.g. "14:30")
var kbankEngPattern = regexp.MustCompile(
	`(?i)Received\s+THB\s+([\d,]+\.\d{2})\s+from\s+(.+?)\s+to\s+(\S+)\s+at\s+(\d{2}/\d{2}/\d{2})\s+(\d{2}:\d{2})`,
)

// kbankSenders is the list of known KBANK SMS sender phone numbers.
// These are the short-code numbers that Kasikorn Bank uses to send
// transaction notification SMSes in Thailand. The service rejects any
// SMS claiming to be from KBANK but originating from a number not in
// this list (anti-spoofing).
var kbankSenders = []string{
	"+66868882888", // Primary KBANK notification sender
	"KBANK",        // Alphanumeric sender ID used on some carriers
	"KBank",        // Alternate capitalisation seen on certain gateways
}

// KBankParser implements BankParser for Kasikorn Bank (KBANK).
// It handles both Thai and English SMS formats by trying two regex
// patterns sequentially.
type KBankParser struct{}

// init registers the KBANK parser with the global registry at program start.
// This ensures KBANK support is available without any manual wiring in main().
func init() {
	RegisterParser(&KBankParser{})
}

// BankCode returns "KBANK", the standard short identifier for Kasikorn Bank.
// This code is stored in the parsed transaction and used for downstream
// routing, reporting, and bank account lookups.
func (p *KBankParser) BankCode() string {
	return "KBANK"
}

// CanParse returns true if the SMS appears to be a KBANK transfer notification.
// It first checks whether the sender number matches a known KBANK sender,
// then verifies the message body contains KBANK-specific keywords.
//
// Parameters:
//   - senderNumber: the originating phone number or alphanumeric sender ID.
//   - message:      the full SMS body text.
//
// Returns:
//   - true if this parser should handle the SMS.
func (p *KBankParser) CanParse(senderNumber string, message string) bool {
	// Step 1: Verify the sender number is in the known KBANK senders list.
	senderMatch := false
	for _, s := range kbankSenders {
		if strings.EqualFold(senderNumber, s) {
			senderMatch = true
			break
		}
	}
	if !senderMatch {
		return false
	}

	// Step 2: Check message body for KBANK-specific keywords.
	// Thai format contains "รับเงิน" (received money).
	// English format contains "Received THB".
	lowerMsg := strings.ToLower(message)
	return strings.Contains(message, "รับเงิน") || strings.Contains(lowerMsg, "received thb")
}

// Parse extracts structured transaction data from a KBANK SMS message.
// It tries the Thai regex first, then falls back to the English regex.
// The function assumes CanParse() has already returned true.
//
// Parameters:
//   - message: the full SMS body text.
//
// Returns:
//   - A populated ParsedTransaction on success.
//   - An error if neither regex matches or if amount/date parsing fails.
func (p *KBankParser) Parse(message string) (*ParsedTransaction, error) {
	// Try Thai format first (more common for domestic transfers).
	if matches := kbankThaiPattern.FindStringSubmatch(message); matches != nil {
		return p.buildTransaction(matches, message)
	}

	// Fall back to English format.
	if matches := kbankEngPattern.FindStringSubmatch(message); matches != nil {
		return p.buildTransaction(matches, message)
	}

	// Neither pattern matched - the message format is unrecognised.
	return nil, fmt.Errorf("kbank: unable to parse message, no regex match")
}

// buildTransaction constructs a ParsedTransaction from regex capture groups.
// Both the Thai and English patterns share the same capture group layout:
//   [1] amount, [2] sender name, [3] account ref, [4] date, [5] time
//
// Parameters:
//   - matches: the regex submatch slice (len >= 6).
//   - rawMsg:  the original unmodified SMS text.
//
// Returns:
//   - A populated ParsedTransaction on success.
//   - An error if amount parsing or date parsing fails.
func (p *KBankParser) buildTransaction(matches []string, rawMsg string) (*ParsedTransaction, error) {
	// Parse the amount string by stripping commas first.
	// Example: "1,000.00" -> "1000.00" -> decimal.Decimal
	amountStr := strings.ReplaceAll(matches[1], ",", "")
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return nil, fmt.Errorf("kbank: invalid amount %q: %w", matches[1], err)
	}

	// Extract the sender name and trim any trailing whitespace.
	senderName := strings.TrimSpace(matches[2])

	// The partial account number serves as a reference.
	reference := strings.TrimSpace(matches[3])

	// Parse the timestamp from DD/MM/YY HH:MM format.
	// Thai banks use Buddhist Era years in some contexts, but SMS short
	// format uses CE two-digit years (e.g. "26" = 2026).
	dateTimeStr := matches[4] + " " + matches[5]
	ts, err := time.Parse("02/01/06 15:04", dateTimeStr)
	if err != nil {
		return nil, fmt.Errorf("kbank: invalid timestamp %q: %w", dateTimeStr, err)
	}

	// Localise to Bangkok timezone (UTC+7) since bank timestamps are local.
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

// ValidSenders returns the list of phone numbers and sender IDs that KBANK
// uses to send transaction notification SMSes. This is used by the anti-
// spoofing layer to reject messages from unknown senders before parsing.
func (p *KBankParser) ValidSenders() []string {
	return kbankSenders
}
