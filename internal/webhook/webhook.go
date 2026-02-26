package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Payload is the JSON body sent to webhook endpoints.
type Payload struct {
	ToolID           string    `json:"tool_id"`
	ToolName         string    `json:"tool_name"`
	ThresholdPercent int       `json:"threshold_percent"`
	CurrentUsage     float64   `json:"current_usage"`
	BudgetLimit      float64   `json:"budget_limit"`
	Timestamp        time.Time `json:"timestamp"`
}

// Dispatcher manages webhook delivery with cooldown deduplication.
type Dispatcher struct {
	cooldowns sync.Map // key: "toolID:pct" → time.Time (last fired)
	cooldown  time.Duration
	client    *http.Client
}

// NewDispatcher creates a webhook dispatcher with the given cooldown period.
func NewDispatcher(cooldown time.Duration) *Dispatcher {
	return &Dispatcher{
		cooldown: cooldown,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// MaybeDispatch fires a webhook if the tool's budget usage has crossed the
// threshold and no webhook has been sent for this tool+threshold within the
// cooldown window. It runs the HTTP POST in a background goroutine.
func (d *Dispatcher) MaybeDispatch(toolID, toolName, webhookURL string, thresholdPct int, budgetLimit, currentUsage float64) {
	if webhookURL == "" || budgetLimit <= 0 {
		return
	}

	usagePct := (currentUsage / budgetLimit) * 100
	if usagePct < float64(thresholdPct) {
		return
	}

	key := fmt.Sprintf("%s:%d", toolID, thresholdPct)
	now := time.Now()

	if val, ok := d.cooldowns.Load(key); ok {
		lastFired := val.(time.Time)
		if now.Sub(lastFired) < d.cooldown {
			return // still in cooldown
		}
	}

	d.cooldowns.Store(key, now)

	payload := Payload{
		ToolID:           toolID,
		ToolName:         toolName,
		ThresholdPercent: thresholdPct,
		CurrentUsage:     currentUsage,
		BudgetLimit:      budgetLimit,
		Timestamp:        now.UTC(),
	}

	go d.fire(webhookURL, payload)
}

func (d *Dispatcher) fire(url string, payload Payload) {
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("webhook marshal error", "error", err)
		return
	}

	resp, err := d.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Warn("webhook delivery failed", "url", url, "error", err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Warn("webhook returned error status", "url", url, "status", resp.StatusCode)
	}
}
