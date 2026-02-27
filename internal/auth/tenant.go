package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Tenant represents a resolved tenant from the request subdomain.
type Tenant struct {
	ID   string
	Name string
	Slug string
	Plan string
}

const tenantContextKey contextKey = iota + 10

// ContextWithTenant returns a new context carrying the given tenant.
func ContextWithTenant(ctx context.Context, t *Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey, t)
}

// TenantFromContext extracts the tenant from the context, or nil if not present.
func TenantFromContext(ctx context.Context) *Tenant {
	t, _ := ctx.Value(tenantContextKey).(*Tenant)
	return t
}

// TenantLookup resolves a tenant by its slug.
type TenantLookup interface {
	GetBySlug(ctx context.Context, slug string) (*Tenant, error)
}

// TenantMiddleware resolves the tenant from the request Host subdomain and
// injects it into the request context. Returns 400 if no subdomain is present,
// 404 if the tenant is not found.
// tenantSkipPaths are operational endpoints that don't require tenant context.
var tenantSkipPaths = map[string]bool{
	"/health":  true,
	"/metrics": true,
}

func TenantMiddleware(lookup TenantLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tenantSkipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			slug := extractSubdomain(r.Host)
			// Fallback: X-Tenant-Slug header for programmatic clients
			// that cannot set a custom Host header (e.g. Node.js fetch).
			if slug == "" {
				slug = r.Header.Get("X-Tenant-Slug")
			}
			if slug == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(errorResponse{
					Error: errorBody{Code: "tenant_required", Message: "tenant required"},
				})
				return
			}

			t, err := lookup.GetBySlug(r.Context(), slug)
			if err != nil || t == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(errorResponse{
					Error: errorBody{Code: "tenant_not_found", Message: "tenant not found"},
				})
				return
			}

			ctx := ContextWithTenant(r.Context(), t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractSubdomain extracts the subdomain from a host string.
// Examples:
//
//	"acme.localhost:8080" -> "acme"
//	"acme.octroi.dev" -> "acme"
//	"localhost:8080" -> ""
//	"127.0.0.1:8080" -> ""
func extractSubdomain(host string) string {
	// Strip port.
	h := host
	if idx := strings.LastIndex(h, ":"); idx != -1 {
		h = h[:idx]
	}

	// Raw IP addresses have no subdomain.
	if isIPAddress(h) {
		return ""
	}

	// Split by dots; if more than one segment, the first is the subdomain.
	parts := strings.SplitN(h, ".", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// isIPAddress returns true if the host looks like an IP address (v4 or v6).
func isIPAddress(host string) bool {
	// IPv6 in brackets is already stripped by Go's http library, but check anyway.
	if strings.Contains(host, ":") {
		return true
	}
	// Simple heuristic: all digits and dots means IPv4.
	for _, c := range host {
		if c != '.' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
