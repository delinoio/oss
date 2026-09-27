package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// The pinned CLI emits an explicit aborted assistant envelope for its partial
// root text. Preserve those bytes as interrupted, never a completed block or
// provider message_stop. Other unfinished content needs its own native proof.
func (b *ExecutionBinding) observeInterruptedAssistant(parent string, message providerMessage, active *providerMessageState, aborted bool) ([]ContentEvent, error) {
	if !aborted || b.interrupt == nil || b.interruptedMessage || parent != "" || active == nil || active.id != message.ID || active.model != message.Model || len(active.blocks) != 1 || len(message.Content) != 1 || message.Stop != nil || message.Sequence != nil {
		return nil, lifecycleUncertain()
	}
	state := active.blocks[0]
	block, err := decodeContentBlock(message.Content[0])
	if err != nil || state == nil || state.completed || state.stopped || state.kind != TextBlock || block.Kind != TextBlock || block.Text == nil || *block.Text != state.text.String() || !bytes.Equal(bytes.TrimSpace(block.Cache), bytes.TrimSpace(state.cache)) || block.Citations != nil || len(state.citations) != 0 {
		return nil, lifecycleUncertain()
	}
	b.content.bufferedBytes -= state.retainedBytes()
	delete(b.content.active, parent)
	b.interruptedMessage = true
	index := uint32(0)
	return []ContentEvent{{Kind: ContentInterrupted, MessageID: message.ID, Model: message.Model, Index: &index, Block: &block, Usage: message.Usage}}, nil
}

// InterruptClaim is retained by the owning Worker before one native control.
// A control acknowledgment is independent of native result, idle and cleanup.
type InterruptClaim struct {
	Version   uint32    `json:"version"`
	OwnerID   domain.ID `json:"owner_id"`
	SessionID domain.ID `json:"session_id"`
	InputID   domain.ID `json:"input_id"`
	TurnID    string    `json:"native_turn_id"`
	RequestID domain.ID `json:"request_id"`
}

type InterruptObservation struct {
	Claim            InterruptClaim
	Claimed          bool
	Attempted        bool
	Acknowledged     bool
	NativeResult     *NativeResult
	ResultCorrelated bool
	Idle             bool
	CleanupJoined    bool
	ProblemCode      domain.Code
}

func (s *Stream) Interrupt(ctx context.Context, id domain.ID) error {
	response, err := s.Call(ctx, id, map[string]any{"subtype": "interrupt"})
	if err != nil {
		return err
	}
	var result struct {
		Queued []json.RawMessage `json:"still_queued"`
	}
	// The pinned CLI reports remaining queued input. This controller owns a
	// single input; even a valid foreign queued identity contradicts that scope.
	if response.Failed || decodeNativeObject(response.Result, &result) != nil || result.Queued == nil || len(result.Queued) != 0 {
		return streamUncertain()
	}
	return nil
}

// Interrupt stops only the current original root run. It stays single-use even
// after an uncertain claim/send or a normal completion racing the request.
// Close remains available independently to contain the original owned process.
func (s *APISession) Interrupt(ctx context.Context, request domain.ID, claim func(context.Context, InterruptClaim) error) (InterruptObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.status(); err != nil {
		return s.interruptObservation(), err
	}
	if err := ctx.Err(); err != nil {
		return s.interruptObservation(), domain.SafeError(err)
	}
	b := s.current
	if request.Validate() != nil || claim == nil || s.interrupt != nil || b == nil || !b.accepted || !b.initialized || b.finished || b.continuing || b.pendingCompaction != nil || s.compaction != nil || !nativeUUID(b.turnID) || b.command != CommandStarted || b.problem != nil || b.runState != RunRunning && b.runState != RunRequiresAction || len(b.tasks) != 0 || len(b.backgroundTasks) != 0 || s.inputs[request] || b.seen[string(request)] || request == s.config.SessionID || request == s.config.Process.OwnerID {
		return s.interruptObservation(), sessionBusy()
	}
	s.interrupt = &InterruptObservation{Claim: InterruptClaim{1, s.config.Process.OwnerID, s.config.SessionID, b.input, b.turnID, request}}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := claim(bounded, s.interrupt.Claim); err != nil {
		problem := s.latch(sessionInterruptPhase, lifecycleUncertain())
		return s.interruptObservation(), problem
	}
	s.interrupt.Claimed, s.interrupt.Attempted = true, true
	copy := s.interrupt.Claim
	b.interrupt = &copy
	if err := s.stream.Interrupt(bounded, request); err != nil {
		problem := s.latch(sessionInterruptPhase, lifecycleUncertain())
		return s.interruptObservation(), problem
	}
	s.interrupt.Acknowledged = true
	if s.config.Process.Logger != nil {
		s.config.Process.Logger.InfoContext(ctx, "claude_interrupt_acknowledged", "owner_id", s.config.Process.OwnerID, "input_id", b.input, "request_id", request)
	}
	return s.interruptObservation(), nil
}

func (s *APISession) InspectInterrupt() (InterruptObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interrupt == nil {
		return InterruptObservation{}, sessionBusy()
	}
	return s.interruptObservation(), nil
}

func (s *APISession) interruptObservation() InterruptObservation {
	if s.interrupt == nil {
		return InterruptObservation{}
	}
	value := *s.interrupt
	if b := s.current; b != nil && b.input == value.Claim.InputID && b.turnID == value.Claim.TurnID {
		value.Idle = b.runState == RunIdle
		if b.terminal != nil {
			// No mutable native usage/content escapes through a cleanup receipt.
			value.NativeResult = &NativeResult{Kind: b.terminal.Kind, Reason: b.terminal.Reason, Error: b.terminal.Error}
			value.ResultCorrelated = true
		} else if b.interruptResult != nil {
			value.NativeResult = &NativeResult{Kind: b.interruptResult.Kind, Reason: b.interruptResult.Reason, Error: b.interruptResult.Error}
		}
	}
	value.CleanupJoined = s.cleanupJoined.Load()
	if s.problem != nil {
		value.ProblemCode = s.problem.Code
	}
	return value
}
