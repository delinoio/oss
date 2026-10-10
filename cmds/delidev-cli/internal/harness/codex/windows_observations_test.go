// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestWindowsWarningsAreRedactedBoundedAndReplaySafe(t *testing.T) {
	for _, sample := range []struct {
		paths  []string
		extra  uint64
		failed bool
		class  windowsWarningClass
	}{
		{[]string{`C:\private-path-marker`}, 0, false, windowsWritablePaths},
		{[]string{}, 0, true, windowsScanFailed},
		{[]string{`C:\private-path-marker`}, 18446744073709551615, true, windowsWritablePathsAndScanFailed},
	} {
		c, turn := observationClient()
		c.execution.paused = false
		before, _ := json.Marshal(c.execution)
		params := map[string]any{"samplePaths": sample.paths, "extraCount": sample.extra, "failedScan": sample.failed}
		event, err := observeFixture(c, "windows/worldWritableWarning", params)
		raw, _ := json.Marshal(event)
		if err != nil || event.Kind != NoticeEvent || event.Notice != domain.NativeWarning || !event.Correlated || event.ThreadID != c.thread || event.Native != nil || event.WindowsWarning == nil || event.WindowsWarning.Class != sample.class || event.WindowsWarning.ExtraCount != sample.extra || event.WindowsWarning.SampleCount != uint32(len(sample.paths)) || strings.Contains(string(raw), "private-path-marker") {
			t.Fatal("warning escaped private boundary", err, string(raw))
		}
		replay, err := observeFixture(c, "windows/worldWritableWarning", params)
		if err != nil || replay.Kind != MetadataEvent || replay.Metadata != WindowsWarningReplayChecked || replay.WindowsWarning != nil {
			t.Fatal("warning replay duplicated effect", err)
		}
		after, _ := json.Marshal(c.execution)
		if string(before) != string(after) || c.execution.active != turn {
			t.Fatal("warning changed original ownership")
		}
		completed, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": map[string]any{"id": turn, "status": "completed", "error": nil, "items": []any{}}})
		if err != nil || completed.Kind != TurnCompletedEvent {
			t.Fatal("warning replaced original outcome", err)
		}
	}
	c, _ := observationClient()
	c.execution.paused = false
	empty, err := observeFixture(c, "windows/worldWritableWarning", map[string]any{"samplePaths": []string{}, "extraCount": 0, "failedScan": false})
	if err != nil || empty.Kind != MetadataEvent || empty.Metadata != WindowsWarningDiscarded {
		t.Fatal("empty observation invented notice", err)
	}
}

func TestWindowsSetupIsValidatedDiscardedWithoutReadiness(t *testing.T) {
	for _, mode := range []string{"elevated", "unelevated"} {
		for _, success := range []bool{false, true} {
			for _, diagnostic := range []any{nil, "private-error-marker"} {
				c, _ := observationClient()
				c.execution.paused = false
				before, _ := json.Marshal(c.execution)
				event, err := observeFixture(c, "windowsSandbox/setupCompleted", map[string]any{"mode": mode, "success": success, "error": diagnostic})
				raw, _ := json.Marshal(event)
				after, _ := json.Marshal(c.execution)
				if err != nil || event.Kind != MetadataEvent || event.Metadata != WindowsSetupDiscarded || event.Native != nil || strings.Contains(string(raw), "private-error-marker") || string(before) != string(after) || c.problem != nil {
					t.Fatal("setup granted readiness or disclosed diagnostic", err)
				}
			}
		}
	}
}

func TestWindowsMalformedEnvelopesRejectWithoutPayloadReflection(t *testing.T) {
	cases := map[string][]string{
		"windows/worldWritableWarning": {
			`null`, `[]`, `{}`, `{"samplePaths":null,"extraCount":0,"failedScan":false}`,
			`{"samplePaths":[],"extraCount":null,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"failedScan":null}`,
			`{"samplePaths":[],"extraCount":-1,"failedScan":false}`, `{"samplePaths":[],"extraCount":18446744073709551616,"failedScan":false}`,
			`{"samplePaths":[],"extraCount":1.5,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"failedScan":false,"private-error-marker":1}`,
			`{"samplePaths":[],"extraCount":0,"extraCount":1,"failedScan":false}`,
			`{"samplePaths":[null],"extraCount":0,"failedScan":false}`,
		},
		"windowsSandbox/setupCompleted": {
			`null`, `[]`, `{}`, `{"mode":null,"success":true,"error":null}`, `{"mode":"unknown","success":true,"error":null}`,
			`{"mode":"elevated","success":null,"error":null}`, `{"mode":"elevated","success":true}`,
			`{"mode":"elevated","success":true,"error":123}`, `{"mode":"elevated","success":true,"error":{},"private-error-marker":1}`,
			`{"mode":"elevated","success":true,"success":false,"error":null}`,
		},
	}
	paths := make([]string, 1001)
	for i := range paths {
		paths[i] = "private-path-marker"
	}
	raw, _ := json.Marshal(map[string]any{"samplePaths": paths, "extraCount": 0, "failedScan": false})
	cases["windows/worldWritableWarning"] = append(cases["windows/worldWritableWarning"], string(raw))
	raw, _ = json.Marshal(map[string]any{"samplePaths": []string{strings.Repeat("x", 4097)}, "extraCount": 0, "failedScan": false})
	cases["windows/worldWritableWarning"] = append(cases["windows/worldWritableWarning"], string(raw))
	raw, _ = json.Marshal(map[string]any{"mode": "elevated", "success": false, "error": strings.Repeat("x", 4097)})
	cases["windowsSandbox/setupCompleted"] = append(cases["windowsSandbox/setupCompleted"], string(raw), strings.Repeat("x", nativewire.MaxFrame+1))
	for method, values := range cases {
		for _, value := range values {
			c, turn := observationClient()
			c.execution.paused = false
			event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: json.RawMessage(value)})
			if err == nil || event.Kind != "" || c.execution.active != turn || len(c.windowsWarnings) != 0 || strings.Contains(err.Error(), "private-error-marker") || strings.Contains(err.Error(), "private-path-marker") {
				t.Fatal("malformed observation acquired authority or reflected input", method, err)
			}
			if validationStage(method) != validationWindows {
				t.Fatal("diagnostic lost closed Windows classification")
			}
		}
	}
}

func TestWindowsRequestsAndStoppedOwnersCannotAcquireNotificationAuthority(t *testing.T) {
	for _, method := range []string{"windows/worldWritableWarning", "windowsSandbox/setupCompleted"} {
		c, _ := observationClient()
		c.execution.paused = false
		event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, Params: json.RawMessage(`{}`)})
		if err == nil && event.Kind != NativeExtensionEvent {
			t.Fatal("server request acquired notification handler")
		}
	}
	for _, condition := range []string{"paused", "failed", "completed", "unbound"} {
		c, turn := observationClient()
		c.execution.paused = false
		switch condition {
		case "paused":
			c.execution.paused = true
		case "failed":
			c.problem = turnUncertain()
		case "completed":
			v := c.execution.turns[turn]
			v.Turn.Status = TurnCompleted
			c.execution.turns[turn] = v
		case "unbound":
			c.execution.active = ""
		}
		event, err := observeFixture(c, "windows/worldWritableWarning", map[string]any{"samplePaths": []string{"private-path-marker"}, "extraCount": 0, "failedScan": false})
		if err != nil || event.Kind != MetadataEvent || event.Metadata != WindowsWarningDiscarded || len(c.windowsWarnings) != 0 {
			t.Fatal("stopped owner gained publication", condition, err)
		}
	}
}
