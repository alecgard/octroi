package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

// Cipher handles AES-256-GCM encryption/decryption.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher creates a Cipher from a hex-encoded 32-byte key.
// Returns nil if key is empty (encryption disabled).
func NewCipher(hexKey string) (*Cipher, error) {
	if hexKey == "" {
		return nil, nil
	}

	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decoding hex key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	return &Cipher{aead: aead}, nil
}

// Encrypt encrypts plaintext and returns base64-encoded ciphertext with prepended nonce.
// If Cipher is nil, returns plaintext unchanged (no-op for backward compat).
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	if c == nil {
		return plaintext, nil
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext (with prepended nonce) and returns plaintext.
// If Cipher is nil, returns ciphertext unchanged (assumes unencrypted).
func (c *Cipher) Decrypt(ciphertext string) (string, error) {
	if c == nil {
		return ciphertext, nil
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decoding base64: %w", err)
	}

	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}

	return string(plaintext), nil
}
