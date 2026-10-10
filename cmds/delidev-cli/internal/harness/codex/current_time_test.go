// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// Controlled test process only: never starts an installed native harness.
func init() {
	if len(os.Args) != 6 || os.Args[1] != "__delidev_time_fixture" {
		return
	}
	capture, thread, turn, mode := os.Args[2], os.Args[3], os.Args[4], os.Args[5]
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var frame struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			os.Exit(3)
		}
		if frame.Method == "emit" {
			_, _ = os.Stdout.Write(append(frame.Params, '\n'))
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": frame.ID, "result": map[string]any{}})
			if mode == "closed-input" {
				_ = os.Stdin.Close()
				_ = os.WriteFile(capture+".closed", nil, 0600)
				for {
					time.Sleep(time.Second)
				}
			}
		} else if frame.Method == "" {
			_ = os.WriteFile(capture, scanner.Bytes(), 0600)
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": thread, "turn": fixtureTurn(domain.ID(turn), TurnRunning)}})
		}
	}
	os.Exit(0)
}

func currentTimeFixture(t *testing.T, mode string) (*Client, string, domain.ID) {
	t.Helper()
	c, turn := observationClient()
	c.execution.paused = false
	root := t.TempDir()
	capture := filepath.Join(root, "reply.json")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c.control = make(chan struct{}, 1)
	c.eventGate = make(chan struct{}, 1)
	c.wire, err = nativewire.Start(context.Background(), process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: exe, Args: []string{"__delidev_time_fixture", capture, string(c.thread), string(turn), mode}, Cwd: root, Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c, capture, turn
}
func emitCurrentTime(t *testing.T, c *Client, raw string) nativewire.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.wire.Call(ctx, domain.NewID(), "emit", json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	event, err := c.wire.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
func TestCurrentTimeReplyConsumesServiceAndContinuesOriginalTurn(t *testing.T) {
	c, path, turn := currentTimeFixture(t, "normal")
	samples := 0
	c.workerClock = func() time.Time { samples++; return time.Unix(1700000000, 999999999) }
	native := emitCurrentTime(t, c, `{"id":17,"method":"currentTime/read","params":{"threadId":"`+string(c.thread)+`"}}`)
	c.pendingEvent = &native
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	event, err := c.NextEvent(ctx)
	if err != nil || event.Kind != TurnStartedEvent || event.TurnID != turn || event.Interaction != nil || samples != 1 {
		t.Fatalf("service interrupted original turn: %+v %v samples=%d", event, err, samples)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"id":17,"result":{"currentTimeAt":1700000000}}` {
		t.Fatalf("wrong original reply: %s", raw)
	}
	if c.execution.active != turn || c.execution.paused || c.problem != nil || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("clock reply changed execution authority")
	}
	for _, replay := range []nativewire.Event{native, func() nativewire.Event { copy := native; copy.Token = domain.NewID(); return copy }()} {
		if _, err := c.replyCurrentTimeLocked(ctx, replay); domain.SafeError(err).Code != domain.Conflict {
			t.Fatal("answered/replaced arrival gained reply", err)
		}
	}
	if samples != 1 {
		t.Fatal("replayed reply resampled clock")
	}
}
func TestCurrentTimeReplyRejectsUnownedOrMalformedRequests(t *testing.T) {
	c, path, _ := currentTimeFixture(t, "normal")
	samples := 0
	c.workerClock = func() time.Time { samples++; return time.Now() }
	for _, params := range []string{`{}`, `{"threadId":null}`, `{"threadId":"` + string(c.thread) + `","extra":null}`, `{"threadId":"` + string(c.thread) + `","threadId":"` + string(c.thread) + `"}`, `{"threadId":"` + string(domain.NewID()) + `"}`} {
		event := nativewire.Event{Kind: nativewire.ServerRequest, Method: "currentTime/read", ID: json.RawMessage(`17`), Token: domain.NewID(), Params: json.RawMessage(params)}
		result, err := c.replyCurrentTimeLocked(context.Background(), event)
		if err == nil && result.Kind != NativeExtensionEvent {
			t.Fatal("unowned request succeeded")
		}
	}
	for _, kind := range []nativewire.EventKind{nativewire.Notification, nativewire.LateResponse} {
		if _, err := c.replyCurrentTimeLocked(context.Background(), nativewire.Event{Kind: kind, Method: "currentTime/read", Token: domain.NewID(), Params: json.RawMessage(`{"threadId":"` + string(c.thread) + `"}`)}); err == nil {
			t.Fatal("wrong envelope answered")
		}
	}
	for _, id := range []string{"null", "1.5", "true", "{}", "\"\""} {
		if _, err := c.replyCurrentTimeLocked(context.Background(), nativewire.Event{Kind: nativewire.ServerRequest, Method: "currentTime/read", ID: json.RawMessage(id), Token: domain.NewID(), Params: json.RawMessage(`{"threadId":"` + string(c.thread) + `"}`)}); err == nil {
			t.Fatal("invalid identity answered")
		}
	}
	if samples != 0 {
		t.Fatal("unowned request sampled clock")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unowned request wrote reply")
	}
	if c.problem != nil || c.execution.paused || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("invalid service changed owner")
	}
}
func TestCurrentTimeReplyStoppedAndUncertainConnectionNeverResamples(t *testing.T) {
	for _, mode := range []string{"stopped", "closed-input"} {
		t.Run(mode, func(t *testing.T) {
			c, path, _ := currentTimeFixture(t, mode)
			samples := 0
			c.workerClock = func() time.Time { samples++; return time.Unix(1700000000, 1) }
			event := emitCurrentTime(t, c, `{"id":"original-time","method":"currentTime/read","params":{"threadId":"`+string(c.thread)+`"}}`)
			if mode == "stopped" {
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				deadline := time.Now().Add(time.Second)
				for {
					if _, err := os.Stat(path + ".closed"); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("fixture input did not close")
					}
					time.Sleep(time.Millisecond)
				}
			}
			c.pendingEvent = &event
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := c.NextEvent(ctx)
			if err == nil {
				t.Fatal("closed reply succeeded")
			}
			if mode == "closed-input" && (!c.execution.paused || c.problem == nil || domain.SafeError(err).Code != domain.RecoveryRequired || samples != 1) {
				t.Fatal("pipe uncertainty lost recovery", err, samples)
			}
			_, again := c.replyCurrentTimeLocked(ctx, event)
			if again == nil {
				t.Fatal("uncertain/stopped request replaced response")
			}
			if samples > 1 {
				t.Fatal("failed reply resampled clock")
			}
			if mode == "stopped" && samples != 0 {
				t.Fatal("stopped request sampled")
			}
			if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), "currentTimeAt") {
				t.Fatal("closed pipe recorded success")
			}
		})
	}
}
