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

func TestSearchTextIncludesImageViewLocationOnly(t *testing.T) {
	observation := func(location string) *ImageViewObservation {
		return &ImageViewObservation{ReferenceID: NewID(), MachineID: NewID(), RepositoryID: NewID(), ManifestDigest: strings.Repeat("b", 64), Location: location}
	}
	started, completed := observation("Images/Started.png"), observation("Images/Completed.png")
	m := ExecutionMessage{Tool: &ExecutionTool{
		Started:   ToolSnapshot{Kind: ImageViewTool, Status: ToolRunning, ImageView: started},
		Completed: &ToolSnapshot{Kind: ImageViewTool, Status: ToolCompleted, ImageView: completed},
	}}
	text := m.SearchText()
	for _, part := range []string{"images/started.png", "images/completed.png"} {
		if !strings.Contains(text, part) {
			t.Fatalf("lost image-view location %q", part)
		}
	}
	if !strings.Contains(text, "images/started.png\x1fimages/completed.png") {
		t.Fatal("image-view locations were not separated")
	}
	for _, private := range []string{string(started.ReferenceID), string(started.MachineID), string(started.RepositoryID), started.ManifestDigest, string(completed.ReferenceID), string(completed.MachineID), string(completed.RepositoryID), completed.ManifestDigest} {
		if strings.Contains(text, strings.ToLower(private)) {
			t.Fatalf("indexed private image-view identity %q", private)
		}
	}
	completedOnly := ExecutionMessage{Tool: &ExecutionTool{Started: ToolSnapshot{Kind: CommandTool, Command: &CommandObservation{Command: "ls"}}, Completed: &ToolSnapshot{Kind: ImageViewTool, Status: ToolCompleted, ImageView: completed}}}
	if !strings.Contains(completedOnly.SearchText(), "images/completed.png") {
		t.Fatal("lost completed-only image-view location")
	}
}
