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

// This fixture records the native request before losing or corrupting its
// acknowledgment. It does not execute a real shell or prove platform cleanup.
func (f *threadFixture) handleNativeShell(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(f.mode, "thread-revert-native-shell-") || method != "thread/shellCommand" {
		return false
	}
	out, err := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(90)
	}
	json.NewEncoder(out).Encode(map[string]any{"method": method, "params": raw})
	out.Close()
	if strings.HasSuffix(f.mode, "lost") {
		os.Exit(0)
	}
	write(id, map[string]any{"unexpected": true})
	return true
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
