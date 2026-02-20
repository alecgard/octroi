package api

import (
	"log/slog"
	"net/http"

	"github.com/alecgard/octroi/internal/auth"
)

// auditLog emits a structured audit log entry for an admin/member action.
func auditLog(r *http.Request, action string, resourceType string, resourceID string, detail ...any) {
	attrs := []any{
		"action", action,
		"resource_type", resourceType,
		"resource_id", resourceID,
		"ip", clientIP(r),
		"request_id", RequestIDFromContext(r.Context()),
	}

	if u := auth.UserFromContext(r.Context()); u != nil {
		attrs = append(attrs, "user_id", u.ID, "user_email", u.Email, "user_role", u.Role)
	}

	attrs = append(attrs, detail...)
	slog.Info("audit", attrs...)
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	return r.RemoteAddr
}
