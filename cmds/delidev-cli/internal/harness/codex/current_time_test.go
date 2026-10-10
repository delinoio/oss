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
	c.currentTimeEnabled = true
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

func TestCurrentTimeProfileOverridesAreClosedToDirectThreads(t *testing.T) {
	config := Config{EnableCurrentTime: true, Mode: ThreadProtocol}
	enabled, err := configureCurrentTime(&config)
	if err != nil || !enabled {
		t.Fatal(enabled, err)
	}
	want := []string{"-c", "features.current_time_reminder.enabled=true", "-c", `features.current_time_reminder.clock_source="external"`}
	if len(config.Process.Args) != len(want) {
		t.Fatal(config.Process.Args)
	}
	for index, value := range want {
		if config.Process.Args[index] != value {
			t.Fatal(config.Process.Args)
		}
	}
	disabled := Config{Mode: ThreadProtocol}
	if enabled, err := configureCurrentTime(&disabled); err != nil || enabled || len(disabled.Process.Args) != 0 {
		t.Fatal("an unselected profile received current-time overrides", enabled, err)
	}

	for _, excluded := range []Config{
		{EnableCurrentTime: true, Mode: ProbeProtocol},
		{EnableCurrentTime: true, Mode: ThreadProtocol, Sidechat: ReadOnlySidechatV1},
		{EnableCurrentTime: true, Mode: ThreadProtocol, API: &APIConfig{TitleProfile: true}},
		{EnableCurrentTime: true, Mode: ProbeProtocol, ModelObservation: true},
	} {
		if enabled, err := configureCurrentTime(&excluded); err == nil || enabled || len(excluded.Process.Args) != 0 {
			t.Fatal("ineligible profile received current-time overrides", excluded, enabled, err)
		}
	}
}

func TestCurrentTimeProfileVerificationRequiresExternalEffectiveConfig(t *testing.T) {
	for _, config := range []map[string]json.RawMessage{
		{"features": json.RawMessage(`{"current_time_reminder":{"enabled":true,"clock_source":"external"}}`)},
	} {
		if !currentTimeConfigEnabled(config) {
			t.Fatal("accepted current-time profile was rejected")
		}
	}
	for _, config := range []map[string]json.RawMessage{
		{},
		{"features": json.RawMessage(`{}`)},
		{"features": json.RawMessage(`{"current_time_reminder":{"enabled":false,"clock_source":"external"}}`)},
		{"features": json.RawMessage(`{"current_time_reminder":{"enabled":true,"clock_source":"system"}}`)},
		{"features": json.RawMessage(`{"current_time_reminder":{"enabled":true}}`)},
		{"features": json.RawMessage(`{"current_time_reminder":{"enabled":true,"enabled":false,"clock_source":"external"}}`)},
	} {
		if currentTimeConfigEnabled(config) {
			t.Fatal("accepted unconfigured current-time profile")
		}
	}
	if !currentTimeFeatureEnabled(json.RawMessage(`{"data":[{"name":"current_time_reminder","enabled":true}],"nextCursor":null}`)) {
		t.Fatal("accepted thread-scoped current-time feature was rejected")
	}
	for _, raw := range []string{
		`{"data":[],"nextCursor":null}`,
		`{"data":[{"name":"current_time_reminder","enabled":false}],"nextCursor":null}`,
		`{"data":[{"name":"current_time_reminder","enabled":true},{"name":"current_time_reminder","enabled":true}],"nextCursor":null}`,
		`{"data":[{"name":"current_time_reminder","enabled":true}],"nextCursor":"more"}`,
		`{"data":[{"name":"current_time_reminder","enabled":true,"enabled":false}],"nextCursor":null}`,
	} {
		if currentTimeFeatureEnabled(json.RawMessage(raw)) {
			t.Fatal("accepted unverified thread-scoped current-time feature", raw)
		}
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

func TestCurrentTimeProfileMismatchBlocksNativeInput(t *testing.T) {
	for _, mode := range []string{"thread-turn-current-time-bad-config", "thread-turn-current-time-bad-feature"} {
		t.Run(mode, func(t *testing.T) {
			c, capture := openThreadFixture(t, mode)
			settings := threadSettings(t)
			if _, err := c.StartThread(context.Background(), domain.NewID(), settings); err != nil {
				t.Fatal(err)
			}
			_, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, domain.Unsupported)
			if len(requestsOf(t, capture, "turn/start")) != 0 {
				t.Fatal("unverified current-time profile sent native input")
			}
		})
	}
}
