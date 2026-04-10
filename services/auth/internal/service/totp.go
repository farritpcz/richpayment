// Package service contains the core business-logic layer for the auth service.
// This file implements TOTP (Time-based One-Time Password) two-factor
// authentication using the RFC 6238 standard via the pquerna/otp library.
package service

import (
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	// totpIssuer is the issuer string embedded in TOTP provisioning URIs.
	// It appears in authenticator apps as the service name alongside the account.
	totpIssuer = "RichPayment"

	// totpDigits specifies the number of digits in each TOTP code.
	// Six digits is the standard used by Google Authenticator and similar apps.
	totpDigits = otp.DigitsSix

	// totpPeriod is the validity window, in seconds, for each TOTP code.
	// A 30-second window is the RFC 6238 default.
	totpPeriod = 30
)

// TOTPService handles TOTP two-factor authentication operations such as
// generating new secrets and validating user-supplied codes. It is stateless
// and safe for concurrent use.
type TOTPService struct{}

// NewTOTPService creates and returns a new TOTPService instance.
func NewTOTPService() *TOTPService {
	return &TOTPService{}
}

// GenerateSecret creates a new TOTP secret for the given account (typically the
// user's email address). It returns:
//   - secret: the base32-encoded shared secret for storage in the database.
//   - qrURL: an otpauth:// provisioning URI suitable for encoding into a QR code
//     that the user scans with their authenticator app.
//   - err: any error encountered during key generation.
func (s *TOTPService) GenerateSecret(accountName string) (secret string, qrURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: accountName,
		Digits:      totpDigits,
		Period:      totpPeriod,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate TOTP key: %w", err)
	}

	return key.Secret(), key.URL(), nil
}

// ValidateCode checks whether the provided TOTP code is valid for the given
// base32-encoded secret at the current time. It uses ValidateCustom with an
// explicit time.Now() reference so that the validation window is anchored to
// the server clock. Returns true when the code matches.
func (s *TOTPService) ValidateCode(secret, code string) bool {
	valid, _ := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    totpPeriod,
		Digits:    totpDigits,
		Algorithm: otp.AlgorithmSHA1,
	})
	return valid
}
