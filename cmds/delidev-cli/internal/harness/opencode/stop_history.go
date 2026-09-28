package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// The original live Stop may race normal completion. Its receipt must prove
// terminal/idle independently of an HTTP acknowledgment, while owner-only
// cleanup and uncertain reply delivery cannot become stored-history evidence.
func (o *inputObserver) stoppedHistoryReady() bool {
	if o.stop == nil || o.stop.claim.Validate() != nil || o.stop.claim.Kind != StopInputMutation || !o.stop.sent || o.stop.cleanupAttempted || o.stop.claim.InputRequestID != o.input.receipt.RequestID || o.stop.claim.SessionID != o.input.receipt.SessionID || o.stop.claim.MessageID != o.input.receipt.MessageID || o.stop.claim.PartID != o.input.receipt.PartID || o.stop.claim.BodyDigest != mutationDigest(nil) || o.stop.claim.RequestID != o.stop.receipt.RequestID {
		return false
	}
	receipt, err := o.stopReceiptLocked()
	if err != nil || receipt.RequestID != o.stop.claim.RequestID || receipt.InputRequestID != o.input.receipt.RequestID || receipt.SessionID != o.input.receipt.SessionID || receipt.MessageID != o.input.receipt.MessageID || !receipt.TerminalObserved || !receipt.IdleObserved || !receipt.HTTPAccepted && !receipt.InterruptedObserved || receipt.RepliesUncertain {
		return false
	}
	for _, interaction := range o.interactions {
		if !interaction.closed && !o.interactionInterrupted(interaction) {
			return false
		}
	}
	return true
}

// Called under the original observer/session locks before and after exact
// history comparison. A missing pending entry is not response acceptance, and
// known interrupted effects remain pending until joined owner cleanup.
func (s *sessionAPI) stoppedHistoryIdle(ctx context.Context, o *inputObserver) error {
	if !o.stoppedHistoryReady() {
		return sessionUncertain()
	}
	for _, inventory := range []struct {
		path string
		kind EventKind
	}{{"/permission", PermissionAskedEvent}, {"/question", QuestionAskedEvent}} {
		raw, _, err := s.request(ctx, http.MethodGet, inventory.path, nil, http.StatusOK)
		if err != nil {
			return err
		}
		var entries []json.RawMessage
		if domain.Decode(raw, &entries) != nil || entries == nil || len(entries) > maxObservedInteractions {
			return observerProblem()
		}
		seen := map[string]bool{}
		for _, raw := range entries {
			value, err := decodeNativeInteraction(inventory.kind, raw)
			original := o.interactions[value.ID]
			if err != nil || seen[value.ID] || value.SessionID != o.input.receipt.SessionID || original == nil || original.closed || !o.interactionInterrupted(original) || !bytes.Equal(canonicalNative(raw), original.raw) {
				return observerProblem()
			}
			seen[value.ID] = true
		}
	}
	raw, _, err := s.request(ctx, http.MethodGet, "/session/status", nil, http.StatusOK)
	if err != nil {
		return err
	}
	status, err := object(raw)
	if err != nil || len(status) != 0 || !o.stoppedHistoryReady() {
		return observerProblem()
	}
	return nil
}
