package claude

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func denialCompletionFixture(t *testing.T) (*APISession, *sessionFixtureTransport, domain.ID) {
	t.Helper()
	s, transport := sessionFixture(t)
	b, request := interruptedDenialFixture(t, true, true)
	lifecycleObserve(t, b, runStateEvent(t, b, RunRunning))
	lifecycleObserve(t, b, interruptedDenialToolResult(t, b))
	lifecycleObserve(t, b, interruptedDenialContext(t, b))
	lifecycleObserve(t, b, interruptedDenialResult(t, b))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCancelled))
	lifecycleObserve(t, b, runStateEvent(t, b, RunIdle))
	s.current = b
	s.config.Process.OwnerID = b.owner
	s.config.SessionID = b.session
	return s, transport, request.ArrivalID
}

func TestOriginalDenialCleanupRequiresOriginalCallbackResultAndIdle(t *testing.T) {
	for _, scenario := range []string{"valid", "permission-changed", "owner", "session", "input", "turn", "arrival", "missing-context", "missing-tool-result", "correlated", "no-result", "wrong-reason", "not-error", "no-echo", "cancelled-callback", "allow", "command", "idle", "reading", "pending-task", "active-content", "callback-bytes", "continuation", "stop", "barrier", "eof"} {
		t.Run(scenario, func(t *testing.T) {
			s, transport, arrival := denialCompletionFixture(t)
			b := s.current
			owner, session, input, turn := b.owner, b.session, b.input, b.turnID
			switch scenario {
			case "permission-changed":
				s.permissionChanged = true
			case "owner":
				owner = domain.NewID()
			case "session":
				session = domain.NewID()
			case "input":
				input = domain.NewID()
			case "turn":
				turn = string(domain.NewID())
			case "arrival":
				arrival = domain.NewID()
			case "missing-context":
				b.denialContext = false
			case "missing-tool-result":
				b.denialToolResult = false
			case "correlated":
				b.terminal = &NativeResult{Kind: ResultSuccess, Reason: Completed}
				b.finished = true
			case "no-result":
				b.interruptResult = nil
			case "wrong-reason":
				b.interruptResult.Reason = AbortedStreaming
			case "not-error":
				b.interruptResult.Error = false
			case "no-echo":
				b.interactions[arrival].echoed = false
			case "cancelled-callback":
				b.interactions[arrival].canceled = true
			case "allow":
				b.interactions[arrival].behavior = PermissionAllow
			case "command":
				b.command = CommandCompleted
			case "idle":
				b.runState = RunRunning
			case "reading":
				s.reading = true
			case "pending-task":
				b.tasks = map[string]nativeTaskState{"task": {status: TaskRunning}}
			case "active-content":
				b.content.active[""] = &providerMessageState{}
			case "callback-bytes":
				b.interactionBytes = 1
			case "continuation":
				b.continuationSeen = true
			case "stop":
				s.interrupt = &InterruptObservation{}
			case "barrier":
				transport.barrier = sessionBusy()
			case "eof":
				transport.finishError = streamUncertain()
			}
			r, err := s.FinishOriginalDenial(context.Background(), owner, session, input, turn, arrival)
			if scenario == "valid" || scenario == "permission-changed" {
				if err != nil || !s.cleanupJoined.Load() || !transport.closed.Load() || r.Kind != ResultExecutionError || r.Reason != AbortedTools || !r.Error || r.Usage != nil || b.finished || b.terminal != nil || s.interrupt != nil {
					t.Fatal("denial cleanup fabricated correlation or lost original scope", err)
				}
				if _, err := s.FinishOriginalDenial(context.Background(), owner, session, input, turn, arrival); err == nil {
					t.Fatal("closed native controller reused")
				}
			} else if err == nil || r.Kind != "" || s.cleanupJoined.Load() || transport.closed.Load() != (scenario == "eof") {
				t.Fatal("unproved denial cleanup accepted", err)
			}
			if transport.sends.Load() != 0 || transport.replies.Load() != 0 || transport.interrupts.Load() != 0 {
				t.Fatal("cleanup resent native work")
			}
		})
	}
}
