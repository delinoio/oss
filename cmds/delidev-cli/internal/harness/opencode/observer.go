package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxObservedBytes = 32 << 20
const maxObservedMessages = 4096
const maxObservedParts = 16384

type NativeSessionStatus string

const (
	NativeStatusUnknown NativeSessionStatus = "unknown"
	NativeStatusBusy    NativeSessionStatus = "busy"
	NativeStatusIdle    NativeSessionStatus = "idle"
	NativeStatusRetry   NativeSessionStatus = "retry"
)

// inputObservation contains fresh copies, never mutable observer state. Session
// notifications and step/message accounting keep their original event owners.
// Recognized registry notifications do not establish effective configuration.
type inputObservation struct {
	EventID            string
	Kind               EventKind
	Message            *NativeMessage `json:"-"`
	Part               *NativePart    `json:"-"`
	Delta              *nativeDelta   `json:"-"`
	Error              *NativeError
	Repeated           bool
	MessageFinalized   bool
	Ancillary          json.RawMessage         `json:"-"`
	Interaction        *NativeInteraction      `json:"-"`
	InteractionReply   *NativeInteractionReply `json:"-"`
	RejectionSources   []string
	AlwaysObservations []string
}

type nativeDelta struct {
	MessageID string
	PartID    string
	Text      string `json:"-"`
}

type inputProgress struct {
	RequestID           domain.ID
	SessionID           string
	MessageID           string
	UserSeen            bool
	InputPartSeen       bool
	AssistantID         string
	Status              NativeSessionStatus
	IdleNotification    bool
	TerminalObserved    bool
	SettledObserved     bool
	NeedsRecovery       bool
	RejectedInteraction bool
	StoppedOnRejection  bool
}

type observedMessage struct {
	raw       []byte
	base      []byte
	value     NativeMessage
	openStep  string
	lastStep  string
	finalized bool
}

type observedPart struct {
	raw   []byte
	value NativePart
	text  string
}

// inputObserver consumes one original uninterrupted event stream in arrival
// order. It does not reconnect, authorize another input, establish persisted
// history, publish product outcomes or prove owned process cleanup.
type inputObserver struct {
	mu              sync.Mutex
	creation        sessionCreation
	input           sessionInput
	cwd, root       string
	logger          *slog.Logger
	owner           domain.ID
	seen            map[string]bool
	bytes           int
	messages        map[string]*observedMessage
	parts           map[string]*observedPart
	attachments     map[string]string
	calls           map[string]string
	progress        inputProgress
	problem         *domain.Error
	ctx             context.Context
	cancel          context.CancelFunc
	interactions    map[string]*observedInteraction
	responseIDs     map[domain.ID]bool
	rejectionPolicy RejectionPolicy
	stop            *inputStopAttempt
}

func observerProblem() *domain.Error {
	return domain.Fail(domain.Unsupported, "OpenCode input events do not match their original owned lifecycle.", "Retain original observations and reconcile native state without sending again or inferring completion.")
}

// The independently inspected native project root is mandatory. A message's
// path must never supply its own comparison authority. Construction after a
// lost HTTP reply is allowed: the original claim, not 204, owns observations.
func (s *sessionAPI) observeInput(ctx context.Context, root string) (*inputObserver, error) {
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.leave()
	if s.runtimeRoot != "" && root != s.runtimeRoot {
		return nil, sessionInvalid()
	}
	if s.creation == nil || s.input == nil || s.events == nil || s.events.ctx == nil || !filepath.IsAbs(root) || domain.Text(root, "native root", 32768, true) != nil {
		return nil, sessionInvalid()
	}
	if s.observer != nil {
		if s.observer.root != root {
			return nil, sessionConflict()
		}
		return s.observer, nil
	}
	creation := *s.creation
	creation.settings.Permission = slices.Clone(creation.settings.Permission)
	input := *s.input
	observerContext, cancel := context.WithCancel(s.events.ctx)
	s.observer = &inputObserver{
		creation: creation, input: input, cwd: s.cwd, root: root, logger: s.logger, owner: s.owner,
		ctx: observerContext, cancel: cancel, interactions: map[string]*observedInteraction{}, responseIDs: map[domain.ID]bool{},
		rejectionPolicy: s.rejectionPolicy,
		seen:            map[string]bool{}, messages: map[string]*observedMessage{}, parts: map[string]*observedPart{}, attachments: map[string]string{}, calls: map[string]string{},
		progress: inputProgress{RequestID: input.receipt.RequestID, SessionID: input.receipt.SessionID, MessageID: input.receipt.MessageID, Status: NativeStatusUnknown},
	}
	return s.observer, nil
}

// canonicalNative retains JSON number spellings, including reported cost. Do
// not marshal typed observations for equality: their private fields are omitted.
func canonicalNative(raw []byte) []byte {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	result, _ := json.Marshal(value)
	return result
}

func messageBase(raw []byte, role MessageRole) []byte {
	fields, _ := object(raw)
	if role == UserMessageRole {
		delete(fields, "summary")
	} else {
		for _, key := range []string{"cost", "tokens", "finish", "error", "structured"} {
			delete(fields, key)
		}
		times, _ := object(fields["time"])
		delete(times, "completed")
		fields["time"], _ = json.Marshal(times)
	}
	result, _ := json.Marshal(fields)
	return canonicalNative(result)
}

func (o *inputObserver) fail(ctx context.Context, stage string, err error) error {
	if o.problem == nil {
		o.problem = domain.SafeError(err)
		o.progress.NeedsRecovery = true
		if o.cancel != nil {
			o.cancel()
		}
		if o.logger != nil {
			o.logger.WarnContext(ctx, "OpenCode input observation needs reconciliation", "owner_id", o.owner, "request_id", o.input.receipt.RequestID, "stage", stage, "code", o.problem.Code)
		}
	}
	return o.problem
}

func (o *inputObserver) snapshot() inputProgress {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.progress
}

// interruption retains earlier terminal/idle observations independently of the
// new recovery requirement. Neither transport EOF nor cancellation is success.
func (o *inputObserver) interruption(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fail(ctx, "transport", sessionUncertain())
}

func (o *inputObserver) observe(ctx context.Context, event NativeEvent) (inputObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.problem != nil {
		return inputObservation{}, o.problem
	}
	if !nativeID(event.ID, "evt") || o.seen[event.ID] || len(event.Properties) > maxHTTPBody {
		return inputObservation{}, o.fail(ctx, "envelope", observerProblem())
	}
	if len(o.seen) >= maxEventIdentities || len(event.Properties) > maxObservedBytes-o.bytes {
		return inputObservation{}, o.fail(ctx, "capacity", eventBound())
	}
	fields, err := object(event.Properties)
	if err != nil {
		return inputObservation{}, o.fail(ctx, "envelope", observerProblem())
	}
	// Copy caller bytes before retaining any state, and preserve arrival order
	// without interpreting the native event ID as a sortable replay sequence.
	event.Properties = canonicalNative(event.Properties)
	result := inputObservation{EventID: event.ID, Kind: event.Kind}
	session := func(required, optional []string) bool {
		_, err := shape(event.Properties, append([]string{"sessionID"}, required...), optional)
		return err == nil && scalar(fields["sessionID"], o.input.receipt.SessionID)
	}
	stage := "notification"
	switch event.Kind {
	case PermissionAskedEvent, QuestionAskedEvent:
		stage = "interaction"
		result.Interaction, err = o.interaction(event)
	case PermissionRepliedEvent, QuestionRepliedEvent, QuestionRejectedEvent:
		stage = "interaction-reply"
		result.InteractionReply, err = o.interactionReply(event)
		if err == nil {
			result.RejectionSources = slices.Clone(o.interactions[result.InteractionReply.RequestID].rejectionSources)
			result.AlwaysObservations = slices.Clone(o.interactions[result.InteractionReply.RequestID].alwaysObservations)
		}
	case MessageUpdatedEvent:
		stage = "message"
		if !session([]string{"info"}, nil) {
			err = observerProblem()
		} else {
			result.Message, result.Repeated, err = o.message(fields["info"])
			if err == nil {
				result.MessageFinalized = o.messages[result.Message.ID].finalized
			}
		}
	case MessagePartUpdatedEvent:
		stage = "part"
		if _, valid := nativeCount(fields["time"]); !session([]string{"part", "time"}, nil) || !valid {
			err = observerProblem()
		} else {
			result.Part, result.Repeated, err = o.part(fields["part"])
		}
	case MessagePartDeltaEvent:
		stage = "delta"
		if !session([]string{"messageID", "partID", "field", "delta"}, nil) {
			err = observerProblem()
		} else {
			result.Delta, err = o.delta(fields)
		}
	case SessionUpdatedEvent:
		if !session([]string{"info"}, nil) {
			err = observerProblem()
		} else if identity, problem := validateSession(fields["info"], o.cwd, &o.creation, false); problem != nil || identity != o.creation.identity {
			err = observerProblem()
		} else {
			result.Ancillary = slices.Clone(event.Properties)
		}
	case SessionDiffEvent:
		if !session([]string{"diff"}, nil) || !validateDiffs(fields["diff"]) {
			err = observerProblem()
		} else {
			result.Ancillary = slices.Clone(event.Properties)
		}
	case SessionStatusEvent:
		if !session([]string{"status"}, nil) {
			err = observerProblem()
		} else {
			err = o.status(fields["status"])
			result.Ancillary = slices.Clone(event.Properties)
		}
	case SessionIdleEvent:
		if !session(nil, nil) || o.progress.Status != NativeStatusIdle {
			err = observerProblem()
		} else {
			o.progress.IdleNotification = true
		}
	case SessionErrorEvent:
		// Native sessionID/error are optional in the schema. An uncorrelated
		// error cannot be assigned to this original claimed input.
		if !session([]string{"error"}, nil) {
			err = observerProblem()
		} else {
			result.Error, err = decodeNativeError(fields["error"])
		}
	case ServerHeartbeatEvent, ModelsDevRefreshedEvent, CatalogUpdatedEvent, ReferenceUpdatedEvent, IntegrationUpdatedEvent:
		_, err = shape(event.Properties, nil, nil)
	case PluginAddedEvent, IntegrationConnectionUpdatedEvent:
		key := "id"
		if event.Kind == IntegrationConnectionUpdatedEvent {
			key = "integrationID"
		}
		_, err = shape(event.Properties, []string{key}, nil)
		if _, valid := boundedString(fields[key], 1024, true); !valid {
			err = observerProblem()
		}
		result.Ancillary = slices.Clone(event.Properties)
	default:
		// Child, permission, question, removal, compaction and session-next
		// families require their own adapters. Recognition is not authority.
		err = observerProblem()
	}
	if err != nil {
		return inputObservation{}, o.fail(ctx, stage, err)
	}
	o.bytes += len(event.Properties)
	o.seen[event.ID] = true
	o.refresh()
	return result, nil
}

func (o *inputObserver) status(raw []byte) error {
	fields, err := object(raw)
	if err != nil {
		return observerProblem()
	}
	value, valid := boundedString(fields["type"], 16, true)
	status := NativeSessionStatus(value)
	if !valid || status != NativeStatusIdle && status != NativeStatusBusy && status != NativeStatusRetry {
		return observerProblem()
	}
	if status == NativeStatusRetry {
		// A retry may change the native step/error lifecycle. Keep it outside
		// this first uninterrupted-input profile until independently bound.
		return observerProblem()
	}
	if _, err := shape(raw, []string{"type"}, nil); err != nil {
		return observerProblem()
	}
	if o.progress.SettledObserved && status != NativeStatusIdle {
		return observerProblem()
	}
	o.progress.Status = status
	if status != NativeStatusIdle {
		o.progress.IdleNotification = false
	}
	return nil
}

func (o *inputObserver) refresh() {
	message := o.messages[o.progress.AssistantID]
	terminal := false
	if message != nil {
		a := message.value.Assistant
		terminal = message.finalized && a.Completed != nil && (a.Error != nil || a.Finish != nil && !o.needsSuccessor(message.value.ID))
	}
	o.progress.TerminalObserved = o.progress.UserSeen && o.progress.InputPartSeen && terminal
	for _, interaction := range o.interactions {
		if o.rejectedTool(interaction) {
			o.progress.RejectedInteraction = true
		}
	}
	o.progress.StoppedOnRejection = message != nil && o.rejectionStopsMessage(message.value.ID)
	o.progress.SettledObserved = o.progress.TerminalObserved && o.progress.Status == NativeStatusIdle && o.progress.IdleNotification
}

func (o *inputObserver) message(raw []byte) (*NativeMessage, bool, error) {
	value, err := decodeNativeMessage(raw)
	if err != nil || value.SessionID != o.input.receipt.SessionID {
		return nil, false, observerProblem()
	}
	raw = canonicalNative(raw)
	base := messageBase(raw, value.Role)
	old := o.messages[value.ID]
	existing := old != nil
	if old != nil && !bytes.Equal(base, old.base) {
		return nil, false, observerProblem()
	}
	if old == nil && len(o.messages) >= maxObservedMessages {
		return nil, false, eventBound()
	}
	if value.User != nil {
		u := value.User
		settings := o.creation.settings
		if value.ID != o.input.receipt.MessageID || u.Agent != string(settings.Agent) || u.Model != settings.Model || u.Provider != settings.Provider || u.Variant != nil || u.System != nil || u.Tools != nil || u.Format != nil {
			return nil, false, observerProblem()
		}
	} else {
		a := value.Assistant
		settings := o.creation.settings
		if !o.progress.UserSeen || !o.progress.InputPartSeen || a.ParentID != o.input.receipt.MessageID || a.Agent != string(settings.Agent) || a.Mode != string(settings.Agent) || a.Provider != settings.Provider || a.Model != settings.Model || a.Cwd != o.cwd || a.Root != o.root || a.Variant != nil || a.Summary != nil || a.Structured != nil {
			return nil, false, observerProblem()
		}
		if old == nil {
			if o.progress.Status != NativeStatusBusy {
				return nil, false, observerProblem()
			}
			if prior := o.messages[o.progress.AssistantID]; prior != nil {
				if !prior.finalized || !o.needsSuccessor(prior.value.ID) {
					return nil, false, observerProblem()
				}
			}
			// Actual fast native runs can deliver final metadata in their
			// first message event before its parts arrive. Preserve that
			// snapshot provisionally; only a subsequent completed update
			// after owned part validation establishes the message boundary.
		} else {
			if old.value.Assistant.Completed != nil && !bytes.Equal(raw, old.raw) && !contentFilterRefinement(old.raw, raw) || value.ID != o.progress.AssistantID && !bytes.Equal(raw, old.raw) {
				return nil, false, observerProblem()
			}
			if a.Completed != nil && !o.messageClosed(value, old) {
				return nil, false, observerProblem()
			}
			if a.Finish != nil {
				step := o.parts[old.lastStep]
				if a.Error == nil && (step == nil || *step.value.Step.Reason != *a.Finish || !sameUsage(a.Usage, *step.value.Step.Usage)) {
					return nil, false, observerProblem()
				}
			}
			if old.value.Assistant.Finish != nil && a.Finish == nil {
				return nil, false, observerProblem()
			}
			if old.value.Assistant.Error != nil && !bytes.Equal(errorBytes(raw), errorBytes(old.raw)) {
				return nil, false, observerProblem()
			}
		}
	}
	repeated := old != nil && bytes.Equal(raw, old.raw)
	if old == nil {
		old = &observedMessage{base: base}
		o.messages[value.ID] = old
		if value.Assistant != nil {
			o.progress.AssistantID = value.ID
		}
	}
	old.raw, old.value = raw, value
	if existing && value.Assistant != nil && value.Assistant.Completed != nil {
		old.finalized = true
	}
	if value.User != nil {
		o.progress.UserSeen = true
	}
	copy, _ := decodeNativeMessage(raw)
	return &copy, repeated, nil
}

func errorBytes(raw []byte) []byte {
	fields, _ := object(raw)
	return canonicalNative(fields["error"])
}

// The pinned outer prompt loop adds ContentFilterError after processor cleanup
// has completed the same content-filter message. Only this exact monotonic
// refinement is accepted; it cannot change identity, finish, usage or content.
func contentFilterRefinement(before, after []byte) bool {
	old, _ := object(before)
	current, _ := object(after)
	if old["error"] != nil || !scalar(old["finish"], string(FinishContentFilter)) {
		return false
	}
	problem, err := decodeNativeError(current["error"])
	if err != nil || problem.Kind != ContentErrorKind {
		return false
	}
	delete(current, "error")
	remaining, _ := json.Marshal(current)
	return bytes.Equal(canonicalNative(remaining), before)
}

func (o *inputObserver) messageClosed(value NativeMessage, state *observedMessage) bool {
	for _, interaction := range o.interactions {
		if interaction.value.Tool.MessageID == value.ID && !interaction.closed && !o.interactionInterrupted(interaction) {
			return false
		}
	}
	if state.openStep != "" && value.Assistant.Error == nil {
		return false
	}
	for _, part := range o.parts {
		if part.value.MessageID != value.ID {
			continue
		}
		if part.value.Tool != nil && part.value.Tool.State != ToolCompleted && part.value.Tool.State != ToolError || part.value.Text != nil && (part.value.Text.Timing == nil || part.value.Text.Timing.End == nil) {
			return false
		}
	}
	return true
}

func (o *inputObserver) needsSuccessor(id string) bool {
	a := o.messages[id].value.Assistant
	if a.Error != nil || a.Finish == nil {
		return false
	}
	if o.rejectionStopsMessage(id) {
		return false
	}
	if *a.Finish == FinishToolCalls || *a.Finish == FinishUnknown {
		return true
	}
	// Native providers may say stop while returning local tools. The pinned
	// native loop continues to return their results to the model. Keep that
	// original successor requirement independently of the finish spelling.
	for _, part := range o.parts {
		if part.value.MessageID != id || part.value.Tool == nil {
			continue
		}
		tool := part.value.Tool
		metadata, _ := object(tool.PartMetadata)
		stateMetadata, _ := object(tool.Metadata)
		if string(metadata["providerExecuted"]) != "true" && !(tool.State == ToolError && string(stateMetadata["interrupted"]) == "true") {
			return true
		}
	}
	return false
}
