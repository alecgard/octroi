package ratelimit

import (
	"context"
	"fmt"
	"time"
)

// ToolRateLimiter checks per-tool rate limits across global, team, and agent scopes.
type ToolRateLimiter struct {
	store   *ToolRateLimitStore
	limiter *Limiter
}

// NewToolRateLimiter creates a ToolRateLimiter using the given store and in-memory limiter.
func NewToolRateLimiter(store *ToolRateLimitStore, limiter *Limiter) *ToolRateLimiter {
	return &ToolRateLimiter{store: store, limiter: limiter}
}

// CheckToolRateLimit resolves the applicable rates for the tool and checks all
// non-zero buckets. All buckets must allow for the request to proceed. Returns
// the tightest limit info for response headers.
func (trl *ToolRateLimiter) CheckToolRateLimit(ctx context.Context, toolID, team, agentID string) (allowed bool, limit, remaining int, resetAt time.Time, err error) {
	globalRate, teamRate, agentRate, err := trl.store.Resolve(ctx, toolID, team, agentID)
	if err != nil {
		return false, 0, 0, time.Time{}, err
	}

	// No tool-level rate limits configured at all.
	if globalRate == 0 && teamRate == 0 && agentRate == 0 {
		return true, 0, 0, time.Time{}, nil
	}

	type scopeCheck struct {
		key  string
		rate int
	}

	var checks []scopeCheck
	if globalRate > 0 {
		checks = append(checks, scopeCheck{
			key:  fmt.Sprintf("tool:%s", toolID),
			rate: globalRate,
		})
	}
	if teamRate > 0 && team != "" {
		checks = append(checks, scopeCheck{
			key:  fmt.Sprintf("tool:%s:team:%s", toolID, team),
			rate: teamRate,
		})
	}
	if agentRate > 0 {
		checks = append(checks, scopeCheck{
			key:  fmt.Sprintf("tool:%s:agent:%s", toolID, agentID),
			rate: agentRate,
		})
	}

	if len(checks) == 0 {
		return true, 0, 0, time.Time{}, nil
	}

	// All buckets must allow. Track the tightest for headers.
	allowed = true
	for _, c := range checks {
		if !trl.limiter.Allow(c.key, c.rate) {
			allowed = false
		}
		l, r, rst := trl.limiter.Status(c.key, c.rate)
		if limit == 0 || l < limit {
			limit = l
			remaining = r
			resetAt = rst
		}
	}

	return allowed, limit, remaining, resetAt, nil
}
