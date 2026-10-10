// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestCurrentTimeParamsRetainExactRootOwnership(t *testing.T) {
	c, turn := observationClient()
	token := domain.NewID()
	valid := `{"threadId":"` + string(c.thread) + `"}`
	for _, raw := range []string{`{}`, `null`, `{"threadId":null}`, `{"threadId":""}`, `{"threadId":"` + string(c.thread) + `","unknown":true}`, `{"threadId":"` + string(c.thread) + `","threadId":"` + string(c.thread) + `"}`} {
		if owned, err := c.currentTimeRequest(nativewire.Event{Kind: nativewire.ServerRequest, Method: "currentTime/read", ID: json.RawMessage(`17`), Token: token, Params: json.RawMessage(raw)}); err == nil || owned {
			t.Fatal("invalid params gained ownership", raw)
		}
	}
	event := nativewire.Event{Kind: nativewire.ServerRequest, Method: "currentTime/read", ID: json.RawMessage(`17`), Token: token, Params: json.RawMessage(valid)}
	if owned, err := c.currentTimeRequest(event); err != nil || !owned {
		t.Fatal(err)
	}
	invalid := event
	invalid.Token = ""
	if owned, err := c.currentTimeRequest(invalid); err == nil || owned {
		t.Fatal("missing token gained reply")
	}
	invalid = event
	invalid.ID = json.RawMessage(`null`)
	if owned, err := c.currentTimeRequest(invalid); err == nil || owned {
		t.Fatal("invalid native ID gained reply")
	}
	event.Kind = nativewire.Notification
	if owned, err := c.currentTimeRequest(event); err != nil || owned {
		t.Fatal("notification gained reply")
	}
	event.Kind = nativewire.ServerRequest
	event.Params = json.RawMessage(`{"threadId":"` + string(domain.NewID()) + `"}`)
	if owned, err := c.currentTimeRequest(event); err != nil || owned {
		t.Fatal("foreign thread gained reply")
	}
	if c.execution.active != turn || c.execution.paused || c.problem != nil || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("validation changed execution/interaction authority")
	}
}
func TestCurrentTimeReplyLeavesOriginalTurnRunning(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "current-time")
	started, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Unix(1700000000, 999999999) }
	fixtureSignal(t, c, "current-time", map[string]any{})
	event := nextKind(t, c, CurrentTimeRepliedEvent)
	if !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" || event.Interaction != nil || event.Native != nil || samples != 1 {
		t.Fatal("technical reply acquired turn authority", event, samples)
	}
	nextKind(t, c, MetadataEvent)
	raw, err := os.ReadFile(capture + ".current-time")
	if err != nil || string(raw) != `{"currentTimeAt":1700000000}` {
		t.Fatal(string(raw), err)
	}
	if c.execution.active != started.TurnID || c.execution.paused || c.problem != nil || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("clock response changed original execution")
	}
	fixtureSignal(t, c, "delta", map[string]any{})
	nextKind(t, c, TextDeltaEvent)
	if samples != 1 {
		t.Fatal("ordinary turn resampled clock")
	}
}
