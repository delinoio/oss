package grok

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// These fixtures preserve observed pinned native fields with private original
// identities replaced consistently. No provider or user account was involved.
//
//go:embed testdata/prompt-result.json
var promptResultFixture []byte

//go:embed testdata/text-chunk.json
var textChunkFixture []byte

//go:embed testdata/turn-completed.json
var turnCompletedFixture []byte

//go:embed testdata/prompt-completed.json
var promptCompletedFixture []byte

const turnFixtureSession domain.ID = "019f6de0-a760-7000-8000-000000000071"
const turnFixturePrompt = "e5833c4a-d764-4428-8bd8-6c2968a34b1b"
const turnFixtureModel = "fixture-model"

func TestNativeTextCompletionRetainsIndependentCounterSemantics(t *testing.T) {
	result, err := parsePromptResult(promptResultFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := parseTextChunk(textChunkFixture, turnFixtureSession, turnFixturePrompt)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := parseTurnCompleted(turnCompletedFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := parsePromptCompleted(promptCompletedFixture, turnFixtureSession, turnFixturePrompt)
	if err != nil {
		t.Fatal(err)
	}
	if err := matchCompletion(result, turn, prompt, turnFixtureModel); err != nil {
		t.Fatal(err)
	}
	if result.Meta.Usage.Total != 16 || result.Meta.Usage.Input != 11 || result.Meta.Usage.Output != 5 || chunk.Meta.ContextTokens == result.Meta.Usage.Total || chunk.Update.Content.Text != "Private fixture response." {
		t.Fatal("native observations were collapsed or changed")
	}
	for _, reason := range []StopReason{EndTurn, MaxTokens, MaxTurnRequests, Refusal, Cancelled} {
		result.Reason, turn.Update.Reason, prompt.Reason = reason, reason, reason
		if matchCompletion(result, turn, prompt, turnFixtureModel) != nil {
			t.Fatal("native stop distinction lost")
		}
	}
	prompt.Reason = EndTurn
	if matchCompletion(result, turn, prompt, turnFixtureModel) == nil {
		t.Fatal("different native outcomes accepted")
	}
}

func TestNativeTurnProfilesRejectForeignOwnershipAndUnknownShape(t *testing.T) {
	for _, test := range []struct {
		name  string
		raw   []byte
		parse func([]byte) error
	}{
		{"result", promptResultFixture, func(raw []byte) error {
			_, err := parsePromptResult(raw, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
			return err
		}},
		{"text", textChunkFixture, func(raw []byte) error {
			_, err := parseTextChunk(raw, turnFixtureSession, turnFixturePrompt)
			return err
		}},
		{"turn", turnCompletedFixture, func(raw []byte) error {
			_, err := parseTurnCompleted(raw, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
			return err
		}},
		{"prompt", promptCompletedFixture, func(raw []byte) error {
			_, err := parsePromptCompleted(raw, turnFixtureSession, turnFixturePrompt)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, raw := range []string{
				strings.ReplaceAll(string(test.raw), string(turnFixtureSession), string(domain.NewID())),
				strings.ReplaceAll(string(test.raw), turnFixturePrompt, "2aed0867-746a-4425-8860-b41c9c0c0b5c"),
				strings.Replace(string(test.raw), "{", `{"foreign":true,`, 1),
				strings.Replace(string(test.raw), "{", `{"sessionId":null,`, 1),
				"null",
			} {
				if test.parse([]byte(raw)) == nil {
					t.Fatal("invalid original native profile accepted")
				}
			}
		})
	}
}

func TestNativeUsageIsExactAndRejectsForeignModelOrMissingCounters(t *testing.T) {
	// All matching native counters retain an integer beyond JavaScript's exact
	// range. The parser must not normalize through a floating-point JSON map.
	raw := strings.ReplaceAll(string(promptResultFixture), `16`, `9007199254740993`)
	result, err := parsePromptResult([]byte(raw), turnFixtureSession, turnFixturePrompt, turnFixtureModel)
	if err != nil || result.Meta.Usage.Total != 9007199254740993 {
		t.Fatal("native counter rounded", err)
	}
	for _, change := range []string{"negative", "fraction", "null", "missing", "foreign-model", "conflicting-model", "unknown-reason", "nullable-usage", "different-request"} {
		t.Run(change, func(t *testing.T) {
			value := fixtureObject(promptResultFixture)
			meta := value["_meta"].(map[string]any)
			usage := meta["usage"].(map[string]any)
			model := usage["modelUsage"].(map[string]any)[turnFixtureModel].(map[string]any)
			switch change {
			case "negative":
				usage["inputTokens"] = -1
			case "fraction":
				usage["inputTokens"] = 0.5
			case "null":
				usage["inputTokens"] = nil
			case "missing":
				delete(model, "cacheCreationTokens")
			case "foreign-model":
				usage["modelUsage"] = map[string]any{"foreign": model}
			case "conflicting-model":
				model["inputTokens"] = 99
			case "unknown-reason":
				value["stopReason"] = "success"
			case "nullable-usage":
				meta["usage"] = nil
			case "different-request":
				meta["requestId"] = "2aed0867-746a-4425-8860-b41c9c0c0b5c"
			}
			raw, _ := json.Marshal(value)
			if _, err := parsePromptResult(raw, turnFixtureSession, turnFixturePrompt, turnFixtureModel); err == nil {
				t.Fatal("invalid usage accepted")
			}
		})
	}
	for _, suffix := range []string{"-1", "01", "1.0", "18446744073709551616"} {
		if _, err := eventIndex(string(turnFixtureSession)+"-"+suffix, turnFixtureSession); err == nil {
			t.Fatal("noncanonical event index accepted")
		}
	}
}
