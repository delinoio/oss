// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestWindowsSandboxObservationsPreserveOriginalAuthority(t *testing.T) {
	c, turn := observationClient()
	before := c.execution.settings
	warning := map[string]any{"samplePaths": []string{"C:\\private-path-marker"}, "extraCount": 0, "failedScan": false}
	event, err := observeFixture(c, "windows/worldWritableWarning", warning)
	if err != nil || event.Kind != NoticeEvent || event.Notice != domain.NativeWarning || event.WindowsSandboxWarning == nil || event.WindowsSandboxWarning.Classification != WindowsWorldWritablePaths {
		t.Fatal(event, err)
	}
	raw, _ := json.Marshal(event)
	if strings.Contains(string(raw), "private-path-marker") {
		t.Fatal("path published")
	}
	replay, err := observeFixture(c, "windows/worldWritableWarning", warning)
	if err != nil || replay.Kind != MetadataEvent || replay.Metadata != WindowsSandboxWarningDiscarded {
		t.Fatal("warning replay duplicated effect", replay, err)
	}
	for _, mode := range []string{"elevated", "unelevated"} {
		for _, success := range []bool{false, true} {
			event, err = observeFixture(c, "windowsSandbox/setupCompleted", map[string]any{"mode": mode, "success": success, "error": "private-error-marker"})
			if err != nil || event.Kind != MetadataEvent || event.Metadata != WindowsSandboxSetupDiscarded || event.WindowsSandboxSetup == nil || event.WindowsSandboxSetup.Success != success {
				t.Fatal(event, err)
			}
			raw, _ = json.Marshal(event)
			if strings.Contains(string(raw), "private-error-marker") {
				t.Fatal("error published")
			}
		}
	}
	if !reflect.DeepEqual(before, c.execution.settings) || c.execution.active != turn || c.execution.paused {
		t.Fatal("observation changed original settings or turn")
	}
	for _, method := range []string{"windows/worldWritableWarning", "windowsSandbox/setupCompleted"} {
		event, err = c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, Params: []byte(`{}`)})
		if err == nil && event.Kind != NativeExtensionEvent {
			t.Fatal("server request gained notification admission", event)
		}
	}
}
