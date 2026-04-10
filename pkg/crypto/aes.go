package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

// ErrCiphertextTooShort is returned by Decrypt when the ciphertext slice is
// shorter than the GCM nonce size, indicating the data is malformed or
// truncated.
var ErrCiphertextTooShort = errors.New("ciphertext too short")

// Encrypt encrypts plaintext using AES-256-GCM with the provided key.
// The key must be exactly 16, 24, or 32 bytes (for AES-128, AES-192, or
// AES-256 respectively). A random nonce is generated and prepended to the
// returned ciphertext so that Decrypt can extract it later.
func Encrypt(plaintext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Generate a cryptographically secure random nonce.
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	// Seal appends the encrypted+authenticated ciphertext after the nonce.
	// Layout of the returned slice: [nonce | ciphertext | GCM tag].
	return aesGCM.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts ciphertext that was produced by Encrypt. It expects the
// nonce to be prepended to the ciphertext (as Encrypt does). The same key
// used for encryption must be provided.
func Decrypt(ciphertext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Split the nonce from the actual ciphertext.
	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrCiphertextTooShort
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return aesGCM.Open(nil, nonce, ciphertext, nil)
}
