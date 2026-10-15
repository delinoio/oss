// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestNativeExtensionLogsOnlyClosedClassification(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "normal")
	for _, method := range []string{"thread/goal/updated", "private-native-method-fixture"} {
		var logs bytes.Buffer
		c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
		raw, _ := json.Marshal(map[string]any{"threadId": c.thread, "goal": "private-native-payload-fixture"})
		c.pendingEvent = &nativewire.Event{Kind: nativewire.Notification, Method: method, Params: raw}
		event, err := c.NextEvent(context.Background())
		if err != nil || event.Kind != NativeExtensionEvent {
			t.Fatal("fixture lost its private extension boundary", err)
		}
		var record map[string]any
		if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
			t.Fatal("extension diagnostic missing", err)
		}
		if record["stage"] != string(event.ExtensionStage) {
			t.Fatal("extension diagnostic lost its closed classification")
		}
		if _, exists := record["method"]; exists || strings.Contains(logs.String(), method) || strings.Contains(logs.String(), "private-native-payload-fixture") {
			t.Fatal("extension diagnostic exposed native method or payload")
		}
	}
}
