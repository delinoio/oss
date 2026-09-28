package grok

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
)

// Original pinned native rejection records, with only generated session/prompt
// identifiers replaced. No provider account or user content was involved.
//
//go:embed testdata/file-reject-result.json
var rejectedFileResultFixture []byte

//go:embed testdata/file-reject-turn.json
var rejectedFileTurnFixture []byte

//go:embed testdata/file-reject-completed.json
var rejectedFileCompletedFixture []byte

//go:embed testdata/file-reject-failed.json
var rejectedFileFailedFixture []byte

func rejectionAccounting() responseAccounting {
	var a responseAccounting
	_ = a.observe(responseUsage{Input: 11, Output: 5})
	return a
}

func TestFileRejectionPreservesOriginalUsageAndDistinctCategory(t *testing.T) {
	r, err := parseRejectedFileResult(rejectedFileResultFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel, rejectionAccounting())
	if err != nil {
		t.Fatal(err)
	}
	u, err := parseRejectedFileTurn(rejectedFileTurnFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseRejectedFileCompletion(rejectedFileCompletedFixture, turnFixtureSession, turnFixturePrompt)
	if err != nil || matchFileRejection(r, u, p, turnFixtureModel) != nil || r.Result.Meta.Usage.Input != 11 || r.Context.Tool != writeFileTool {
		t.Fatal("original permission rejection lost usage/context", err)
	}
	p.Context.Reason = "changed"
	if matchFileRejection(r, u, p, turnFixtureModel) == nil {
		t.Fatal("changed native rejection context accepted")
	}
	if _, err := parseInterruptedPromptResult(rejectedFileResultFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel); err == nil {
		t.Fatal("rejection was treated as user Stop")
	}
	for _, profile := range []struct {
		raw   []byte
		parse func([]byte) error
	}{
		{rejectedFileResultFixture, func(raw []byte) error {
			_, e := parseRejectedFileResult(raw, turnFixtureSession, turnFixturePrompt, turnFixtureModel, rejectionAccounting())
			return e
		}},
		{rejectedFileTurnFixture, func(raw []byte) error {
			_, e := parseRejectedFileTurn(raw, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
			return e
		}},
		{rejectedFileCompletedFixture, func(raw []byte) error {
			_, e := parseRejectedFileCompletion(raw, turnFixtureSession, turnFixturePrompt)
			return e
		}},
	} {
		for _, raw := range []string{
			strings.ReplaceAll(string(profile.raw), "PermissionRejected", "MidTurnAbort"),
			strings.ReplaceAll(string(profile.raw), "cancelled", "end_turn"),
			strings.ReplaceAll(string(profile.raw), `"write"`, `"read_file"`),
			strings.ReplaceAll(string(profile.raw), `"tool_name"`, `"Tool_name"`),
			strings.ReplaceAll(string(profile.raw), `"cancellationCategory":`, `"cancellationCategory": "PermissionRejected", "cancellationCategory":`),
			strings.ReplaceAll(string(profile.raw), `"cancellationContext":`, `"CancellationContext": {}, "cancellationContext":`),
			strings.ReplaceAll(string(profile.raw), `"reason": "User rejected the execution"`, `"reason": null`),
			strings.ReplaceAll(string(profile.raw), turnFixturePrompt, "e5833c4a-d764-4428-8bd8-6c2968a34b1c"),
		} {
			if profile.parse([]byte(raw)) == nil {
				t.Fatal("invalid rejection accepted")
			}
		}
	}
	large := []byte(strings.Replace(string(rejectedFileResultFixture), `"totalTokens": 16`, `"totalTokens": 9007199254740993`, 1))
	exact, err := parseRejectedFileResult(large, turnFixtureSession, turnFixturePrompt, turnFixtureModel, rejectionAccounting())
	if err != nil || exact.Result.Meta.Total != 9007199254740993 {
		t.Fatal("context rounded during extension parsing", err)
	}
}

func TestFailedWriteIsAnObservationNotAnAnswer(t *testing.T) {
	events := fileToolEvents(t, true)
	events[len(events)-1].Params = rejectedFileFailedFixture
	o, _ := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
	for i, e := range events {
		fact, err := o.observe(e)
		if err != nil {
			t.Fatal(err)
		}
		if i == len(events)-1 && (fact.Observation.Phase != fileToolFailed || fact.Observation.Failure == nil || fact.Observation.Write != nil) {
			t.Fatal("failure acquired completed output")
		}
	}
	if o.settled() {
		t.Fatal("failure acquired successful settlement")
	}
	for _, change := range []string{"status", "output", "read", "empty"} {
		value := fixtureObject(rejectedFileFailedFixture)
		input := fileToolInput{Name: writeFileTool, Path: "fixture.txt", Content: "written"}
		switch change {
		case "status":
			value["_meta"].(map[string]any)["updateParams"].(map[string]any)["status"] = "Completed"
		case "output":
			value["update"].(map[string]any)["rawOutput"] = nil
		case "read":
			input.Name = readFileTool
		case "empty":
			value["update"].(map[string]any)["content"] = []any{}
		}
		raw, _ := json.Marshal(value)
		if _, err := parseFileToolObservation(raw, turnFixtureSession, turnFixturePrompt, &input); err == nil {
			t.Fatal("invalid failed tool accepted", change)
		}
	}
}
