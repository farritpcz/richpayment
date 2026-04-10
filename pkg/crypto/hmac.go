package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HMACSign computes an HMAC-SHA256 of the message using the given secret and
// returns the result as a lowercase hex-encoded string. This is used to sign
// webhook payloads and API requests so the receiver can verify authenticity.
func HMACSign(message []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

// HMACVerify checks whether the provided hex-encoded signature matches the
// HMAC-SHA256 of the message computed with the given secret. The comparison
// is constant-time (via hmac.Equal) to prevent timing side-channel attacks.
func HMACVerify(message []byte, secret []byte, signature string) bool {
	expected := HMACSign(message, secret)
	return hmac.Equal([]byte(expected), []byte(signature))
}
