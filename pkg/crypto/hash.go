// Package crypto provides cryptographic helper functions used across the
// RichPayment platform, including password hashing (bcrypt), HMAC signing,
// and AES-GCM encryption/decryption.
package crypto

import (
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work factor passed to bcrypt.GenerateFromPassword.
// A cost of 12 provides a good balance between security and performance;
// each increment roughly doubles the computation time.
const bcryptCost = 12

// HashPassword generates a bcrypt hash of the given plaintext password.
// The returned string is safe to store in the database. Returns an error
// if bcrypt fails (e.g. the password exceeds 72 bytes).
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// CheckPassword compares a plaintext password against a bcrypt hash.
// Returns true if the password matches, false otherwise. Timing-safe
// comparison is handled internally by the bcrypt library.
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// SHA256 computes the SHA-256 digest of data and returns it as a lowercase
// hex-encoded string. This is a one-way hash used for fingerprinting, not
// for password storage (use HashPassword for that).
func SHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
