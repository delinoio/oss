package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenCodeChangeProgressKeepsMissingFieldsAndRejectsInvalidCounts(t *testing.T) {
	for _, raw := range []string{`{"additions":0,"deletions":0}`, `{"file":"","patch":"","additions":1,"deletions":2,"status":"modified"}`} {
		var d OpenCodeFileDiff
		if Decode([]byte(raw), &d) != nil {
			t.Fatal("valid original diff rejected")
		}
		encoded, _ := json.Marshal(d)
		if string(encoded) != raw {
			t.Fatal("native omission or empty content was changed")
		}
	}
	for _, raw := range []string{`{}`, `{"additions":null,"deletions":0}`, `{"additions":-1,"deletions":0}`, `{"additions":1.5,"deletions":0}`, `{"additions":0,"deletions":0,"extra":true}`} {
		var d OpenCodeFileDiff
		if Decode([]byte(raw), &d) == nil {
			t.Fatal("invalid native diff counts became content")
		}
	}
	for _, name := range []string{"missing-list", "foreign-message", "session-message", "unknown-source", "unknown-status", "count", "size", "mixed"} {
		t.Run(name, func(t *testing.T) {
			changes := &OpenCodeChanges{Source: OpenCodeSessionDiff, NativeEventID: "evt_01960dcbe1faABCDEFGHIJKLMN", Diffs: []OpenCodeFileDiff{}}
			p := NativeProgress{Kind: OpenCodeChangesProgressKind, Changes: changes}
			switch name {
			case "missing-list":
				changes.Diffs = nil
			case "foreign-message":
				changes.Source = OpenCodeInputSummary
				changes.NativeMessageID = "foreign"
			case "session-message":
				changes.NativeMessageID = "msg_01960dcbe1faABCDEFGHIJKLMN"
			case "unknown-source":
				changes.Source = "other"
			case "unknown-status":
				status := OpenCodeDiffStatus("other")
				changes.Diffs = []OpenCodeFileDiff{{Status: &status}}
			case "count":
				changes.Diffs = []OpenCodeFileDiff{{Additions: 1 << 53}}
			case "size":
				patch := strings.Repeat("x", MaxMessageText+1)
				changes.Diffs = []OpenCodeFileDiff{{Patch: &patch}}
			case "mixed":
				p.Plan = &NativePlan{Steps: []PlanStep{}}
			}
			if (ExecutionProgressUpdate{ID: NewID(), Progress: p}).Validate() == nil {
				t.Fatal("invalid original change progress was accepted")
			}
		})
	}
}

func TestOpenCodeRevisionRequiresExclusiveBoundedOriginalContent(t *testing.T) {
	for _, r := range []OpenCodeRevision{{Source: OpenCodeSnapshotRevision, Hash: "original", Files: []string{}}, {Source: OpenCodePatchRevision, Hash: "original"}, {Source: OpenCodePatchRevision, Hash: "original", Files: []string{""}}, {Source: OpenCodeSnapshotRevision, Hash: ""}, {Source: "other", Hash: "original"}} {
		if r.Validate() == nil {
			t.Fatal("invalid original revision shape accepted")
		}
	}
	r := &OpenCodeRevision{Source: OpenCodePatchRevision, Hash: "original", Files: []string{}}
	s := ArtifactSnapshot{Kind: OpenCodeRevisionArtifact, Revision: r}
	if s.Validate() != nil {
		t.Fatal("explicit empty native patch file list rejected")
	}
	s.Text = "invented patch"
	if s.Validate() == nil {
		t.Fatal("revision reference became invented content")
	}
	s.Text = ""
	s.Kind = ReasoningTextArtifact
	if s.Validate() == nil {
		t.Fatal("revision payload mixed with reasoning")
	}
}
