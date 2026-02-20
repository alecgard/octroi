package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Agent represents an authenticated API agent.
type Agent struct {
	ID        string
	Name      string
	Team      string
	RateLimit int
}

// APIKey holds the hashed key and a short prefix for identification.
type APIKey struct {
	Hash   string
	Prefix string // first 8 characters of the plaintext key
}

// AgentLookup is the interface for retrieving agents by their key hash.
type AgentLookup interface {
	GetByKeyHash(ctx context.Context, hash string) (*Agent, error)
}

// Service provides authentication operations backed by an agent store.
type Service struct {
	store AgentLookup
}

// NewService creates a new authentication service.
func NewService(store AgentLookup) *Service {
	return &Service{store: store}
}

// GenerateAPIKey creates a new API key with the "octroi_" prefix followed by
// 32 URL-safe random characters. It returns the APIKey struct (containing the
// hash and prefix) and the full plaintext key.
func GenerateAPIKey() (APIKey, string, error) {
	b := make([]byte, 24) // 24 bytes -> 32 base64url chars
	if _, err := rand.Read(b); err != nil {
		return APIKey{}, "", fmt.Errorf("generating random bytes: %w", err)
	}

	random := base64.RawURLEncoding.EncodeToString(b)
	plaintext := "octroi_" + random

	key := APIKey{
		Hash:   HashKey(plaintext),
		Prefix: plaintext[:8],
	}

	return key, plaintext, nil
}

// HashKey returns the hex-encoded SHA-256 hash of the given plaintext key.
func HashKey(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}
