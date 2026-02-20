package crypto

import (
	"encoding/hex"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	// Fixed 32-byte key for deterministic tests.
	return hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
}

func TestRoundtrip(t *testing.T) {
	c, err := NewCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	original := `{"api_key":"secret-123","token":"xyz"}`
	encrypted, err := c.Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if encrypted == original {
		t.Fatal("encrypted text should differ from plaintext")
	}

	decrypted, err := c.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if decrypted != original {
		t.Errorf("roundtrip failed: got %q, want %q", decrypted, original)
	}
}

func TestDifferentCiphertexts(t *testing.T) {
	c, err := NewCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	plaintext := "same input"
	enc1, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}
	enc2, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}

	if enc1 == enc2 {
		t.Error("two encryptions of the same plaintext should produce different ciphertexts (random nonce)")
	}

	// Both should decrypt to the same value.
	dec1, _ := c.Decrypt(enc1)
	dec2, _ := c.Decrypt(enc2)
	if dec1 != dec2 {
		t.Error("both ciphertexts should decrypt to the same plaintext")
	}
}

func TestNilCipherPassthrough(t *testing.T) {
	var c *Cipher

	text := `{"key":"value"}`
	encrypted, err := c.Encrypt(text)
	if err != nil {
		t.Fatalf("nil Encrypt: %v", err)
	}
	if encrypted != text {
		t.Errorf("nil Encrypt should return plaintext unchanged, got %q", encrypted)
	}

	decrypted, err := c.Decrypt(text)
	if err != nil {
		t.Fatalf("nil Decrypt: %v", err)
	}
	if decrypted != text {
		t.Errorf("nil Decrypt should return ciphertext unchanged, got %q", decrypted)
	}
}

func TestEmptyKeyReturnsNil(t *testing.T) {
	c, err := NewCipher("")
	if err != nil {
		t.Fatalf("NewCipher with empty key: %v", err)
	}
	if c != nil {
		t.Error("NewCipher with empty key should return nil")
	}
}

func TestInvalidKeyLength(t *testing.T) {
	// 16-byte key (too short for AES-256).
	short := hex.EncodeToString([]byte("0123456789abcdef"))
	_, err := NewCipher(short)
	if err == nil {
		t.Error("expected error for 16-byte key")
	}
	if !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("error should mention 32 bytes, got: %v", err)
	}

	// Invalid hex.
	_, err = NewCipher("not-hex")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestDecryptInvalidData(t *testing.T) {
	c, err := NewCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	// Not base64.
	_, err = c.Decrypt("!!!not-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}

	// Valid base64 but too short.
	_, err = c.Decrypt("YQ==")
	if err == nil {
		t.Error("expected error for too-short ciphertext")
	}

	// Valid base64, correct length, but tampered.
	encrypted, _ := c.Encrypt("hello")
	tampered := []byte(encrypted)
	// Flip a character in the middle of the base64 string.
	if tampered[len(tampered)/2] == 'A' {
		tampered[len(tampered)/2] = 'B'
	} else {
		tampered[len(tampered)/2] = 'A'
	}
	_, err = c.Decrypt(string(tampered))
	if err == nil {
		t.Error("expected error for tampered ciphertext")
	}
}
