package worker

import (
	"context"
	"encoding/hex"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
)

// Closure is fixed at original API creation. The publisher grants it only after
// every original text/response receipt is acknowledged and before one close.
func (c *GrokBindingPublisher) Closure(ctx context.Context, claim grok.ClosureClaim) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokTextClosing || claim.RequestID != c.closureID || claim.ProductSessionID != c.reference.SessionID || claim.NativeSessionID != c.thread || claim.NativePromptID != c.turn {
		return c.block()
	}
	if _, err := c.readClaims(); err != nil {
		return c.block()
	}
	if err := c.journal.Closure(ctx, claim); err != nil {
		return c.block()
	}
	claims, err := c.readClaims()
	if err != nil {
		return c.block()
	}
	c.proof = claims
	return nil
}

// CloseText closes and compares only the original completed plain-text API.
// The publisher keeps inference authority until native summary/close/cleanup
// finishes, then publishes the original terminal through the same outbox.
// A lost terminal receipt is recovered with ReplayPending, never another close.
func (c *GrokBindingPublisher) CloseText(ctx context.Context, api *grok.OwnedAPI) error {
	if c == nil || c.journal == nil || api == nil {
		return publicationUncertain()
	}
	scope, err := api.CompletedTextScope(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	expected := grok.CompletedTextScope{OwnerID: c.reference.JobID, ProductSessionID: c.reference.SessionID, CreationRequestID: c.reference.CreationRequestID, InputID: c.reference.InputRequestID, NativeSessionID: c.thread, NativePromptID: c.turn}
	claims, claimErr := c.readClaims()
	if c.stage != grokInputAccepted || c.mode != domain.GrokDefaultMode || c.content.Responses != 1 || c.content.MessageID != "" || len(c.textChunks) == 0 || claimErr != nil || len(claims) != 4 || scope != expected {
		c.mu.Unlock()
		return publicationUncertain()
	}
	c.stage, c.closureID = grokTextClosing, domain.NewID()
	closureID := c.closureID
	c.mu.Unlock()
	closed, err := api.CloseText(ctx, closureID)
	var observed grok.ClosedTextObservation
	if err == nil {
		observed, err = api.ObserveClosedText(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || c.stage != grokTextClosing || closed.RequestID != closureID || closed.NativeSessionID != c.thread || closed.NativePromptID != c.turn {
		return c.block()
	}
	return c.publishClosedText(ctx, observed, closureID)
}

// Called with the original coordinator lock after independently joined native
// closure; kept separate so comparisons cannot accidentally repeat native work.
func (c *GrokBindingPublisher) publishClosedText(ctx context.Context, observed grok.ClosedTextObservation, closureID domain.ID) error {
	if c.stage != grokTextClosing || c.closureID != closureID || observed.Validate(c.publisher.input.Configuration.NativeModel) != nil {
		return c.block()
	}
	claims, err := c.readClaims()
	if err != nil || len(claims) != 6 || claims[5].Closure == nil || claims[5].Closure.Phase != grok.BindClosure || claims[5].Closure.RequestID != closureID {
		return c.block()
	}
	h := observed.History
	r := observed.Terminal.Result
	t := observed.Terminal.Turn
	if h.InputID != c.reference.InputRequestID || h.ClosureID != closureID || h.NativeSessionID != c.thread || h.NativePromptID != c.turn || h.TextChunks != uint64(len(c.textChunks)) || !reflect.DeepEqual(observed.ChunkDigests, c.textChunks) || observed.OutputDigest != hex.EncodeToString(c.textOutput.Sum(nil)) || r.Meta.Session != c.thread || r.Meta.Prompt != c.turn || r.Meta.Model != c.publisher.input.Configuration.NativeModel || r.Reason != grok.EndTurn || t.Session != c.thread || t.Update.Prompt != c.turn {
		return c.block()
	}
	number := func(n uint64) string { return strconv.FormatUint(n, 10) }
	u := r.Meta.Usage
	terminal := domain.GrokTextTerminal{Kind: domain.GrokClosedFirstText, NativeEventID: t.Meta.Event, TimestampMS: number(t.Meta.TimestampMS), ElapsedMS: number(t.Update.ElapsedMS), Model: r.Meta.Model, Counts: domain.GrokResponseCounts{Input: number(u.Input), Output: number(u.Output), CachedRead: number(u.CachedRead), CacheCreation: number(u.CacheCreation), Reasoning: number(u.Reasoning)}, TotalTokens: number(u.Total), ModelCalls: number(u.Calls), APIDurationMS: number(u.DurationMS), Turns: number(u.Turns), ClosureID: closureID, HistoryDigest: h.FilesDigest}
	if terminal.Validate(string(c.thread)) != nil || terminal.Counts != c.lastResponse {
		return c.block()
	}
	c.terminal = &terminal
	c.proof = claims
	c.sequence++
	c.stage = grokTerminalPending
	if err := c.publish(ctx, domain.ExecutionEvent{Sequence: c.sequence, Kind: domain.ExecutionTurnFinished, NativeThreadID: string(c.thread), NativeTurnID: c.turn, Outcome: domain.ExecutionSucceeded, GrokTerminal: &terminal}); err != nil {
		return err
	}
	c.stage = grokTextFinished
	return nil
}

// TextCompletion proves published original native closure only. The job runner
// must separately join its workspace lease before retaining/reporting this
// version-1 completion, which deliberately grants no continuation checkpoint.
func (c *GrokBindingPublisher) TextCompletion() (domain.ExecutionCompletion, error) {
	if c == nil || c.journal == nil {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	claims, err := c.readClaims()
	sequence, sequenceErr := c.publisher.acknowledgedSequence()
	if c.stage != grokTextFinished || c.terminal == nil || err != nil || len(claims) != 6 || sequenceErr != nil || sequence != c.sequence {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	value := domain.ExecutionCompletion{Version: 1, ExecutionID: c.reference.ExecutionID, InputID: c.reference.InputID, NativeThreadID: domain.NativeIdentity(c.thread), NativeTurnID: domain.NativeIdentity(c.turn), LastSequence: sequence, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
	if err := value.ValidateForHarness(domain.GrokBuild); err != nil {
		return domain.ExecutionCompletion{}, err
	}
	return value, nil
}
