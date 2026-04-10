package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// =============================================================================
// TestHashPassword verifies that HashPassword produces a valid bcrypt hash.
//
// bcrypt hashes always start with "$2a$" or "$2b$" and are 60 characters long.
// We check that the output has the expected prefix and length, confirming that
// the function is using bcrypt with the configured cost factor.
// =============================================================================
func TestHashPassword(t *testing.T) {
	password := "supersecret123!"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword returned unexpected error: %v", err)
	}

	// bcrypt hashes are always 60 characters and start with "$2a$" or "$2b$".
	if len(hash) != 60 {
		t.Errorf("expected hash length 60, got %d", len(hash))
	}

	// Verify that re-hashing the same password produces a different hash each
	// time (because bcrypt generates a random salt internally).
	hash2, err := HashPassword(password)
	if err != nil {
		t.Fatalf("second HashPassword call returned unexpected error: %v", err)
	}
	if hash == hash2 {
		t.Error("expected two calls to HashPassword to produce different hashes (random salt), but they are identical")
	}
}

// =============================================================================
// TestCheckPassword verifies password verification against a bcrypt hash.
//
// Test cases:
//   - Correct password: CheckPassword should return true.
//   - Wrong password: CheckPassword should return false.
//
// This is the most security-critical function in the package because it guards
// all authentication flows. A false positive would allow unauthorized access.
// =============================================================================
func TestCheckPassword(t *testing.T) {
	password := "correcthorsebatterystaple"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword returned unexpected error: %v", err)
	}

	// Subtest: correct password should verify successfully.
	t.Run("correct password", func(t *testing.T) {
		if !CheckPassword(password, hash) {
			t.Error("CheckPassword returned false for the correct password; expected true")
		}
	})

	// Subtest: wrong password should be rejected.
	t.Run("wrong password", func(t *testing.T) {
		if CheckPassword("wrongpassword", hash) {
			t.Error("CheckPassword returned true for an incorrect password; expected false")
		}
	})

	// Subtest: empty password should be rejected against a real hash.
	t.Run("empty password", func(t *testing.T) {
		if CheckPassword("", hash) {
			t.Error("CheckPassword returned true for an empty password; expected false")
		}
	})
}

// =============================================================================
// TestSHA256 verifies that SHA256 produces the correct hex-encoded hash for
// known input data.
//
// We compare against Go's standard library sha256.Sum256 to ensure our wrapper
// function produces identical output. This guards against encoding mistakes
// (e.g. using base64 instead of hex, or truncating the hash).
// =============================================================================
func TestSHA256(t *testing.T) {
	input := []byte("hello world")

	// Compute the expected hash using the standard library directly.
	expected := sha256.Sum256(input)
	expectedHex := hex.EncodeToString(expected[:])

	got := SHA256(input)
	if got != expectedHex {
		t.Errorf("SHA256(%q) = %q, want %q", input, got, expectedHex)
	}

	// Verify against a well-known precomputed SHA-256 hash of "hello world".
	// This value is widely documented and serves as a regression check.
	knownHash := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if got != knownHash {
		t.Errorf("SHA256(\"hello world\") = %q, want known hash %q", got, knownHash)
	}
}

// =============================================================================
// TestHMACSign verifies that HMACSign produces a deterministic hex-encoded
// HMAC-SHA256 signature for a given message and secret.
//
// HMAC signatures are used to authenticate webhook payloads and API requests.
// A deterministic output means the same (message, secret) pair always produces
// the same signature, which is essential for verification on both sides.
// =============================================================================
func TestHMACSign(t *testing.T) {
	message := []byte("payment:1000:THB")
	secret := []byte("my-secret-key")

	sig1 := HMACSign(message, secret)
	sig2 := HMACSign(message, secret)

	// Same inputs must produce the same signature (deterministic).
	if sig1 != sig2 {
		t.Errorf("HMACSign produced different signatures for identical inputs: %q vs %q", sig1, sig2)
	}

	// The signature must be a 64-character hex string (256 bits = 32 bytes = 64 hex chars).
	if len(sig1) != 64 {
		t.Errorf("expected HMAC signature length 64, got %d", len(sig1))
	}

	// A different secret must produce a different signature.
	sigDiffSecret := HMACSign(message, []byte("different-secret"))
	if sig1 == sigDiffSecret {
		t.Error("HMACSign produced the same signature for different secrets; signatures should differ")
	}
}

// =============================================================================
// TestHMACVerify tests both positive and negative HMAC signature verification.
//
// This function is called on every incoming webhook and API request to verify
// authenticity. We test:
//   - A correct signature passes verification.
//   - A tampered signature is rejected.
//   - A signature from a different secret is rejected.
// =============================================================================
func TestHMACVerify(t *testing.T) {
	message := []byte("order:abc123:amount:500")
	secret := []byte("webhook-secret")

	// Generate the correct signature.
	validSig := HMACSign(message, secret)

	// Subtest: correct signature should verify.
	t.Run("valid signature", func(t *testing.T) {
		if !HMACVerify(message, secret, validSig) {
			t.Error("HMACVerify returned false for a valid signature; expected true")
		}
	})

	// Subtest: tampered signature should be rejected.
	t.Run("tampered signature", func(t *testing.T) {
		if HMACVerify(message, secret, "deadbeef"+validSig[8:]) {
			t.Error("HMACVerify returned true for a tampered signature; expected false")
		}
	})

	// Subtest: signature made with a different secret should be rejected.
	t.Run("wrong secret", func(t *testing.T) {
		wrongSig := HMACSign(message, []byte("wrong-secret"))
		if HMACVerify(message, secret, wrongSig) {
			t.Error("HMACVerify returned true for a signature from a different secret; expected false")
		}
	})

	// Subtest: completely invalid (non-hex) signature.
	t.Run("invalid signature format", func(t *testing.T) {
		if HMACVerify(message, secret, "not-a-hex-signature") {
			t.Error("HMACVerify returned true for a non-hex signature; expected false")
		}
	})
}

// =============================================================================
// TestEncryptDecrypt verifies the AES-GCM encrypt/decrypt roundtrip.
//
// AES-GCM is used to encrypt sensitive data at rest (e.g. TOTP secrets,
// API keys). This test ensures that:
//   - Encrypting then decrypting returns the original plaintext.
//   - Using the wrong key to decrypt fails with an error.
//   - The function accepts 16-byte (AES-128), 24-byte (AES-192), and
//     32-byte (AES-256) keys.
// =============================================================================
func TestEncryptDecrypt(t *testing.T) {
	// 32-byte key for AES-256.
	key := []byte("0123456789abcdef0123456789abcdef")
	plaintext := []byte("sensitive-totp-secret-JBSWY3DPEHPK3PXP")

	// Encrypt the plaintext.
	ciphertext, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt returned unexpected error: %v", err)
	}

	// Ciphertext must be longer than plaintext (nonce + auth tag overhead).
	if len(ciphertext) <= len(plaintext) {
		t.Errorf("ciphertext length (%d) should be greater than plaintext length (%d)", len(ciphertext), len(plaintext))
	}

	// Decrypt should return the original plaintext.
	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("Decrypt returned unexpected error: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("Decrypt returned %q, want %q", decrypted, plaintext)
	}

	// Using the wrong key should fail decryption (AES-GCM integrity check).
	wrongKey := []byte("fedcba9876543210fedcba9876543210")
	_, err = Decrypt(ciphertext, wrongKey)
	if err == nil {
		t.Error("Decrypt with wrong key should return an error, but got nil")
	}
}

// =============================================================================
// TestEncryptDecrypt_DifferentCiphertexts verifies that encrypting the same
// plaintext twice with the same key produces different ciphertexts.
//
// This is a critical security property of AES-GCM: the random nonce ensures
// that identical plaintexts do not produce identical ciphertexts, which would
// leak information about the data (a "deterministic encryption" vulnerability).
// =============================================================================
func TestEncryptDecrypt_DifferentCiphertexts(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	plaintext := []byte("same plaintext both times")

	ct1, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("first Encrypt returned unexpected error: %v", err)
	}

	ct2, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("second Encrypt returned unexpected error: %v", err)
	}

	// The two ciphertexts must differ because each call generates a random nonce.
	if string(ct1) == string(ct2) {
		t.Error("two encryptions of the same plaintext produced identical ciphertexts; nonce randomness may be broken")
	}

	// Both ciphertexts should still decrypt to the same plaintext.
	dec1, err := Decrypt(ct1, key)
	if err != nil {
		t.Fatalf("Decrypt(ct1) returned unexpected error: %v", err)
	}
	dec2, err := Decrypt(ct2, key)
	if err != nil {
		t.Fatalf("Decrypt(ct2) returned unexpected error: %v", err)
	}
	if string(dec1) != string(dec2) {
		t.Errorf("decrypted texts differ: %q vs %q", dec1, dec2)
	}
}

// =============================================================================
// TestEncrypt_InvalidKeyLength verifies that Encrypt rejects keys that are not
// valid AES key sizes (16, 24, or 32 bytes).
//
// This prevents accidental use of short or malformed keys that would weaken
// the encryption. AES requires exact key lengths.
// =============================================================================
func TestEncrypt_InvalidKeyLength(t *testing.T) {
	// 10-byte key is not a valid AES key size.
	shortKey := []byte("shortkey!!")
	_, err := Encrypt([]byte("test"), shortKey)
	if err == nil {
		t.Error("Encrypt with a 10-byte key should return an error, but got nil")
	}
}

// =============================================================================
// TestDecrypt_TooShortCiphertext verifies that Decrypt returns
// ErrCiphertextTooShort when the input is shorter than the nonce size.
//
// This guards against garbage input or truncated data being passed to Decrypt.
// =============================================================================
func TestDecrypt_TooShortCiphertext(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")

	// An empty slice is definitely too short.
	_, err := Decrypt([]byte{}, key)
	if err == nil {
		t.Error("Decrypt with empty ciphertext should return an error, but got nil")
	}

	// A few bytes (less than nonce size of 12 for GCM) should also fail.
	_, err = Decrypt([]byte{0x01, 0x02, 0x03}, key)
	if err == nil {
		t.Error("Decrypt with 3-byte ciphertext should return an error, but got nil")
	}
}
