package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

// RequestStop coordinates one original live abort with the existing mutation
// journal. The caller must keep consuming the original stream after a lost HTTP
// response. Owner-only termination and event gaps retain their recovery path.
func (c *OpenCodeEventPublisher) RequestStop(ctx context.Context, request domain.ID) (opencode.StopReceipt, error) {
	if c == nil || c.api == nil || c.text == nil || request.Validate() != nil {
		return opencode.StopReceipt{}, publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.blocked || c.finished || c.stopRequest != "" {
		return opencode.StopReceipt{}, publicationUncertain()
	}
	b := c.text.binding
	b.mu.Lock()
	claims, err := b.readClaims()
	valid := err == nil && b.validPublicationClaims(claims) && b.stage == openCodeAccepted && b.stopClaim == nil && !c.text.blocked
	digest := sha256.Sum256(nil)
	expected := opencode.SessionClaim{RequestID: request, Kind: opencode.StopInputMutation, SessionID: b.thread, MessageID: b.turn, PartID: b.inputClaim.PartID, InputRequestID: b.reference.InputRequestID, BodyDigest: hex.EncodeToString(digest[:])}
	if valid && expected.Validate() == nil {
		b.expectedStop = &expected
	} else {
		valid = false
	}
	b.mu.Unlock()
	if !valid {
		return opencode.StopReceipt{}, c.fail(publicationUncertain())
	}
	c.stopRequest, c.stopSeen = request, map[string]bool{}
	receipt, err := c.api.Interrupt(ctx, request)
	b.mu.Lock()
	claims, claimErr := b.readClaims()
	valid = claimErr == nil && b.validPublicationClaims(claims) && b.stopClaim != nil && *b.stopClaim == expected
	b.mu.Unlock()
	if !valid || receipt.RequestID != request || receipt.InputRequestID != b.reference.InputRequestID || receipt.SessionID != b.thread || receipt.MessageID != b.turn || !receipt.NativeAttempted {
		return receipt, c.fail(publicationUncertain())
	}
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "opencode_original_stop_requested", "job_id", b.publisher.job, "request_id", request, "http_accepted", receipt.HTTPAccepted)
	}
	return receipt, err
}

func (c *OpenCodeEventPublisher) bufferStoppedObservation(o opencode.Observation) error {
	if domain.NativeIdentity(o.EventID).Validate(domain.OpenCode, domain.NativeEventIdentity) != nil || c.seen[o.EventID] || c.stopSeen[o.EventID] || len(c.seen)+len(c.stopSeen) >= 65536 || c.stopped != nil {
		return c.fail(publicationUncertain())
	}
	frozen, err := o.Freeze()
	if err != nil || frozen.Bytes() <= 0 || frozen.Bytes() > maxOpenCodeTextBytes-c.text.bytes-c.stopBytes {
		return c.fail(publicationUncertain())
	}
	c.stopBuffer = append(c.stopBuffer, frozen)
	c.stopSeen[o.EventID] = true
	c.stopBytes += frozen.Bytes()
	return nil
}

func (c *OpenCodeEventPublisher) finishStoppedPublication(ctx context.Context) (opencode.HistoryObservation, error) {
	b := c.text.binding
	validClaims := func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		claims, err := b.readClaims()
		return err == nil && b.validPublicationClaims(claims) && b.stage == openCodeAccepted && b.stopClaim != nil && b.stopClaim.RequestID == c.stopRequest && !c.text.blocked && !c.usage.blocked
	}
	if c.stopped != nil || !validClaims() {
		return opencode.HistoryObservation{}, publicationUncertain()
	}
	proof, err := c.api.CloseAfterStop(ctx)
	r, h := proof.Stop, proof.History
	if err != nil {
		return opencode.HistoryObservation{}, err
	}
	if !validClaims() || r.RequestID != c.stopRequest || r.InputRequestID != b.reference.InputRequestID || r.SessionID != b.thread || r.MessageID != b.turn || !r.NativeAttempted || r.RepliesUncertain || h.RequestID != r.InputRequestID || h.SessionID != r.SessionID || h.InputID != r.MessageID {
		return opencode.HistoryObservation{}, publicationUncertain()
	}
	observation := domain.OpenCodeStopObservation{RequestID: r.RequestID, InputRequestID: r.InputRequestID, InputPartID: b.inputClaim.PartID, AssistantID: h.AssistantID, HistoryDigest: h.Digest, HTTPAccepted: r.HTTPAccepted, InterruptedObserved: r.InterruptedObserved, TerminalObserved: r.TerminalObserved, IdleObserved: r.IdleObserved, PendingCleared: r.PendingCleared, CleanupVerified: r.CleanupVerified}
	observation.RetryObservations = slices.Clone(proof.Retries)
	observation.RetryCanceledObserved = r.RetryCanceledObserved
	if observation.Validate() != nil {
		return opencode.HistoryObservation{}, publicationUncertain()
	}
	c.stopped, c.stopObservation = &proof, &observation
	if observation.RetryCanceledObserved {
		c.usage.stoppedBackoffAssistant = h.AssistantID
	}
	closed := map[string]bool{}
	for i, frozen := range c.stopBuffer {
		o, err := frozen.Thaw()
		if err != nil {
			return opencode.HistoryObservation{}, err
		}
		c.stopBytes -= frozen.Bytes()
		c.stopBuffer[i] = nil
		if o.Part != nil && o.Part.Tool != nil && o.Part.Tool.State == opencode.ToolError {
			for _, canceled := range proof.Canceled {
				if canceled.PartID == o.Part.ID {
					if canceled.MessageID != o.Part.MessageID || canceled.CallID != o.Part.Tool.CallID {
						return opencode.HistoryObservation{}, publicationUncertain()
					}
					if closed[canceled.RequestID] {
						continue
					}
					if err := c.closeStoppedInteraction(ctx, canceled); err != nil {
						return opencode.HistoryObservation{}, err
					}
					closed[canceled.RequestID] = true
				}
			}
		}
		if err := c.publishObservation(ctx, o); err != nil {
			return opencode.HistoryObservation{}, err
		}
	}
	c.stopBuffer, c.stopSeen = nil, nil
	if len(closed) != len(proof.Canceled) || len(c.interactions) != 0 || !validClaims() {
		return opencode.HistoryObservation{}, publicationUncertain()
	}
	return h, nil
}

func (c *OpenCodeEventPublisher) closeStoppedInteraction(ctx context.Context, canceled opencode.StoppedInteractionObservation) error {
	original := c.interactions[canceled.RequestID]
	if original.ID == "" || original.OpenCode == nil || original.OpenCode.NativeEventID != canceled.ArrivalID || original.OpenCode.NativeMessageID != canceled.MessageID || original.NativeItemID != canceled.PartID || original.OpenCode.CallID != canceled.CallID || c.responses[canceled.RequestID] != nil || c.closedInteractions[canceled.RequestID].original.ID != "" || (original.Type == domain.UserQuestionInteraction) != (canceled.Kind == opencode.QuestionInteraction) {
		return publicationUncertain()
	}
	closure := domain.ExecutionInteractionUpdate{ID: original.ID, NativeRequestID: original.NativeRequestID, NativeItemID: original.NativeItemID, Type: original.Type, Closure: domain.InteractionTurnEnded, OpenCodeStop: &domain.OpenCodeStopClosure{Stop: *c.stopObservation, ProposalEventID: canceled.ArrivalID, ToolInterrupted: true}}
	raw, err := json.Marshal(closure)
	if err != nil || closure.Validate(domain.ExecutionInteractionClosed) != nil || len(raw) > maxOpenCodeTextBytes-c.text.bytes-c.stopBytes {
		return publicationUncertain()
	}
	b := c.text.binding
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionInteractionClosed, NativeThreadID: b.thread, NativeTurnID: b.turn, Interaction: &closure}); err != nil {
		return err
	}
	if c.closedInteractions == nil {
		c.closedInteractions = map[string]openCodeClosedInteraction{}
	}
	c.closedInteractions[canceled.RequestID] = openCodeClosedInteraction{original: original}
	delete(c.interactions, canceled.RequestID)
	c.text.bytes += len(raw)
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "opencode_original_request_canceled", "job_id", b.publisher.job, "request_id", c.stopRequest, "interaction_id", original.ID)
	}
	return nil
}
