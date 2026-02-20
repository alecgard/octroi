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
	Prefix string // first 14 characters of the plaintext key
}

// TeamMembership represents a user's membership in a team with a role.
type TeamMembership struct {
	Team string `json:"team"`
	Role string `json:"role"` // "admin" or "member"
}

// User represents an authenticated UI user.
type User struct {
	ID    string
	Email string
	Name  string
	Teams []TeamMembership
	Role  string // "org_admin" or "member"
}

// TeamNames returns the list of team names the user belongs to.
func (u *User) TeamNames() []string {
	names := make([]string, len(u.Teams))
	for i, tm := range u.Teams {
		names[i] = tm.Team
	}
	return names
}

// IsTeamAdmin returns true if the user is an admin of the given team.
func (u *User) IsTeamAdmin(team string) bool {
	for _, tm := range u.Teams {
		if tm.Team == team && tm.Role == "admin" {
			return true
		}
	}
	return false
}

// IsOrgAdmin returns true if the user has the org_admin role.
func (u *User) IsOrgAdmin() bool {
	return u.Role == "org_admin"
}

// InTeam returns true if the user is a member of the given team.
func (u *User) InTeam(team string) bool {
	for _, tm := range u.Teams {
		if tm.Team == team {
			return true
		}
	}
	return false
}

// CanManageTeam returns true if the user can manage members of the given team.
func (u *User) CanManageTeam(team string) bool {
	return u.IsOrgAdmin() || u.IsTeamAdmin(team)
}

// AgentLookup is the interface for retrieving agents by their key hash.
type AgentLookup interface {
	GetByKeyHash(ctx context.Context, hash string) (*Agent, error)
}

// SessionLookup is the interface for resolving session tokens to users.
type SessionLookup interface {
	LookupSession(ctx context.Context, token string) (*User, error)
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
		Prefix: plaintext[:14],
	}

	return key, plaintext, nil
}

// HashKey returns the hex-encoded SHA-256 hash of the given plaintext key.
func HashKey(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}
