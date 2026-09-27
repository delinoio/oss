package claude

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestStreamInterruptRequiresExactRemainingQueueAndNeverReusesRequest(t *testing.T) {
	for _, mode := range []string{"normal", "interrupt-null", "interrupt-missing", "interrupt-queued", "interrupt-extra", "interrupt-alias", "interrupt-failed"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := streamFixture(t, mode)
			request := domain.NewID()
			err := s.Interrupt(context.Background(), request)
			if (err == nil) != (mode == "normal") {
				t.Fatal("changed native acknowledgment accepted", err)
			}
			if err := s.Interrupt(context.Background(), request); err == nil || domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("original interrupt request was reused")
			}
		})
	}
}

func interruptFixture(t *testing.T) (*APISession, *sessionFixtureTransport) {
	t.Helper()
	s, f := sessionFixture(t)
	b := s.current
	b.finished, b.terminal, b.command, b.runState = false, nil, CommandStarted, RunRunning
	return s, f
}

func TestInterruptPreservesSingleClaimAndIndependentCleanup(t *testing.T) {
	for _, failure := range []string{"none", "claim", "transport"} {
		t.Run(failure, func(t *testing.T) {
			s, f := interruptFixture(t)
			if failure == "transport" {
				f.interruptError = streamUncertain()
			}
			request := domain.NewID()
			claims := 0
			received, err := s.Interrupt(context.Background(), request, func(_ context.Context, c InterruptClaim) error {
				claims++
				if f.interrupts.Load() != 0 || c != (InterruptClaim{1, s.config.Process.OwnerID, s.config.SessionID, s.current.input, s.current.turnID, request}) {
					t.Fatal("native control preceded original claim")
				}
				if failure == "claim" {
					return errors.New("private-claim-sentinel")
				}
				return nil
			})
			if (err != nil) != (failure != "none") || claims != 1 || received.Claim.RequestID != request || received.Claimed != (failure != "claim") || received.Attempted != (failure != "claim") || received.Acknowledged != (failure == "none") || received.NativeResult != nil || received.Idle || received.CleanupJoined {
				t.Fatal("Stop facts were collapsed", err)
			}
			wantCalls := int64(1)
			if failure == "claim" {
				wantCalls = 0
			}
			if f.interrupts.Load() != wantCalls {
				t.Fatal("uncertain claim reached native control")
			}
			received.Claim.InputID = domain.NewID()
			if _, err := s.Interrupt(context.Background(), domain.NewID(), func(context.Context, InterruptClaim) error { t.Fatal("second claim attempted"); return nil }); err == nil {
				t.Fatal("Stop replayed")
			}
			if _, err := s.SendInput(context.Background(), domain.NewID(), "Blocked", ResumeTerminalRun); err == nil {
				t.Fatal("Stop authorized another input")
			}
			if _, err := s.StartCompaction(context.Background(), domain.NewID()); err == nil {
				t.Fatal("Stop authorized compaction")
			}
			if err := s.Reply(context.Background(), domain.NewID(), PermissionReply{Behavior: PermissionAllow}); err == nil {
				t.Fatal("Stop authorized a reply")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			retained, err := s.InspectInterrupt()
			if (retained.ProblemCode != "") != (failure != "none") {
				t.Fatal("receipt discarded original uncertainty")
			}
			if err != nil || !retained.CleanupJoined || retained.NativeResult != nil || retained.ResultCorrelated || retained.Claim.InputID != s.current.input || retained.Idle || f.interrupts.Load() != wantCalls {
				t.Fatal("cleanup rewrote native evidence")
			}
		})
	}
}

func TestInterruptRejectsForeignOrUnownedRunBeforeClaim(t *testing.T) {
	for _, name := range []string{"no-input", "unaccepted", "finished", "automatic", "task", "background", "compacting", "uninitialized", "idle", "bad-request", "input-reuse", "owner-reuse", "session-reuse", "native-reuse", "canceled", "no-claim"} {
		t.Run(name, func(t *testing.T) {
			s, f := interruptFixture(t)
			request := domain.NewID()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			claim := func(context.Context, InterruptClaim) error { t.Fatal("invalid Stop reached durable claim"); return nil }
			switch name {
			case "no-input":
				s.current = nil
			case "unaccepted":
				s.current.accepted = false
			case "finished":
				s.current.finished = true
			case "automatic":
				s.current.continuing = true
			case "task":
				s.current.tasks = map[string]nativeTaskState{"child": {status: TaskRunning}}
			case "background":
				s.current.backgroundTasks = map[string]bool{"child": true}
			case "compacting":
				s.current.pendingCompaction = &compactionSummaryBinding{}
			case "uninitialized":
				s.current.initialized = false
			case "idle":
				s.current.runState = RunIdle
			case "bad-request":
				request = "bad"
			case "input-reuse":
				request = s.current.input
			case "owner-reuse":
				request = s.config.Process.OwnerID
			case "session-reuse":
				request = s.config.SessionID
			case "native-reuse":
				s.current.seen[string(request)] = true
			case "canceled":
				cancel()
			case "no-claim":
				claim = nil
			}
			if _, err := s.Interrupt(ctx, request, claim); err == nil || f.interrupts.Load() != 0 || s.interrupt != nil {
				t.Fatal("invalid Stop mutated original run")
			}
		})
	}
}

func TestInterruptRacingNormalCompletionPreservesOriginalSuccess(t *testing.T) {
	s, f := interruptFixture(t)
	f.blockingNext = make(chan struct{})
	read := make(chan error, 1)
	go func() { _, err := s.Next(context.Background()); read <- err }()
	<-f.blockingNext
	if _, err := s.Interrupt(context.Background(), domain.NewID(), func(context.Context, InterruptClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.events <- lifecycleResult(t, s.current, Completed, false)
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	f.blockingNext = nil
	for _, event := range []StreamEvent{lifecycleCommand(t, s.current, CommandCompleted), runStateEvent(t, s.current, RunIdle)} {
		f.events <- event
		if _, err := s.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.InspectInterrupt()
	if err != nil || result.NativeResult == nil || !result.NativeResult.Successful() || !result.ResultCorrelated || !result.Idle || result.CleanupJoined {
		t.Fatal("Stop acknowledgment replaced normal completion")
	}
	result.NativeResult.Reason = AbortedStreaming
	unchanged, _ := s.InspectInterrupt()
	if !unchanged.NativeResult.Successful() {
		t.Fatal("caller mutated original result")
	}
}

func TestInterruptBlockedAcknowledgmentCanBeContainedByOriginalClose(t *testing.T) {
	s, f := interruptFixture(t)
	f.blockingInterrupt = make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := s.Interrupt(context.Background(), domain.NewID(), func(context.Context, InterruptClaim) error { return nil })
		finished <- err
	}()
	<-f.blockingInterrupt
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("owned Close waited behind the native acknowledgment")
	}
	if err := <-finished; err == nil {
		t.Fatal("closed interrupt was acknowledged")
	}
	o, err := s.InspectInterrupt()
	if err != nil || o.Acknowledged || !o.Attempted || !o.CleanupJoined || o.NativeResult != nil || o.ProblemCode != domain.RecoveryRequired || f.interrupts.Load() != 1 {
		t.Fatal("forced cleanup invented native completion or lost uncertainty")
	}
}

func interruptedTextFixture(t *testing.T) (*ExecutionBinding, StreamEvent) {
	t.Helper()
	b := contentFixture(t)
	b.interrupt = &InterruptClaim{1, b.owner, b.session, b.input, b.turnID, domain.NewID()}
	contentStart(t, b, "", "msg_interrupted")
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}))
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Original partial text."}}))
	event := contentCompleted(t, b, "", "msg_interrupted", map[string]any{"type": "text", "text": "Original partial text."})
	return b, lifecycleChange(t, event, "aborted", true)
}

func TestInterruptedContentRequiresOriginalClaimAndExactPartialBytes(t *testing.T) {
	for _, name := range []string{"valid", "no-claim", "false-aborted", "null-aborted", "changed", "wrong-message", "child", "finished-block", "citation", "stop-reason"} {
		t.Run(name, func(t *testing.T) {
			b, event := interruptedTextFixture(t)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(event.Body, &fields)
			message := contentMessage("msg_interrupted", map[string]any{"type": "text", "text": "Original partial text."})
			switch name {
			case "no-claim":
				b.interrupt = nil
			case "false-aborted":
				event = lifecycleChange(t, event, "aborted", false)
			case "null-aborted":
				event = lifecycleChange(t, event, "aborted", json.RawMessage("null"))
			case "changed":
				message["content"] = []any{map[string]any{"type": "text", "text": "Different"}}
			case "wrong-message":
				message["id"] = "foreign"
			case "child":
				event = lifecycleChange(t, event, "parent_tool_use_id", "foreign")
			case "finished-block":
				b.content.active[""].blocks[0].completed = true
			case "citation":
				message["content"] = []any{map[string]any{"type": "text", "text": "Original partial text.", "citations": []any{}}}
			case "stop-reason":
				message["stop_reason"] = "end_turn"
			}
			event = lifecycleChange(t, event, "message", message)
			o, err := b.Observe(event)
			if name != "valid" {
				if err == nil {
					t.Fatal("changed interrupted content accepted")
				}
				return
			}
			if err != nil || len(o.Content) != 1 || o.Content[0].Kind != ContentInterrupted || o.Content[0].Block == nil || *o.Content[0].Block.Text != "Original partial text." || b.content.bufferedBytes != 0 || b.content.active[""] != nil || b.finished || b.terminal != nil {
				t.Fatal("partial content became completion", err)
			}
			// Neither an interrupted block nor its native marker invents input result.
			marker := contentResult(t, b, "", map[string]any{"type": "text", "text": "[Request interrupted by user]"})
			mo := lifecycleObserve(t, b, marker)
			if mo.Content[0].Kind != NativeInterruptContext {
				t.Fatal("native marker became product input")
			}
			result := lifecycleResult(t, b, AbortedStreaming, true)
			var body map[string]json.RawMessage
			_ = json.Unmarshal(result.Body, &body)
			delete(body, "user_message_uuid")
			result.Body, _ = json.Marshal(body)
			stopped := lifecycleObserve(t, b, result)
			if stopped.Kind != InterruptResultObserved || stopped.InputID != "" || stopped.Accepted || b.finished || b.terminal != nil || b.interruptResult == nil {
				t.Fatal("uncorrelated Stop result gained input identity")
			}
		})
	}
}
