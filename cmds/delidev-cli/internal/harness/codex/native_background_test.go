// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Transport fixtures model only the native API. They do not prove real native
// process exit, account acceptance, PTY survival, or platform cleanup.
func (f *threadFixture) handleBackground(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(f.mode, "thread-revert-background-") || method != "turn/start" && !strings.HasPrefix(method, "thread/backgroundTerminals/") {
		return false
	}
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		os.Exit(95)
	}
	out, e := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		os.Exit(96)
	}
	json.NewEncoder(out).Encode(map[string]any{"method": method, "params": params})
	out.Close()
	if params["threadId"] != string(f.thread["id"].(domain.ID)) {
		os.Exit(97)
	}
	switch method {
	case "turn/start":
		f.turn = domain.NewID()
		f.notify("turn/started", map[string]any{"threadId": f.thread["id"], "turn": fixtureTurn(f.turn, TurnRunning)})
		f.background = []map[string]any{}
		for n, process := range []string{"500", "501"} {
			item := commandFixture()
			item["id"], item["processId"], item["command"], item["cwd"], item["source"] = []string{"bg-one", "bg-two"}[n], process, "controlled-native-"+process, f.thread["cwd"], "unifiedExecStartup"
			f.notify("item/started", map[string]any{"threadId": f.thread["id"], "turnId": f.turn, "item": item, "startedAtMs": 1})
			f.background = append(f.background, map[string]any{"itemId": item["id"], "processId": process, "command": item["command"], "cwd": item["cwd"], "osPid": nil, "cpuPercent": nil, "rssKb": nil})
		}
		write(id, map[string]any{"turn": fixtureTurn(f.turn, TurnRunning)})
	case "thread/backgroundTerminals/list":
		data := f.background
		if strings.HasSuffix(f.mode, "foreign") {
			data = append(append([]map[string]any{}, data...), map[string]any{"itemId": "borrowed", "processId": "900", "command": "foreign", "cwd": f.thread["cwd"], "osPid": nil, "cpuPercent": nil, "rssKb": nil})
		}
		write(id, map[string]any{"data": data, "nextCursor": nil})
	case "thread/backgroundTerminals/terminate":
		remaining := []map[string]any{}
		terminated := false
		for _, t := range f.background {
			if t["processId"] == params["processId"] {
				terminated = true
			} else {
				remaining = append(remaining, t)
			}
		}
		f.background = remaining
		if strings.HasSuffix(f.mode, "lost") {
			os.Exit(0)
		}
		if strings.HasSuffix(f.mode, "malformed") {
			write(id, map[string]any{"unexpected": true})
		} else {
			write(id, map[string]any{"terminated": terminated})
		}
	case "thread/backgroundTerminals/clean":
		f.background = []map[string]any{}
		write(id, map[string]any{})
	}
	return true
}
func backgroundNativeFixture(t *testing.T, mode string) (*Client, string, []domain.NativeBackgroundTerminal) {
	t.Helper()
	c, capture := openThreadFixture(t, "thread-revert-background-"+mode)
	if _, e := c.StartThread(context.Background(), domain.NewID(), threadSettings(t)); e != nil {
		t.Fatal("background StartThread fixture", e)
	}
	if _, e := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), domain.SessionInput{Prompt: "controlled input", Mode: domain.ExecuteMode}); e != nil {
		t.Fatal("background StartTurn fixture", e)
	}
	for len(c.background) < 2 {
		if _, e := c.NextEvent(context.Background()); e != nil {
			t.Fatal("background NextEvent fixture", e)
		}
	}
	if mode == "foreign" {
		return c, capture, nil
	}
	inventory, e := c.InspectBackground(context.Background())
	if e != nil || len(inventory) != 2 {
		t.Fatal("original inventory unavailable", e)
	}
	return c, capture, inventory
}
func backgroundNativeAction(c *Client, op domain.NativeBackgroundOperation, targets []domain.NativeBackgroundTerminal) domain.NativeBackgroundAction {
	a := domain.NativeBackgroundAction{Version: 1, ActionType: domain.NativeBackgroundActionType, ActionID: domain.NewID(), SessionID: c.execution.thread.SessionID, JobID: c.ownerID, ExecutionID: domain.NewID(), NativeThreadID: domain.NativeIdentity(c.thread), InventorySequence: 1, Operation: op, Targets: targets, Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.NativeBackgroundClaimed, Claim: &domain.NativeShellClaim{RequestID: domain.NewID(), MachineID: domain.NewID(), InstanceID: domain.NewID(), DeviceID: domain.NewID()}}
	if op == domain.NativeBackgroundTerminate {
		a.ItemID, a.ProcessID = targets[0].ItemID, targets[0].ProcessID
	}
	return a
}
func TestNativeBackgroundTwoOriginalProcessesTerminateThenClean(t *testing.T) {
	c, capture, inventory := backgroundNativeFixture(t, "ready")
	a := backgroundNativeAction(c, domain.NativeBackgroundTerminate, inventory[:1])
	o, e := c.ControlBackground(context.Background(), a)
	if e != nil || !o.Acknowledged || o.Terminated == nil || !*o.Terminated || o.NativeEmpty {
		t.Fatal("terminate ACK became terminal/cleanup", e)
	}
	current, e := c.InspectBackground(context.Background())
	if e != nil || current[0].Present || !current[1].Present || current[0].ExitCode != nil {
		t.Fatal("terminate changed another process or fabricated exit", e)
	}
	clean := backgroundNativeAction(c, domain.NativeBackgroundClean, current[1:])
	if o, e = c.ControlBackground(context.Background(), clean); e != nil || !o.Acknowledged || o.NativeEmpty {
		t.Fatal("clean ACK fabricated cleanup", e)
	}
	current, e = c.InspectBackground(context.Background())
	if e != nil || current[0].Present || current[1].Present {
		t.Fatal("original native scope did not become empty", e)
	}
	calls := requestsOf(t, capture, "thread/backgroundTerminals/terminate")
	if len(calls) != 1 || calls[0]["processId"] != "500" || calls[0]["threadId"] != string(c.thread) {
		t.Fatal("terminate adopted a foreign or OS process")
	}
	if len(requestsOf(t, capture, "thread/backgroundTerminals/clean")) != 1 || len(requestsOf(t, capture, "thread/shellCommand")) != 0 {
		t.Fatal("clean used another native or product command")
	}
}
func TestNativeBackgroundStaleForeignAndLostAcknowledgmentNeverResend(t *testing.T) {
	c, capture, inventory := backgroundNativeFixture(t, "ready")
	stale := backgroundNativeAction(c, domain.NativeBackgroundTerminate, inventory[:1])
	stale.Targets = append([]domain.NativeBackgroundTerminal{}, stale.Targets...)
	stale.Targets[0].ProcessID = "999"
	stale.ProcessID = "999"
	if _, e := c.ControlBackground(context.Background(), stale); e == nil || len(requestsOf(t, capture, "thread/backgroundTerminals/terminate")) != 0 {
		t.Fatal("stale identity was sent")
	}
	for _, mode := range []string{"lost", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, inventory := backgroundNativeFixture(t, mode)
			a := backgroundNativeAction(c, domain.NativeBackgroundTerminate, inventory[:1])
			if _, e := c.ControlBackground(context.Background(), a); e == nil {
				t.Fatal("uncertain native ACK accepted")
			}
			if _, e := c.ControlBackground(context.Background(), a); e == nil {
				t.Fatal("duplicate reopened original send")
			}
			if len(requestsOf(t, capture, "thread/backgroundTerminals/terminate")) != 1 {
				t.Fatal("lost acknowledgment repeated native mutation")
			}
		})
	}
	foreign, _, _ := backgroundNativeFixture(t, "foreign")
	if _, e := foreign.InspectBackground(context.Background()); e == nil {
		t.Fatal("list membership adopted an unobserved process")
	}
}
func TestNativeBackgroundOutputBoundPreservesUTF8AndOriginalPrefix(t *testing.T) {
	terminal := domain.NativeBackgroundTerminal{}
	boundedBackgroundOutput(&terminal, strings.Repeat("한", domain.MaxNativeBackgroundOutput))
	before := terminal.Output
	boundedBackgroundOutput(&terminal, "replacement")
	if !terminal.OutputTruncated || !utf8.ValidString(terminal.Output) || len(terminal.Output) > domain.MaxNativeBackgroundOutput || !strings.HasPrefix(terminal.Output, before) {
		t.Fatal("bounded output replaced original bytes")
	}
}

func TestNativeBackgroundObservedAbsenceCannotReopenReusedIdentity(t *testing.T) {
	c, capture, inventory := backgroundNativeFixture(t, "ready")
	original := c.background[inventory[0].ItemID]
	original.Present = false
	c.background[inventory[0].ItemID] = original
	action := backgroundNativeAction(c, domain.NativeBackgroundTerminate, inventory[:1])
	if _, err := c.ControlBackground(context.Background(), action); err == nil {
		t.Fatal("absent identity was adopted again")
	}
	if len(requestsOf(t, capture, "thread/backgroundTerminals/terminate")) != 0 {
		t.Fatal("reused native identity received control")
	}
}
