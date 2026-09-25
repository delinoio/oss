package claude

import (
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/google/uuid"
)

type LifecycleKind string

const (
	CommandObserved         LifecycleKind = "command-observed"
	SessionInitialized      LifecycleKind = "session-initialized"
	InputAccepted           LifecycleKind = "input-accepted"
	InputFinished           LifecycleKind = "input-finished"
	UncorrelatedTermination LifecycleKind = "uncorrelated-termination"
	ContentObserved         LifecycleKind = "content-observed"
	InteractionObserved     LifecycleKind = "interaction-observed"
	TaskObserved            LifecycleKind = "task-observed"
	ProgressObserved        LifecycleKind = "progress-observed"
	RunStateObserved        LifecycleKind = "run-state-observed"
	ContinuationInitialized LifecycleKind = "continuation-initialized"
	ContinuationFinished    LifecycleKind = "continuation-finished"
	PrivateObservation      LifecycleKind = "private-observation"
)

type CommandState string

type lifecyclePhase string

const (
	envelopeValidation    lifecyclePhase = "envelope"
	commandValidation     lifecyclePhase = "command"
	initValidation        lifecyclePhase = "initialize"
	replayValidation      lifecyclePhase = "replay"
	contentValidation     lifecyclePhase = "content"
	resultValidation      lifecyclePhase = "result"
	interactionValidation lifecyclePhase = "interaction"
	taskValidation        lifecyclePhase = "task"
	progressValidation    lifecyclePhase = "progress"
	runValidation         lifecyclePhase = "run"
)

const (
	CommandQueued    CommandState = "queued"
	CommandStarted   CommandState = "started"
	CommandCompleted CommandState = "completed"
	CommandCancelled CommandState = "cancelled"
	CommandDiscarded CommandState = "discarded"
)

// LifecycleObservation retains native acceptance independently of completion.
// Content, Result and Interaction contain validated native observations. Native
// additionally retains private extensions; none of these facts grants
// public publication authority or permission to discard unhandled families.
type LifecycleObservation struct {
	Kind        LifecycleKind
	SessionID   domain.ID
	InputID     domain.ID
	NativeID    string
	Command     CommandState
	Accepted    bool
	Result      *NativeResult
	Interaction *InteractionObservation
	Task        *NativeTaskObservation     `json:"-"`
	Progress    *NativeProgressObservation `json:"-"`
	Run         *NativeRunObservation      `json:"-"`
	Compaction  *NativeCompaction          `json:"-"`
	Summary     *NativeCompactionSummary   `json:"-"`
	// TurnID is the original native init envelope identity. An automatic
	// continuation has its own turn and never acknowledges a product input.
	TurnID  string
	Content []ContentEvent
	Native  *StreamEvent `json:"-"`
}

// ExecutionBinding validates one immutable input attempt. It sends nothing,
// grants no public capability and cannot authorize a new input or resume. The
// Worker must retain its durable claim before constructing this binding and
// must still process every private observation through its dedicated adapter.
type ExecutionBinding struct {
	mu                 sync.Mutex
	session            domain.ID
	input              domain.ID
	digest             [sha256.Size]byte
	model              string
	workspace          string
	home               string
	permission         NativePermission
	command            CommandState
	initialized        bool
	accepted           bool
	finished           bool
	terminal           *NativeResult
	seen               map[string]bool
	problem            *domain.Error
	logger             *slog.Logger
	owner              domain.ID
	content            contentState
	advertisedTools    map[string]bool
	interactions       map[domain.ID]*interactionState
	interactionBytes   int
	tasks              map[string]nativeTaskState
	backgroundTasks    map[string]bool
	runState           NativeRunState
	turnID             string
	continuing         bool
	continuationSeen   bool
	continuationFailed bool
	notifications      uint32
	pendingCompaction  *compactionSummaryBinding
}

func BindExecution(config APIStreamConfig, input domain.ID, text string) (*ExecutionBinding, error) {
	if config.Version != SupportedVersion || config.SessionID.Validate() != nil || input.Validate() != nil || domain.Text(text, "native input", 256<<10, true) != nil || domain.Text(config.Model, "native model", 256, true) != nil ||
		!filepath.IsAbs(config.Workspace) || filepath.Clean(config.Workspace) != config.Workspace || !filepath.IsAbs(config.Home) || filepath.Clean(config.Home) != config.Home || !validNativePermission(config.Permission) {
		return nil, apiConfigurationError()
	}
	return &ExecutionBinding{session: config.SessionID, input: input, digest: sha256.Sum256([]byte(text)), model: config.Model, workspace: config.Workspace, home: config.Home, permission: config.Permission, seen: map[string]bool{}, logger: config.Process.Logger, owner: config.Process.OwnerID}, nil
}

func validNativePermission(permission NativePermission) bool {
	switch permission {
	case DefaultPermission, PlanPermission, AcceptEditsPermission, DontAskPermission, BypassPermission:
		return true
	default:
		return false
	}
}

func nativeUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value && (id.Version() == 4 || id.Version() == 7) && id.Variant() == uuid.RFC4122
}

func lifecycleUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Claude Code input lifecycle no longer matches its original execution.", "Retain the original native session and inspect acceptance before any retry or new input.")
}

// Observe must receive the original ordered stream, including messages that
// need another adapter. A protocol contradiction permanently blocks this
// binding; a later apparently successful result cannot erase uncertainty.
func (b *ExecutionBinding) Observe(event StreamEvent) (observation LifecycleObservation, returned error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.problem != nil {
		return LifecycleObservation{}, b.problem
	}
	phase := envelopeValidation
	defer func() {
		if returned != nil {
			b.problem = lifecycleUncertain()
			returned = b.problem
			if b.logger != nil {
				b.logger.Warn("Claude Code execution lifecycle validation failed", "owner_id", b.owner, "phase", phase, "code", b.problem.Code)
			}
		}
	}()
	observation = LifecycleObservation{Kind: PrivateObservation, SessionID: b.session, InputID: b.input, Accepted: b.accepted, Native: &event}
	observation.TurnID = b.turnID
	if b.continuing {
		observation.InputID, observation.Accepted = "", false
	}
	if event.Kind != NativeMessage {
		switch event.Kind {
		case NativeRequest, NativeCancellation, NativeReplyEcho:
			phase = interactionValidation
			value, err := b.observeInteraction(event)
			if err != nil {
				return LifecycleObservation{}, err
			}
			observation.Kind, observation.Interaction = InteractionObserved, value
			observation.InputID, observation.TurnID = value.InputID, value.TurnID
			observation.Accepted = value.InputID != ""
			if b.logger != nil {
				b.logger.Debug("Claude Code interaction observed", "owner_id", b.owner, "arrival_id", value.ArrivalID, "state", value.Kind, "canceled", value.Canceled)
			}
			return observation, nil
		case NativeLateResponse:
			return observation, nil
		default:
			return LifecycleObservation{}, lifecycleUncertain()
		}
	}
	var fields map[string]json.RawMessage
	if domain.Decode(event.Body, &fields) != nil {
		return LifecycleObservation{}, lifecycleUncertain()
	}
	var header struct {
		Type    string    `json:"type"`
		Subtype string    `json:"subtype"`
		Session domain.ID `json:"session_id"`
		UUID    string    `json:"uuid"`
		Replay  bool      `json:"isReplay"`
		Parent  *string   `json:"parent_tool_use_id"`
	}
	for name, target := range map[string]any{"type": &header.Type, "subtype": &header.Subtype, "session_id": &header.Session, "uuid": &header.UUID, "isReplay": &header.Replay, "parent_tool_use_id": &header.Parent} {
		if raw := fields[name]; len(raw) != 0 && json.Unmarshal(raw, target) != nil {
			return LifecycleObservation{}, lifecycleUncertain()
		}
	}
	if header.Type != event.Type || header.Session != b.session || !nativeUUID(header.UUID) || b.seen[header.UUID] || len(b.seen) >= domain.MaxExecutionEvents {
		return LifecycleObservation{}, lifecycleUncertain()
	}
	observation.NativeID = header.UUID
	if b.pendingCompaction != nil && (event.Type == "result" || ((event.Type == "stream_event" || event.Type == "assistant") && header.Parent == nil) || (event.Type == "system" && header.Subtype == "session_state_changed")) {
		return LifecycleObservation{}, lifecycleUncertain()
	}
	switch event.Type {
	case "command_lifecycle":
		phase = commandValidation
		var command struct {
			Type    string       `json:"type"`
			Session domain.ID    `json:"session_id"`
			UUID    string       `json:"uuid"`
			Input   domain.ID    `json:"command_uuid"`
			State   CommandState `json:"state"`
		}
		if decodeNativeObject(event.Body, &command) != nil || command.Input != b.input || !b.acceptCommand(command.State) {
			return LifecycleObservation{}, lifecycleUncertain()
		}
		observation.Kind, observation.Command = CommandObserved, command.State
		b.command = command.State
	case "system":
		if header.Subtype == "init" {
			phase = initValidation
			if b.initialized {
				if !b.canStartContinuation() || b.validateSystemInit(event.Body) != nil {
					return LifecycleObservation{}, lifecycleUncertain()
				}
				b.continuing, b.continuationSeen = true, true
				b.turnID = header.UUID
				observation.Kind, observation.TurnID = ContinuationInitialized, b.turnID
				observation.InputID, observation.Accepted = "", false
				break
			}
			if b.finished || b.command != CommandStarted || b.validateSystemInit(event.Body) != nil {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			b.initialized = true
			b.turnID = header.UUID
			observation.Kind, observation.TurnID = SessionInitialized, b.turnID
		} else if header.Subtype == "session_state_changed" {
			phase = runValidation
			value, err := b.observeRunState(event.Body)
			if err != nil {
				return LifecycleObservation{}, err
			}
			observation.Kind, observation.Run = RunStateObserved, value
			observation.InputID, observation.Accepted = "", false
		} else if header.Subtype == "compact_boundary" {
			phase = contentValidation
			value, err := b.observeCompaction(event.Body)
			if err != nil {
				return LifecycleObservation{}, err
			}
			observation.Kind, observation.Compaction = CompactionObserved, value
			// Compaction can run before original input replay. It cannot accept
			// that input or provide another product input identity.
			observation.InputID, observation.Accepted = "", false
		} else {
			switch TaskEventKind(header.Subtype) {
			case TaskStarted, TaskProgress, TaskUpdated, TaskNotification, BackgroundTasksChanged:
				phase = taskValidation
				value, err := b.observeTask(TaskEventKind(header.Subtype), event.Body)
				if err != nil {
					return LifecycleObservation{}, err
				}
				observation.Kind, observation.Task = TaskObserved, value
				if value.Kind == TaskNotification {
					b.notifications++
				}
			}
			if header.Subtype == "status" {
				phase = progressValidation
				value, err := b.observeProgress(event)
				if err != nil {
					return LifecycleObservation{}, err
				}
				observation.Kind, observation.Progress = ProgressObserved, value
			}
		}
	case "tool_progress", "tool_use_summary":
		phase = progressValidation
		value, err := b.observeProgress(event)
		if err != nil {
			return LifecycleObservation{}, err
		}
		observation.Kind, observation.Progress = ProgressObserved, value
	case "user":
		if b.pendingCompaction != nil && !header.Replay && (header.Parent == nil || header.UUID == b.pendingCompaction.anchor) {
			phase = contentValidation
			value, err := b.observeCompactionSummary(event.Body)
			if err != nil {
				return LifecycleObservation{}, err
			}
			observation.Kind, observation.Summary = CompactionSummaryObserved, value
			observation.InputID, observation.Accepted = "", false
		} else if header.Replay {
			phase = replayValidation
			if err := b.acceptReplay(event.Body); err != nil {
				return LifecycleObservation{}, err
			}
			b.accepted = true
			observation.Kind, observation.Accepted = InputAccepted, true
		} else {
			phase = contentValidation
			content, err := b.observeContent(event)
			if err != nil {
				return LifecycleObservation{}, err
			}
			observation.Kind, observation.Content = ContentObserved, content
		}
	case "stream_event", "assistant":
		phase = contentValidation
		content, err := b.observeContent(event)
		if err != nil {
			return LifecycleObservation{}, err
		}
		observation.Kind, observation.Content = ContentObserved, content
	case "result":
		phase = resultValidation
		result, correlated, err := b.validateResult(event.Body)
		if err != nil {
			return LifecycleObservation{}, err
		}
		observation.Result = &result
		if b.continuing {
			b.continuing = false
			b.continuationFailed = b.continuationFailed || !result.Successful()
			observation.Kind, observation.InputID, observation.Accepted = ContinuationFinished, "", false
		} else if correlated {
			b.finished = true
			retained := NativeResult{Kind: result.Kind, Reason: result.Reason, Error: result.Error}
			b.terminal = &retained
			observation.Kind = InputFinished
		} else {
			// Native API failures can omit user_message_uuid. Retain the session
			// failure without attaching it to this input or resolving acceptance.
			observation.Kind, observation.InputID, observation.Accepted = UncorrelatedTermination, "", false
			b.problem = lifecycleUncertain()
			if b.logger != nil {
				b.logger.Warn("Claude Code terminal observation lacks its input identity", "owner_id", b.owner, "phase", phase, "code", b.problem.Code)
			}
		}
	}
	b.seen[header.UUID] = true
	return observation, nil
}

func (b *ExecutionBinding) acceptCommand(next CommandState) bool {
	switch next {
	case CommandQueued:
		return b.command == "" && !b.finished
	case CommandStarted:
		return b.command == CommandQueued && !b.finished
	case CommandCompleted:
		return b.command == CommandStarted && (b.terminal == nil || !b.terminal.cancelsCommand())
	case CommandCancelled:
		return (b.command == CommandQueued || b.command == CommandStarted) && (b.terminal == nil || b.terminal.cancelsCommand())
	case CommandDiscarded:
		return b.command == CommandQueued
	default:
		return false
	}
}

func (b *ExecutionBinding) acceptReplay(raw []byte) error {
	var replay struct {
		Type      string          `json:"type"`
		Session   domain.ID       `json:"session_id"`
		UUID      domain.ID       `json:"uuid"`
		Timestamp string          `json:"timestamp"`
		Parent    *string         `json:"parent_tool_use_id"`
		Replay    bool            `json:"isReplay"`
		Origin    json.RawMessage `json:"origin"`
		Message   json.RawMessage `json:"message"`
	}
	var message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if decodeNativeObject(raw, &replay) != nil || decodeNativeObject(replay.Message, &message) != nil || b.accepted || b.finished || !b.initialized || b.command != CommandStarted || replay.UUID != b.input || replay.Parent != nil || len(replay.Origin) != 0 || message.Role != "user" || sha256.Sum256([]byte(message.Content)) != b.digest {
		return lifecycleUncertain()
	}
	if _, err := time.Parse(time.RFC3339Nano, replay.Timestamp); err != nil {
		return lifecycleUncertain()
	}
	return nil
}
