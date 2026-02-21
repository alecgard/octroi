package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey int

const (
	agentContextKey contextKey = iota
	userContextKey
)

// ContextWithAgent returns a new context carrying the given agent.
func ContextWithAgent(ctx context.Context, agent *Agent) context.Context {
	return context.WithValue(ctx, agentContextKey, agent)
}

// AgentFromContext extracts the agent from the context, or nil if not present.
func AgentFromContext(ctx context.Context) *Agent {
	agent, _ := ctx.Value(agentContextKey).(*Agent)
	return agent
}

// ContextWithUser returns a new context carrying the given user.
func ContextWithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext extracts the user from the context, or nil if not present.
func UserFromContext(ctx context.Context) *User {
	user, _ := ctx.Value(userContextKey).(*User)
	return user
}

// AgentAuthMiddleware returns middleware that authenticates requests using an
// API key in the Authorization header. The key is hashed and looked up via the
// service's agent store. On success the agent is injected into the request
// context.
func AgentAuthMiddleware(svc *Service, callbacks ...func()) func(http.Handler) http.Handler {
	var onFailure, onSuccess func()
	if len(callbacks) > 0 {
		onFailure = callbacks[0]
	}
	if len(callbacks) > 1 {
		onSuccess = callbacks[1]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				if onFailure != nil {
					onFailure()
				}
				writeUnauthorized(w, "missing or malformed authorization header")
				return
			}

			hash := HashKey(token)
			agent, err := svc.store.GetByKeyHash(r.Context(), hash)
			if err != nil || agent == nil {
				if onFailure != nil {
					onFailure()
				}
				writeUnauthorized(w, "invalid api key")
				return
			}

			if onSuccess != nil {
				onSuccess()
			}
			ctx := ContextWithAgent(r.Context(), agent)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminSessionMiddleware validates the session token and requires org_admin role.
func AdminSessionMiddleware(sessions SessionLookup, callbacks ...func()) func(http.Handler) http.Handler {
	var onFailure, onSuccess func()
	if len(callbacks) > 0 {
		onFailure = callbacks[0]
	}
	if len(callbacks) > 1 {
		onSuccess = callbacks[1]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				if onFailure != nil {
					onFailure()
				}
				writeUnauthorized(w, "missing or malformed authorization header")
				return
			}

			user, err := sessions.LookupSession(r.Context(), token)
			if err != nil || user == nil {
				if onFailure != nil {
					onFailure()
				}
				writeUnauthorized(w, "invalid or expired session")
				return
			}
			if user.Role != "org_admin" {
				if onFailure != nil {
					onFailure()
				}
				writeForbidden(w, "admin access required")
				return
			}

			if onSuccess != nil {
				onSuccess()
			}
			ctx := ContextWithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// MemberAuthMiddleware validates the session token and injects the user into
// context. Any role (admin or member) is accepted.
func MemberAuthMiddleware(sessions SessionLookup, callbacks ...func()) func(http.Handler) http.Handler {
	var onFailure, onSuccess func()
	if len(callbacks) > 0 {
		onFailure = callbacks[0]
	}
	if len(callbacks) > 1 {
		onSuccess = callbacks[1]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				if onFailure != nil {
					onFailure()
				}
				writeUnauthorized(w, "missing or malformed authorization header")
				return
			}

			user, err := sessions.LookupSession(r.Context(), token)
			if err != nil || user == nil {
				if onFailure != nil {
					onFailure()
				}
				writeUnauthorized(w, "invalid or expired session")
				return
			}

			if onSuccess != nil {
				onSuccess()
			}
			ctx := ContextWithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: errorBody{
			Code:    "unauthorized",
			Message: message,
		},
	})
}

func writeForbidden(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: errorBody{
			Code:    "forbidden",
			Message: message,
		},
	})
}
