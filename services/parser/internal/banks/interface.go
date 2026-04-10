// Package banks defines the plugin interface and registry for bank SMS parsers.
//
// The parser-service uses a plugin architecture to support multiple Thai banks.
// Each bank has distinct SMS formats for incoming transfer notifications; a
// dedicated parser implements the BankParser interface to extract structured
// transaction data from the raw SMS text.
//
// To add support for a new bank:
//  1. Create a new file (e.g. bbl.go) in this package.
//  2. Define a struct that implements BankParser.
//  3. Call RegisterParser() in an init() function so the parser is
//     automatically available at startup.
package banks

import (
	"time"

	"github.com/shopspring/decimal"
)

// ParsedTransaction represents the structured data extracted from a bank
// notification SMS. Every bank parser must produce this struct from the raw
// SMS text. Fields are intentionally kept flat (no nested structs) to make
// JSON serialisation and database mapping straightforward.
type ParsedTransaction struct {
	// BankCode is the short identifier for the bank that sent the SMS.
	// Examples: "KBANK" (Kasikorn), "SCB" (Siam Commercial), "BBL" (Bangkok Bank).
	BankCode string

	// Amount is the transaction amount in Thai Baht (THB).
	// We use shopspring/decimal to avoid floating-point rounding errors that
	// are unacceptable in financial calculations.
	Amount decimal.Decimal

	// Reference is the bank-generated transaction reference number.
	// Some banks include it in the SMS; if absent the parser may leave it empty.
	Reference string

	// SenderName is the name of the person or entity that initiated the transfer.
	// Thai bank SMSes typically include a partial name (e.g. "สมชาย xxx").
	SenderName string

	// ReceiverName is the name associated with the receiving bank account.
	// Not all SMS formats include this; it may be empty.
	ReceiverName string

	// Timestamp is when the bank says the transaction occurred.
	// Parsed from the date/time portion of the SMS text.
	Timestamp time.Time

	// RawMessage is the original, unmodified SMS text.
	// Stored for audit trails and debugging when parsing goes wrong.
	RawMessage string
}

// BankParser is the interface that every bank SMS parser must implement.
//
// The parser-service iterates over all registered BankParsers to find one
// that can handle an incoming SMS. The lookup is two-phase:
//   - ValidSenders() provides a fast pre-filter by sender phone number.
//   - CanParse() performs a deeper content-based check on the message body.
//
// Once a matching parser is found, Parse() is called to extract the
// structured transaction data.
type BankParser interface {
	// BankCode returns the short identifier for this bank (e.g. "KBANK", "SCB").
	// This value is stored alongside every parsed transaction for downstream
	// routing and reporting.
	BankCode() string

	// CanParse returns true if this parser is capable of handling the given SMS.
	// senderNumber is the originating phone number (e.g. "+66xxxxxxx") and
	// message is the full SMS body text. Implementations should check for
	// bank-specific keywords or patterns to confirm ownership.
	CanParse(senderNumber string, message string) bool

	// Parse extracts structured transaction data from the raw SMS text.
	// It returns a fully populated ParsedTransaction on success, or an error
	// if the message format is unrecognised or critical fields cannot be extracted.
	// Parse is only called after CanParse has returned true.
	Parse(message string) (*ParsedTransaction, error)

	// ValidSenders returns the list of known SMS sender phone numbers for this
	// bank. Thai banks use dedicated short-codes or phone numbers to send
	// transaction alerts. This list is used for anti-spoofing validation:
	// an SMS claiming to be from KBANK but arriving from an unknown number
	// is rejected before parsing begins.
	ValidSenders() []string
}
