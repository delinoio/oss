package grok

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

//go:embed testdata/cancel-result.json
var interruptedResultFixture []byte

//go:embed testdata/cancel-turn.json
var interruptedTurnFixture []byte

//go:embed testdata/cancel-completed.json
var interruptedCompletedFixture []byte

func TestInterruptedNativeResultPreservesMissingUsage(t *testing.T) {
	result, err := parseInterruptedPromptResult(interruptedResultFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := parseInterruptedTurn(interruptedTurnFixture, turnFixtureSession, turnFixturePrompt)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := parseInterruptedPromptCompleted(interruptedCompletedFixture, turnFixtureSession, turnFixturePrompt)
	if err != nil {
		t.Fatal(err)
	}
	if matchInterruption(result, turn, complete) != nil || result.Meta.ContextTokens != 2262 {
		t.Fatal("native interrupted facts changed")
	}
	if _, err := parsePromptResult(interruptedResultFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel); err == nil {
		t.Fatal("interruption fabricated successful usage")
	}
	if _, err := parseTurnCompleted(interruptedTurnFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel); err == nil {
		t.Fatal("interrupted turn acquired successful usage")
	}
	raw := strings.ReplaceAll(string(interruptedResultFixture), "2262", "9007199254740993")
	exact, err := parseInterruptedPromptResult([]byte(raw), turnFixtureSession, turnFixturePrompt, turnFixtureModel)
	if err != nil || exact.Meta.ContextTokens != 9007199254740993 {
		t.Fatal("context observation rounded", err)
	}
	complete.Session = domain.NewID()
	if matchInterruption(result, turn, complete) == nil {
		t.Fatal("foreign completion joined interruption")
	}
}

func TestInterruptedNativeProfilesRejectForeignAndInventedFacts(t *testing.T) {
	for _, profile := range []struct {
		name  string
		raw   []byte
		parse func([]byte) error
	}{
		{"result", interruptedResultFixture, func(raw []byte) error {
			_, err := parseInterruptedPromptResult(raw, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
			return err
		}},
		{"turn", interruptedTurnFixture, func(raw []byte) error {
			_, err := parseInterruptedTurn(raw, turnFixtureSession, turnFixturePrompt)
			return err
		}},
		{"completed", interruptedCompletedFixture, func(raw []byte) error {
			_, err := parseInterruptedPromptCompleted(raw, turnFixtureSession, turnFixturePrompt)
			return err
		}},
	} {
		for _, raw := range []string{
			strings.ReplaceAll(string(profile.raw), string(turnFixtureSession), string(domain.NewID())),
			strings.ReplaceAll(string(profile.raw), turnFixturePrompt, "2aed0867-746a-4425-8860-b41c9c0c0b5c"),
			strings.ReplaceAll(string(profile.raw), "cancelled", "end_turn"),
			strings.ReplaceAll(string(profile.raw), "MidTurnAbort", "unknown"),
			strings.Replace(string(profile.raw), "{", `{"usage":{},`, 1),
			strings.Replace(string(profile.raw), "{", `{"cancellationCategory":null,`, 1),
			"null",
		} {
			if profile.parse([]byte(raw)) == nil {
				t.Fatal("invalid interruption accepted", profile.name)
			}
		}
		root := fixtureObject(profile.raw)
		var inspect func(any)
		inspect = func(value any) {
			switch node := value.(type) {
			case map[string]any:
				keys := make([]string, 0, len(node))
				for key := range node {
					keys = append(keys, key)
				}
				for _, key := range keys {
					child := node[key]
					node[strings.ToUpper(key)] = child
					raw, _ := json.Marshal(root)
					delete(node, strings.ToUpper(key))
					if profile.parse(raw) == nil {
						t.Fatal("nested alias accepted", profile.name, key)
					}
					if key != "agentResult" {
						node[key] = nil
						raw, _ = json.Marshal(root)
						node[key] = child
						if profile.parse(raw) == nil {
							t.Fatal("null native fact accepted", profile.name, key)
						}
					}
					inspect(child)
				}
			case []any:
				for _, child := range node {
					inspect(child)
				}
			}
		}
		inspect(root)
	}
}
