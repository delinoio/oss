package claude

import (
	"context"
	"crypto/sha256"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ClosedAPISession is an opaque single-use process handoff, not a durable Worker
// checkpoint or crash-recovery record. Only a settled controller with its own
// original event ledger and confirmed process cleanup can create it. It cannot
// be reconstructed from a filename, serialized JSON or a caller's status flags.
type ClosedAPISession struct {
	mu             sync.Mutex
	used           bool
	previous       *APISession
	transcript     TranscriptObservation
	requiresResume bool
}

func continuationUnavailable() *domain.Error {
	return domain.Fail(domain.Unsupported, "This Claude Code session needs additional native history evidence before process replacement.", "Preserve the native runtime and original task, tool and interaction records; do not replace the session or resend input.")
}

func (s *APISession) CloseForContinuation(ctx context.Context) (*ClosedAPISession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.status(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	b := s.current
	if s.reading || s.permissionChanged || b == nil || b.problem != nil || !b.accepted || !b.finished || b.terminal == nil || b.runState != RunIdle || b.continuing || b.pendingCompaction != nil || (s.compaction != nil && !s.compaction.settled) {
		return nil, sessionBusy()
	}
	// Root-only closed conversations have a complete transcript path. Native
	// child/tool auxiliary files and callback recovery still need their own
	// joined evidence before they can cross this process-replacement boundary.
	if s.history == nil || !s.owners[s.config.Process.OwnerID] || len(s.authorities) == 0 || len(b.tasks) != 0 || len(b.backgroundTasks) != 0 || len(b.interactions) != 0 || len(b.content.tools) != 0 || len(b.content.serverTools) != 0 || len(b.content.active) != 0 || b.content.openTools != 0 || b.interactionBytes != 0 || s.history.boundary != nil || s.history.action != "" || len(s.history.messages) == 0 {
		return nil, continuationUnavailable()
	}
	if err := s.stream.inputBarrier(); err != nil {
		return nil, err
	}
	if err := s.finishLocked(ctx); err != nil {
		return nil, s.latch(sessionTransportPhase, streamUncertain())
	}
	observed, err := s.readRetainedTranscript(ctx)
	if err != nil {
		return nil, s.latch(sessionEventPhase, domain.SafeError(err))
	}
	requiresResume := !b.terminal.Successful() || b.continuationFailed || (s.compaction != nil && s.compaction.status == CompactFailed)
	if s.config.Process.Logger != nil {
		s.config.Process.Logger.InfoContext(ctx, "Claude Code closed session retained for process handoff", "owner_id", s.config.Process.OwnerID, "session_id", s.config.SessionID, "requires_resume", requiresResume)
	}
	return &ClosedAPISession{previous: s, transcript: observed, requiresResume: requiresResume}, nil
}

func (s *APISession) readRetainedTranscript(ctx context.Context) (TranscriptObservation, error) {
	h := s.history
	observed, err := readMainTranscript(ctx, s.config.Home, s.config.SessionID, s.config.Workspace, h.messages, h.compactions, h.actions, h.resumes, s.config.Process.Logger)
	if err != nil {
		return TranscriptObservation{}, err
	}
	// Manual commands add one original unforwarded native meta caveat each.
	// Any other unobserved conversation cannot become continuation authority.
	if observed.AdditionalMessages != uint32(len(h.actions))+observed.ResumeContextMessages {
		return TranscriptObservation{}, historyUncertain()
	}
	return observed, nil
}

// ContinueAPISession preserves the original native session/configuration but
// requires a never-used process owner and execution credential on the same
// server. The caller must independently hold its exclusive workspace/runtime
// lease and durably authorize this execution before calling. It sends no input.
// Failed launches consume the handoff: never retry an uncertain replacement.
func ContinueAPISession(ctx context.Context, closed *ClosedAPISession, owner domain.ID, api APIConfig, intent InputIntent) (*APISession, error) {
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if closed == nil || closed.previous == nil || owner.Validate() != nil || !apiproxy.ValidToken(api.Token) || (intent != ContinueSuccessfulRun && intent != ResumeTerminalRun) {
		return nil, apiConfigurationError()
	}
	closed.mu.Lock()
	defer closed.mu.Unlock()
	if closed.used {
		return nil, sessionBusy()
	}
	previous := closed.previous
	authority := sha256.Sum256([]byte(api.Token))
	if !previous.closed.Load() || previous.problem != nil || previous.owners[owner] || previous.authorities[authority] || api.ServerOrigin != previous.serverOrigin || (closed.requiresResume && intent != ResumeTerminalRun) || len(previous.owners) >= maxStreamIdentities || len(previous.authorities) >= maxStreamIdentities {
		return nil, sessionBusy()
	}
	closed.used = true
	observed, err := previous.readRetainedTranscript(ctx)
	if err != nil || observed != closed.transcript {
		return nil, historyUncertain()
	}
	config := previous.config
	config.Process.OwnerID, config.API = owner, api
	stream, err := openAPIStreamMode(ctx, config, true)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*APISession, error) {
		if cleanup := stream.Close(); cleanup != nil {
			return nil, streamUncertain()
		}
		return nil, err
	}
	initial, ok := stream.InitialAppliedSettings()
	if !ok || !sameAppliedSettings(initial, previous.initial) {
		return fail(settingsUncertain())
	}
	// Initialization may not silently rewrite or substitute accepted history.
	observed, err = previous.readRetainedTranscript(ctx)
	if err != nil || observed != closed.transcript {
		return fail(historyUncertain())
	}
	if err := stream.inputBarrier(); err != nil {
		return fail(err)
	}
	if previous.compaction != nil {
		history := previous.history
		if len(history.actions) == 0 || len(history.resumes) >= maxStreamIdentities {
			return fail(historyUncertain())
		}
		proof := historyResumeProof{transcript: closed.transcript, messages: len(history.messages), action: history.actions[len(history.actions)-1].ActionID}
		if count := len(history.resumes); count != 0 && history.resumes[count-1].action == proof.action && history.resumes[count-1].messages == proof.messages {
			// A clean replacement closed before another input may only advance
			// that still-unconsumed prefix, not mint a second synthetic branch.
			history.resumes[count-1] = proof
		} else {
			history.resumes = append(history.resumes, proof)
		}
	}
	config.API = APIConfig{}
	next := &APISession{stream: stream, config: config, initial: initial, current: previous.current, compaction: previous.compaction, inputs: previous.inputs, history: previous.history, authorities: previous.authorities, owners: previous.owners, serverOrigin: previous.serverOrigin}
	next.owners[owner], next.authorities[authority] = true, true
	if config.Process.Logger != nil {
		config.Process.Logger.InfoContext(ctx, "Claude Code native session resumed in a new owned process", "owner_id", owner, "session_id", config.SessionID, "intent", intent)
	}
	return next, nil
}
