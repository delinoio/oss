package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func originalClosedTextFixture(t *testing.T) (*OwnedAPI, string) {
	t.Helper()
	a, _, directory := textHistoryFixture(t)
	result, err := parsePromptResult(promptResultFixture, a.session, a.completedText.prompt, a.profile.model)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := parseTurnCompleted(turnCompletedFixture, a.session, a.completedText.prompt, a.profile.model)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := parsePromptCompleted(promptCompletedFixture, a.session, a.completedText.prompt)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := retainTextTerminal(result, turn, prompt)
	if err != nil {
		t.Fatal(err)
	}
	a.completedText.terminalFacts = &terminal
	return &OwnedAPI{connection: a}, directory
}
func TestClosedTextObservationPreservesIndependentOriginalFacts(t *testing.T) {
	api, _ := originalClosedTextFixture(t)
	ctx := context.Background()
	first, err := api.ObserveClosedText(ctx)
	if err != nil || first.Validate(turnFixtureModel) != nil || first.Terminal.Result.Meta.Usage.Input != 11 || first.Terminal.Turn.Update.Usage.Input != 11 || first.Terminal.Prompt.Reason != EndTurn {
		t.Fatal("closed observation lost original terminal", err)
	}
	chunk, err := parseTextChunk(textChunkFixture, turnFixtureSession, turnFixturePrompt)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := TextChunkDigest(chunk)
	if err != nil || first.ChunkDigests[0] != digest {
		t.Fatal("original chunk comparison changed", err)
	}
	baseline, _ := json.Marshal(first)
	first.Terminal.Result.Meta.Usage.Models[0] = 'x'
	first.Terminal.Turn.Update.Usage.Models[0] = 'x'
	first.Terminal.Prompt.AgentResult[0] = 'x'
	first.ChunkDigests[0] = "changed"
	second, err := api.ObserveClosedText(ctx)
	retained, _ := json.Marshal(second)
	if err != nil || string(baseline) != string(retained) {
		t.Fatal("caller replaced original terminal", err)
	}
	if _, err := api.CompletedTextScope(ctx); err == nil {
		t.Fatal("closed observation acquired live closure scope")
	}
}
func TestClosedTextObservationRejectsLostChangedAndForeignEvidence(t *testing.T) {
	for _, mutation := range []string{"missing", "result", "turn", "prompt", "retained-digest", "closed", "history", "files", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			api, dir := originalClosedTextFixture(t)
			ctx := context.Background()
			prior, err := api.ObserveClosedText(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "missing":
				api.connection.completedText.terminalFacts = nil
			case "result":
				api.connection.completedText.terminalFacts.Result.Meta.Usage.Input++
			case "turn":
				api.connection.completedText.terminalFacts.Turn.Meta.Event = string(turnFixtureSession) + "-99"
			case "prompt":
				api.connection.completedText.terminalFacts.Prompt.Reason = Cancelled
			case "retained-digest":
				api.connection.completedText.terminal[0]++
			case "closed":
				api.connection.completedText.closed = ""
			case "history":
				api.connection.completedText.history.FilesDigest = "changed"
			case "files":
				if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := api.ObserveClosedText(ctx); err == nil {
				t.Fatal("changed original evidence was observed")
			}
			if mutation != "history" && api.connection.completedText.history != nil && !reflect.DeepEqual(*api.connection.completedText.history, prior.History) {
				t.Fatal("failure repinned original history")
			}
		})
	}
}
