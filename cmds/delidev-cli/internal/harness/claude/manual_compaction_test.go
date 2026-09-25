package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func manualCompactionEvents(t *testing.T, b *ExecutionBinding, failed bool) []StreamEvent {
	t.Helper()
	status := CompactSucceeded
	if failed {
		status = CompactFailed
	}
	events := []StreamEvent{
		runStateEvent(t, b, RunRunning), lifecycleCommand(t, b, CommandQueued), lifecycleCommand(t, b, CommandStarted),
		lifecycleMessage(t, b, "system", map[string]any{"subtype": "status", "status": SessionCompacting}),
		lifecycleMessage(t, b, "system", map[string]any{"subtype": "status", "status": nil, "compact_result": status}),
		lifecycleInit(t, b),
	}
	if !failed {
		boundary, summary := compactionSummaryFixture(t, b)
		var fields map[string]any
		_ = json.Unmarshal(boundary.Body, &fields)
		fields["compact_metadata"].(map[string]any)["trigger"] = ManualCompaction
		boundary.Body, _ = json.Marshal(fields)
		summary = lifecycleChange(t, summary, "message", map[string]any{"role": "user", "content": "Private native summary."})
		summary = lifecycleChange(t, summary, "isReplay", false)
		events = append(events, boundary, summary)
		events = append(events, manualCommandReplay(t, b, string(domain.NewID()), "<local-command-stdout>Compacted </local-command-stdout>"))
	} else {
		message := contentMessage(string(domain.NewID()), map[string]any{"type": "text", "text": "Private compaction diagnostic."})
		message["model"] = "<synthetic>"
		events = append(events, lifecycleMessage(t, b, "assistant", map[string]any{"parent_tool_use_id": nil, "timestamp": "2026-09-26T00:00:00Z", "message": message}))
	}
	events = append(events, manualCommandReplay(t, b, string(b.input), "<command-name>/compact</command-name>\n <command-message>compact</command-message>\n <command-args></command-args>"))
	result := lifecycleResult(t, b, Completed, false)
	result = lifecycleChange(t, result, "user_message_uuid", nil)
	result = lifecycleChange(t, result, "terminal_reason", nil)
	result = lifecycleChange(t, result, "duration_api_ms", uint64(0))
	result = lifecycleChange(t, result, "num_turns", uint64(0))
	var fields map[string]any
	_ = json.Unmarshal(result.Body, &fields)
	fields["stop_reason"], fields["result"] = nil, ""
	if failed {
		fields["result"] = "Private compaction diagnostic."
	}
	result.Body, _ = json.Marshal(fields)
	return append(events, result, lifecycleCommand(t, b, CommandCompleted), runStateEvent(t, b, RunIdle))
}

func manualCommandReplay(t *testing.T, b *ExecutionBinding, id, text string) StreamEvent {
	t.Helper()
	return lifecycleMessage(t, b, "user", map[string]any{"uuid": id, "parent_tool_use_id": nil, "timestamp": "2026-09-26T00:00:00Z", "isReplay": true, "message": map[string]any{"role": "user", "content": text}})
}

func TestManualCompactionSeparatesNativeStatusFromInputAndOuterSuccess(t *testing.T) {
	for _, failed := range []bool{false, true} {
		s, f := sessionFixture(t)
		original := s.current
		terminal := *original.terminal
		action := domain.NewID()
		if _, err := s.StartCompaction(context.Background(), action); err != nil {
			t.Fatal(err)
		}
		if f.sends.Load() != 1 || f.reads.Load() != 1 {
			t.Fatal("compaction did not claim one native send and fresh settings read")
		}
		for _, event := range manualCompactionEvents(t, s.compaction.core, failed) {
			f.events <- event
			value, err := s.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if value.ActionID != action || value.InputID != "" || value.Accepted || value.Result != nil || s.current != original || *original.terminal != terminal {
				t.Fatal("native compaction became a conversation input or outcome")
			}
			if value.CompactResult != nil {
				if value.CompactResult.Error || (value.CompactResult.Status == CompactFailed) != failed || value.CompactResult.Kind != ResultSuccess {
					t.Fatal("outer native success erased compaction failure")
				}
			}
			raw, _ := json.Marshal(value)
			if bytes.Contains(raw, []byte("Private")) {
				t.Fatal("native command data entered ordinary JSON")
			}
		}
		if !s.compaction.settled || s.compaction.core.accepted || s.compaction.core.terminal != nil {
			t.Fatal("action invented original-input acceptance or terminal reason")
		}
		if _, err := s.StartCompaction(context.Background(), action); err == nil {
			t.Fatal("compaction action replayed")
		}
		intent := ContinueSuccessfulRun
		if failed {
			if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, intent); err == nil {
				t.Fatal("failed compaction implicitly advanced native input")
			}
			intent = ResumeTerminalRun
		}
		if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, intent); err != nil {
			t.Fatal(err)
		}
		if s.compaction != nil || f.sends.Load() != 2 || s.current.input == original.input {
			t.Fatal("settled native compaction blocked or duplicated next input")
		}
	}
}

func TestManualCompactionRejectsForeignChangedAndIncompleteNativeLifecycle(t *testing.T) {
	for _, name := range []string{"foreign-session", "duplicate-id", "wrong-command", "early-init", "early-status", "duplicate-status", "wrong-status", "permission-change", "wrong-trigger", "duplicate-boundary", "wrong-summary", "replay-summary", "input-replay-during-summary", "missing-summary", "wrong-echo", "foreign-echo", "duplicate-echo", "unknown-output", "foreign-parent", "missing-parent", "input-result-id", "fabricated-terminal-reason", "wrong-result", "error-result", "provider-turn", "api-duration", "early-completion", "early-idle", "unknown-event", "real-provider-diagnostic", "changed-diagnostic-result", "result-before-diagnostic"} {
		t.Run(name, func(t *testing.T) {
			core, _ := lifecycleFixture(t)
			b := &manualCompactionBinding{core: core}
			failed := name == "real-provider-diagnostic" || name == "changed-diagnostic-result" || name == "result-before-diagnostic"
			events := manualCompactionEvents(t, core, failed)
			bad := len(events) - 3
			switch name {
			case "foreign-session":
				bad = 0
				events[bad] = lifecycleChange(t, events[bad], "session_id", domain.NewID())
			case "duplicate-id":
				bad = 2
				var h struct {
					ID string `json:"uuid"`
				}
				_ = json.Unmarshal(events[1].Body, &h)
				events[bad] = lifecycleChange(t, events[bad], "uuid", h.ID)
			case "wrong-command":
				bad = 1
				events[bad] = lifecycleChange(t, events[bad], "command_uuid", domain.NewID())
			case "early-init":
				bad = 3
				events[bad] = events[5]
			case "early-status":
				bad = 1
				events[bad] = events[3]
			case "duplicate-status":
				bad = 4
				events[bad] = lifecycleChange(t, events[3], "uuid", string(domain.NewID()))
			case "wrong-status":
				bad = 3
				events[bad] = lifecycleChange(t, events[bad], "status", SessionRequesting)
			case "permission-change":
				bad = 3
				events[bad] = lifecycleChange(t, events[bad], "permissionMode", DefaultPermission)
			case "wrong-trigger":
				bad = 6
				events[bad] = compactionFixture(t, core, map[string]any{"trigger": AutomaticCompaction, "pre_tokens": 1})
			case "duplicate-boundary":
				bad = 8
				events[bad] = lifecycleChange(t, events[6], "uuid", string(domain.NewID()))
			case "wrong-summary":
				bad = 7
				events[bad] = lifecycleChange(t, events[bad], "uuid", string(domain.NewID()))
			case "replay-summary":
				bad = 7
				events[bad] = lifecycleChange(t, events[bad], "isReplay", true)
			case "input-replay-during-summary":
				bad = 7
				core.digest = sha256.Sum256([]byte(lifecycleFixtureText))
				events[bad] = lifecycleReplay(t, core)
			case "missing-summary":
				bad = 7
				events[bad] = events[8]
			case "wrong-echo":
				bad = 9
				events[bad] = manualCommandReplay(t, core, string(core.input), "<command-name>/clear</command-name><command-message>clear</command-message><command-args></command-args>")
			case "foreign-echo":
				bad = 9
				events[bad] = lifecycleChange(t, events[bad], "uuid", string(domain.NewID()))
			case "duplicate-echo":
				bad = 10
				events[bad] = events[9]
			case "unknown-output":
				bad = 8
				events[bad] = manualCommandReplay(t, core, string(domain.NewID()), "<arbitrary>Compacted</arbitrary>")
			case "foreign-parent":
				bad = 8
				events[bad] = lifecycleChange(t, events[bad], "parent_tool_use_id", "foreign")
			case "missing-parent":
				bad = 8
				events[bad] = lifecycleChange(t, events[bad], "parent_tool_use_id", nil)
			case "input-result-id":
				events[bad] = lifecycleChange(t, events[bad], "user_message_uuid", core.input)
			case "fabricated-terminal-reason":
				events[bad] = lifecycleChange(t, events[bad], "terminal_reason", Completed)
			case "wrong-result":
				events[bad] = lifecycleChange(t, events[bad], "result", "changed")
			case "error-result":
				events[bad] = lifecycleChange(t, events[bad], "is_error", true)
			case "provider-turn":
				events[bad] = lifecycleChange(t, events[bad], "num_turns", 1)
			case "api-duration":
				events[bad] = lifecycleChange(t, events[bad], "duration_api_ms", 1)
			case "early-completion":
				events[bad] = events[len(events)-2]
			case "early-idle":
				events[bad] = events[len(events)-1]
			case "unknown-event":
				bad = 6
				events[bad] = lifecycleMessage(t, core, "future-event", map[string]any{})
			case "real-provider-diagnostic":
				bad = 6
				var f map[string]any
				_ = json.Unmarshal(events[bad].Body, &f)
				f["message"].(map[string]any)["model"] = core.model
				events[bad].Body, _ = json.Marshal(f)
			case "changed-diagnostic-result":
				events[bad] = lifecycleChange(t, events[bad], "result", "changed diagnostic")
			case "result-before-diagnostic":
				bad = 6
				events[bad] = events[len(events)-3]
			}
			for i, event := range events {
				_, err := b.observe(event)
				if i < bad {
					if err != nil {
						t.Fatal("valid setup failed", i, err)
					}
					continue
				}
				if err == nil || b.problem == nil || b.settled {
					t.Fatal("invalid compaction established native completion", i)
				}
				if _, err := b.observe(events[len(events)-1]); err == nil {
					t.Fatal("later idle erased uncertainty")
				}
				break
			}
		})
	}
}

func TestNativeCommandFragmentValidationDoesNotScrapeHumanOutcome(t *testing.T) {
	for _, text := range []string{"<local-command-stdout>Any native text &amp; punctuation.</local-command-stdout>", " \n<local-command-stdout></local-command-stdout>\n"} {
		if _, err := nativeCommandElements(text, []string{"local-command-stdout"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"Compacted", "<local-command-stdout>done", "<local-command-stdout x=\"y\">done</local-command-stdout>", "<local-command-stdout><b>done</b></local-command-stdout>", "<!--comment--><local-command-stdout>done</local-command-stdout>", "<!DOCTYPE a><local-command-stdout>done</local-command-stdout>", "<local-command-stdout xmlns=\"other\">done</local-command-stdout>", "<local-command-stdout>done</local-command-stdout>other"} {
		if _, err := nativeCommandElements(text, []string{"local-command-stdout"}); err == nil {
			t.Fatal("unrecognized command fragment accepted")
		}
	}
}

func TestSessionCompactionGateAndUncertainSendNeverRetry(t *testing.T) {
	for _, name := range []string{"fresh", "running", "unfinished-run", "pending-task", "pending-summary", "callback", "permission", "reading", "duplicate", "native-id-collision", "capacity", "queued-event", "bad-settings", "send-failed"} {
		t.Run(name, func(t *testing.T) {
			s, f := sessionFixture(t)
			action := domain.NewID()
			switch name {
			case "fresh":
				s.current = nil
			case "running":
				s.current.runState = RunRunning
			case "unfinished-run":
				s.current.finished = false
			case "pending-task":
				s.current.tasks = map[string]nativeTaskState{"task": {status: TaskRunning}}
			case "pending-summary":
				s.current.pendingCompaction = &compactionSummaryBinding{anchor: string(domain.NewID())}
			case "callback":
				s.current.interactions = map[domain.ID]*interactionState{domain.NewID(): {}}
			case "permission":
				s.permissionChanged = true
			case "reading":
				s.reading = true
			case "duplicate":
				action = s.current.input
			case "native-id-collision":
				s.current.seen[string(action)] = true
			case "capacity":
				for i := 0; i < maxStreamIdentities; i++ {
					s.inputs[domain.NewID()] = true
				}
			case "queued-event":
				f.barrier = sessionBusy()
			case "bad-settings":
				f.applied.Model = "changed"
			case "send-failed":
				f.sendError = streamUncertain()
			}
			if _, err := s.StartCompaction(context.Background(), action); err == nil {
				t.Fatal("ineligible native compaction started")
			}
			if name == "send-failed" || name == "bad-settings" {
				if s.problem == nil {
					t.Fatal("uncertainty did not latch")
				}
				reads, sends := f.reads.Load(), f.sends.Load()
				if _, err := s.StartCompaction(context.Background(), domain.NewID()); err == nil || f.reads.Load() != reads || f.sends.Load() != sends {
					t.Fatal("uncertain compaction replayed")
				}
			} else if f.sends.Load() != 0 {
				t.Fatal("gate transmitted a native command")
			}
		})
	}
}

func TestCompactionFailureDiagnosticIsPrivateAndCannotBeMutatedIntoSuccess(t *testing.T) {
	core, _ := lifecycleFixture(t)
	b := &manualCompactionBinding{core: core}
	for _, event := range manualCompactionEvents(t, core, true) {
		observed, err := b.observe(event)
		if err != nil {
			t.Fatal(err)
		}
		if observed.CompactCommand != nil && observed.CompactCommand.Diagnostic != nil {
			*observed.CompactCommand.Diagnostic.Blocks[0].Text = "Changed by display"
		}
		if observed.CompactResult != nil && (!strings.Contains(observed.CompactResult.Text, "Private") || observed.CompactResult.Status != CompactFailed) {
			t.Fatal("display mutation changed original diagnostic or native outcome")
		}
	}
}

func TestCompactionLateCanceledReplyKeepsOriginalInputOwner(t *testing.T) {
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
	if _, err := s.StartCompaction(context.Background(), domain.NewID()); err != nil {
		t.Fatal(err)
	}
	f.events <- interactionEcho(event, reply)
	observation, err := s.Next(context.Background())
	if err != nil || observation.InputID != originalInput || observation.TurnID != originalTurn || observation.Interaction == nil || !observation.Interaction.Canceled || observation.ActionID != "" || s.current.input != originalInput || s.compaction == nil {
		t.Fatal("late echo borrowed new input ownership", err)
	}
}

func TestManualCompactionAfterSettledFailureKeepsExplicitResumeRequirement(t *testing.T) {
	s, f := sessionFixture(t)
	s.current.terminal.Error = true
	original := s.current
	if _, err := s.StartCompaction(context.Background(), domain.NewID()); err != nil {
		t.Fatal(err)
	}
	for _, event := range manualCompactionEvents(t, s.compaction.core, false) {
		f.events <- event
		if _, err := s.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if s.current != original || s.current.terminal.Successful() {
		t.Fatal("compaction erased original failed input")
	}
	if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ContinueSuccessfulRun); err == nil {
		t.Fatal("compaction implicitly resumed original failure")
	}
	if _, err := s.SendInput(context.Background(), domain.NewID(), lifecycleFixtureText, ResumeTerminalRun); err != nil {
		t.Fatal(err)
	}
}
