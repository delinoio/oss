// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func TestDynamicToolSnapshotPreservesOriginalAndRejectsMixedContent(t *testing.T) {
	success := false
	duration := int64(17)
	text := "original"
	media := "https://inert.invalid/image"
	audio := "data:audio/original"
	items := []DynamicToolContent{{Type: DynamicText, Text: &text}, {Type: DynamicImage, ImageURL: &media}, {Type: DynamicAudio, AudioURL: &audio}}
	v := &DynamicToolObservation{Tool: "fixture_tool", Arguments: json.RawMessage(`{"original":true}`), ContentItems: &items, Success: &success, DurationMS: &duration}
	snapshot := ToolSnapshot{Kind: DynamicTool, Status: ToolCompleted, Dynamic: v}
	if snapshot.Validate() != nil {
		t.Fatal("closed dynamic snapshot rejected")
	}
	copy := CloneDynamicTool(v)
	*copy.Success = true
	copy.Arguments[2] = 'x'
	if *v.Success || SameDynamicToolCall(v, copy) {
		t.Fatal("caller mutation changed frozen dynamic call")
	}
	items[0].ImageURL = &media
	if snapshot.Validate() == nil {
		t.Fatal("mixed native content accepted")
	}
}
