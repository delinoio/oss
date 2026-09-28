package codex

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// The pinned official RawResponseCompletedNotification is emitted for one live
// upstream completion, never from accumulated/replayed TokenCount snapshots.
// See docs/cmds-delidev-usage-contract.md for the exact upstream source contract.
func (c *Client) observeResponseUsageLocked(native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID   domain.ID        `json:"threadId"`
		TurnID     domain.ID        `json:"turnId"`
		ResponseID string           `json:"responseId"`
		Usage      *tokenCountsWire `json:"usage"`
		Metadata   *struct {
			Amount *string `json:"amount"`
		} `json:"usageMetadata"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil || domain.Text(params.ResponseID, "native response identity", 1024, true) != nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[params.TurnID]
	if !known {
		return Event{}, incompatible()
	}
	digest := sha256.Sum256([]byte(params.ResponseID))
	usage := &domain.NativeResponseUsage{ResponseDigest: hex.EncodeToString(digest[:]), CostEvidence: domain.UsageCostMissing}
	if params.Usage != nil {
		counts := params.Usage.normalized()
		usage.Counts = &counts
	}
	if params.Metadata != nil && params.Metadata.Amount != nil {
		if domain.Text(*params.Metadata.Amount, "native usage metadata", 256, false) != nil {
			return Event{}, incompatible()
		}
		usage.CostEvidence = domain.UsageCostUnspecified
	}
	if usage.Validate() != nil {
		return Event{}, incompatible()
	}
	return Event{Kind: ResponseUsageEvent, ThreadID: c.thread, TurnID: params.TurnID, ResponseUsage: usage, Correlated: true, Late: turn.Turn.Status.terminal()}, nil
}
