package grok

import (
	"context"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// StopClaim is original notification ownership, not native acknowledgment. ACP
// cancellation has no independent response; the original prompt must terminate.
type StopClaim struct {
	Version          uint32    `json:"version"`
	OwnerID          domain.ID `json:"owner_id"`
	ProductSessionID domain.ID `json:"product_session_id"`
	InputRequestID   domain.ID `json:"input_request_id"`
	RequestID        domain.ID `json:"request_id"`
	NativeSessionID  domain.ID `json:"native_session_id"`
	NativePromptID   string    `json:"native_prompt_id"`
	BodyDigest       string    `json:"body_digest"`
}

func (claim StopClaim) Validate() error {
	digest, err := ClosureClaimDigest(claim.NativeSessionID)
	if err != nil || claim.Version != 1 || claim.OwnerID.Validate() != nil || claim.ProductSessionID.Validate() != nil || claim.InputRequestID.Validate() != nil || claim.RequestID.Validate() != nil || claim.RequestID == claim.InputRequestID || claim.RequestID == claim.ProductSessionID || claim.RequestID == claim.OwnerID || claim.RequestID == claim.NativeSessionID || !nativeUUID(claim.NativePromptID, 4) || claim.BodyDigest != digest {
		return apiConfigurationError()
	}
	return nil
}

type StopObservation struct {
	Claim         StopClaim
	Claimed       bool
	Attempted     bool
	Delivered     bool
	NativeReason  StopReason
	Category      CancellationCategory
	Idle          bool
	CleanupJoined bool
	ProblemCode   domain.Code
}

type textStop struct {
	observation StopObservation
	done        chan struct{}
}

type textControl struct {
	mu          sync.Mutex
	input       domain.ID
	prompt      string
	running     bool
	terminal    bool
	stop        *textStop
	profile     inputProfile
	permissions map[domain.ID]*fileReply
	questions   map[domain.ID]*questionReply
	plans       map[domain.ID]*planReply
	inputDone   <-chan struct{}
	editPolicy  domain.ID
}

func (a *apiConnection) activateText(request domain.ID, profile inputProfile) {
	a.controlMu.Lock()
	a.control = &textControl{input: request, profile: profile}
	a.controlMu.Unlock()
}

func (a *apiConnection) textControl() *textControl {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	return a.control
}

func (c *textControl) accept(prompt string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompt, c.running = prompt, true
}

func (c *textControl) originalStop() *textStop {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stop
}

func (c *textControl) finish() *textStop {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.terminal = true
	return c.stop
}

func (c *textControl) observation() StopObservation {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop == nil {
		return StopObservation{}
	}
	return c.stop.observation
}

// StopText may run independently of a blocked publication callback. It claims
// at most one original native cancellation and returns only pipe-delivery facts.
func (a *apiConnection) StopText(ctx context.Context, request domain.ID, record func(context.Context, StopClaim) error) (result StopObservation, returned error) {
	if request.Validate() != nil || record == nil {
		return result, apiConfigurationError()
	}
	if ctx.Err() != nil {
		return result, domain.SafeError(ctx.Err())
	}
	control := a.textControl()
	if control == nil {
		return result, sessionUncertain()
	}
	control.mu.Lock()
	if control.profile != plainTextInput {
		control.mu.Unlock()
		return result, incompatible()
	}
	if !control.running || control.terminal || control.stop != nil || request == a.creationRequest || request == a.inspection.OwnerID || request == control.input || request == a.product {
		control.mu.Unlock()
		return control.observation(), sessionUncertain()
	}
	digest, err := ClosureClaimDigest(a.session)
	claim := StopClaim{Version: 1, OwnerID: a.inspection.OwnerID, ProductSessionID: a.product, InputRequestID: control.input, RequestID: request, NativeSessionID: a.session, NativePromptID: control.prompt, BodyDigest: digest}
	if err != nil || claim.Validate() != nil {
		control.mu.Unlock()
		return result, apiConfigurationError()
	}
	stop := &textStop{observation: StopObservation{Claim: claim}, done: make(chan struct{})}
	control.stop = stop
	control.mu.Unlock()
	life, cancel := context.WithTimeout(ctx, 10*time.Second)
	watchStop, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-a.wire.Done():
			cancel()
		case <-watchStop:
		}
	}()
	defer func() {
		cancel()
		if returned != nil {
			_ = a.Close()
			control.mu.Lock()
			stop.observation.ProblemCode = domain.RecoveryRequired
			control.mu.Unlock()
			returned = sessionUncertain()
		}
		close(watchStop)
		<-watchDone
		result = control.observation()
		if logger := a.inspection.Logger; logger != nil {
			logger.InfoContext(ctx, "Grok Build original Stop submission settled", "owner_id", a.inspection.OwnerID, "input_id", claim.InputRequestID, "request_id", request, "claimed", result.Claimed, "attempted", result.Attempted, "delivered", result.Delivered, "code", result.ProblemCode)
		}
		close(stop.done)
	}()
	if err := record(life, claim); err != nil {
		return result, sessionUncertain()
	}
	control.mu.Lock()
	stop.observation.Claimed = true
	stop.observation.Attempted = true
	control.mu.Unlock()
	if err := a.wire.Notify(life, "session/cancel", closeParams{Session: a.session}); err != nil {
		return result, sessionUncertain()
	}
	control.mu.Lock()
	stop.observation.Delivered = true
	control.mu.Unlock()
	return result, nil
}

func (a *apiConnection) InspectStop() (StopObservation, error) {
	control := a.textControl()
	if control == nil || control.originalStop() == nil {
		return StopObservation{}, sessionUncertain()
	}
	return control.observation(), nil
}

func (a *apiConnection) settleStop(ctx context.Context, control *textControl, reason StopReason, category CancellationCategory) error {
	stop := control.finish()
	if stop == nil {
		return sessionUncertain()
	}
	select {
	case <-stop.done:
	case <-ctx.Done():
		return sessionUncertain()
	}
	observation := control.observation()
	if !observation.Claimed || !observation.Attempted || !observation.Delivered || observation.ProblemCode != "" || reason != Cancelled && reason != EndTurn || reason == Cancelled && category != MidTurnAbort || reason == EndTurn && category != "" {
		return sessionUncertain()
	}
	control.mu.Lock()
	stop.observation.NativeReason = reason
	stop.observation.Category = category
	stop.observation.Idle = true
	control.mu.Unlock()
	if err := a.Close(); err != nil {
		return sessionUncertain()
	}
	control.mu.Lock()
	stop.observation.CleanupJoined = true
	control.mu.Unlock()
	return nil
}

func (a *apiConnection) waitStopIdle(ctx context.Context, settled *completedText, prompt string) error {
	for !settled.idle {
		event, err := a.wire.Next(ctx)
		if err != nil {
			return sessionUncertain()
		}
		if event.Kind != nativewire.Notification {
			return incompatible()
		}
		switch event.Method {
		case "_x.ai/sessions/changed":
			state, err := parseActivity(event.Params, a.session, a.workspace)
			if err != nil || state != idleActivity {
				return incompatible()
			}
			settled.idle = true
		case "_x.ai/session_notification":
			observation, err := parsePassiveObservation(event.Params, event.Method, a.session, prompt)
			if err != nil || observation.kind != lastTurnMetadata || settled.summarySeen {
				return incompatible()
			}
			settled.summary, settled.summarySeen = observation.text, true
		default:
			return incompatible()
		}
	}
	return nil
}
