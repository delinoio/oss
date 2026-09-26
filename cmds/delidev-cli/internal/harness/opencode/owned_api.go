package opencode

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// APIExecutionConfig is an internal Worker integration boundary, not a public
// product API. Its Claim must durably bind every mutation to the accepted
// assignment; Token must already have its separately registered relay scope.
type APIExecutionConfig = apiSessionConfig

// Observation and Progress retain private typed native facts. They are not
// normalized publication events, terminal reports or continuation authority.
type Observation = inputObservation
type Progress = inputProgress

// OwnedAPI has no endpoint-adoption or reconstruction constructor. Only the
// original verified process can supply its mutation, observation and cleanup
// authority. Next serializes consumption through observation so concurrent
// callers cannot reorder the original stream after dequeueing its events.
type OwnedAPI struct {
	session *sessionAPI
	reading chan struct{}
}

func OpenOwnedAPI(ctx context.Context, config APIExecutionConfig) (*OwnedAPI, error) {
	session, err := openAPISession(ctx, config)
	if err != nil {
		return nil, err
	}
	return &OwnedAPI{session: session, reading: make(chan struct{}, 1)}, nil
}

func (a *OwnedAPI) valid() bool {
	return a != nil && a.session != nil && a.reading != nil
}

func (a *OwnedAPI) InitialSettings(ctx context.Context) (domain.ObservedExecutionSettings, error) {
	if !a.valid() {
		return domain.ObservedExecutionSettings{}, sessionInvalid()
	}
	return a.session.initialObservedSettings(ctx)
}

// CreateSession uses the initializer's immutable selection; a caller cannot
// replace native settings after verification or fabricate the native identity.
func (a *OwnedAPI) CreateSession(ctx context.Context, request domain.ID) (string, error) {
	if !a.valid() || a.session.apiProfile == nil {
		return "", sessionInvalid()
	}
	return a.session.create(ctx, request, a.session.apiProfile.Settings)
}

// InspectSession reconciles only this original live creation attempt. Native
// identity can be discovered after response loss without repeating creation;
// missing evidence never authorizes replacement or reconstructs an HTTP ack.
func (a *OwnedAPI) InspectSession(ctx context.Context) (SessionReceipt, error) {
	if !a.valid() {
		return SessionReceipt{}, sessionInvalid()
	}
	return a.session.reconcileCreation(ctx)
}

// StartText allocates original IDs and validates the complete body, then
// subscribes before once-claiming native input. The context owns that subscription's
// lifetime. A lost HTTP reply still binds an observer to the original claim;
// the returned scheduling error is retained without replay or replacement.
func (a *OwnedAPI) StartText(ctx context.Context, request domain.ID, text string) (InputReceipt, error) {
	if !a.valid() {
		return InputReceipt{}, sessionInvalid()
	}
	if request.Validate() != nil || domain.Text(text, "input", 256<<10, true) != nil {
		return InputReceipt{}, sessionInvalid()
	}
	if err := a.session.enter(ctx); err != nil {
		return InputReceipt{}, err
	}
	creation := a.session.creation
	validRequest := creation != nil && request != creation.request
	a.session.leave()
	if !validRequest {
		return InputReceipt{}, sessionInvalid()
	}
	message, part, err := freshInputIDPair()
	if err != nil {
		return InputReceipt{}, err
	}
	// JSON escaping can exceed the transport bound even for bounded UTF-8
	// text. Preflight the complete original body before claiming a listener.
	if _, err := encodeTextInput(creation.settings, message, part, text); err != nil {
		return InputReceipt{}, err
	}
	if _, err := a.session.openEvents(ctx); err != nil {
		return InputReceipt{}, err
	}
	receipt, submission := a.session.submit(ctx, request, message, part, text)
	if receipt.MessageID == "" {
		return receipt, submission
	}
	_, observed := a.session.observeInput(ctx, a.session.runtimeRoot)
	if submission != nil {
		return receipt, submission
	}
	return receipt, observed
}

func (a *OwnedAPI) observer(ctx context.Context) (*inputObserver, *eventStream, error) {
	if !a.valid() {
		return nil, nil, sessionInvalid()
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return nil, nil, err
	}
	observer, events, root := s.observer, s.events, s.runtimeRoot
	hasInput := s.input != nil
	s.leave()
	if events == nil || !hasInput {
		return nil, nil, sessionInvalid()
	}
	// A canceled scheduling call may have retained its original claim before
	// observer construction. A separate caller context can bind that same
	// in-memory claim for inspection/cleanup, without HTTP or replay authority.
	if observer == nil {
		var err error
		observer, err = s.observeInput(ctx, root)
		if err != nil {
			return nil, nil, err
		}
	}
	return observer, events, nil
}

func (a *OwnedAPI) Next(ctx context.Context) (Observation, error) {
	if !a.valid() {
		return Observation{}, sessionInvalid()
	}
	select {
	case a.reading <- struct{}{}:
		defer func() { <-a.reading }()
	case <-ctx.Done():
		return Observation{}, unavailable()
	}
	observer, stream, err := a.observer(ctx)
	if err != nil {
		return Observation{}, err
	}
	event, err := stream.Next(ctx)
	if err != nil {
		_ = observer.interruption(ctx)
		return Observation{}, err
	}
	return observer.observe(ctx, event)
}

func (a *OwnedAPI) Progress(ctx context.Context) (Progress, error) {
	observer, _, err := a.observer(ctx)
	if err != nil {
		return Progress{}, err
	}
	return observer.snapshot(), nil
}

func (a *OwnedAPI) InspectInput(ctx context.Context) (InputReceipt, error) {
	if !a.valid() {
		return InputReceipt{}, sessionInvalid()
	}
	return a.session.inspectInput(ctx)
}

func (a *OwnedAPI) Reply(ctx context.Context, request domain.ID, interaction string, response InteractionResponse) (InteractionReceipt, error) {
	observer, _, err := a.observer(ctx)
	if err != nil {
		return InteractionReceipt{}, err
	}
	return a.session.replyInteraction(ctx, observer, request, interaction, response)
}

func (a *OwnedAPI) InteractionReceipt(ctx context.Context, interaction string) (InteractionReceipt, error) {
	observer, _, err := a.observer(ctx)
	if err != nil {
		return InteractionReceipt{}, err
	}
	return observer.interactionReceipt(interaction)
}

// Native interruption and owned-process cleanup intent stay separate explicit
// actions. Failure of one does not silently claim or send the other.
func (a *OwnedAPI) Interrupt(ctx context.Context, request domain.ID) (StopReceipt, error) {
	observer, _, err := a.observer(ctx)
	if err != nil {
		return StopReceipt{}, err
	}
	return a.session.stopInput(ctx, observer, request)
}

func (a *OwnedAPI) ClaimOwnedStop(ctx context.Context, request domain.ID) (StopReceipt, error) {
	observer, _, err := a.observer(ctx)
	if err != nil {
		return StopReceipt{}, err
	}
	return a.session.claimOwnedStop(ctx, observer, request)
}

func (a *OwnedAPI) VerifyStop(ctx context.Context) (StopReceipt, error) {
	observer, _, err := a.observer(ctx)
	if err != nil {
		return StopReceipt{}, err
	}
	return a.session.verifyStop(ctx, observer)
}

func (a *OwnedAPI) FinishStopCleanup(ctx context.Context) (StopReceipt, error) {
	observer, _, err := a.observer(ctx)
	if err != nil {
		return StopReceipt{}, err
	}
	return a.session.closeStoppedRuntime(ctx, observer)
}

func (a *OwnedAPI) RecoverStopCleanup(ctx context.Context, request domain.ID) (StopReceipt, error) {
	observer, _, err := a.observer(ctx)
	if err != nil {
		return StopReceipt{}, err
	}
	return a.session.recoverStoppedRuntime(ctx, observer, request)
}

// Close joins original owner cleanup and the event reader. It cannot establish
// successful input completion, interaction acceptance or a native Stop receipt.
// A claimed Stop uses FinishStopCleanup/RecoverStopCleanup for its own proof.
func (a *OwnedAPI) Close(ctx context.Context) error {
	if !a.valid() {
		return sessionInvalid()
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	err := s.closeOwned(ctx)
	if s.events != nil {
		s.events.Close()
	}
	return err
}
