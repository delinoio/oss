// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func windowsWarningFixture(paths []string, extra uint64, failed bool) map[string]any {
	return map[string]any{"samplePaths": paths, "extraCount": extra, "failedScan": failed}
}

func TestWindowsWarningCountsClassesAndReplayRemainBounded(t *testing.T) {
	for _, tc := range []struct {
		paths  []string
		extra  uint64
		failed bool
		class  windowsWarningClass
	}{
		{[]string{}, 0, false, windowsNoWarning},
		{[]string{""}, 0, false, windowsWorldWritable},
		{[]string{}, ^uint64(0), false, windowsWorldWritable},
		{[]string{}, 0, true, windowsScanFailed},
		{[]string{"private-native-path"}, ^uint64(0), true, windowsWorldWritableScanFailed},
		{make([]string, maxWindowsWarningPaths), 0, false, windowsWorldWritable},
		{[]string{strings.Repeat("p", maxWindowsSandboxText)}, 0, false, windowsWorldWritable},
	} {
		c, turn := observationClient()
		beforeSettings, state := c.execution.settings, c.execution
		beforeInputs, beforePending := len(state.inputs), len(state.pending)
		raw, _ := json.Marshal(windowsWarningFixture(tc.paths, tc.extra, tc.failed))
		observed, err := decodeWindowsWarning(raw)
		if err != nil || observed.class != tc.class || observed.samples != uint16(len(tc.paths)) || observed.extra != tc.extra {
			t.Fatal("native warning count or classification changed", err)
		}
		if encoded, _ := json.Marshal(observed); string(encoded) != "{}" {
			t.Fatal("private observation is serializable")
		}
		for replay := 0; replay < 4; replay++ {
			event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "windows/worldWritableWarning", Params: raw})
			want := MetadataEvent
			if replay == 0 && tc.class != windowsNoWarning {
				want = NoticeEvent
			}
			if err != nil || event.Kind != want || !event.Correlated || event.ThreadID != c.thread || event.Native != nil {
				t.Fatal("valid notification failed or duplicated notice", err)
			}
			if want == NoticeEvent && (event.Notice != domain.NativeWarning || event.TurnID != turn) {
				t.Fatal("notice escaped original active turn")
			}
			if want == MetadataEvent && (event.Metadata != WindowsSandboxWarningDiscarded || event.TurnID != "") {
				t.Fatal("discarded warning gained turn authority")
			}
			encoded, _ := json.Marshal(event)
			if strings.Contains(string(encoded), "private-native") || strings.Contains(string(encoded), "samplePaths") || strings.Contains(string(encoded), "extraCount") {
				t.Fatal("native warning content escaped event")
			}
			if c.execution != state || c.problem != nil || c.execution.active != turn || c.execution.paused || !reflect.DeepEqual(c.execution.settings, beforeSettings) || len(state.inputs) != beforeInputs || len(state.pending) != beforePending || len(state.interactions.arrivals) != 0 {
				t.Fatal("warning changed original authority")
			}
		}
		event, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
		if err != nil || event.Kind != TurnCompletedEvent || event.TurnID != turn {
			t.Fatal("warning replaced original completion", err)
		}
	}
}

func TestWindowsWarningsDeduplicateOnlyWithinOriginalProcess(t *testing.T) {
	c, _ := observationClient()
	for _, params := range []map[string]any{
		windowsWarningFixture([]string{"private-first"}, 0, false),
		windowsWarningFixture([]string{}, 0, true),
		windowsWarningFixture([]string{"private-second"}, 2, true),
	} {
		event, err := observeFixture(c, "windows/worldWritableWarning", params)
		if err != nil || event.Kind != NoticeEvent {
			t.Fatal("new warning class was suppressed", err)
		}
		replay, err := observeFixture(c, "windows/worldWritableWarning", params)
		if err != nil || replay.Metadata != WindowsSandboxWarningDiscarded {
			t.Fatal("warning replay was republished", err)
		}
		fresh, _ := observationClient()
		event, err = observeFixture(fresh, "windows/worldWritableWarning", params)
		if err != nil || event.Kind != NoticeEvent {
			t.Fatal("another original process lost its notice", err)
		}
	}
	if c.execution.windowsWarnings != 14 {
		t.Fatal("warning registry exceeded the three finite classes")
	}
}

func TestWindowsSetupCompletionIsAlwaysPassive(t *testing.T) {
	for _, mode := range []windowsSandboxMode{windowsSandboxElevated, windowsSandboxUnelevated} {
		for _, success := range []bool{true, false} {
			for _, errorState := range []string{"omitted", "null", "empty", "text", "boundary"} {
				c, turn := observationClient()
				settings, state := c.execution.settings, c.execution
				c.execution.paused = true
				problem := domain.Fail(domain.RecoveryRequired, "Fixture recovery.", "Preserve recovery.")
				c.problem = problem
				params := map[string]any{"mode": mode, "success": success}
				switch errorState {
				case "null":
					params["error"] = nil
				case "empty":
					params["error"] = ""
				case "text":
					params["error"] = "private-native-error"
				case "boundary":
					params["error"] = strings.Repeat("e", maxWindowsSandboxText)
				}
				for replay := 0; replay < 2; replay++ {
					event, err := observeFixture(c, "windowsSandbox/setupCompleted", params)
					if err != nil || event.Kind != MetadataEvent || event.Metadata != WindowsSandboxSetupDiscarded || !event.Correlated || event.TurnID != "" || event.Native != nil {
						t.Fatal("setup completion acquired active authority", err)
					}
					encoded, _ := json.Marshal(event)
					if strings.Contains(string(encoded), "private-native") || strings.Contains(string(encoded), "success") || strings.Contains(string(encoded), "elevated") {
						t.Fatal("native setup content escaped")
					}
					if c.execution != state || c.problem != problem || !c.execution.paused || c.execution.active != turn || c.execution.windowsWarnings != 0 || !reflect.DeepEqual(settings, c.execution.settings) || len(state.pending) != 0 || len(state.inputs) != 0 || len(state.interactions.arrivals) != 0 {
						t.Fatal("setup cleared recovery or changed permissions/input")
					}
				}
			}
		}
	}
}

func TestWindowsSandboxRejectsMalformedClosedPayloads(t *testing.T) {
	warnings := []string{
		``, `null`, `[]`, `{}`, `{"samplePaths":[],"extraCount":0}`, `{"extraCount":0,"failedScan":false}`, `{"samplePaths":[],"failedScan":false}`,
		`{"samplePaths":null,"extraCount":0,"failedScan":false}`, `{"samplePaths":[],"extraCount":null,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"failedScan":null}`,
		`{"samplePaths":[null],"extraCount":0,"failedScan":false}`, `{"samplePaths":[2],"extraCount":0,"failedScan":false}`, `{"samplePaths":{},"extraCount":0,"failedScan":false}`,
		`{"samplePaths":[],"extraCount":-1,"failedScan":false}`, `{"samplePaths":[],"extraCount":1.5,"failedScan":false}`, `{"samplePaths":[],"extraCount":1e1,"failedScan":false}`, `{"samplePaths":[],"extraCount":"0","failedScan":false}`,
		`{"samplePaths":[],"extraCount":18446744073709551616,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"failedScan":1}`,
		`{"samplePaths":[],"extraCount":0,"failedScan":false,"unknown":true}`, `{"samplePaths":[],"extraCount":0,"extraCount":1,"failedScan":false}`,
		`{"samplePaths":[],"samplePaths":[],"extraCount":0,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"failedScan":true,"failedScan":false}`,
		`{"SamplePaths":[],"extraCount":0,"failedScan":false}`, `{"samplePaths":[],"ExtraCount":0,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"FailedScan":false}`,
		`{"samplePaths":["p\u0000p"],"extraCount":0,"failedScan":false}`,
	}
	for _, params := range []map[string]any{
		windowsWarningFixture(make([]string, maxWindowsWarningPaths+1), 0, false),
		windowsWarningFixture([]string{strings.Repeat("p", maxWindowsSandboxText+1)}, 0, false),
		windowsWarningFixture([]string{strings.Repeat("p", nativewire.MaxFrame)}, 0, false),
	} {
		raw, _ := json.Marshal(params)
		warnings = append(warnings, string(raw))
	}
	warnings = append(warnings, string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}))
	for i, raw := range warnings {
		c, _ := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "windows/worldWritableWarning", Params: json.RawMessage(raw)}); err == nil || c.execution.windowsWarnings != 0 {
			t.Fatalf("invalid warning %d acquired authority", i)
		}
	}
	setups := []string{
		``, `null`, `[]`, `{}`, `{"mode":"elevated"}`, `{"success":true}`, `{"mode":null,"success":true}`, `{"mode":"elevated","success":null}`,
		`{"mode":"unknown","success":true}`, `{"mode":1,"success":true}`, `{"mode":"elevated","success":"true"}`,
		`{"mode":"elevated","success":false,"error":{}}`, `{"mode":"elevated","success":false,"error":false}`,
		`{"mode":"elevated","mode":"unelevated","success":true}`, `{"mode":"elevated","success":true,"success":false}`, `{"mode":"elevated","success":true,"error":null,"error":"p"}`,
		`{"mode":"elevated","success":true,"unknown":true}`, `{"Mode":"elevated","success":true}`, `{"mode":"elevated","Success":true}`, `{"mode":"elevated","success":true,"Error":null}`,
		`{"mode":"elevated","success":false,"error":"e\u0000e"}`,
	}
	for _, size := range []int{maxWindowsSandboxText + 1, nativewire.MaxFrame} {
		raw, _ := json.Marshal(map[string]any{"mode": "elevated", "success": false, "error": strings.Repeat("e", size)})
		setups = append(setups, string(raw))
	}
	for i, raw := range setups {
		if validateWindowsSetup(json.RawMessage(raw)) == nil {
			t.Fatalf("invalid setup %d was accepted", i)
		}
	}
	if validationStage("windows/worldWritableWarning") != validationWindowsSandbox || validationStage("windowsSandbox/setupCompleted") != validationWindowsSandbox {
		t.Fatal("Windows validation lost its closed diagnostic stage")
	}
}

func TestWindowsSandboxCannotInventCurrentOrRequestAuthority(t *testing.T) {
	for _, change := range []string{"completed", "failed", "no-active", "unknown-active", "paused", "problem", "foreign-process", "unbound", "server-request"} {
		for _, method := range []string{"windows/worldWritableWarning", "windowsSandbox/setupCompleted"} {
			c, turn := observationClient()
			native := nativewire.Event{Kind: nativewire.Notification, Method: method}
			params := windowsWarningFixture([]string{"private-path"}, 1, true)
			if method == "windowsSandbox/setupCompleted" {
				params = map[string]any{"mode": "elevated", "success": true, "error": "private-error"}
			}
			native.Params, _ = json.Marshal(params)
			switch change {
			case "completed", "failed":
				tracked := c.execution.turns[turn]
				tracked.Turn.Status = TurnCompleted
				if change == "failed" {
					tracked.Turn.Status = TurnFailed
				}
				c.execution.turns[turn] = tracked
			case "no-active":
				c.execution.active = ""
			case "unknown-active":
				c.execution.active = domain.NewID()
			case "paused":
				c.execution.paused = true
			case "problem":
				c.problem = domain.Fail(domain.RecoveryRequired, "Fixture recovery.", "Preserve recovery.")
			case "foreign-process":
				c.execution.thread.ID = domain.NewID()
			case "unbound":
				c.execution = nil
			case "server-request":
				native.Kind = nativewire.ServerRequest
				native.ID = json.RawMessage(`7`)
				native.Token = domain.NewID()
			}
			beforeState, beforeProblem := c.execution, c.problem
			event, err := c.observeEventLocked(native)
			if err == nil && event.Kind != MetadataEvent && event.Kind != NativeExtensionEvent {
				t.Fatal("unowned or terminal observation acquired notice/request authority", change, method)
			}
			if c.execution != beforeState || c.problem != beforeProblem || c.execution != nil && c.execution.windowsWarnings != 0 {
				t.Fatal("inert observation changed original context")
			}
		}
	}
}

func TestWindowsSandboxOrderedWirePreservesOriginalTurnAndSafeLogs(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	warning := windowsWarningFixture([]string{"private-wire-native-path"}, ^uint64(0), true)
	fixtureSignal(t, c, "notify", map[string]any{"method": "windows/worldWritableWarning", "params": warning})
	event := nextKind(t, c, NoticeEvent)
	if event.Notice != domain.NativeWarning || event.TurnID != accepted.TurnID {
		t.Fatal("warning notice escaped original turn")
	}
	fixtureSignal(t, c, "notify", map[string]any{"method": "windows/worldWritableWarning", "params": warning})
	if nextKind(t, c, MetadataEvent).Metadata != WindowsSandboxWarningDiscarded {
		t.Fatal("wire replay duplicated warning")
	}
	fixtureSignal(t, c, "notify", map[string]any{"method": "windowsSandbox/setupCompleted", "params": map[string]any{"mode": "elevated", "success": false, "error": "private-wire-native-error"}})
	if nextKind(t, c, MetadataEvent).Metadata != WindowsSandboxSetupDiscarded {
		t.Fatal("unsolicited setup completion gained authority")
	}
	if strings.Contains(logs.String(), "private-wire") || !strings.Contains(logs.String(), `"classification":"world-writable-scan-failed"`) || !strings.Contains(logs.String(), `"extra_count":18446744073709551615`) || !strings.Contains(logs.String(), string(WindowsSandboxSetupDiscarded)) {
		t.Fatal("diagnostics leaked native text or lost bounded classification/count")
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.problem != nil || c.execution.active != "" || c.execution.paused || len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "windowsSandbox/setupStart")) != 0 || len(requestsOf(t, capture, "account/login/start")) != 0 {
		t.Fatal("safety notice changed original result or issued setup/input")
	}
}

func TestWindowsSandboxMalformedWireLogsOnlyClosedStage(t *testing.T) {
	for _, method := range []string{"windows/worldWritableWarning", "windowsSandbox/setupCompleted"} {
		c, _, _, _ := boundTurnFixture(t, "normal")
		if _, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
			t.Fatal(err)
		}
		nextKind(t, c, MessageCompletedEvent)
		var logs bytes.Buffer
		c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
		params := map[string]any{"samplePaths": []string{"private-malformed-path"}, "extraCount": -1, "failedScan": false}
		if method == "windowsSandbox/setupCompleted" {
			params = map[string]any{"mode": "elevated", "success": false, "error": map[string]any{"native": "private-malformed-error"}}
		}
		fixtureSignal(t, c, "notify", map[string]any{"method": method, "params": params})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var failed error
		for failed == nil {
			_, failed = c.NextEvent(ctx)
		}
		cancel()
		if domain.SafeError(failed).Code != domain.RecoveryRequired || c.problem == nil || !c.execution.paused {
			t.Fatal("malformed observation did not preserve rejection/recovery", failed)
		}
		if strings.Contains(logs.String(), "private-malformed") || !strings.Contains(logs.String(), `"stage":"windows-sandbox"`) {
			t.Fatal("malformed native diagnostics lost their closed redaction boundary")
		}
	}
}
