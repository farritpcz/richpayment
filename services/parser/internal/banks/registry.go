package banks

// registry.go implements a global parser registry that bank parser plugins
// register themselves into via init() functions. At runtime the service
// iterates over registered parsers to find one that can handle a given SMS.
//
// The registry is intentionally kept simple: a package-level slice that is
// populated at init-time (before any goroutines are spawned), so there are
// no concurrency concerns during registration. Read access from request
// handlers is safe because the slice is never mutated after program start.

import (
	"strings"
)

// parsers holds every registered BankParser implementation.
// Each bank plugin (kbank.go, scb.go, etc.) appends to this slice in its
// init() function. The slice is read-only after program initialisation.
var parsers []BankParser

// RegisterParser adds a BankParser implementation to the global registry.
// This function must be called from an init() function inside each bank
// parser file. Calling it after the program has started serving requests
// is unsafe (no mutex protection) and will lead to data races.
//
// Example usage in a bank parser file:
//
//	func init() {
//	    RegisterParser(&KBankParser{})
//	}
func RegisterParser(parser BankParser) {
	parsers = append(parsers, parser)
}

// FindParser iterates over all registered parsers and returns the first one
// whose CanParse method returns true for the given sender number and message.
//
// The lookup order is determined by registration order (i.e. init() execution
// order), which in practice is alphabetical by filename within this package.
// If no parser matches, nil is returned and the caller should treat the SMS
// as unrecognised.
//
// Parameters:
//   - senderNumber: the phone number that sent the SMS (e.g. "+66xxxxxxx").
//   - message:      the full SMS body text.
//
// Returns:
//   - A matching BankParser, or nil if no parser can handle the SMS.
func FindParser(senderNumber string, message string) BankParser {
	// Normalise the sender number by trimming whitespace so callers don't
	// have to worry about trailing newlines or spaces from SMS gateways.
	senderNumber = strings.TrimSpace(senderNumber)
	message = strings.TrimSpace(message)

	for _, p := range parsers {
		if p.CanParse(senderNumber, message) {
			return p
		}
	}
	return nil
}

// GetAllParsers returns a copy of the registered parsers slice.
// Returning a copy prevents callers from accidentally mutating the global
// registry. This is primarily used for diagnostic endpoints (e.g. listing
// which banks are supported) and for iterating sender numbers during
// anti-spoofing validation.
func GetAllParsers() []BankParser {
	// Allocate a new slice and copy elements to guarantee isolation.
	result := make([]BankParser, len(parsers))
	copy(result, parsers)
	return result
}

// IsKnownSender checks whether the given phone number belongs to any
// registered bank's list of valid senders. This is a convenience function
// used by the anti-spoofing step: if the number is not recognised by any
// bank parser, the SMS is rejected immediately.
//
// Parameters:
//   - senderNumber: the phone number to validate (e.g. "+66xxxxxxx").
//
// Returns:
//   - true if at least one registered parser lists this number in ValidSenders().
func IsKnownSender(senderNumber string) bool {
	senderNumber = strings.TrimSpace(senderNumber)
	for _, p := range parsers {
		for _, s := range p.ValidSenders() {
			if s == senderNumber {
				return true
			}
		}
	}
	return false
}
