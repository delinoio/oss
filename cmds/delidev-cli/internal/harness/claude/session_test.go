package claude

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type sessionFixtureTransport struct {
	applied                          AppliedSettings
	events                           chan StreamEvent
	done                             chan struct{}
	once                             sync.Once
	closed                           atomic.Bool
	reads, sends, replies            atomic.Int64
	barrier                          error
	readError, sendError, replyError error
	interruptError                   error
	finishError                      error
	interrupts                       atomic.Int64
	blockingRead                     chan struct{}
	blockingNext                     chan struct{}
	blockingInterrupt                chan struct{}
}

func (f *sessionFixtureTransport) ReadAppliedSettings(ctx context.Context, _ domain.ID, _ string, _ NativeEffort) (AppliedSettings, error) {
	f.reads.Add(1)
	if f.blockingRead != nil {
		close(f.blockingRead)
		select {
		case <-ctx.Done():
			return AppliedSettings{}, ctx.Err()
		case <-f.done:
			return AppliedSettings{}, streamEnded()
		}
	}
	return f.applied, f.readError
}
func (f *sessionFixtureTransport) SendInput(context.Context, domain.ID, domain.ID, string) error {
	f.sends.Add(1)
	return f.sendError
}
func (f *sessionFixtureTransport) Next(ctx context.Context) (StreamEvent, error) {
	if f.blockingNext != nil {
		close(f.blockingNext)
	}
	select {
	case event := <-f.events:
		return event, nil
	case <-ctx.Done():
		return StreamEvent{}, ctx.Err()
	case <-f.done:
		return StreamEvent{}, streamEnded()
	}
}
func (f *sessionFixtureTransport) Reply(context.Context, StreamEvent, any) error {
	f.replies.Add(1)
	return f.replyError
}
func (f *sessionFixtureTransport) inputBarrier() error { return f.barrier }
func (f *sessionFixtureTransport) Interrupt(ctx context.Context, _ domain.ID) error {
	f.interrupts.Add(1)
	if f.blockingInterrupt != nil {
		close(f.blockingInterrupt)
		select {
		case <-f.done:
			return streamEnded()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.interruptError
}
func (f *sessionFixtureTransport) Err() *domain.Error {
	if f.closed.Load() {
		return streamEnded()
	}
	return nil
}
func (f *sessionFixtureTransport) Close() error {
	f.once.Do(func() { f.closed.Store(true); close(f.done) })
	return nil
}

func (f *sessionFixtureTransport) Finish(context.Context) error {
	if f.finishError != nil {
		return f.finishError
	}
	return f.Close()
}

func sessionFixture(t *testing.T) (*APISession, *sessionFixtureTransport) {
	t.Helper()
	b, cfg := lifecycleFixture(t)
	lifecycleObserve(t, b, runStateEvent(t, b, RunRunning))
	lifecycleReady(t, b)
	lifecycleObserve(t, b, lifecycleReplay(t, b))
	lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCompleted))
	lifecycleObserve(t, b, runStateEvent(t, b, RunIdle))
	effort := HighEffort
	applied := AppliedSettings{Model: cfg.Model, Effort: &effort}
	f := &sessionFixtureTransport{applied: applied, events: make(chan StreamEvent, 32), done: make(chan struct{})}
	return &APISession{stream: f, config: cfg, initial: applied, current: b, inputs: map[domain.ID]bool{b.input: true}}, f
}

func TestSessionInputGatePreservesPendingWorkAndFailedRun(t *testing.T) {
	for _, name := range []string{"no-idle", "unfinished", "no-result", "active-continuation", "pending-task", "background-task", "open-tool", "active-content", "callback-body", "callback-unconfirmed", "original-failed", "continuation-failed", "uncertain", "duplicate-input", "invalid-intent", "queued-native-events", "input-capacity", "reading", "changed-permission", "pending-compaction"} {
		t.Run(name, func(t *testing.T) {
			s, f := sessionFixture(t)
			id := domain.NewID()
			intent := ContinueSuccessfulRun
			switch name {
			case "no-idle":
				s.current.runState = RunRunning
			case "unfinished":
				s.current.finished = false
			case "no-result":
				s.current.terminal = nil
			case "active-continuation":
				s.current.continuing = true
			case "pending-task":
				s.current.tasks = map[string]nativeTaskState{"task": {status: TaskRunning}}
			case "background-task":
				s.current.backgroundTasks = map[string]bool{"task": true}
			case "open-tool":
				s.current.content.openTools = 1
			case "active-content":
				s.current.content.active = map[string]*providerMessageState{"": {}}
			case "pending-compaction":
				s.current.pendingCompaction = &compactionSummaryBinding{boundary: string(domain.NewID()), anchor: string(domain.NewID())}
			case "callback-body":
				s.current.interactionBytes = 1
			case "callback-unconfirmed":
				s.current.interactions = map[domain.ID]*interactionState{domain.NewID(): {}}
			case "original-failed":
				s.current.terminal.Error = true
			case "continuation-failed":
				s.current.continuationFailed = true
			case "uncertain":
				s.problem = lifecycleUncertain()
			case "duplicate-input":
				id = s.current.input
			case "invalid-intent":
				intent = "unknown"
			case "queued-native-events":
				f.barrier = sessionBusy()
			case "input-capacity":
				for i := 0; i < maxStreamIdentities; i++ {
					s.inputs[domain.NewID()] = true
				}
			case "reading":
				s.reading = true
			case "changed-permission":
				s.permissionChanged = true
			}
			previous := s.current
			if _, err := s.SendInput(context.Background(), id, lifecycleFixtureText, intent); err == nil {
				t.Fatal("ineligible session sent a new input")
			}
			if f.reads.Load() != 0 || f.sends.Load() != 0 || s.current != previous {
				t.Fatal("failed gate changed native input ownership")
			}
		})
	}
}

func TestSessionRetainsProcessWideIdentitiesAndRequiresExplicitFailedResume(t *testing.T) {
	s, f := sessionFixture(t)
	previous := s.current
	previous.content = contentState{active: map[string]*providerMessageState{}, seen: map[string]bool{"old-provider": true}, tools: map[string]nativeToolState{"old-tool": {name: "Read", finished: true}}}
	previous.tasks = map[string]nativeTaskState{"old-task": {status: TaskCompleted, notified: true}}
	previous.notifications = 1
	previous.continuationFailed = true
	id := domain.NewID()
	observed, err := s.SendInput(context.Background(), id, lifecycleFixtureText, ResumeTerminalRun)
	if err != nil || f.reads.Load() != 1 || f.sends.Load() != 1 || observed.Model != s.config.Model {
		t.Fatal("explicit terminal continuation did not verify settings and send once", err)
	}
	if s.current.input != id || s.current.accepted || s.current.finished || s.current.runState != "" || s.current.notifications != 0 || !s.current.content.seen["old-provider"] || !s.current.content.tools["old-tool"].finished || !s.current.seen[previous.turnID] || !s.current.tasks["old-task"].notified {
		t.Fatal("new run lost retained identity or inherited old acceptance")
	}
	if _, err := s.SendInput(context.Background(), id, lifecycleFixtureText, ResumeTerminalRun); err == nil || f.sends.Load() != 1 {
		t.Fatal("delivered input was sent twice")
	}
}

func TestSessionUncertainSettingsOrSendBlocksEveryRetry(t *testing.T) {
	for _, name := range []string{"read-failure", "changed-default", "missing-effort", "send-failure"} {
		t.Run(name, func(t *testing.T) {
			s, f := sessionFixture(t)
			s.config.Effort = ""
			switch name {
			case "read-failure":
				f.readError = streamUncertain()
			case "changed-default":
				low := LowEffort
				f.applied.Effort = &low
			case "missing-effort":
				f.applied.Effort = nil
			case "send-failure":
				f.sendError = streamUncertain()
			}
			if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ContinueSuccessfulRun); err == nil || s.problem == nil {
				t.Fatal("uncertain preparation/send did not latch")
			}
			reads, sends := f.reads.Load(), f.sends.Load()
			if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ResumeTerminalRun); err == nil || f.reads.Load() != reads || f.sends.Load() != sends {
				t.Fatal("uncertain input was replayed")
			}
			if name != "send-failure" && sends != 0 {
				t.Fatal("changed settings reached input pipe")
			}
		})
	}
}

func TestSessionCloseInterruptsBlockedPreflightWithoutWaitingForMutationLock(t *testing.T) {
	s, f := sessionFixture(t)
	f.blockingRead = make(chan struct{})
	sent := make(chan error, 1)
	go func() {
		_, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ContinueSuccessfulRun)
		sent <- err
	}()
	<-f.blockingRead
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close waited behind a blocked native control")
	}
	select {
	case err := <-sent:
		if err == nil {
			t.Fatal("closed preflight succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("closed preflight remained blocked")
	}
	if f.sends.Load() != 0 {
		t.Fatal("closed preflight sent input")
	}
}

func TestSessionReplyCanProceedWhileReaderWaitsAndCannotReplay(t *testing.T) {
	s, f := sessionFixture(t)
	b := contentFixture(t)
	s.current = b
	s.config.SessionID = b.session
	event := interactionTool(t, b, "Bash", map[string]any{"command": "private-fixture-command"})
	lifecycleObserve(t, b, event)
	f.blockingNext = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	read := make(chan error, 1)
	go func() { _, err := s.Next(ctx); read <- err }()
	<-f.blockingNext
	if _, err := s.SendInput(ctx, domain.NewID(), lifecycleFixtureText, ContinueSuccessfulRun); err == nil {
		t.Fatal("input overtook pending reader")
	}
	if err := s.Reply(ctx, event.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err != nil || f.replies.Load() != 1 {
		t.Fatal("pending reader prevented original reply", err)
	}
	if err := s.Reply(ctx, event.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err == nil || f.replies.Load() != 1 {
		t.Fatal("original reply replayed")
	}
	cancel()
	if err := <-read; err == nil || s.problem != nil {
		t.Fatal("reader cancellation lost or fabricated protocol failure")
	}
}

func TestSessionLateCanceledReplyKeepsEarlierInputAndTurnOwner(t *testing.T) {
	s, f := sessionFixture(t)
	b, cfg := lifecycleFixture(t)
	s.current = b
	s.config = cfg
	lifecycleObserve(t, b, runStateEvent(t, b, RunRunning))
	lifecycleReady(t, b)
	lifecycleObserve(t, b, lifecycleReplay(t, b))
	event := interactionTool(t, b, "Bash", map[string]any{"command": "fixture"})
	lifecycleObserve(t, b, event)
	_, reply, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleObserve(t, b, StreamEvent{Kind: NativeCancellation, RequestID: event.RequestID, ArrivalID: event.ArrivalID})
	lifecycleObserve(t, b, contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "toolu_callback", "content": "Canceled", "is_error": true}))
	lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCompleted))
	lifecycleObserve(t, b, runStateEvent(t, b, RunIdle))
	originalInput, originalTurn := b.input, b.turnID
	if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	f.events <- interactionEcho(event, reply)
	observation, err := s.Next(context.Background())
	if err != nil || observation.InputID != originalInput || observation.TurnID != originalTurn || observation.Interaction == nil || !observation.Interaction.Canceled || s.current.accepted {
		t.Fatal("late echo borrowed new input ownership", err)
	}
}

func TestChildCallbackDuringAutomaticTurnRetainsItsParentInput(t *testing.T) {
	b := taskFixture(t)
	lifecycleObserve(t, b, runStateEvent(t, b, RunRunning))
	contentTool(t, b, "", "other-parent", "Agent")
	lifecycleObserve(t, b, taskStart(t, b, "other-task", "other-parent"))
	lifecycleObserve(t, b, taskEvent(t, b, TaskNotification, map[string]any{"task_id": "other-task", "tool_use_id": "other-parent", "status": "completed", "output_file": "/private/task-output", "summary": "Done"}))
	lifecycleObserve(t, b, contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "parent", "content": "Backgrounded"}, map[string]any{"type": "tool_result", "tool_use_id": "other-parent", "content": "Done"}))
	lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCompleted))
	originalInput, originalTurn := b.input, b.turnID
	lifecycleObserve(t, b, lifecycleInit(t, b))
	if b.turnID == originalTurn {
		t.Fatal("missing automatic turn")
	}
	contentTool(t, b, "parent", "late-child", "Read")
	raw, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "tool_use_id": "late-child", "input": map[string]any{}})
	request := StreamEvent{Kind: NativeRequest, RequestID: "late-child-request", ArrivalID: domain.NewID(), Body: raw}
	observation := lifecycleObserve(t, b, request)
	if observation.InputID != originalInput || observation.TurnID != originalTurn {
		t.Fatal("child callback borrowed concurrent automatic turn ownership")
	}
}

func TestSessionPermissionObservationCannotSilentlyAuthorizeAnotherInput(t *testing.T) {
	s, f := sessionFixture(t)
	f.events <- lifecycleMessage(t, s.current, "system", map[string]any{"subtype": "status", "status": nil, "permissionMode": DefaultPermission})
	observation, err := s.Next(context.Background())
	if err != nil || observation.Progress == nil || !s.permissionChanged || s.problem != nil {
		t.Fatal("permission observation was lost or prevented original transcript completion", err)
	}
	if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ResumeTerminalRun); err == nil || f.sends.Load() != 0 {
		t.Fatal("explicit Resume silently adopted changed native permissions")
	}
}

func TestSessionUncertainReplyRetainsClaimWithoutRetry(t *testing.T) {
	s, f := sessionFixture(t)
	s.current = contentFixture(t)
	request := interactionTool(t, s.current, "Bash", map[string]any{"command": "private reply fixture"})
	lifecycleObserve(t, s.current, request)
	f.replyError = streamUncertain()
	if err := s.Reply(context.Background(), request.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err == nil || s.problem == nil || !s.current.interactions[request.ArrivalID].prepared {
		t.Fatal("uncertain reply lost its once-claimed arrival")
	}
	if err := s.Reply(context.Background(), request.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err == nil || f.replies.Load() != 1 {
		t.Fatal("uncertain native reply replayed")
	}
	if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ResumeTerminalRun); err == nil || f.sends.Load() != 0 {
		t.Fatal("uncertain reply authorized a new input")
	}
}

func TestSessionUnknownNativeFamilyCannotBeSilentlyDiscarded(t *testing.T) {
	s, f := sessionFixture(t)
	f.events <- lifecycleMessage(t, s.current, "system", map[string]any{"subtype": "unsupported-native-extension"})
	observation, err := s.Next(context.Background())
	if err == nil || s.problem == nil || observation.Kind != PrivateObservation || observation.Native == nil {
		t.Fatal("unknown native family was silently discarded")
	}
	if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ResumeTerminalRun); err == nil || f.sends.Load() != 0 {
		t.Fatal("unknown native state authorized another input")
	}
}
