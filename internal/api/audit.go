package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alecgard/octroi/internal/audit"
	"github.com/alecgard/octroi/internal/auth"
)

var (
	auditStore   *audit.Store
	auditStoreMu sync.RWMutex
)

// SetAuditStore sets the package-level audit store for DB dual-write.
func SetAuditStore(s *audit.Store) {
	auditStoreMu.Lock()
	defer auditStoreMu.Unlock()
	auditStore = s
}

// auditLog emits a structured audit log entry for an admin/member action.
// It always logs to slog and optionally writes to the audit_log DB table.
func auditLog(r *http.Request, action string, resourceType string, resourceID string, detail ...any) {
	attrs := []any{
		"action", action,
		"resource_type", resourceType,
		"resource_id", resourceID,
		"ip", clientIP(r),
		"request_id", RequestIDFromContext(r.Context()),
	}

	var userID *string
	if u := auth.UserFromContext(r.Context()); u != nil {
		attrs = append(attrs, "user_id", u.ID, "user_email", u.Email, "user_role", u.Role)
		userID = &u.ID
	}

	attrs = append(attrs, detail...)
	slog.Info("audit", attrs...)

	// Async DB write.
	auditStoreMu.RLock()
	s := auditStore
	auditStoreMu.RUnlock()

	if s != nil {
		details := make(map[string]any)
		for i := 0; i+1 < len(detail); i += 2 {
			key, ok := detail[i].(string)
			if !ok {
				continue
			}
			details[key] = detail[i+1]
		}

		entry := audit.Entry{
			UserID:       userID,
			Action:       action,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			Details:      details,
			IP:           clientIP(r),
			RequestID:    RequestIDFromContext(r.Context()),
		}

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.Insert(ctx, entry); err != nil {
				slog.Warn("audit DB write failed", "error", err)
			}
		}()
	}
}

// clientIP extracts the client IP address from the request.
// It checks X-Forwarded-For first (taking the leftmost/first IP),
// then X-Real-IP, and falls back to RemoteAddr.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return parseForwardedFor(fwd)
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return strings.TrimSpace(realIP)
	}
	return r.RemoteAddr
}

// parseForwardedFor extracts the first (leftmost/client) IP from an
// X-Forwarded-For header value, which may contain a comma-separated list.
func parseForwardedFor(fwd string) string {
	if i := strings.IndexByte(fwd, ','); i > 0 {
		return strings.TrimSpace(fwd[:i])
	}
	return strings.TrimSpace(fwd)
}
