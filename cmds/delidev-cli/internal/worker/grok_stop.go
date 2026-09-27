package worker

import (
	"context"
	"encoding/hex"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
)

// Stop deliberately does not acquire the publication mutex: an original native
// cancellation must still be submitted while an acknowledged-content receipt is
// blocked. The journal independently checks the four original input records.
func (c *GrokBindingPublisher) Stop(ctx context.Context, claim grok.StopClaim) error {
	if c == nil || c.journal == nil || !c.accepted.Load() || c.mode != domain.GrokDefaultMode || claim.Validate() != nil || claim.OwnerID != c.reference.JobID || claim.ProductSessionID != c.reference.SessionID || claim.InputRequestID != c.reference.InputRequestID || !c.stopClaim.CompareAndSwap(nil, &claim) {
		return publicationUncertain()
	}
	return c.journal.Stop(ctx, claim)
}

// Only the original once-claimed Stop may extend the acknowledged native claim
// prefix during a content publication. This never changes the pending receipt.
// Caller holds the publication mutex; readClaims separately verifies disk/state.
func (c *GrokBindingPublisher) acceptStopExtension(claims []grokClaim) bool {
	if len(claims) == len(c.proof) {
		return true
	}
	stop := c.stopClaim.Load()
	if len(c.proof) != 4 || len(claims) != 5 || stop == nil || claims[4].Stop == nil || *claims[4].Stop != *stop || stop.NativeSessionID != c.thread || stop.NativePromptID != c.turn {
		return false
	}
	c.proof = claims
	return true
}

// PublishStopped reads only the independently retained original controller.
// Native work has already joined; receipt loss cannot repeat its cancellation.
func (c *GrokBindingPublisher) PublishStopped(ctx context.Context, api *grok.OwnedAPI) error {
	if c == nil || c.journal == nil || api == nil {
		return publicationUncertain()
	}
	original, err := api.ObserveStoppedText(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publishStoppedText(ctx, original)
}

func (c *GrokBindingPublisher) publishStoppedText(ctx context.Context, v grok.StoppedTextObservation) error {
	claims, err := c.readClaims()
	stop := c.stopClaim.Load()
	if c.stage != grokInputAccepted || c.mode != domain.GrokDefaultMode || c.terminal != nil || c.stopped != nil || err != nil || !c.acceptStopExtension(claims) || len(claims) != 5 || stop == nil || claims[4].Stop == nil || *claims[4].Stop != *stop || v.Validate(c.publisher.input.Configuration.NativeModel) != nil || v.Stop.Claim != *stop || v.CreationRequestID != c.reference.CreationRequestID || v.Stop.Claim.NativeSessionID != c.thread || v.Stop.Claim.NativePromptID != c.turn || claims[2].Input == nil || v.InputDigest != claims[2].Input.BodyDigest || !slices.Equal(v.ChunkDigests, c.textChunks) || v.OutputDigest != hex.EncodeToString(c.textOutput.Sum(nil)) {
		return c.block()
	}
	number := func(n uint64) string { return strconv.FormatUint(n, 10) }
	result := domain.GrokStopObservation{RequestID: stop.RequestID, InputID: c.reference.InputID, InputRequestID: stop.InputRequestID, MessageID: c.firstTextID, Model: c.publisher.input.Configuration.NativeModel, OutputDigest: v.OutputDigest, TextChunks: uint32(len(v.ChunkDigests)), Delivered: v.Stop.Delivered, Idle: v.Stop.Idle, CleanupJoined: v.Stop.CleanupJoined}
	for _, retry := range v.Retries {
		result.Retries = append(result.Retries, domain.GrokStopRetry{NativeEventID: retry.Event, TimestampMS: number(retry.TimestampMS), Kind: domain.GrokRetryKind(retry.Kind), Error: domain.GrokRetryError(retry.Error), Attempt: number(retry.Attempt), MaxRetries: number(retry.MaxRetries)})
	}
	if v.Interrupted != nil {
		if c.content.Responses != 0 || c.content.MessageID != c.firstTextID {
			return c.block()
		}
		r, t := v.Interrupted.Result, v.Interrupted.Turn
		contextTokens := number(r.Meta.ContextTokens)
		result.Kind, result.Category, result.ContextTokens = domain.GrokInterruptedText, domain.GrokCancellationCategory(r.Meta.Category), &contextTokens
		if len(v.ChunkDigests) == 0 {
			if c.firstTextID != "" || c.content != (domain.GrokContentState{}) {
				return c.block()
			}
			result.Kind = domain.GrokInterruptedBeforeText
		}
		result.NativeEventID, result.TimestampMS, result.ElapsedMS = t.Meta.Event, number(t.Meta.TimestampMS), number(t.Update.ElapsedMS)
	} else {
		if c.content.Responses != 1 || c.content.MessageID != "" || v.Completed == nil {
			return c.block()
		}
		u, t := v.Completed.Result.Meta.Usage, v.Completed.Turn
		counts := domain.GrokResponseCounts{Input: number(u.Input), Output: number(u.Output), CachedRead: number(u.CachedRead), CacheCreation: number(u.CacheCreation), Reasoning: number(u.Reasoning)}
		if counts != c.lastResponse {
			return c.block()
		}
		result.Kind = domain.GrokCompletedDuringStop
		result.NativeEventID, result.TimestampMS, result.ElapsedMS = t.Meta.Event, number(t.Meta.TimestampMS), number(t.Update.ElapsedMS)
		result.Completed = &domain.GrokStopCompletion{Counts: counts, TotalTokens: number(u.Total), ModelCalls: number(u.Calls), APIDurationMS: number(u.DurationMS), Turns: number(u.Turns)}
	}
	last, lastErr := domain.GrokEventIndex(c.content.LastEvent, string(c.thread))
	terminal, terminalErr := domain.GrokEventIndex(result.NativeEventID, string(c.thread))
	if result.Validate(string(c.thread)) != nil || terminalErr != nil || result.Kind != domain.GrokInterruptedBeforeText && (lastErr != nil || terminal <= last) {
		return c.block()
	}
	c.stopped, c.sequence, c.stage = &result, c.sequence+1, grokTerminalPending
	if err := c.publish(ctx, domain.ExecutionEvent{Sequence: c.sequence, Kind: domain.ExecutionTurnFinished, NativeThreadID: string(c.thread), NativeTurnID: c.turn, Outcome: result.Outcome(), GrokStop: &result}); err != nil {
		return err
	}
	c.stage = grokTextFinished
	return nil
}
