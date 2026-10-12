// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeShellRejectsActualInitializedVersionBeforeClaim(t *testing.T) {
	config := fixtureConfig(t, "thread-ready")
	config.Mode = ThreadProtocol
	config.Version = NativeShellVersion
	config.NativeShell = true
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_VERSION_FIXTURE=0.163.0")
	client, err := Open(context.Background(), config)
	if client != nil || err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("configured version substituted for the initialized native shell profile", err)
	}
}

// This fixture records the native request before losing or corrupting its
// acknowledgment. It does not execute a real shell or prove platform cleanup.
func (f *threadFixture) handleNativeShell(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(f.mode, "thread-revert-native-shell-") || method != "thread/shellCommand" && method != "turn/interrupt" {
		return false
	}
	out, err := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(90)
	}
	json.NewEncoder(out).Encode(map[string]any{"method": method, "params": raw})
	out.Close()
	if method == "turn/interrupt" {
		var params struct {
			Thread domain.ID `json:"threadId"`
			Turn   domain.ID `json:"turnId"`
		}
		if domain.Decode(raw, &params) != nil || params.Thread != f.thread["id"] || params.Turn != f.turn {
			os.Exit(91)
		}
		write(id, map[string]any{})
		f.finishNativeShellFixture(true)
		return true
	}
	if strings.HasSuffix(f.mode, "lost") {
		os.Exit(0)
	}
	if strings.HasSuffix(f.mode, "malformed") {
		write(id, map[string]any{"unexpected": true})
		return true
	}
	write(id, map[string]any{})
	f.turn = domain.NewID()
	f.notify("turn/started", map[string]any{"threadId": f.thread["id"], "turn": fixtureTurn(f.turn, TurnRunning)})
	item := f.nativeShellItemFixture(false, false)
	f.notify("item/started", map[string]any{"threadId": f.thread["id"], "turnId": f.turn, "item": item, "startedAtMs": 1})
	if strings.HasSuffix(f.mode, "ready") {
		f.finishNativeShellFixture(false)
	}
	return true
}
func (f *threadFixture) nativeShellItemFixture(completed, canceled bool) map[string]any {
	item := commandFixture()
	item["source"], item["cwd"], item["command"] = "userShell", f.thread["cwd"], "/bin/sh -lc fixture-script"
	if completed {
		item["status"], item["exitCode"], item["durationMs"], item["aggregatedOutput"] = "failed", 17, 8, "native aggregate"
		if canceled {
			item["exitCode"] = -1
		}
	}
	return item
}
func (f *threadFixture) finishNativeShellFixture(canceled bool) {
	f.notify("item/commandExecution/outputDelta", map[string]any{"threadId": f.thread["id"], "turnId": f.turn, "itemId": "native-command", "delta": "original output\n"})
	item := f.nativeShellItemFixture(true, canceled)
	f.notify("item/completed", map[string]any{"threadId": f.thread["id"], "turnId": f.turn, "item": item, "completedAtMs": 9})
	status := TurnCompleted
	if canceled {
		status = TurnInterrupted
	}
	turn := fixtureTurn(f.turn, status)
	turn["itemsView"], turn["items"] = "full", []any{item}
	var page struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(f.history, &page) != nil {
		os.Exit(92)
	}
	last, _ := json.Marshal(turn)
	page.Data = append(page.Data, last)
	f.history, _ = json.Marshal(map[string]any{"data": page.Data, "nextCursor": nil, "backwardsCursor": nil})
	f.thread["status"] = map[string]any{"type": "idle"}
	f.notify("turn/completed", map[string]any{"threadId": f.thread["id"], "turn": fixtureTurn(f.turn, status)})
}

func TestNativeShellLostOrMalformedAcknowledgmentCannotResend(t *testing.T) {
	for _, mode := range []string{"lost", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, source, _, _ := revertFixture(t, "native-shell-"+mode)
			before, _ := json.Marshal(source)
			action := domain.NewID()
			timeout := uint64(37)
			command := domain.NativeShellCommand{Command: "  printf 'exact bytes\\n'  ", FullAccess: true, Actor: domain.Principal{Type: domain.OwnerDevice}, TimeoutMS: &timeout}
			err := c.StartNativeShell(context.Background(), action, command, source)
			if err == nil {
				t.Fatal("lost or malformed reply authorized certainty")
			}
			if err := c.StartNativeShell(context.Background(), action, command, source); err == nil {
				t.Fatal("uncertain command was sent again")
			}
			requests := requestsOf(t, capture, "thread/shellCommand")
			if len(requests) != 1 || requests[0]["command"] != command.Command {
				t.Fatal("exact once-only native request changed")
			}
			after, _ := json.Marshal(source)
			if !reflect.DeepEqual(before, after) || len(requestsOf(t, capture, "thread/compact/start")) != 0 || len(requestsOf(t, capture, "turn/start")) != 0 {
				t.Fatal("shell rewrote source or dispatched ordinary/compaction input")
			}
		})
	}
}

func nativeShellObservationFixture() (*Client, domain.ID) {
	c, original := observationClient()
	c.ownerID = domain.NewID()
	c.execution.active = ""
	c.execution.turns[original] = trackedTurn{Turn: Turn{ID: original, Status: TurnCompleted}, Mode: domain.PlanMode}
	c.execution.shell = &nativeShellAttempt{observation: domain.NativeShellObservation{ActionID: domain.NewID(), SessionID: domain.NewID(), ExecutionID: c.ownerID, NativeThreadID: domain.NativeIdentity(c.thread), State: domain.NativeShellClaimed, Sequence: 1, Acknowledged: true}}
	return c, domain.NewID()
}

func TestNativeShellLifecyclePreservesExitAndOriginalExecution(t *testing.T) {
	c, turn := nativeShellObservationFixture()
	old, _ := json.Marshal(c.execution.turns)
	if e, err := observeFixture(c, "turn/started", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnRunning)}); err != nil || e.Kind != MetadataEvent {
		t.Fatal("shell turn not bound", err)
	}
	item := commandFixture()
	item["source"] = "userShell"
	// Codex displays shell-derived argv, which is not the accepted raw script.
	item["command"] = "/bin/sh -lc \"printf hello\""
	e, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "startedAtMs": 1})
	if err != nil || e.Kind != NativeShellEvent || e.NativeShell.State != domain.NativeShellRunning || e.NativeShell.NativeProcessID != nil {
		t.Fatal("native item did not retain its independent identity", err)
	}
	e, err = observeFixture(c, "item/commandExecution/outputDelta", map[string]any{"threadId": c.thread, "turnId": turn, "itemId": item["id"], "delta": "partial 한글\n"})
	if err != nil || e.NativeShell.Output != "partial 한글\n" {
		t.Fatal("typed stream lost output", err)
	}
	item["status"], item["exitCode"], item["durationMs"], item["aggregatedOutput"] = "failed", 17, 8, "independent bounded aggregate"
	e, err = observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "completedAtMs": 9})
	if err != nil || *e.NativeShell.ExitCode != 17 || *e.NativeShell.AggregatedOutput != "independent bounded aggregate" || c.execution.shell.terminal {
		t.Fatal("item exit incorrectly completed its turn", err)
	}
	e, err = observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
	if err != nil || e.NativeShell.State != domain.NativeShellExited || !c.execution.shell.terminal || e.NativeShell.CleanupVerified {
		t.Fatal("terminal turn invented process cleanup", err)
	}
	current, _ := json.Marshal(c.execution.turns)
	if !reflect.DeepEqual(old, current) || c.execution.contextBase != nil || c.execution.compaction != nil || c.execution.active != "" {
		t.Fatal("shell changed ordinary checkpoint/context/compaction authority")
	}
}

func TestNativeShellRejectsAgentForeignDuplicateAndOversizedEvents(t *testing.T) {
	for _, scenario := range []string{"agent", "foreign-thread", "foreign-turn", "duplicate", "oversized", "extra-envelope", "negative-time", "changed-command"} {
		t.Run(scenario, func(t *testing.T) {
			c, turn := nativeShellObservationFixture()
			if _, err := observeFixture(c, "turn/started", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnRunning)}); err != nil {
				t.Fatal(err)
			}
			item := commandFixture()
			item["source"] = "userShell"
			params := map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "startedAtMs": 1}
			switch scenario {
			case "agent":
				item["source"] = "agent"
			case "foreign-thread":
				params["threadId"] = domain.NewID()
			case "foreign-turn":
				params["turnId"] = domain.NewID()
			case "extra-envelope":
				params["arbitrary"] = true
			case "negative-time":
				params["startedAtMs"] = -1
			}
			_, err := observeFixture(c, "item/started", params)
			switch scenario {
			case "duplicate":
				_, err = observeFixture(c, "item/started", params)
			case "oversized":
				_, err = observeFixture(c, "item/commandExecution/outputDelta", map[string]any{"threadId": c.thread, "turnId": turn, "itemId": item["id"], "delta": strings.Repeat("x", domain.MaxNativeShellOutput+1)})
			case "changed-command":
				item["command"], item["status"], item["exitCode"], item["durationMs"], item["aggregatedOutput"] = "replacement", "completed", 0, 1, ""
				_, err = observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "completedAtMs": 2})
			}
			if err == nil {
				t.Fatal("unowned or incompatible native observation accepted")
			}
		})
	}
}

func TestNativeShellScriptedRunAndOriginalTurnCancellation(t *testing.T) {
	for _, mode := range []string{"ready", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, source, _, _ := revertFixture(t, "native-shell-"+mode)
			original, _ := json.Marshal(source)
			action := domain.NewID()
			if err := c.StartNativeShell(context.Background(), action, domain.NativeShellCommand{Command: "exact original script", FullAccess: true, Actor: domain.Principal{Type: domain.OwnerDevice}}, source); err != nil {
				t.Fatal(err)
			}
			nextKind(t, c, MetadataEvent)
			started := nextKind(t, c, NativeShellEvent)
			if started.NativeShell.State != domain.NativeShellRunning {
				t.Fatal("scripted native process did not start")
			}
			if mode == "cancel" {
				if err := c.CancelNativeShell(context.Background(), domain.NewID()); err != nil {
					t.Fatal(err)
				}
			}
			var terminal Event
			for {
				terminal = nextKind(t, c, NativeShellEvent)
				if c.NativeShellTerminal() {
					break
				}
			}
			expected, exit := domain.NativeShellExited, int32(17)
			if mode == "cancel" {
				expected, exit = domain.NativeShellCanceled, -1
			}
			if terminal.NativeShell.State != expected || terminal.NativeShell.ExitCode == nil || *terminal.NativeShell.ExitCode != exit || terminal.NativeShell.Output != "original output\n" || terminal.NativeShell.CleanupVerified {
				t.Fatal("original native outcome changed")
			}
			retained, err := c.RetainNativeShellCheckpoint(context.Background())
			if err != nil || retained.ActionID != action || retained.BeforeCount != 3 || retained.TurnsCount != 4 || retained.NativeTurnID != domain.ID(started.NativeShell.NativeTurnID) {
				t.Fatal("separate original shell history not retained", err)
			}
			after, _ := json.Marshal(source)
			if !reflect.DeepEqual(original, after) || len(requestsOf(t, capture, "thread/shellCommand")) != 1 || len(requestsOf(t, capture, "thread/compact/start")) != 0 {
				t.Fatal("scripted action rewrote or resent ordinary history")
			}
			interrupts := requestsOf(t, capture, "turn/interrupt")
			if mode == "cancel" && (len(interrupts) != 1 || interrupts[0]["turnId"] != string(started.NativeShell.NativeTurnID)) || mode == "ready" && len(interrupts) != 0 {
				t.Fatal("cancellation lost original native owner")
			}
			if err := c.CancelNativeShell(context.Background(), domain.NewID()); err == nil {
				t.Fatal("terminal native action admitted another interruption")
			}
		})
	}
}
