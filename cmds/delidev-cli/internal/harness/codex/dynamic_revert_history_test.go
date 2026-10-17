// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestDynamicRevertHistoryPreservesSettledImageAndFailure(t *testing.T) {
	image := strings.ReplaceAll(dynamicCompletedFixture, `"fixture_tool","namespace":null`, `"imagegen","namespace":"delidev_image"`)
	image = strings.Replace(image, `"success":false`, `"success":true`, 1)
	image = strings.Replace(image, "private-image-sentinel", "data:image/png;base64,"+strings.Repeat("A", 1<<20), 1)
	for _, item := range []string{dynamicCompletedFixture, image} {
		raw := json.RawMessage(item)
		if id, err := verifyRevertHistoryItem(raw); err != nil || id != "dynamic-call" {
			t.Fatal("settled dynamic item rejected", err)
		}
		turnID := domain.NewID()
		turn, _ := json.Marshal(map[string]any{"id": turnID, "status": "completed", "items": []json.RawMessage{raw}})
		history := []json.RawMessage{turn}
		joined, err := joinRevertHistory(history, []revertHistoryItem{{TurnID: turnID, Item: raw}}, enlargedNativeFrameBytes)
		if err != nil || len(joined) != 1 || !equivalentForkJSON(joined[0], turn) {
			t.Fatal("revert changed protected dynamic history", err)
		}
		repeated, err := joinRevertHistory(joined, []revertHistoryItem{{TurnID: turnID, Item: raw}}, enlargedNativeFrameBytes)
		if err != nil || historyDigest(repeated) != historyDigest(joined) {
			t.Fatal("repeated history hydration changed its digest", err)
		}
		var wire turnWire
		if json.Unmarshal(joined[0], &wire) != nil || len(wire.Items) != 1 || !equivalentForkJSON(raw, wire.Items[0]) {
			t.Fatal("revert lost original dynamic arguments or output")
		}
	}
}

func TestDynamicRevertHistoryRejectsUnsettledMalformedAndOversizedItems(t *testing.T) {
	for _, item := range []string{
		dynamicStartedFixture,
		strings.Replace(dynamicCompletedFixture, `"completed"`, `"unknown"`, 1),
		strings.Replace(dynamicCompletedFixture, `"durationMs":9007199254740993`, `"durationMs":-1`, 1),
		strings.Replace(dynamicCompletedFixture, `"inputImage"`, `"unknown"`, 1),
		strings.Replace(dynamicCompletedFixture, "private-image-sentinel", strings.Repeat("A", 512<<10), 1),
		strings.ReplaceAll(dynamicCompletedFixture, `"fixture_tool","namespace":null`, `"imagegen","namespace":"delidev_image"`) + strings.Repeat(" ", enlargedNativeFrameBytes),
	} {
		if _, err := verifyRevertHistoryItem(json.RawMessage(item)); err == nil {
			t.Fatal("invalid dynamic item became revert eligible")
		}
	}
}
