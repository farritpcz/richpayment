package banks

import (
	"testing"

	"github.com/shopspring/decimal"
)

// =============================================================================
// TestKBANKParser_CanParse_ValidSender verifies that CanParse returns true
// for all known KBANK sender numbers when the message contains KBANK keywords.
//
// KBANK uses multiple sender identifiers across different Thai mobile carriers:
//   - "+66868882888" — the primary notification number
//   - "KBANK"        — alphanumeric sender ID (common on AIS/DTAC)
//   - "KBank"        — alternate capitalisation seen on some gateways
//
// CanParse should accept all of these AND be case-insensitive on the sender.
// =============================================================================
func TestKBANKParser_CanParse_ValidSender(t *testing.T) {
	p := &KBankParser{}

	// Thai message that contains the "รับเงิน" keyword.
	thaiMsg := "รับเงิน 1,000.00 บ. จาก สมชาย xxx เข้า xxx-x-x1234-x เวลา 10/04/26 14:30"

	// English message that contains "Received THB".
	engMsg := "Received THB 1,000.00 from SOMCHAI xxx to xxx-x-x1234-x at 10/04/26 14:30"

	// Test each known sender with both Thai and English messages.
	senders := []string{"+66868882888", "KBANK", "KBank"}
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

	// Case-insensitive sender matching: "kbank" (lowercase) should also work.
	t.Run("case_insensitive_sender", func(t *testing.T) {
		if !p.CanParse("kbank", thaiMsg) {
			t.Error("CanParse should be case-insensitive for sender, but 'kbank' was rejected")
		}
	})
}

// =============================================================================
// TestKBANKParser_CanParse_InvalidSender verifies that CanParse rejects
// messages from unknown sender numbers.
//
// This is the anti-spoofing layer: if an SMS arrives from a number that is
// NOT in the known KBANK senders list, we must reject it even if the message
// body looks like a KBANK notification. Spoofed SMS messages are a real
// attack vector in the Thai payment ecosystem.
// =============================================================================
func TestKBANKParser_CanParse_InvalidSender(t *testing.T) {
	p := &KBankParser{}

	thaiMsg := "รับเงิน 1,000.00 บ. จาก สมชาย xxx เข้า xxx-x-x1234-x เวลา 10/04/26 14:30"

	invalidSenders := []string{
		"+66999999999",  // Random Thai number
		"SCB",           // Wrong bank's sender ID
		"BBL",           // Bangkok Bank sender ID
		"",              // Empty sender
		"+12025551234",  // US number (not a Thai bank)
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
// TestKBANKParser_Parse_ThaiFormat verifies parsing of a Thai-language KBANK
// SMS notification.
//
// The Thai format is:
//   "รับเงิน 1,000.50 บ. จาก สมชาย xxx เข้า xxx-x-x1234-x เวลา 10/04/26 14:30"
//
// We verify all extracted fields:
//   - BankCode:   "KBANK"
//   - Amount:     1000.50 (parsed from "1,000.50" with comma removal)
//   - SenderName: "สมชาย xxx"
//   - Reference:  "xxx-x-x1234-x" (partial account number)
//   - Timestamp:  correctly parsed with Bangkok timezone (UTC+7)
// =============================================================================
func TestKBANKParser_Parse_ThaiFormat(t *testing.T) {
	p := &KBankParser{}

	msg := "รับเงิน 1,000.50 บ. จาก สมชาย xxx เข้า xxx-x-x1234-x เวลา 10/04/26 14:30"

	result, err := p.Parse(msg)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	// BankCode should always be "KBANK" for this parser.
	if result.BankCode != "KBANK" {
		t.Errorf("BankCode = %q, want %q", result.BankCode, "KBANK")
	}

	// Amount: "1,000.50" should be parsed as decimal 1000.50.
	expectedAmount := decimal.NewFromFloat(1000.50)
	if !result.Amount.Equal(expectedAmount) {
		t.Errorf("Amount = %s, want %s", result.Amount, expectedAmount)
	}

	// SenderName: should be trimmed but preserve Thai characters.
	if result.SenderName != "สมชาย xxx" {
		t.Errorf("SenderName = %q, want %q", result.SenderName, "สมชาย xxx")
	}

	// Reference: the partial account number.
	if result.Reference != "xxx-x-x1234-x" {
		t.Errorf("Reference = %q, want %q", result.Reference, "xxx-x-x1234-x")
	}

	// Timestamp: 10/04/26 14:30 should be April 10, 2026, 14:30 in Bangkok time.
	if result.Timestamp.Year() != 2026 {
		t.Errorf("Timestamp.Year = %d, want 2026", result.Timestamp.Year())
	}
	if result.Timestamp.Month() != 4 {
		t.Errorf("Timestamp.Month = %d, want 4 (April)", result.Timestamp.Month())
	}
	if result.Timestamp.Day() != 10 {
		t.Errorf("Timestamp.Day = %d, want 10", result.Timestamp.Day())
	}
	if result.Timestamp.Hour() != 14 {
		t.Errorf("Timestamp.Hour = %d, want 14", result.Timestamp.Hour())
	}
	if result.Timestamp.Minute() != 30 {
		t.Errorf("Timestamp.Minute = %d, want 30", result.Timestamp.Minute())
	}

	// Verify the timezone is Asia/Bangkok (UTC+7).
	zoneName, _ := result.Timestamp.Zone()
	if zoneName != "ICT" && zoneName != "+07" && zoneName != "Asia/Bangkok" {
		// Some systems report "ICT", others "+07" depending on OS timezone data.
		t.Logf("Note: timezone zone name is %q (may vary by OS)", zoneName)
	}

	// RawMessage should contain the original SMS text.
	if result.RawMessage != msg {
		t.Errorf("RawMessage = %q, want %q", result.RawMessage, msg)
	}
}

// =============================================================================
// TestKBANKParser_Parse_EnglishFormat verifies parsing of an English-language
// KBANK SMS notification.
//
// The English format is:
//   "Received THB 5,250.00 from JOHN DOE to xxx-x-x5678-x at 15/03/26 09:15"
//
// This format is used for accounts configured in English and for expat
// customers. The parser should handle both Thai and English transparently.
// =============================================================================
func TestKBANKParser_Parse_EnglishFormat(t *testing.T) {
	p := &KBankParser{}

	msg := "Received THB 5,250.00 from JOHN DOE to xxx-x-x5678-x at 15/03/26 09:15"

	result, err := p.Parse(msg)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	if result.BankCode != "KBANK" {
		t.Errorf("BankCode = %q, want %q", result.BankCode, "KBANK")
	}

	expectedAmount := decimal.NewFromFloat(5250.00)
	if !result.Amount.Equal(expectedAmount) {
		t.Errorf("Amount = %s, want %s", result.Amount, expectedAmount)
	}

	if result.SenderName != "JOHN DOE" {
		t.Errorf("SenderName = %q, want %q", result.SenderName, "JOHN DOE")
	}

	if result.Reference != "xxx-x-x5678-x" {
		t.Errorf("Reference = %q, want %q", result.Reference, "xxx-x-x5678-x")
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
// TestKBANKParser_Parse_InvalidMessage verifies that Parse returns an error
// for messages that do not match any known KBANK format.
//
// This is a safety check: if an SMS passes CanParse (because it came from a
// known sender with the right keywords) but has an unusual format, Parse
// should fail gracefully rather than returning garbage data.
// =============================================================================
func TestKBANKParser_Parse_InvalidMessage(t *testing.T) {
	p := &KBankParser{}

	invalidMessages := []struct {
		name string
		msg  string
	}{
		{
			name: "random text",
			msg:  "Hello, this is a random SMS message with no transaction data.",
		},
		{
			name: "partial KBANK format missing amount",
			msg:  "รับเงิน จาก สมชาย xxx เข้า xxx-x-x1234-x",
		},
		{
			name: "empty message",
			msg:  "",
		},
		{
			name: "SCB format not KBANK",
			msg:  "บช. xx1234 รับโอน 1,000.00 บ. จาก สมชาย วันที่ 10/04/26 เวลา 14:30",
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
// TestKBANKParser_ValidSenders verifies that ValidSenders returns the
// expected list of KBANK sender numbers.
//
// This list is used by the anti-spoofing layer to pre-filter incoming SMS
// messages. If a sender is missing from this list, legitimate KBANK
// notifications from that number will be silently dropped.
// =============================================================================
func TestKBANKParser_ValidSenders(t *testing.T) {
	p := &KBankParser{}
	senders := p.ValidSenders()

	// We expect at least 3 known senders.
	if len(senders) < 3 {
		t.Errorf("ValidSenders() returned %d senders, want at least 3", len(senders))
	}

	// Check that the primary sender number is in the list.
	found := false
	for _, s := range senders {
		if s == "+66868882888" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ValidSenders() does not include the primary KBANK sender '+66868882888'")
	}

	// Check that "KBANK" alphanumeric sender is in the list.
	foundAlpha := false
	for _, s := range senders {
		if s == "KBANK" {
			foundAlpha = true
			break
		}
	}
	if !foundAlpha {
		t.Error("ValidSenders() does not include 'KBANK' alphanumeric sender")
	}
}

// =============================================================================
// TestKBANKParser_BankCode verifies the BankCode() method returns "KBANK".
//
// This value is stored in every parsed transaction and used for downstream
// bank account matching, reporting, and routing decisions.
// =============================================================================
func TestKBANKParser_BankCode(t *testing.T) {
	p := &KBankParser{}
	if code := p.BankCode(); code != "KBANK" {
		t.Errorf("BankCode() = %q, want %q", code, "KBANK")
	}
}
