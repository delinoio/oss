package claude

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type InputIntent string
type sessionPhase string

const (
	ContinueSuccessfulRun InputIntent  = "continue-successful-run"
	ResumeTerminalRun     InputIntent  = "resume-terminal-run"
	sessionTransportPhase sessionPhase = "transport"
	sessionSettingsPhase  sessionPhase = "settings"
	sessionInputPhase     sessionPhase = "input"
	sessionEventPhase     sessionPhase = "event"
	sessionReplyPhase     sessionPhase = "reply"
	sessionInterruptPhase sessionPhase = "interrupt"
)

type sessionTransport interface {
	ReadAppliedSettings(context.Context, domain.ID, string, NativeEffort) (AppliedSettings, error)
	SendInput(context.Context, domain.ID, domain.ID, string) error
	Next(context.Context) (StreamEvent, error)
	Reply(context.Context, StreamEvent, any) error
	Interrupt(context.Context, domain.ID) error
	inputBarrier() error
	Err() *domain.Error
	Close() error
	Finish(context.Context) error
	finishDenial(context.Context) error
}

// APISession owns the ordered live protocol for one private process. The
// Worker must durably claim each input and authorize any reply before calling
// these methods. An eligible closed root conversation can explicitly hand off
// to a replacement process; crash recovery and public dispatch remain separate.
// Consume Next through idle before SendInput; a concurrent pending read cannot
// be overtaken by a new input. Reply remains usable while Next waits.
type APISession struct {
	mu                sync.Mutex
	stream            sessionTransport
	config            APIStreamConfig
	initial           AppliedSettings
	current           *ExecutionBinding
	compaction        *manualCompactionBinding
	inputs            map[domain.ID]bool
	reading           bool
	closed            atomic.Bool
	cleanupJoined     atomic.Bool
	interrupt         *InterruptObservation
	problem           *domain.Error
	originalInputEOF  bool
	handoffRetained   bool
	permissionChanged bool
	history           *sessionHistory
	authorities       map[[sha256.Size]byte]bool
	owners            map[domain.ID]bool
	serverOrigin      string
}

func OpenAPISession(ctx context.Context, config APIStreamConfig) (*APISession, error) {
	config.WorkspaceRoots = slices.Clone(config.WorkspaceRoots)
	stream, err := OpenAPIStream(ctx, config)
	if err != nil {
		return nil, err
	}
	initial, ok := stream.InitialAppliedSettings()
	if !ok {
		if err := stream.Close(); err != nil {
			return nil, streamUncertain()
		}
		return nil, settingsUncertain()
	}
	// The stream owns its secret launch state. The controller needs only the
	// immutable non-secret settings used by each typed execution binding.
	origin, authority := config.API.ServerOrigin, sha256.Sum256([]byte(config.API.Token))
	config.API = APIConfig{}
	config.Process.Env = retainedLookupEnvironment(config.Process.Env)
	config.Process.Args = nil
	return &APISession{stream: stream, config: config, initial: initial, inputs: map[domain.ID]bool{}, history: &sessionHistory{}, authorities: map[[sha256.Size]byte]bool{authority: true}, owners: map[domain.ID]bool{config.Process.OwnerID: true}, serverOrigin: origin}, nil
}

func sessionBusy() *domain.Error {
	return domain.Fail(domain.Conflict, "The Claude Code session has not reached an eligible input boundary.", "Retain the current run and consume its native lifecycle; do not resend accepted input.")
}

func (s *APISession) latch(phase sessionPhase, problem *domain.Error) *domain.Error {
	if s.problem == nil {
		s.problem = problem
		if s.config.Process.Logger != nil {
			s.config.Process.Logger.Warn("Claude Code live session requires reconciliation", "owner_id", s.config.Process.OwnerID, "phase", phase, "code", problem.Code)
		}
	}
	return s.problem
}

func (s *APISession) status() error {
	if s.problem != nil {
		return s.problem
	}
	if s.closed.Load() {
		return domain.Fail(domain.Canceled, "The Claude Code session is closed.", "Reconcile native history and cleanup before explicit recovery.")
	}
	if err := s.stream.Err(); err != nil {
		return s.latch(sessionTransportPhase, lifecycleUncertain())
	}
	return nil
}

func (s *APISession) SendInput(ctx context.Context, input domain.ID, text string, intent InputIntent) (AppliedSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.status(); err != nil {
		return AppliedSettings{}, err
	}
	if err := ctx.Err(); err != nil {
		return AppliedSettings{}, domain.SafeError(err)
	}
	if intent != ContinueSuccessfulRun && intent != ResumeTerminalRun {
		return AppliedSettings{}, apiConfigurationError()
	}
	if s.interrupt != nil || s.reading || s.inputs[input] || (s.current != nil && s.current.seen[string(input)]) || s.permissionChanged || (s.compaction != nil && !s.compaction.settled) {
		return AppliedSettings{}, sessionBusy()
	}
	if s.compaction != nil && s.compaction.status == CompactFailed && intent != ResumeTerminalRun {
		return AppliedSettings{}, sessionBusy()
	}
	if len(s.inputs) >= maxStreamIdentities {
		return AppliedSettings{}, domain.Fail(domain.ResourceExhausted, "The live Claude Code session reached its input identity bound.", "Retain the native history for explicit process recovery.")
	}
	next, err := BindExecution(s.config, input, text)
	if err != nil {
		return AppliedSettings{}, err
	}
	if previous := s.current; previous != nil {
		work := previous.knownWork()
		if previous.problem != nil || previous.runState != RunIdle || !previous.finished || previous.terminal == nil || previous.continuing || previous.pendingCompaction != nil || work.PendingTasks != 0 || work.BackgroundTasks != 0 || previous.content.openTools != 0 || len(previous.content.active) != 0 || previous.interactionBytes != 0 {
			return AppliedSettings{}, sessionBusy()
		}
		if intent == ContinueSuccessfulRun && (!previous.terminal.Successful() || previous.continuationFailed) {
			return AppliedSettings{}, sessionBusy()
		}
		for _, interaction := range previous.interactions {
			if !interaction.echoed && !interaction.canceled {
				return AppliedSettings{}, sessionBusy()
			}
		}
		// Reuse identity and closed ownership registries for the lifetime of this
		// process. A later run cannot reuse an old tool/message/callback identity or
		// attribute a late exact echo to its new input. Notification eligibility is
		// deliberately per run, never inherited from an earlier completed task.
		next.seen = previous.seen
		next.content = previous.content
		next.tasks = previous.tasks
		next.backgroundTasks = previous.backgroundTasks
		next.interactions = previous.interactions
	}
	if err := s.stream.inputBarrier(); err != nil {
		return AppliedSettings{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	applied, err := s.stream.ReadAppliedSettings(bounded, domain.NewID(), s.config.Model, s.config.Effort)
	if err != nil || !sameAppliedSettings(applied, s.initial) {
		return AppliedSettings{}, s.latch(sessionSettingsPhase, settingsUncertain())
	}
	if err := s.stream.inputBarrier(); err != nil {
		return AppliedSettings{}, err
	}
	// Retain the input attempt before pipe delivery. Any uncertain send blocks
	// all new inputs; neither a later terminal nor a retry clears that latch.
	s.inputs[input], s.current, s.compaction = true, next, nil
	if err := s.stream.SendInput(ctx, input, s.config.SessionID, text); err != nil {
		return AppliedSettings{}, s.latch(sessionInputPhase, lifecycleUncertain())
	}
	if s.config.Process.Logger != nil {
		s.config.Process.Logger.InfoContext(ctx, "Claude Code input delivered", "owner_id", s.config.Process.OwnerID, "input_id", input, "intent", intent)
	}
	return applied, nil
}

func sameAppliedSettings(a, b AppliedSettings) bool {
	return a.Model == b.Model && ((a.Effort == nil && b.Effort == nil) || (a.Effort != nil && b.Effort != nil && *a.Effort == *b.Effort))
}

func (s *APISession) Next(ctx context.Context) (LifecycleObservation, error) {
	s.mu.Lock()
	if err := s.status(); err != nil {
		s.mu.Unlock()
		return LifecycleObservation{}, err
	}
	if s.current == nil || s.reading {
		s.mu.Unlock()
		return LifecycleObservation{}, sessionBusy()
	}
	s.reading = true
	s.mu.Unlock()
	event, err := s.stream.Next(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reading = false
	if status := s.status(); status != nil {
		return LifecycleObservation{}, status
	}
	if err != nil {
		if ctx.Err() != nil {
			return LifecycleObservation{}, domain.SafeError(ctx.Err())
		}
		return LifecycleObservation{}, s.latch(sessionEventPhase, lifecycleUncertain())
	}
	var observation LifecycleObservation
	if s.compaction != nil && event.Kind == NativeMessage {
		observation, err = s.compaction.observe(event)
	} else {
		observation, err = s.current.Observe(event)
	}
	if err != nil || observation.Kind == PrivateObservation {
		if observation.Kind == PrivateObservation && s.interrupt != nil && s.config.Process.Logger != nil {
			var header struct {
				Subtype string `json:"subtype"`
			}
			_ = json.Unmarshal(event.Body, &header)
			s.config.Process.Logger.Warn("claude_stop_private_observation", "owner_id", s.config.Process.OwnerID, "event_kind", event.Kind, "system", event.Type == "system", "api_retry", header.Subtype == "api_retry", "rate_limit", event.Type == "rate_limit_event", "auth_status", event.Type == "auth_status")
		}
		return observation, s.latch(sessionEventPhase, lifecycleUncertain())
	}
	if s.current.problem != nil {
		s.latch(sessionEventPhase, s.current.problem)
	}
	if observation.Progress != nil && observation.Progress.Permission != nil && *observation.Progress.Permission != s.config.Permission {
		// Native Plan tools can change permissions inside the accepted turn.
		// Preserve that observation and finish its transcript, but do not send
		// another input under silently changed permission authority.
		s.permissionChanged = true
		if s.config.Process.Logger != nil {
			s.config.Process.Logger.Info("Claude Code native permission transition blocks additional input", "owner_id", s.config.Process.OwnerID, "permission", *observation.Progress.Permission)
		}
	}
	if s.history != nil {
		if err := s.history.observe(observation); err != nil {
			return observation, s.latch(sessionEventPhase, historyUncertain())
		}
	}
	return observation, nil
}

// StartCompaction claims a native local command, not a conversation input. The
// caller must durably retain the action before this one pipe send and consume
// Next through its original idle boundary. No arguments, retry or separate
// summarization model are added to the pinned native /compact operation.
func (s *APISession) StartCompaction(ctx context.Context, action domain.ID) (AppliedSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.status(); err != nil {
		return AppliedSettings{}, err
	}
	if err := ctx.Err(); err != nil {
		return AppliedSettings{}, domain.SafeError(err)
	}
	if action.Validate() != nil {
		return AppliedSettings{}, apiConfigurationError()
	}
	if s.interrupt != nil || s.reading || s.inputs[action] || s.permissionChanged || s.current == nil || s.current.seen[string(action)] || (s.compaction != nil && !s.compaction.settled) {
		return AppliedSettings{}, sessionBusy()
	}
	if len(s.inputs) >= maxStreamIdentities {
		return AppliedSettings{}, domain.Fail(domain.ResourceExhausted, "The live Claude Code session reached its operation identity bound.", "Retain the native history for explicit process recovery.")
	}
	previous := s.current
	work := previous.knownWork()
	if previous.problem != nil || previous.runState != RunIdle || !previous.finished || previous.terminal == nil || previous.continuing || previous.pendingCompaction != nil || work.PendingTasks != 0 || work.BackgroundTasks != 0 || previous.content.openTools != 0 || len(previous.content.active) != 0 || previous.interactionBytes != 0 {
		return AppliedSettings{}, sessionBusy()
	}
	for _, interaction := range previous.interactions {
		if !interaction.echoed && !interaction.canceled {
			return AppliedSettings{}, sessionBusy()
		}
	}
	core, err := BindExecution(s.config, action, "/compact")
	if err != nil {
		return AppliedSettings{}, err
	}
	core.seen = previous.seen
	if err := s.stream.inputBarrier(); err != nil {
		return AppliedSettings{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	applied, err := s.stream.ReadAppliedSettings(bounded, domain.NewID(), s.config.Model, s.config.Effort)
	if err != nil || !sameAppliedSettings(applied, s.initial) {
		return AppliedSettings{}, s.latch(sessionSettingsPhase, settingsUncertain())
	}
	if err := s.stream.inputBarrier(); err != nil {
		return AppliedSettings{}, err
	}
	s.inputs[action], s.compaction = true, &manualCompactionBinding{core: core}
	if err := s.stream.SendInput(ctx, action, s.config.SessionID, "/compact"); err != nil {
		return AppliedSettings{}, s.latch(sessionInputPhase, lifecycleUncertain())
	}
	if s.config.Process.Logger != nil {
		s.config.Process.Logger.InfoContext(ctx, "Claude Code manual compaction delivered", "owner_id", s.config.Process.OwnerID, "action_id", action)
	}
	return applied, nil
}

func (s *APISession) Reply(ctx context.Context, arrival domain.ID, reply PermissionReply) error {
	return s.reply(ctx, arrival, reply, nil)
}

// ReplyClaimed requires the owning Worker to synchronize exact native ownership
// and reply digest before pipe transmission. Preparation is once-only even when
// that barrier fails; a replacement process cannot adopt this callback.
func (s *APISession) ReplyClaimed(ctx context.Context, arrival domain.ID, reply PermissionReply, claim func(context.Context, PermissionReplyClaim) error) error {
	if claim == nil {
		return lifecycleUncertain()
	}
	return s.reply(ctx, arrival, reply, claim)
}

func (s *APISession) reply(ctx context.Context, arrival domain.ID, reply PermissionReply, claim func(context.Context, PermissionReplyClaim) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.status(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	if s.current == nil || s.interrupt != nil {
		return sessionBusy()
	}
	event, raw, err := s.current.PreparePermissionReply(arrival, reply)
	if err != nil {
		return err
	}
	if claim != nil {
		original, err := s.current.permissionReplyClaim(arrival)
		if err != nil || claim(ctx, original) != nil {
			return s.latch(sessionReplyPhase, lifecycleUncertain())
		}
	}
	if err := s.stream.Reply(ctx, event, raw); err != nil {
		return s.latch(sessionReplyPhase, lifecycleUncertain())
	}
	return nil
}

// Close always joins owned process cleanup, including after protocol failure.
// It does not turn an uncertain input or reply into a completed operation.
func (s *APISession) Close() error {
	s.closed.Store(true)
	err := s.stream.Close()
	if err == nil {
		s.cleanupJoined.Store(true)
	}
	return err
}

// Finish joins a native EOF exit only after all observed work is settled. It
// does not accept unknown history or create a process-replacement capability.
func (s *APISession) Finish(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishInputLocked(ctx)
}

// FinishOriginalInput joins the exact original controller's clean EOF exit.
// It returns classification only; it neither proves retained history nor grants
// replacement/continuation authority after a permission transition or callback.
func (s *APISession) FinishOriginalInput(ctx context.Context, owner, session, input domain.ID, turn string) (NativeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.current
	if b == nil || s.config.Process.OwnerID != owner || s.config.SessionID != session || b.input != input || b.turnID != turn || b.terminal == nil || b.continuationSeen || s.interrupt != nil || b.interruptedReply != "" {
		return NativeResult{}, lifecycleUncertain()
	}
	result := NativeResult{Kind: b.terminal.Kind, Reason: b.terminal.Reason, Error: b.terminal.Error}
	if err := s.finishInputLocked(ctx); err != nil {
		return NativeResult{}, err
	}
	s.originalInputEOF = true
	return result, nil
}

func (s *APISession) finishInputLocked(ctx context.Context) error {
	if err := s.status(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	b := s.current
	if s.reading || b == nil || b.problem != nil || b.runState != RunIdle || !b.finished || b.terminal == nil || b.continuing || b.pendingCompaction != nil || (s.compaction != nil && !s.compaction.settled) {
		return sessionBusy()
	}
	work := b.knownWork()
	if work.PendingTasks != 0 || work.BackgroundTasks != 0 || b.content.openTools != 0 || len(b.content.active) != 0 || b.interactionBytes != 0 {
		return sessionBusy()
	}
	for _, interaction := range b.interactions {
		if !interaction.echoed && !interaction.canceled {
			return sessionBusy()
		}
	}
	return s.finishLocked(ctx)
}

func (s *APISession) finishLocked(ctx context.Context) error {
	return s.finishWithLocked(ctx, s.stream.Finish)
}

func (s *APISession) finishWithLocked(ctx context.Context, finish func(context.Context) error) error {
	if err := s.stream.inputBarrier(); err != nil {
		return err
	}
	s.closed.Store(true)
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := finish(bounded); err != nil {
		// A failed EOF handoff must still join forced cleanup; it cannot leave
		// the native process alive behind a closed controller or mint a proof.
		if cleanup := s.stream.Close(); cleanup != nil {
			return s.latch(sessionTransportPhase, streamUncertain())
		}
		return s.latch(sessionTransportPhase, domain.SafeError(err))
	}
	s.cleanupJoined.Store(true)
	return nil
}

func (s *Stream) inputBarrier() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.statusLocked(); err != nil {
		return err
	}
	if len(s.events) != 0 || len(s.pending) != 0 || s.activeIncoming != 0 {
		return sessionBusy()
	}
	return nil
}

// Preserve only the already validated non-secret platform lookup context for
// a future owned process. Never retain caller credentials or loader overrides.
func retainedLookupEnvironment(env []string) []string {
	var result []string
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && (strings.EqualFold(key, "PATH") || strings.EqualFold(key, "SYSTEMROOT") || strings.EqualFold(key, "WINDIR")) {
			result = append(result, entry)
		}
	}
	return result
}
