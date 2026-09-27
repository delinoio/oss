package worker

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// RequestStop consumes one original intent before native I/O. The runner joins
// response controls first; a raced terminal or unsupported native interruption
// preserves recovery and still closes the original owned process.
type claudeStopController interface {
	Interrupt(context.Context, domain.ID, func(context.Context, claude.InterruptClaim) error) (claude.InterruptObservation, error)
	Close() error
	InspectInterrupt() (claude.InterruptObservation, error)
}

func (c *ClaudeContentPublisher) RequestStop(ctx context.Context, api claudeStopController, request domain.ID) error {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if c.verify() != nil || api == nil || c.stop != nil || b.journal.StopClaim != nil || !c.inputPublished || c.resultUsage || c.interruption != nil || !c.toolsComplete() || !c.interactionsSettled() || request.Validate() != nil || request == b.journal.ThreadRequestID || request == b.journal.InputRequestID {
		return publicationUncertain()
	}
	c.stop = &domain.ClaudeStopObservation{RequestID: request, InputID: b.journal.InputID}
	observed, err := api.Interrupt(ctx, request, func(ctx context.Context, claim claude.InterruptClaim) error {
		if ctx.Err() != nil || b.verify() != nil || claim != (claude.InterruptClaim{Version: 1, OwnerID: b.journal.JobID, SessionID: b.journal.SessionID, InputID: b.journal.InputID, TurnID: b.turn, RequestID: request}) {
			return b.block()
		}
		b.journal.StopClaim = &claim
		raw, err := json.Marshal(b.journal)
		if err != nil || security.WriteAtomic(b.path, raw) != nil {
			return b.block()
		}
		b.saved = raw
		return nil
	})
	if err != nil || !observed.Claimed || !observed.Attempted || !observed.Acknowledged || observed.NativeResult != nil || observed.Idle || observed.CleanupJoined || observed.ProblemCode != "" {
		return b.block()
	}
	c.stop.Acknowledged = true
	return nil
}

// ObserveStop retains separate original envelopes, never converting the
// missing native result input into a correlated successful/failed input result.
func (c *ClaudeContentPublisher) ObserveStop(o claude.LifecycleObservation) (bool, error) {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	v := c.stop
	if v == nil {
		return false, nil
	}
	partial := o.Kind == claude.ContentObserved && len(o.Content) == 1 && o.Content[0].Kind == claude.ContentInterrupted
	streamClosed := o.Kind == claude.ContentObserved && len(o.Content) == 1 && o.Content[0].Kind == claude.ContentInterruptStreamClosed
	messageClosed := o.Kind == claude.ContentObserved && len(o.Content) == 1 && o.Content[0].Kind == claude.ContentInterruptMessageClosed
	retry := o.Kind == claude.ProgressObserved && o.Progress != nil && o.Progress.Kind == claude.APIRetryObserved
	contextEvent := o.Kind == claude.ContentObserved && len(o.Content) == 1 && o.Content[0].Kind == claude.NativeInterruptContext
	result := o.Kind == claude.InterruptResultObserved
	command := o.Kind == claude.CommandObserved && o.Command == claude.CommandCancelled
	idle := o.Kind == claude.RunStateObserved && o.Run != nil && o.Run.State == claude.RunIdle
	if !partial && !streamClosed && !messageClosed && !retry && !contextEvent && !result && !command && !idle {
		if v.InterruptedNativeID != "" || v.ContextNativeID != "" {
			return true, b.block()
		}
		return false, nil
	}
	if c.verify() != nil || !v.Acknowledged || v.Idle || o.SessionID != b.journal.SessionID || o.TurnID != b.turn || domain.NativeIdentity(o.NativeID).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil || o.NativeID == b.turn || o.NativeID == string(b.journal.SessionID) || o.NativeID == string(v.RequestID) || o.NativeID == string(v.InputID) || c.seen[o.NativeID] || c.usageSeen[o.NativeID] || b.progressSeen[o.NativeID] || len(c.seen) >= 65536 {
		return true, b.block()
	}
	if partial || streamClosed || messageClosed || retry || contextEvent || command {
		if o.InputID != b.journal.InputID || !o.Accepted || o.Result != nil || o.Run != nil {
			return true, b.block()
		}
	} else if o.InputID != "" || o.Accepted || len(o.Content) != 0 || o.Command != "" {
		return true, b.block()
	}
	switch {
	case retry:
		r := o.Progress.APIRetry
		if r == nil || v.ResultNativeID != "" || len(v.Retries) >= 128 || !reflect.DeepEqual(*o.Progress, claude.NativeProgressObservation{Kind: claude.APIRetryObserved, APIRetry: r}) {
			return true, b.block()
		}
		report := domain.ClaudeStopRetryObservation{NativeEventID: o.NativeID, Attempt: domain.ClaudeProgressCount(strconv.FormatUint(r.Attempt, 10)), MaxRetries: domain.ClaudeProgressCount(strconv.FormatUint(r.MaxRetries, 10)), DelayMS: domain.ClaudeProgressCount(strconv.FormatUint(r.DelayMS, 10)), Error: domain.ClaudeAPIProblem(r.Error)}
		if r.ErrorStatus != nil {
			value := *r.ErrorStatus
			report.ErrorStatus = &value
		}
		if report.Validate() != nil {
			return true, b.block()
		}
		v.Retries = append(v.Retries, report)
	case messageClosed:
		n := o.Content[0]
		if v.BlockStopNativeID == "" || v.MessageStopNativeID != "" || v.InterruptedNativeID != "" || c.active != n.MessageID || n.ParentToolID != "" || n.Model != b.publisher.input.Configuration.NativeModel || n.Index != nil || n.Block != nil || n.Usage != nil {
			return true, b.block()
		}
		v.MessageStopNativeID = o.NativeID
		v.ContentEvidence = domain.ClaudeRetryStreamClosed
	case streamClosed:
		n := o.Content[0]
		prior, ok := c.messages[n.MessageID]
		if v.InterruptedNativeID != "" || v.BlockStopNativeID != "" || !ok || prior.state != domain.MessageStreaming || prior.content == nil || len(prior.content.Blocks) != 1 || c.active != n.MessageID || n.Index == nil || *n.Index != 0 || n.ParentToolID != "" || n.Model != b.publisher.input.Configuration.NativeModel || n.Block != nil || n.Usage != nil {
			return true, b.block()
		}
		v.BlockStopNativeID = o.NativeID
		v.MessageID, v.NativeMessageID, v.Text = prior.id, n.MessageID, prior.content.Blocks[0].Block.Text
	case partial:
		n := o.Content[0]
		prior, ok := c.messages[n.MessageID]
		if v.InterruptedNativeID != "" || !ok || c.active != n.MessageID || prior.state != domain.MessageStreaming || prior.content == nil || len(prior.content.Blocks) != 1 || n.Index == nil || *n.Index != 0 || n.ParentToolID != "" || n.Model != b.publisher.input.Configuration.NativeModel || n.Block == nil || n.Block.Kind != claude.TextBlock || n.Block.Text == nil || n.Block.Citations != nil || prior.content.Blocks[0].State != domain.ClaudeBlockStreaming || prior.content.Blocks[0].Block.Kind != domain.ClaudeText || prior.content.Blocks[0].Block.Text != *n.Block.Text {
			return true, b.block()
		}
		v.InterruptedNativeID, v.MessageID, v.NativeMessageID, v.Text = o.NativeID, prior.id, n.MessageID, *n.Block.Text
		v.ContentEvidence = domain.ClaudeAbortedAssistant
		if n.Usage != nil && convertClaudeUsage(n.Usage, &v.PartialUsage) != nil {
			return true, b.block()
		}
	case contextEvent:
		n := o.Content[0]
		if (v.InterruptedNativeID == "" && (v.MessageStopNativeID == "" || len(v.Retries) == 0)) || v.ContextNativeID != "" || len(n.Blocks) != 1 || !reflect.DeepEqual(n, claude.ContentEvent{Kind: claude.NativeInterruptContext, Blocks: n.Blocks}) {
			return true, b.block()
		}
		block, err := claudeDisplayBlock(&n.Blocks[0])
		if err != nil || block.Kind != domain.ClaudeText || block.Text != domain.ClaudeStopContextText {
			return true, b.block()
		}
		v.ContextNativeID, v.Context = o.NativeID, block.Text
	case result:
		r := o.Result
		if v.ContextNativeID == "" || v.ResultNativeID != "" || r == nil || r.Kind != claude.ResultExecutionError || r.Reason != claude.AbortedStreaming || !r.Error || r.Origin != nil || r.KnownWork != (claude.NativeWorkObservation{}) || r.Usage == nil || convertClaudeUsage(r.Usage, &v.Usage) != nil {
			return true, b.block()
		}
		v.ResultNativeID, v.Kind, v.Reason, v.Error = o.NativeID, domain.ClaudeResultKind(r.Kind), domain.ClaudeTerminalReason(r.Reason), r.Error
		if b.publisher.config.Logger != nil {
			b.publisher.config.Logger.Info("claude_original_stop_result", "job_id", b.journal.JobID, "reason", r.Reason, "aborted_assistant", v.InterruptedNativeID != "", "retry_count", len(v.Retries))
		}
	case command:
		if v.ResultNativeID == "" || v.CommandNativeID != "" || len(o.Content) != 0 {
			return true, b.block()
		}
		v.CommandNativeID, v.Command = o.NativeID, domain.ClaudeCommandCancelled
	case idle:
		if v.CommandNativeID == "" || o.Result != nil || o.Run.KnownWork != (claude.NativeWorkObservation{}) || o.Run.ContinuationFailed {
			return true, b.block()
		}
		v.IdleNativeID, v.Idle = o.NativeID, true
	}
	c.seen[o.NativeID] = true
	return true, nil
}

// PublishStopped joins original owned cleanup before publishing the composite
// Stop proof. The separate report still requires the workspace lease to close.
func (c *ClaudeContentPublisher) PublishStopped(ctx context.Context, api claudeStopController) (domain.ExecutionCompletion, error) {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if c.verify() != nil || c.stop == nil || !c.stop.Idle || api == nil || b.journal.StopClaim == nil || c.resultUsage || c.interruption != nil {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	if err := api.Close(); err != nil {
		return domain.ExecutionCompletion{}, b.block()
	}
	observed, err := api.InspectInterrupt()
	v := c.stop
	if err != nil || observed.Claim != *b.journal.StopClaim || !observed.Acknowledged || !observed.Idle || !observed.CleanupJoined || observed.ResultCorrelated || observed.ProblemCode != "" || observed.NativeResult == nil || observed.NativeResult.Kind != claude.ResultExecutionError || domain.ClaudeTerminalReason(observed.NativeResult.Reason) != v.Reason || !observed.NativeResult.Error || b.verify() != nil {
		return domain.ExecutionCompletion{}, b.block()
	}
	v.CleanupVerified = true
	prior := c.messages[v.NativeMessageID]
	closed, err := domain.InterruptClaudeContent(prior.content, prior.state, *v)
	if err != nil {
		return domain.ExecutionCompletion{}, b.block()
	}
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionTurnFinished, Outcome: domain.ExecutionStopped, ClaudeStop: v}, native: v.NativeMessageID, next: claudePublishedContent{id: prior.id, state: domain.MessageComplete, content: closed}}}
	if err := c.drain(ctx); err != nil {
		return domain.ExecutionCompletion{}, err
	}
	return c.stoppedCompletion()
}

// Available after exact terminal outbox replay, with no additional native I/O.
func (c *ClaudeContentPublisher) StoppedCompletion() (domain.ExecutionCompletion, error) {
	c.binding.mu.Lock()
	defer c.binding.mu.Unlock()
	return c.stoppedCompletion()
}

func (c *ClaudeContentPublisher) stoppedCompletion() (domain.ExecutionCompletion, error) {
	b := c.binding
	if b.verify() != nil || b.stage != claudeTerminalPublished || c.stop == nil || c.stop.Validate() != nil || c.terminal != nil || c.terminalSequence != b.sequence || c.pending || len(c.queue) != 0 {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	v := domain.ExecutionCompletion{Version: 1, ExecutionID: b.journal.ExecutionID, InputID: b.journal.InputID, NativeThreadID: domain.NativeIdentity(b.journal.SessionID), NativeTurnID: domain.NativeIdentity(b.turn), LastSequence: c.terminalSequence, Outcome: domain.ExecutionStopped, CleanupVerified: true}
	return v, v.ValidateForHarness(domain.ClaudeCode)
}
