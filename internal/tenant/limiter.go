package tenant

// Limiter checks tenant-level resource limits.
type Limiter interface {
	CheckAgentLimit(tenantID string, currentCount int) error
	CheckToolLimit(tenantID string, currentCount int) error
	CheckUserLimit(tenantID string, currentCount int) error
	CheckRequestQuota(tenantID string) error
}

// NoopLimiter allows everything — used for self-hosted / unlimited plans.
type NoopLimiter struct{}

func (NoopLimiter) CheckAgentLimit(string, int) error   { return nil }
func (NoopLimiter) CheckToolLimit(string, int) error    { return nil }
func (NoopLimiter) CheckUserLimit(string, int) error    { return nil }
func (NoopLimiter) CheckRequestQuota(string) error      { return nil }
