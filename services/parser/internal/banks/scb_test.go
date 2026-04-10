package banks

import (
	"testing"

	"github.com/shopspring/decimal"
)

// =============================================================================
// TestSCBParser_CanParse_ValidSender verifies that CanParse returns true for
// all known SCB sender numbers when the message contains SCB keywords.
//
// SCB uses these sender identifiers in Thailand:
//   - "+66218951111" — primary SCB notification sender
//   - "SCB"         — alphanumeric sender ID
//   - "SCBeasy"     — sender ID used by the SCB Easy app
//
// CanParse should accept all of them with case-insensitive matching.
// =============================================================================
func TestSCBParser_CanParse_ValidSender(t *testing.T) {
	p := &SCBParser{}

	// Thai message with "รับโอน" keyword.
	thaiMsg := "บช. xx1234 รับโอน 1,000.00 บ. จาก สมชาย วันที่ 10/04/26 เวลา 14:30"

	// English message with "transfer received" keyword.
	engMsg := "Acct xx1234 transfer received THB 1,000.00 from SOMCHAI on 10/04/26 at 14:30"

	senders := []string{"+66218951111", "SCB", "SCBeasy"}
	for _, sender := range senders {
		t.Run("sender_"+sender+"_thai", func(t *testing.T) {
			if !p.CanParse(sender, thaiMsg) {
				t.Errorf("CanParse(%q, thaiMsg) = false, want true", sender)
			}
		})
		t.Run("sender_"+sender+"_english", func(t *testing.T) {
			if !p.CanParse(sender, engMsg) {
				t.Errorf("CanParse(%q, engMsg) = false, want true", sender)
			}
		})
	}

	// Case-insensitive sender matching: "scb" (lowercase) should work.
	t.Run("case_insensitive_sender", func(t *testing.T) {
		if !p.CanParse("scb", thaiMsg) {
			t.Error("CanParse should be case-insensitive for sender, but 'scb' was rejected")
		}
	})
}

// =============================================================================
// TestSCBParser_CanParse_InvalidSender verifies that CanParse rejects messages
// from unknown sender numbers, even if the message body looks like an SCB SMS.
//
// This anti-spoofing check is critical because attackers can forge SMS sender
// IDs to make fraudulent messages appear to come from a bank.
// =============================================================================
func TestSCBParser_CanParse_InvalidSender(t *testing.T) {
	p := &SCBParser{}

	thaiMsg := "บช. xx1234 รับโอน 1,000.00 บ. จาก สมชาย วันที่ 10/04/26 เวลา 14:30"

	invalidSenders := []string{
		"+66999999999",  // Random Thai number
		"KBANK",         // Wrong bank's sender ID
		"BBL",           // Bangkok Bank sender ID
		"",              // Empty sender
	}

	for _, sender := range invalidSenders {
		t.Run("reject_"+sender, func(t *testing.T) {
			if p.CanParse(sender, thaiMsg) {
				t.Errorf("CanParse(%q, ...) = true, want false; should reject unknown sender", sender)
			}
		})
	}
}

// =============================================================================
// TestSCBParser_Parse_ThaiFormat verifies parsing of a Thai-language SCB SMS.
//
// The Thai format is:
//   "บช. xx1234 รับโอน 2,500.75 บ. จาก สมหญิง วันที่ 10/04/26 เวลา 14:30"
//
// Key differences from KBANK:
//   - Starts with "บช." (account) followed by a partial account number
//   - Uses "รับโอน" (received transfer) instead of "รับเงิน" (received money)
//   - The account fragment appears at the start, not in the middle
//
// We verify all extracted fields match expectations.
// =============================================================================
func TestSCBParser_Parse_ThaiFormat(t *testing.T) {
	p := &SCBParser{}

	msg := "บช. xx1234 รับโอน 2,500.75 บ. จาก สมหญิง วันที่ 10/04/26 เวลา 14:30"

	result, err := p.Parse(msg)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	// BankCode should be "SCB".
	if result.BankCode != "SCB" {
		t.Errorf("BankCode = %q, want %q", result.BankCode, "SCB")
	}

	// Amount: "2,500.75" -> 2500.75
	expectedAmount := decimal.NewFromFloat(2500.75)
	if !result.Amount.Equal(expectedAmount) {
		t.Errorf("Amount = %s, want %s", result.Amount, expectedAmount)
	}

	// SenderName from the "จาก" (from) field.
	if result.SenderName != "สมหญิง" {
		t.Errorf("SenderName = %q, want %q", result.SenderName, "สมหญิง")
	}

	// Reference is the partial account number from "บช." prefix.
	if result.Reference != "xx1234" {
		t.Errorf("Reference = %q, want %q", result.Reference, "xx1234")
	}

	// Timestamp: 10/04/26 14:30 -> April 10, 2026, 14:30 Bangkok time.
	if result.Timestamp.Year() != 2026 {
		t.Errorf("Timestamp.Year = %d, want 2026", result.Timestamp.Year())
	}
	if result.Timestamp.Month() != 4 {
		t.Errorf("Timestamp.Month = %d, want 4 (April)", result.Timestamp.Month())
	}
	if result.Timestamp.Day() != 10 {
		t.Errorf("Timestamp.Day = %d, want 10", result.Timestamp.Day())
	}
	if result.Timestamp.Hour() != 14 || result.Timestamp.Minute() != 30 {
		t.Errorf("Timestamp time = %02d:%02d, want 14:30", result.Timestamp.Hour(), result.Timestamp.Minute())
	}

	// RawMessage should be the original SMS.
	if result.RawMessage != msg {
		t.Errorf("RawMessage = %q, want %q", result.RawMessage, msg)
	}
}

// =============================================================================
// TestSCBParser_Parse_EnglishFormat verifies parsing of an English-language
// SCB SMS notification.
//
// The English format is:
//   "Acct xx5678 transfer received THB 10,000.00 from JANE DOE on 15/03/26 at 09:15"
//
// This format is used for English-configured SCB accounts.
// =============================================================================
func TestSCBParser_Parse_EnglishFormat(t *testing.T) {
	p := &SCBParser{}

	msg := "Acct xx5678 transfer received THB 10,000.00 from JANE DOE on 15/03/26 at 09:15"

	result, err := p.Parse(msg)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	if result.BankCode != "SCB" {
		t.Errorf("BankCode = %q, want %q", result.BankCode, "SCB")
	}

	expectedAmount := decimal.NewFromFloat(10000.00)
	if !result.Amount.Equal(expectedAmount) {
		t.Errorf("Amount = %s, want %s", result.Amount, expectedAmount)
	}

	if result.SenderName != "JANE DOE" {
		t.Errorf("SenderName = %q, want %q", result.SenderName, "JANE DOE")
	}

	if result.Reference != "xx5678" {
		t.Errorf("Reference = %q, want %q", result.Reference, "xx5678")
	}

	// Timestamp: 15/03/26 09:15 = March 15, 2026 at 09:15.
	if result.Timestamp.Year() != 2026 || result.Timestamp.Month() != 3 || result.Timestamp.Day() != 15 {
		t.Errorf("Timestamp date = %v, want 2026-03-15", result.Timestamp)
	}
	if result.Timestamp.Hour() != 9 || result.Timestamp.Minute() != 15 {
		t.Errorf("Timestamp time = %02d:%02d, want 09:15", result.Timestamp.Hour(), result.Timestamp.Minute())
	}
}

// =============================================================================
// TestSCBParser_Parse_InvalidMessage verifies that Parse returns an error for
// messages that do not match any known SCB format.
//
// Even if CanParse passed (because the sender and keywords matched), the regex
// might fail on malformed messages. Parse should return a clear error rather
// than panicking or returning partial data.
// =============================================================================
func TestSCBParser_Parse_InvalidMessage(t *testing.T) {
	p := &SCBParser{}

	invalidMessages := []struct {
		name string
		msg  string
	}{
		{
			name: "random text",
			msg:  "Hello, this is not a bank notification at all.",
		},
		{
			name: "KBANK format not SCB",
			msg:  "รับเงิน 1,000.00 บ. จาก สมชาย xxx เข้า xxx-x-x1234-x เวลา 10/04/26 14:30",
		},
		{
			name: "empty message",
			msg:  "",
		},
		{
			name: "partial SCB format missing amount",
			msg:  "บช. xx1234 รับโอน จาก สมชาย วันที่ 10/04/26 เวลา 14:30",
		},
	}

	for _, tt := range invalidMessages {
		t.Run(tt.name, func(t *testing.T) {
			result, err := p.Parse(tt.msg)
			if err == nil {
				t.Errorf("Parse(%q) should return an error for invalid message, got result: %+v", tt.msg, result)
			}
		})
	}
}

// =============================================================================
// TestSCBParser_ValidSenders verifies that ValidSenders returns the expected
// list of SCB sender numbers.
//
// Missing a sender from this list means legitimate SCB notifications from that
// number will be silently dropped, causing deposits to go unmatched.
// =============================================================================
func TestSCBParser_ValidSenders(t *testing.T) {
	p := &SCBParser{}
	senders := p.ValidSenders()

	// We expect at least 3 known senders.
	if len(senders) < 3 {
		t.Errorf("ValidSenders() returned %d senders, want at least 3", len(senders))
	}

	// Check that the primary sender number is in the list.
	found := false
	for _, s := range senders {
		if s == "+66218951111" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ValidSenders() does not include the primary SCB sender '+66218951111'")
	}

	// Check for "SCB" alphanumeric sender.
	foundAlpha := false
	for _, s := range senders {
		if s == "SCB" {
			foundAlpha = true
			break
		}
	}
	if !foundAlpha {
		t.Error("ValidSenders() does not include 'SCB' alphanumeric sender")
	}

	// Check for "SCBeasy" sender.
	foundEasy := false
	for _, s := range senders {
		if s == "SCBeasy" {
			foundEasy = true
			break
		}
	}
	if !foundEasy {
		t.Error("ValidSenders() does not include 'SCBeasy' sender")
	}
}

// =============================================================================
// TestSCBParser_BankCode verifies the BankCode() method returns "SCB".
//
// This value is stored in every parsed transaction from SCB and is used
// for downstream routing and bank account matching.
// =============================================================================
func TestSCBParser_BankCode(t *testing.T) {
	p := &SCBParser{}
	if code := p.BankCode(); code != "SCB" {
		t.Errorf("BankCode() = %q, want %q", code, "SCB")
	}
}
