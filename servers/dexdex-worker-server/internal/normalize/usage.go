package normalize

import (
	"sync"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

// Cost rates per token (approximate Claude pricing).
const (
	inputCostPerToken  = 3.0 / 1_000_000  // $3 per 1M input tokens
	outputCostPerToken = 15.0 / 1_000_000 // $15 per 1M output tokens
)

// UsageAccumulator tracks per-session token usage from agent output.
type UsageAccumulator struct {
	mu       sync.RWMutex
	sessions map[string]*dexdexv1.UsageMetrics
}

// NewUsageAccumulator creates a new UsageAccumulator.
func NewUsageAccumulator() *UsageAccumulator {
	return &UsageAccumulator{
		sessions: make(map[string]*dexdexv1.UsageMetrics),
	}
}

// AccumulateUsage adds token counts from a single agent output event.
func (a *UsageAccumulator) AccumulateUsage(sessionID string, inputTokens, outputTokens int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	metrics, ok := a.sessions[sessionID]
	if !ok {
		metrics = &dexdexv1.UsageMetrics{}
		a.sessions[sessionID] = metrics
	}

	metrics.InputTokens += inputTokens
	metrics.OutputTokens += outputTokens
	metrics.EstimatedCostUsd = EstimateCost(metrics.InputTokens, metrics.OutputTokens)
}

// GetSessionUsage returns accumulated usage for a session.
// Returns nil if no usage has been recorded for the session.
func (a *UsageAccumulator) GetSessionUsage(sessionID string) *dexdexv1.UsageMetrics {
	a.mu.RLock()
	defer a.mu.RUnlock()

	metrics, ok := a.sessions[sessionID]
	if !ok {
		return nil
	}

	// Return a copy to prevent external mutation.
	copied := *metrics
	return &copied
}

// EstimateCost computes estimated cost based on model pricing.
func EstimateCost(inputTokens, outputTokens int64) float64 {
	return float64(inputTokens)*inputCostPerToken + float64(outputTokens)*outputCostPerToken
}
