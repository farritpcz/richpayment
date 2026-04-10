// Package service contains the core business logic for the order-service.
// This file implements PromptPay QR code generation following the EMVCo
// QR Code specification used by Thai PromptPay. The generated payload is
// a TLV (Tag-Length-Value) string that can be rendered as a QR code image
// for the customer to scan in their mobile banking application.
package service

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	qrcode "github.com/skip2/go-qrcode"
)

// promptPayAID is the PromptPay Application Identifier used in the EMVCo
// merchant-presented QR payload. This is the registered AID for PromptPay
// under the Bank of Thailand's national payment scheme.
const promptPayAID = "A000000677010111"

// GeneratePromptPayQR builds a PromptPay-compliant EMVCo QR payload string
// and renders it as a PNG QR code image encoded in base64.
//
// Parameters:
//   - accountNumber: The PromptPay-registered bank account number or phone
//     number (e.g. "0812345678" for mobile or "1234567890123" for national ID).
//   - amount: The transfer amount in THB. Must be positive.
//
// Returns:
//   - qrPayload: The raw EMVCo TLV string that encodes the payment request.
//   - qrBase64Image: A base64-encoded PNG image of the QR code (256x256 px).
//   - err: Non-nil if QR code rendering fails.
//
// The generated QR code follows the EMVCo Merchant-Presented Mode specification
// with the following top-level TLV structure:
//
//	Tag 00 - Payload Format Indicator ("01")
//	Tag 01 - Point of Initiation Method ("12" = dynamic, one-time use)
//	Tag 29 - Merchant Account Information (PromptPay)
//	  Sub-tag 00 - AID
//	  Sub-tag 01 - Account number (phone / national ID)
//	Tag 53 - Transaction Currency ("764" = THB)
//	Tag 54 - Transaction Amount
//	Tag 58 - Country Code ("TH")
//	Tag 63 - CRC-16 checksum
func GeneratePromptPayQR(accountNumber string, amount decimal.Decimal) (string, string, error) {
	// Build the PromptPay merchant account information (tag 29).
	// Sub-tag 00: application identifier for PromptPay.
	// Sub-tag 01: the payee's account number (phone or citizen ID).
	merchantAccountInfo := tlv("00", promptPayAID) + tlv("01", formatAccount(accountNumber))

	// Assemble the top-level EMVCo payload without the CRC (tag 63).
	// The CRC is computed over the entire payload including tag 63's header.
	payload := ""
	payload += tlv("00", "01")                      // Payload Format Indicator: version 01
	payload += tlv("01", "12")                      // Point of Initiation: 12 = dynamic QR
	payload += tlv("29", merchantAccountInfo)        // PromptPay merchant info
	payload += tlv("53", "764")                     // Currency: 764 = Thai Baht
	payload += tlv("54", amount.StringFixed(2))     // Amount with 2 decimal places
	payload += tlv("58", "TH")                      // Country code: Thailand

	// Append CRC placeholder ("6304") and compute CRC-16/CCITT-FALSE over the
	// entire payload string including the "6304" prefix.
	payload += "6304"
	crc := crc16CCITTFalse([]byte(payload))
	qrPayload := fmt.Sprintf("%s%04X", payload[:len(payload)-4], crc)
	// Re-append the computed CRC to the payload (replacing placeholder).
	qrPayload = payload[:len(payload)-4] + fmt.Sprintf("6304%04X", crc)

	// Render the payload string as a 256x256 PNG QR code image.
	png, err := qrcode.Encode(qrPayload, qrcode.Medium, 256)
	if err != nil {
		return "", "", fmt.Errorf("encode qr image: %w", err)
	}

	// Base64-encode the PNG bytes for easy transport in JSON responses.
	qrBase64Image := base64.StdEncoding.EncodeToString(png)

	return qrPayload, qrBase64Image, nil
}

// tlv builds a single EMVCo TLV (Tag-Length-Value) element.
// The tag is a two-character string (e.g. "00", "29").
// The length is the number of characters in the value, zero-padded to two digits.
// Example: tlv("00", "01") -> "000201"
func tlv(tag, value string) string {
	return fmt.Sprintf("%s%02d%s", tag, len(value), value)
}

// formatAccount normalises a PromptPay account number for embedding in the
// QR payload. Phone numbers are zero-padded to 13 digits with the "0066"
// country prefix (Thailand +66). National ID numbers (13 digits) are used
// as-is. This follows the PromptPay specification for account identification.
func formatAccount(account string) string {
	// Strip any dashes or spaces that the caller may have included.
	account = strings.ReplaceAll(account, "-", "")
	account = strings.ReplaceAll(account, " ", "")

	// If the account looks like a Thai mobile number (9-10 digits starting
	// with 0), convert it to the international format with "0066" prefix
	// and zero-pad to 13 characters total.
	if len(account) <= 10 && strings.HasPrefix(account, "0") {
		// Remove leading zero and prepend "0066" (Thailand country code).
		account = "0066" + account[1:]
	}

	// Zero-pad to 13 characters if shorter (required by PromptPay spec).
	for len(account) < 13 {
		account = "0" + account
	}

	return account
}

// crc16CCITTFalse computes the CRC-16/CCITT-FALSE checksum used by EMVCo QR
// codes. This is a standard CRC-16 variant with polynomial 0x1021 and an
// initial value of 0xFFFF (no final XOR). The algorithm processes each byte
// of the input data bit-by-bit.
//
// Reference: ISO/IEC 13239, used by EMVCo QR specification for payload
// integrity verification.
func crc16CCITTFalse(data []byte) uint16 {
	// Initial CRC value per CCITT-FALSE specification.
	crc := uint16(0xFFFF)

	for _, b := range data {
		// XOR the current byte into the high byte of the CRC register.
		crc ^= uint16(b) << 8

		// Process each of the 8 bits in the byte.
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				// If the MSB is set, shift left and XOR with the polynomial.
				crc = (crc << 1) ^ 0x1021
			} else {
				// Otherwise just shift left.
				crc <<= 1
			}
		}
	}

	return crc
}
