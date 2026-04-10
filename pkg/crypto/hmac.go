package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func HMACSign(message []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

func HMACVerify(message []byte, secret []byte, signature string) bool {
	expected := HMACSign(message, secret)
	return hmac.Equal([]byte(expected), []byte(signature))
}
