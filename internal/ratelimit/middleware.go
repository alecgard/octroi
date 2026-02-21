package ratelimit

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alecgard/octroi/internal/auth"
)

// Middleware returns an HTTP middleware that enforces rate limits using the
// provided Limiter. It expects an authenticated agent in the request context
// (set by auth.AgentAuthMiddleware). The agent's ID is used as the bucket key
// and its RateLimit field as the custom rate override.
//
// Rate-limit headers are always set on the response:
//
//	X-RateLimit-Limit     — maximum requests allowed in the window
//	X-RateLimit-Remaining — tokens remaining in the current window
//	X-RateLimit-Reset     — Unix timestamp when the bucket is fully replenished
//
// When the limit is exceeded the middleware responds with HTTP 429 and a JSON
// error body.
func Middleware(limiter *Limiter, onReject ...func()) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			agent := auth.AgentFromContext(r.Context())
			if agent == nil {
				// No agent in context — skip rate limiting.
				next.ServeHTTP(w, r)
				return
			}

			key := agent.ID
			customRate := agent.RateLimit

			// Always set headers so callers can inspect their quota.
			limit, remaining, resetAt := limiter.Status(key, customRate)
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetAt.Unix()))

			if !limiter.Allow(key, customRate) {
				for _, fn := range onReject {
					fn()
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]string{
						"code":    "rate_limited",
						"message": "Rate limit exceeded. Try again later.",
					},
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
