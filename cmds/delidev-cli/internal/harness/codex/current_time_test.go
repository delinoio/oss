// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func (f *threadFixture) currentTimeReply(id, raw json.RawMessage) bool {
	var result map[string]json.RawMessage
	if domain.Decode(raw, &result) != nil || len(result) != 1 || string(result["currentTimeAt"]) != "1700000000" {
		return false
	}
	out, err := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return false
	}
	defer out.Close()
	return json.NewEncoder(out).Encode(map[string]any{"serviceId": id, "serviceResult": result}) == nil
}

func currentTimeOriginal(t *testing.T, c *Client) nativewire.Event {
	t.Helper()
	fixtureSignal(t, c, "current-time", map[string]any{"requestId": 17, "params": map[string]any{"threadId": c.thread}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		event, err := c.wire.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Method == "currentTime/read" {
			return event
		}
	}
}

func TestCurrentTimeAutomaticResponseKeepsOriginalTurn(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "current-time")
	samples := 0
	c.timeServiceClock = func() time.Time { samples++; return time.Unix(1700000000, 987654321) }
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, TurnStartedEvent)
	nextKind(t, c, MessageCompletedEvent)
	beforeInputs := len(c.execution.inputs)
	fixtureSignal(t, c, "current-time", map[string]any{"requestId": 17, "params": map[string]any{"threadId": c.thread}})
	fixtureSignal(t, c, "started", map[string]any{})
	event := nextKind(t, c, TurnStartedEvent)
	if event.TurnID != turn.TurnID || c.execution.active != turn.TurnID || c.problem != nil || samples != 1 || len(c.execution.inputs) != beforeInputs || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("time service changed original turn/input/interaction authority")
	}
	fixtureSignal(t, c, "metadata", map[string]any{}) // Acknowledgment fences fixture receipt capture.
	responses := 0
	for _, row := range capturedThreads(t, capture) {
		if row["serviceId"] != nil {
			responses++
			if row["serviceId"] != float64(17) || row["serviceResult"].(map[string]any)["currentTimeAt"] != float64(1700000000) {
				t.Fatal("wrong original time reply", row)
			}
		}
	}
	if responses != 1 {
		t.Fatal("original request did not get one reply", responses)
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	if nextKind(t, c, TurnCompletedEvent).TurnID != turn.TurnID {
		t.Fatal("ordinary original turn failed after time response")
	}
}

func TestCurrentTimeRetainsOnceOnlyOriginalArrival(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "current-time")
	native := currentTimeOriginal(t, c)
	samples := 0
	c.timeServiceClock = func() time.Time { samples++; return time.Unix(1700000000, 1) }
	if err := c.answerCurrentTimeLocked(context.Background(), native, c.wire.Reply); err != nil {
		t.Fatal(err)
	}
	for _, replay := range []nativewire.Event{native, func() nativewire.Event { changed := native; changed.Token = domain.NewID(); return changed }()} {
		if err := c.answerCurrentTimeLocked(context.Background(), replay, c.wire.Reply); domain.SafeError(err).Code != domain.Conflict {
			t.Fatal("replayed/replaced request sent again", err)
		}
	}
	fixtureSignal(t, c, "metadata", map[string]any{})
	count := 0
	for _, row := range capturedThreads(t, capture) {
		if row["serviceResult"] != nil {
			count++
		}
	}
	if count != 1 || samples != 1 {
		t.Fatal("duplicate time sample or write", samples, count)
	}
}

func TestCurrentTimeRejectsWrongScopeAndCompleteParams(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "current-time")
	native := currentTimeOriginal(t, c)
	samples := 0
	c.timeServiceClock = func() time.Time { samples++; return time.Unix(1700000000, 0) }
	for _, raw := range []string{`{}`, `{"threadId":null}`, `{"threadId":""}`, `{"threadId":"` + string(domain.NewID()) + `"}`, `{"threadId":"` + string(c.thread) + `","extra":true}`, `{"threadId":"` + string(c.thread) + `","threadId":"` + string(c.thread) + `"}`, strings.Repeat(" ", 4097) + `{}`} {
		changed := native
		changed.Params = json.RawMessage(raw)
		if err := c.answerCurrentTimeLocked(context.Background(), changed, c.wire.Reply); err == nil {
			t.Fatal("malformed/foreign request accepted", raw)
		}
	}
	changed := native
	changed.Kind = nativewire.Notification
	if err := c.answerCurrentTimeLocked(context.Background(), changed, c.wire.Reply); err == nil {
		t.Fatal("notification answered")
	}
	changed = native
	changed.ID = json.RawMessage(`17.0`)
	if err := c.answerCurrentTimeLocked(context.Background(), changed, c.wire.Reply); err == nil {
		t.Fatal("wrong request ID accepted")
	}
	if samples != 0 || len(c.timeRequests) != 0 || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("refused request mutated ownership")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.answerCurrentTimeLocked(context.Background(), native, c.wire.Reply); err == nil || samples != 0 {
		t.Fatal("stopped native connection answered", err)
	}
}

func TestCurrentTimeUncertainResponseNeverResamplesOrResends(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "current-time")
	native := currentTimeOriginal(t, c)
	samples, writes := 0, 0
	c.timeServiceClock = func() time.Time { samples++; return time.Unix(1700000000, 77) }
	// Model lost delivery evidence after the real original correlated pipe write.
	// This fixture is not proof of a native ten-second acceptance acknowledgment.
	reply := func(ctx context.Context, event nativewire.Event, result any) error {
		writes++
		if err := c.wire.Reply(ctx, event, result); err != nil {
			return err
		}
		return domain.Fail(domain.RecoveryRequired, "Fixture delivery evidence lost.", "Reconcile original connection.")
	}
	if err := c.answerCurrentTimeLocked(context.Background(), native, reply); domain.SafeError(err).Code != domain.RecoveryRequired || c.problem == nil || !c.execution.paused {
		t.Fatal("delivery uncertainty lost recovery", err)
	}
	if err := c.answerCurrentTimeLocked(context.Background(), native, reply); err == nil || samples != 1 || writes != 1 {
		t.Fatal("uncertain request was sampled/sent again", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal("original cleanup did not complete independently", err)
	}
}
