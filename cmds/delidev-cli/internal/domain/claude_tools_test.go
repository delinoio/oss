package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func claudeToolFixture() ClaudeToolUpdate {
	initial := "{}"
	return ClaudeToolUpdate{Mutation: ClaudeToolStart, Reference: ClaudeToolReference{ID: NewID(), NativeID: "toolu_original", Name: "Read"}, MessageID: NewID(), NativeMessageID: "msg_original", Index: 0, InitialInput: &initial}
}
func TestClaudeToolPreservesOriginalProposalAndIndependentResult(t *testing.T) {
	u := claudeToolFixture()
	content, state, err := ApplyClaudeTool(nil, "", u)
	if err != nil {
		t.Fatal(err)
	}
	delta := `{ "file_path":"/inert/original", "large":9007199254740993 }`
	u.Mutation, u.InitialInput, u.Delta = ClaudeToolInputAppend, nil, &delta
	next, _, err := ApplyClaudeTool(content, state, u)
	if err != nil || content.InputDelta != nil || *next.InputDelta != delta {
		t.Fatal("input fragment changed prior data", err)
	}
	content = next
	u.Mutation, u.Delta, u.Proposal = ClaudeToolProposalComplete, nil, &ClaudeToolProposal{Proposed: delta, Applied: `{"file_path":"/inert/original","native_added":true}`}
	content, state, err = ApplyClaudeTool(content, state, u)
	if err != nil || state != MessageStreaming || content.Result != nil || content.Proposal.Proposed != delta {
		t.Fatal("proposal completion fabricated execution", err)
	}
	u.Proposal.Proposed = "caller mutation"
	if content.Proposal.Proposed != delta {
		t.Fatal("proposal was not copied")
	}
	resultText := "Original native result 🐦"
	metadata := `{"exact":9007199254740993}`
	u.Mutation, u.Proposal, u.Result = ClaudeToolResultObserved, nil, &ClaudeToolResult{NativeEventID: string(NewID()), Text: &resultText, Structured: &metadata}
	next, state, err = ApplyClaudeTool(content, state, u)
	if err != nil || state != MessageComplete || content.Result != nil || next.Result.Error != nil || *next.Result.Structured != metadata || *next.Result.Text != resultText {
		t.Fatal("result scope, availability or exact data changed", err)
	}
	resultText = "caller mutation"
	if *next.Result.Text == resultText {
		t.Fatal("result was not copied")
	}
	if _, _, err := ApplyClaudeTool(next, state, u); err == nil {
		t.Fatal("original result repeated")
	}
}

func TestClaudeToolRejectsMalformedOrForeignTransitionsAtomically(t *testing.T) {
	for _, name := range []string{"identity", "provider", "index", "caller", "result-before-proposal", "mixed", "changed-proposal", "invalid-json", "overflow"} {
		t.Run(name, func(t *testing.T) {
			u := claudeToolFixture()
			prior, state, err := ApplyClaudeTool(nil, "", u)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(prior)
			delta := `{}`
			u.Mutation, u.InitialInput, u.Delta = ClaudeToolInputAppend, nil, &delta
			switch name {
			case "identity":
				u.Reference.ID = NewID()
			case "provider":
				u.NativeMessageID = "other"
			case "index":
				u.Index = 1
			case "caller":
				caller := ClaudeDirectToolCaller
				u.Caller = &caller
			case "result-before-proposal":
				u.Mutation, u.Delta, u.Result = ClaudeToolResultObserved, nil, &ClaudeToolResult{NativeEventID: string(NewID())}
			case "mixed":
				u.Result = &ClaudeToolResult{NativeEventID: string(NewID())}
			case "changed-proposal":
				u.Mutation, u.Delta, u.Proposal = ClaudeToolProposalComplete, nil, &ClaudeToolProposal{Proposed: `{"changed":true}`, Applied: `{}`}
			case "invalid-json":
				u.Mutation, u.Delta, u.Proposal = ClaudeToolProposalComplete, nil, &ClaudeToolProposal{Proposed: `[]`, Applied: `{}`}
			case "overflow":
				delta = strings.Repeat("x", MaxMessageText+1)
			}
			if _, _, err := ApplyClaudeTool(prior, state, u); err == nil {
				t.Fatal("invalid transition accepted")
			}
			after, _ := json.Marshal(prior)
			if string(before) != string(after) {
				t.Fatal("rejection changed prior state")
			}
		})
	}
}

func TestClaudeResultMetadataPreservesExplicitErrorAndRejectsNull(t *testing.T) {
	yes, no := true, false
	for _, metadata := range []string{`"original error"`, `""`, `null`, `[]`, `1`, `true`, `{"valid":1}`, `{"duplicate":1,"duplicate":2}`} {
		for _, failed := range []*bool{nil, &no, &yes} {
			expected := metadata == `{"valid":1}` || failed != nil && *failed && (metadata == `"original error"` || metadata == `""`)
			if ValidClaudeToolResultMetadata(metadata, failed) != expected {
				t.Fatal("metadata lost original shape or native error requirement", metadata, failed)
			}
		}
	}
}
