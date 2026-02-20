package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const sessionDuration = 7 * 24 * time.Hour

// Store provides database operations for users and sessions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new user store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// scanUser scans a user row, handling JSONB teams column.
func scanUser(scan func(dest ...any) error) (*User, error) {
	u := &User{}
	var teamsJSON []byte
	err := scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &teamsJSON, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	if len(teamsJSON) > 0 {
		if err := json.Unmarshal(teamsJSON, &u.Teams); err != nil {
			return nil, fmt.Errorf("unmarshaling teams: %w", err)
		}
	}
	if u.Teams == nil {
		u.Teams = []TeamMembership{}
	}
	return u, nil
}

// marshalTeams converts teams to JSON for storage.
func marshalTeams(teams []TeamMembership) ([]byte, error) {
	if teams == nil {
		teams = []TeamMembership{}
	}
	return json.Marshal(teams)
}

// Create inserts a new user with a bcrypt-hashed password.
func (s *Store) Create(ctx context.Context, in CreateUserInput) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	role := in.Role
	if role == "" {
		role = "member"
	}

	teamsJSON, err := marshalTeams(in.Teams)
	if err != nil {
		return nil, fmt.Errorf("marshaling teams: %w", err)
	}

	u, err := scanUser(func(dest ...any) error {
		return s.pool.QueryRow(ctx,
			`INSERT INTO users (email, password_hash, name, teams, role)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING id, email, password_hash, name, teams, role, created_at`,
			in.Email, string(hash), in.Name, teamsJSON, role,
		).Scan(dest...)
	})
	if err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

// GetByID retrieves a user by primary key.
func (s *Store) GetByID(ctx context.Context, id string) (*User, error) {
	u, err := scanUser(func(dest ...any) error {
		return s.pool.QueryRow(ctx,
			`SELECT id, email, password_hash, name, teams, role, created_at
			 FROM users WHERE id = $1`, id,
		).Scan(dest...)
	})
	if err != nil {
		return nil, fmt.Errorf("getting user by id: %w", err)
	}
	return u, nil
}

// GetByEmail retrieves a user by email address.
func (s *Store) GetByEmail(ctx context.Context, email string) (*User, error) {
	u, err := scanUser(func(dest ...any) error {
		return s.pool.QueryRow(ctx,
			`SELECT id, email, password_hash, name, teams, role, created_at
			 FROM users WHERE email = $1`, email,
		).Scan(dest...)
	})
	if err != nil {
		return nil, fmt.Errorf("getting user by email: %w", err)
	}
	return u, nil
}

// List returns all users ordered by created_at DESC.
func (s *Store) List(ctx context.Context) ([]*User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, email, password_hash, name, teams, role, created_at
		 FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u, err := scanUser(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning user row: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// Update performs a partial update on the user with the given id.
func (s *Store) Update(ctx context.Context, id string, in UpdateUserInput) (*User, error) {
	var setClauses []string
	var args []any
	argIdx := 1

	if in.Email != nil {
		setClauses = append(setClauses, fmt.Sprintf("email = $%d", argIdx))
		args = append(args, *in.Email)
		argIdx++
	}
	if in.Password != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("hashing password: %w", err)
		}
		setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", argIdx))
		args = append(args, string(hash))
		argIdx++
	}
	if in.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *in.Name)
		argIdx++
	}
	if in.Teams != nil {
		teamsJSON, err := marshalTeams(*in.Teams)
		if err != nil {
			return nil, fmt.Errorf("marshaling teams: %w", err)
		}
		setClauses = append(setClauses, fmt.Sprintf("teams = $%d", argIdx))
		args = append(args, teamsJSON)
		argIdx++
	}
	if in.Role != nil {
		setClauses = append(setClauses, fmt.Sprintf("role = $%d", argIdx))
		args = append(args, *in.Role)
		argIdx++
	}

	if len(setClauses) == 0 {
		return s.GetByID(ctx, id)
	}

	args = append(args, id)
	query := fmt.Sprintf(
		`UPDATE users SET %s WHERE id = $%d
		 RETURNING id, email, password_hash, name, teams, role, created_at`,
		strings.Join(setClauses, ", "), argIdx,
	)

	u, err := scanUser(func(dest ...any) error {
		return s.pool.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if err != nil {
		return nil, fmt.Errorf("updating user: %w", err)
	}
	return u, nil
}

// Delete removes a user by id.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	return nil
}

// CheckPassword verifies a plaintext password against the user's stored hash.
func CheckPassword(u *User, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

// CreateSession creates a new session for the given user. It returns the
// opaque plaintext token (to be sent to the client) and the stored session.
func (s *Store) CreateSession(ctx context.Context, userID string) (string, *Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generating session token: %w", err)
	}
	plaintext := hex.EncodeToString(b)
	tokenHash := hashToken(plaintext)

	now := time.Now()
	expiresAt := now.Add(sessionDuration)

	sess := &Session{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING token_hash, user_id, created_at, expires_at`,
		tokenHash, userID, now, expiresAt,
	).Scan(&sess.TokenHash, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt)
	if err != nil {
		return "", nil, fmt.Errorf("creating session: %w", err)
	}

	return plaintext, sess, nil
}

// GetSessionUser looks up a session by its plaintext token and returns the
// associated user. Returns nil if the session is expired or not found.
func (s *Store) GetSessionUser(ctx context.Context, plaintext string) (*User, error) {
	tokenHash := hashToken(plaintext)

	u, err := scanUser(func(dest ...any) error {
		return s.pool.QueryRow(ctx,
			`SELECT u.id, u.email, u.password_hash, u.name, u.teams, u.role, u.created_at
			 FROM sessions s JOIN users u ON s.user_id = u.id
			 WHERE s.token_hash = $1 AND s.expires_at > now()`,
			tokenHash,
		).Scan(dest...)
	})
	if err != nil {
		return nil, fmt.Errorf("getting session user: %w", err)
	}
	return u, nil
}

// DeleteSession removes a session by its plaintext token.
func (s *Store) DeleteSession(ctx context.Context, plaintext string) error {
	tokenHash := hashToken(plaintext)
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// CleanExpiredSessions deletes all sessions that have expired.
func (s *Store) CleanExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("cleaning expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

func hashToken(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}
