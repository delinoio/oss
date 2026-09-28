package domain

import (
	"strings"
	"testing"
)

func TestSearchTextRetainsVisibleNativeContentWithoutMetadata(t *testing.T) {
	output, aggregate, diff, explanation := "stream output", "aggregate output", "turn diff", "plan explanation"
	index := int64(0)
	m := ExecutionMessage{Text: "Assistant", NativeID: "private-native-identity", Tool: &ExecutionTool{Started: ToolSnapshot{Command: &CommandObservation{Command: "command text", AggregatedOutput: &aggregate}}, Output: &output, Inputs: []SequencedToolInput{{Input: ToolInputObservation{Text: "terminal input"}}}, Patches: []SequencedToolPatch{{Changes: []FileChangeObservation{{Diff: "patch diff"}}}}}, Artifact: &ExecutionArtifact{Started: ArtifactSnapshot{Text: "old plan"}, Completed: &ArtifactSnapshot{Text: "final plan", Summary: []string{"summary"}, Content: []string{"visible reasoning"}}, Deltas: []SequencedArtifactDelta{{Delta: ArtifactDelta{Kind: ReasoningContentDelta, Index: &index, Text: "split "}}, {Delta: ArtifactDelta{Kind: ReasoningContentDelta, Index: &index, Text: "content"}}}}, Progress: &NativeProgress{Diff: &diff, Plan: &NativePlan{Explanation: &explanation, Steps: []PlanStep{{Step: "plan step"}}}}}
	text := m.SearchText()
	for _, part := range []string{"assistant", "stream output", "aggregate output", "command text", "terminal input", "patch diff", "old plan", "final plan", "summary", "visible reasoning", "split content", "turn diff", "plan explanation", "plan step"} {
		if !strings.Contains(text, part) {
			t.Fatalf("lost retained text %q", part)
		}
	}
	if strings.Contains(text, m.NativeID) {
		t.Fatal("indexed private native identifier")
	}
	if strings.Contains(text, "assistantcommand") {
		t.Fatal("joined unrelated fields")
	}
}
