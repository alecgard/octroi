package user

import "time"

// TeamMembership represents a user's membership in a team with a role.
type TeamMembership struct {
	Team string `json:"team"`
	Role string `json:"role"` // "admin" or "member"
}

// User represents a registered user account.
type User struct {
	ID           string           `json:"id"`
	Email        string           `json:"email"`
	PasswordHash string           `json:"-"`
	Name         string           `json:"name"`
	Teams        []TeamMembership `json:"teams"`
	Role         string           `json:"role"` // "org_admin" or "member"
	CreatedAt    time.Time        `json:"created_at"`
}

// CreateUserInput holds the fields required to create a new user.
type CreateUserInput struct {
	Email    string           `json:"email"`
	Password string           `json:"password"`
	Name     string           `json:"name"`
	Teams    []TeamMembership `json:"teams"`
	Role     string           `json:"role"`
}

// UpdateUserInput holds optional fields for a partial user update.
type UpdateUserInput struct {
	Email    *string           `json:"email,omitempty"`
	Password *string           `json:"password,omitempty"`
	Name     *string           `json:"name,omitempty"`
	Teams    *[]TeamMembership `json:"teams,omitempty"`
	Role     *string           `json:"role,omitempty"`
}

// Session represents an active user session.
type Session struct {
	TokenHash string    `json:"-"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}
