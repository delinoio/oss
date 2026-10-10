// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestWindowsSandboxWarningsAreRedactedAndDoNotGrantAuthority(t *testing.T) {
	for _, value := range []struct {
		raw   string
		kind  WindowsWarningKind
		count uint32
		extra uint64
	}{
		{`{"samplePaths":["C:\\private-marker"],"extraCount":0,"failedScan":false}`, WorldWritablePaths, 1, 0},
		{`{"samplePaths":[],"extraCount":0,"failedScan":true}`, WorldWritableScanFailed, 0, 0},
		{`{"samplePaths":[],"extraCount":18446744073709551615,"failedScan":true}`, WorldWritablePathsAndScanFailed, 0, ^uint64(0)},
	} {
		c, turn := observationClient()
		paused := c.execution.paused
		settings := c.execution.settings
		native := nativewire.Event{Kind: nativewire.Notification, Method: "windows/worldWritableWarning", Params: json.RawMessage(value.raw)}
		e, err := c.observeEventLocked(native)
		encoded, _ := json.Marshal(e)
		if err != nil || e.Kind != NoticeEvent || e.Notice != domain.NativeWarning || !e.Correlated || e.Native != nil || e.WindowsWarning == nil || e.WindowsWarning.Kind != value.kind || e.WindowsWarning.SampleCount != value.count || e.WindowsWarning.ExtraCount != value.extra || strings.Contains(string(encoded), "private-marker") || c.execution.active != turn || c.execution.paused != paused || c.execution.settings.Model != settings.Model || c.execution.settings.Sandbox.Type != settings.Sandbox.Type {
			t.Fatal("security notice changed authority or exposed private data", err)
		}
		replay, err := c.observeEventLocked(native)
		if err != nil || replay.Kind != MetadataEvent || replay.Metadata != WindowsSandboxDiscarded || c.execution.active != turn {
			t.Fatal("warning replay gained another effect", err)
		}
	}
	c, _ := observationClient()
	e, err := observeFixture(c, "windows/worldWritableWarning", map[string]any{"samplePaths": []string{}, "extraCount": 0, "failedScan": false})
	if err != nil || e.Kind != MetadataEvent || e.Metadata != WindowsSandboxDiscarded {
		t.Fatal("empty scan invented a warning", err)
	}
}

func TestWindowsSandboxSetupCompletionNeverProvesReadiness(t *testing.T) {
	for _, raw := range []string{`{"mode":"unelevated","success":true,"error":null}`, `{"mode":"elevated","success":false,"error":"private-error-marker"}`, `{"mode":"elevated","success":true}`} {
		c, turn := observationClient()
		paused := c.execution.paused
		e, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "windowsSandbox/setupCompleted", Params: json.RawMessage(raw)})
		encoded, _ := json.Marshal(e)
		if err != nil || e.Kind != MetadataEvent || e.Metadata != WindowsSandboxDiscarded || e.Native != nil || e.WindowsWarning != nil || strings.Contains(string(encoded), "private-error-marker") || c.execution.active != turn || c.execution.paused != paused {
			t.Fatal("unsolicited completion gained readiness or exposed diagnostics", err)
		}
	}
	if validationStage("windows/worldWritableWarning") != validationWindowsSandbox || validationStage("windowsSandbox/setupCompleted") != validationWindowsSandbox {
		t.Fatal("missing closed sandbox classification")
	}
}

func TestWindowsSandboxRejectsMalformedNotificationsAndServerRequests(t *testing.T) {
	warning := []string{`null`, `{}`, `{"samplePaths":null,"extraCount":0,"failedScan":false}`, `{"samplePaths":[null],"extraCount":0,"failedScan":false}`, `{"samplePaths":[],"extraCount":-1,"failedScan":false}`, `{"samplePaths":[],"extraCount":18446744073709551616,"failedScan":false}`, `{"samplePaths":[],"extraCount":null,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"failedScan":null}`, `{"samplePaths":[],"extraCount":0}`, `{"samplePaths":[],"extraCount":0,"failedScan":false,"unknown":true}`, `{"samplePaths":[],"extraCount":0,"extraCount":1,"failedScan":false}`, `{"samplePaths":[],"ExtraCount":0,"failedScan":false}`, `{"samplePaths":["` + strings.Repeat("x", 4097) + `"],"extraCount":0,"failedScan":false}`, `{"samplePaths":[` + strings.Repeat(`"a",`, 1024) + `"a"],"extraCount":0,"failedScan":false}`}
	setup := []string{`null`, `{}`, `{"mode":"unknown","success":true}`, `{"mode":null,"success":true}`, `{"mode":"elevated","success":null}`, `{"mode":"elevated","success":true,"error":{}}`, `{"mode":"elevated","success":true,"success":false}`, `{"mode":"elevated","Success":true}`, `{"mode":"elevated","success":true,"unknown":true}`, `{"mode":"elevated","success":true,"error":"` + strings.Repeat("x", 4097) + `"}`}
	for method, cases := range map[string][]string{"windows/worldWritableWarning": warning, "windowsSandbox/setupCompleted": setup} {
		for _, raw := range cases {
			c, turn := observationClient()
			if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: json.RawMessage(raw)}); err == nil || c.execution.active != turn || len(c.windowsWarnings) != 0 {
				t.Fatal("malformed sandbox observation accepted or changed ownership")
			}
		}
		c, turn := observationClient()
		e, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, Params: json.RawMessage(`{}`)})
		if err == nil && (e.Kind == MetadataEvent || e.Kind == NoticeEvent) || c.execution.active != turn || len(c.windowsWarnings) != 0 {
			t.Fatal("server request acquired passive notification authority")
		}
	}
}
