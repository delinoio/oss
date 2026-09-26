package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// StopReceipt separates native observations from original process cleanup.
// Neither grants continuation. An accepted abort can race normal completion;
// InterruptedObserved must never be inferred from HTTP success or idle.
type StopReceipt struct {
	RequestID           domain.ID
	InputRequestID      domain.ID
	SessionID           string
	MessageID           string
	HTTPAccepted        bool
	TerminalObserved    bool
	InterruptedObserved bool
	IdleObserved        bool
	IdleVerified        bool
	PendingCleared      bool
	RepliesUncertain    bool
	CleanupVerified     bool
}

type inputStopAttempt struct {
	receipt          StopReceipt
	claim            SessionClaim
	sent             bool
	cleanupAttempted bool
}

func interruptedTool(tool *NativeToolPart) bool {
	if tool == nil || tool.State != ToolError {
		return false
	}
	metadata, err := object(tool.Metadata)
	return err == nil && string(metadata["interrupted"]) == "true"
}

func (o *inputObserver) interactionInterrupted(value *observedInteraction) bool {
	if o.stop == nil || !o.stop.sent {
		return false
	}
	part := o.parts[o.calls[value.value.Tool.CallID]]
	return part != nil && interruptedTool(part.value.Tool)
}

func (o *inputObserver) prepareStop(request domain.ID) (*inputStopAttempt, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.problem != nil {
		return nil, o.problem
	}
	if o.stop != nil {
		return nil, sessionConflict()
	}
	if request.Validate() != nil || request == o.creation.request || request == o.input.receipt.RequestID || o.responseIDs[request] {
		return nil, sessionInvalid()
	}
	// This live profile requires the original input to have reached its native
	// assistant loop. Aborting a merely scheduled prompt can race runner setup;
	// idle alone cannot establish cancellation of that different lifecycle.
	if !o.progress.UserSeen || !o.progress.InputPartSeen || o.progress.AssistantID == "" || o.progress.Status != NativeStatusBusy || o.progress.TerminalObserved {
		return nil, sessionConflict()
	}
	r := o.input.receipt
	attempt := &inputStopAttempt{
		receipt: StopReceipt{RequestID: request, InputRequestID: r.RequestID, SessionID: r.SessionID, MessageID: r.MessageID},
		claim:   SessionClaim{RequestID: request, Kind: StopInputMutation, SessionID: r.SessionID, MessageID: r.MessageID, PartID: r.PartID, InputRequestID: r.RequestID, BodyDigest: mutationDigest(nil)},
	}
	o.stop = attempt
	o.responseIDs[request] = true
	return attempt, nil
}

func (o *inputObserver) stopReceipt() (StopReceipt, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stopReceiptLocked()
}

func (o *inputObserver) stopReceiptLocked() (StopReceipt, error) {
	if o.stop == nil {
		return StopReceipt{}, sessionInvalid()
	}
	result := o.stop.receipt
	result.TerminalObserved = o.progress.TerminalObserved
	result.IdleObserved = o.progress.Status == NativeStatusIdle && o.progress.IdleNotification
	message := o.messages[o.progress.AssistantID]
	result.InterruptedObserved = result.TerminalObserved && message != nil && message.value.Assistant.Error != nil && message.value.Assistant.Error.Kind == AbortedErrorKind
	for _, value := range o.interactions {
		if value.attempt != nil && value.attempt.sent && !value.attempt.receipt.NativeAccepted {
			result.RepliesUncertain = true
		}
	}
	return result, nil
}

func (s *sessionAPI) stopInput(ctx context.Context, observer *inputObserver, request domain.ID) (StopReceipt, error) {
	if err := s.enter(ctx); err != nil {
		return StopReceipt{}, err
	}
	defer s.leave()
	if observer == nil || observer != s.observer || s.claim == nil || s.events == nil || s.input == nil {
		return StopReceipt{}, sessionInvalid()
	}
	if s.problem != nil {
		return StopReceipt{}, s.problem
	}
	if problem := s.events.status(); problem != nil {
		return StopReceipt{}, problem
	}
	if _, err := s.readSession(ctx); err != nil {
		return StopReceipt{}, err
	}
	attempt, err := observer.prepareStop(request)
	if err != nil {
		return StopReceipt{}, err
	}
	current := func() StopReceipt { value, _ := observer.stopReceipt(); return value }
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(observer.ctx, cancel)
	defer stop()
	if err := s.claim(bounded, attempt.claim); err != nil {
		s.diagnostic(ctx, StopInputMutation, err)
		return current(), sessionUncertain()
	}
	observer.mu.Lock()
	if observer.problem == nil && bounded.Err() == nil {
		attempt.sent = true
	}
	sent := attempt.sent
	observer.mu.Unlock()
	if !sent {
		return current(), sessionUncertain()
	}
	s.abortAttempt = true
	defer func() { s.abortAttempt = false }()
	raw, _, err := s.request(bounded, http.MethodPost, "/session/"+attempt.receipt.SessionID+"/abort", nil, http.StatusOK)
	if err != nil || string(bytes.TrimSpace(raw)) != "true" {
		if err == nil {
			err = sessionProblem()
		}
		s.diagnostic(ctx, StopInputMutation, err)
		return current(), sessionUncertain()
	}
	observer.mu.Lock()
	attempt.receipt.HTTPAccepted = true
	observer.mu.Unlock()
	return current(), nil
}

// verifyStop joins independent live terminal/idle observations with fresh
// original-session, native status and both pending inventories. It does not
// repeat abort, reconnect events, resolve an unacknowledged answer or establish
// operating-system descendant cleanup. Missing inventory entries alone never
// classify permission rejection or successful answer acceptance.
func (s *sessionAPI) verifyStop(ctx context.Context, observer *inputObserver) (StopReceipt, error) {
	if err := s.enter(ctx); err != nil {
		return StopReceipt{}, err
	}
	defer s.leave()
	if observer == nil || observer != s.observer || s.events == nil {
		return StopReceipt{}, sessionInvalid()
	}
	current := func() StopReceipt { result, _ := observer.stopReceipt(); return result }
	if s.problem != nil {
		return current(), s.problem
	}
	uncertain := func(phase string) (StopReceipt, error) {
		s.diagnostic(ctx, StopInputMutation, sessionUncertain(), "phase", phase)
		return current(), sessionUncertain()
	}
	ready := func() bool {
		observer.mu.Lock()
		defer observer.mu.Unlock()
		value, err := observer.stopReceiptLocked()
		return err == nil && observer.problem == nil && observer.stop.sent && value.TerminalObserved && value.IdleObserved && (value.HTTPAccepted || value.InterruptedObserved)
	}
	if !ready() {
		return uncertain("terminal-before-inspection")
	}
	if problem := s.events.status(); problem != nil {
		return current(), problem
	}
	if _, err := s.readSession(ctx); err != nil {
		return current(), err
	}
	stored, err := s.readStoredInput(ctx)
	if err != nil {
		if domain.SafeError(err).Code == domain.Unsupported {
			s.problem = sessionProblem()
		}
		return current(), err
	}
	if !stored.Recorded {
		return uncertain("original-storage")
	}
	for _, inventory := range []struct {
		path string
		kind EventKind
	}{{"/permission", PermissionAskedEvent}, {"/question", QuestionAskedEvent}} {
		raw, _, err := s.request(ctx, http.MethodGet, inventory.path, nil, http.StatusOK)
		if err != nil {
			return current(), err
		}
		var entries []json.RawMessage
		if domain.Decode(raw, &entries) != nil || entries == nil || len(entries) > maxObservedInteractions {
			return current(), observer.pendingFailure(ctx)
		}
		seen := map[string]bool{}
		pending := false
		for _, raw := range entries {
			value, err := decodeNativeInteraction(inventory.kind, raw)
			if err != nil || seen[value.ID] {
				return current(), observer.pendingFailure(ctx)
			}
			seen[value.ID] = true
			if value.SessionID == s.input.receipt.SessionID {
				pending = true
			}
		}
		if pending {
			phase := "permission-pending"
			if inventory.kind == QuestionAskedEvent {
				phase = "question-pending"
			}
			return uncertain(phase)
		}
	}
	raw, _, err := s.request(ctx, http.MethodGet, "/session/status", nil, http.StatusOK)
	if err != nil {
		return current(), err
	}
	status, err := object(raw)
	// This profile owns one native session/instance. Native idle removes its
	// status-map entry; other active sessions cannot be adopted as our runtime.
	if err != nil || len(status) != 0 {
		return uncertain("native-status")
	}
	if !ready() {
		return uncertain("terminal-after-inspection")
	}
	if problem := s.events.status(); problem != nil {
		return current(), problem
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.problem != nil {
		return observer.stop.receipt, observer.problem
	}
	for _, value := range observer.interactions {
		if !value.closed && !observer.interactionInterrupted(value) {
			return observer.stop.receipt, sessionUncertain()
		}
	}
	for _, value := range observer.interactions {
		if !value.closed {
			value.closed, value.canceled = true, true
		}
	}
	observer.stop.receipt.IdleVerified, observer.stop.receipt.PendingCleared = true, true
	result, _ := observer.stopReceiptLocked()
	if result.RepliesUncertain {
		return result, sessionUncertain()
	}
	return result, nil
}

// closeStoppedRuntime invokes only the original owner's joined process cleanup.
// The pinned native tool effects can stay in permission/question inventories
// after /abort; never dismiss or answer those requests to simulate cancellation.
// Even when native pending cleanup improves, OS descendant cleanup remains an
// independent prerequisite for releasing the private runtime/workspace.
func (s *sessionAPI) closeStoppedRuntime(ctx context.Context, observer *inputObserver) (StopReceipt, error) {
	if err := s.enter(ctx); err != nil {
		return StopReceipt{}, err
	}
	defer s.leave()
	if observer == nil || observer != s.observer || s.closeOwned == nil || s.owner.Validate() != nil || s.events == nil {
		return StopReceipt{}, sessionInvalid()
	}
	observer.mu.Lock()
	if observer.stop == nil {
		observer.mu.Unlock()
		return StopReceipt{}, sessionInvalid()
	}
	if observer.stop.cleanupAttempted {
		result, _ := observer.stopReceiptLocked()
		observer.mu.Unlock()
		if result.CleanupVerified && !result.RepliesUncertain {
			return result, nil
		}
		return result, sessionUncertain()
	}
	observer.stop.cleanupAttempted = true
	observer.mu.Unlock()
	if err := s.closeOwned(ctx); err != nil {
		s.diagnostic(ctx, StopInputMutation, err, "phase", "owned-cleanup")
		result, _ := observer.stopReceipt()
		return result, sessionUncertain()
	}
	s.events.Close()
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.stop.receipt.CleanupVerified = true
	observer.stop.receipt.PendingCleared = true
	for _, value := range observer.interactions {
		if !value.closed {
			value.closed, value.canceled = true, true
		}
	}
	result, _ := observer.stopReceiptLocked()
	if result.RepliesUncertain {
		return result, sessionUncertain()
	}
	return result, nil
}
