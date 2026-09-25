package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestResponseUsagePreservesExactCountersWithoutPrivateMetadata(t *testing.T) {
	c, turn := observationClient()
	params := map[string]any{"threadId": c.thread, "turnId": turn, "responseId": "private-response", "usage": usageCounts(20), "usageMetadata": map[string]any{"amount": "private-amount"}}
	event, err := observeFixture(c, "rawResponse/completed", params)
	digest := sha256.Sum256([]byte("private-response"))
	if err != nil || event.Kind != ResponseUsageEvent || !event.Correlated || event.Late || event.Native != nil || event.Usage != nil || event.ResponseUsage == nil {
		t.Fatal("missing exact response", err)
	}
	usage := event.ResponseUsage
	if usage.ResponseDigest != hex.EncodeToString(digest[:]) || usage.CostEvidence != domain.UsageCostUnspecified || usage.Counts == nil || *usage.Counts.Total != 20 || *usage.Counts.Cached != 4 || *usage.Counts.Reasoning != 3 || usage.Counts.CacheWrite != nil {
		t.Fatal("changed native scope or fabricated cost/counts")
	}
	body, _ := json.Marshal(event)
	if strings.Contains(string(body), "private-") {
		t.Fatal("raw identity or amount escaped")
	}
	duplicate, err := observeFixture(c, "rawResponse/completed", params)
	if err != nil || duplicate.ResponseUsage.ResponseDigest != usage.ResponseDigest {
		t.Fatal("native retry changed deduplication identity", err)
	}
	params["usage"], params["usageMetadata"] = nil, nil
	missing, err := observeFixture(c, "rawResponse/completed", params)
	if err != nil || missing.ResponseUsage.Counts != nil || missing.ResponseUsage.CostEvidence != domain.UsageCostMissing {
		t.Fatal("missing became zero", err)
	}
	params["threadId"] = domain.NewID()
	foreign, err := observeFixture(c, "rawResponse/completed", params)
	if err != nil || foreign.Kind != NativeExtensionEvent || foreign.ResponseUsage != nil {
		t.Fatal("foreign child attributed to root", err)
	}
	params["threadId"] = c.thread
	closed := c.execution.turns[turn]
	closed.Turn.Status = TurnCompleted
	c.execution.turns[turn] = closed
	late, err := observeFixture(c, "rawResponse/completed", params)
	if err != nil || !late.Late {
		t.Fatal("lost terminal ownership", err)
	}
}

func TestResponseUsageRejectsMalformedAndUnownedEvidence(t *testing.T) {
	for _, bad := range []string{"partial", "negative", "overflow", "fraction", "unknown-count", "unknown-metadata", "long-amount", "empty-id", "long-id", "unknown-turn", "raw-content"} {
		t.Run(bad, func(t *testing.T) {
			c, turn := observationClient()
			counts := usageCounts(20)
			params := map[string]any{"threadId": c.thread, "turnId": turn, "responseId": "response", "usage": counts}
			switch bad {
			case "partial":
				delete(counts, "inputTokens")
			case "negative":
				counts["cachedInputTokens"] = -1
			case "overflow":
				counts["totalTokens"] = json.Number("9223372036854775808")
			case "fraction":
				counts["totalTokens"] = 0.5
			case "unknown-count":
				counts["cost"] = "private"
			case "unknown-metadata":
				params["usageMetadata"] = map[string]any{"currency": "USD"}
			case "long-amount":
				params["usageMetadata"] = map[string]any{"amount": strings.Repeat("1", 257)}
			case "empty-id":
				params["responseId"] = ""
			case "long-id":
				params["responseId"] = strings.Repeat("r", 1025)
			case "unknown-turn":
				params["turnId"] = domain.NewID()
			case "raw-content":
				params["output"] = "private"
			}
			if _, err := observeFixture(c, "rawResponse/completed", params); err == nil {
				t.Fatal("accepted malformed or foreign response")
			}
		})
	}
}
