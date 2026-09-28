package worker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func grokClosedPublicationFixture(t *testing.T) (*GrokBindingPublisher, *openCodeBindingRPC, grok.ClosedTextObservation) {
	t.Helper()
	c, client := acceptedGrokContentFixture(t)
	ctx := context.Background()
	if err := c.ObserveContent(ctx, grokContentText(c)); err != nil {
		t.Fatal(err)
	}
	if err := c.ObserveContent(ctx, grokContentResponse(t, c)); err != nil {
		t.Fatal(err)
	}
	c.stage, c.closureID = grokTextClosing, domain.NewID()
	digest, _ := grok.ClosureClaimDigest(c.thread)
	claim := grok.ClosureClaim{Phase: grok.ClaimClosure, RequestID: c.closureID, ProductSessionID: c.reference.SessionID, NativeSessionID: c.thread, NativePromptID: c.turn, BodyDigest: digest}
	if err := c.Closure(ctx, claim); err != nil {
		t.Fatal(err)
	}
	claim.Phase = grok.BindClosure
	if err := c.Closure(ctx, claim); err != nil {
		t.Fatal(err)
	}
	model := c.publisher.input.Configuration.NativeModel
	models, _ := json.Marshal(map[string]grok.ModelUsage{model: {Input: 11, Output: 5, Total: 16, Calls: 1, DurationMS: 2}})
	usage := grok.TurnUsage{Input: 11, Output: 5, Total: 16, Calls: 1, DurationMS: 2, Models: models, Turns: 1}
	result := grok.PromptResult{Reason: grok.EndTurn}
	result.Meta.Session = c.thread
	result.Meta.Request = c.turn
	result.Meta.Prompt = c.turn
	result.Meta.Model = model
	result.Meta.Total = 16
	result.Meta.Input = 11
	result.Meta.Output = 5
	result.Meta.Usage = usage
	turn := grok.TurnCompleted{Session: c.thread}
	turn.Update.Kind = "turn_completed"
	turn.Update.Prompt = c.turn
	turn.Update.Reason = grok.EndTurn
	turn.Update.Usage = usage
	turn.Update.ElapsedMS = 3
	turn.Meta.Event = string(c.thread) + "-11"
	turn.Meta.TimestampMS = 1
	observed := grok.ClosedTextObservation{History: grok.TextHistory{User: domain.GrokUserHistory{Source: domain.GrokClosedFirstText, NativeEventID: string(c.thread) + "-2", TimestampMS: "0", PromptIndex: "0", Model: model, InputDigest: domain.GrokUserInputDigest(c.publisher.input.Input.Prompt)}, InputID: c.reference.InputRequestID, ClosureID: c.closureID, NativeSessionID: c.thread, NativePromptID: c.turn, FilesDigest: strings.Repeat("ab", 32), TextChunks: 1}, Terminal: grok.TextTerminal{Result: result, Turn: turn, Prompt: grok.PromptCompleted{Session: c.thread, Prompt: c.turn, Reason: grok.EndTurn, AgentResult: json.RawMessage(`null`)}}, OutputDigest: hex.EncodeToString(c.textOutput.Sum(nil)), ChunkDigests: append([]string{}, c.textChunks...)}
	if err := observed.Validate(model); err != nil {
		t.Fatal(err)
	}
	return c, client, observed
}
func TestGrokOriginalTerminalLostReceiptNeverRepeatsClosure(t *testing.T) {
	c, client, v := grokClosedPublicationFixture(t)
	ctx := context.Background()
	before, _ := security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
	client.lose = true
	if c.publishClosedText(ctx, v, c.closureID) == nil || c.stage != grokTerminalPending {
		t.Fatal("lost terminal receipt advanced completion")
	}
	if _, err := c.TextCompletion(); err == nil {
		t.Fatal("pending terminal granted completion")
	}
	client.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
	if !bytes.Equal(before, after) || !bytes.Equal(client.events[4], client.events[5]) || client.requests[4] != client.requests[5] {
		t.Fatal("receipt replay changed native work")
	}
	report, err := c.TextCompletion()
	if err != nil || report.Version != 1 || report.NativeCheckpointDigest != "" || report.LastSequence != 5 {
		t.Fatal("original terminal completion missing", err)
	}
	again, err := c.TextCompletion()
	if err != nil || again != report || len(client.events) != 6 {
		t.Fatal("reading completion replayed work")
	}
	if c.publishClosedText(ctx, v, c.closureID) == nil || len(client.events) != 6 {
		t.Fatal("completed text published twice")
	}
}
func TestGrokOriginalTerminalRejectsChangedComparisonEvidence(t *testing.T) {
	for _, mutation := range []string{"chunk", "output", "input", "closure", "model", "native-event", "prompt", "counter", "history", "stage", "journal", "user-text", "user-model", "user-index", "user-event", "missing-user", "user-order"} {
		t.Run(mutation, func(t *testing.T) {
			c, client, v := grokClosedPublicationFixture(t)
			switch mutation {
			case "user-order":
				v.History.User.NativeEventID = c.firstTextEvent
			case "user-text":
				v.History.User.InputDigest = domain.GrokUserInputDigest("changed")
			case "user-model":
				v.History.User.Model = "foreign"
			case "user-index":
				v.History.User.PromptIndex = "1"
			case "user-event":
				v.History.User.NativeEventID = string(domain.NewID()) + "-2"
			case "missing-user":
				v.History.User = domain.GrokUserHistory{}
			case "chunk":
				v.ChunkDigests[0] = strings.Repeat("cd", 32)
			case "output":
				v.OutputDigest = strings.Repeat("cd", 32)
			case "input":
				v.History.InputID = domain.NewID()
			case "closure":
				v.History.ClosureID = domain.NewID()
			case "model":
				v.Terminal.Result.Meta.Model = "foreign"
			case "native-event":
				v.Terminal.Turn.Meta.Event = string(domain.NewID()) + "-11"
			case "prompt":
				v.Terminal.Prompt.Reason = grok.Cancelled
			case "counter":
				v.Terminal.Result.Meta.Usage.Input++
			case "history":
				v.History.FilesDigest = ""
			case "stage":
				c.stage = grokInputAccepted
			case "journal":
				c.journal.closed = true
			}
			before := len(client.events)
			if c.publishClosedText(context.Background(), v, c.closureID) == nil || len(client.events) != before {
				t.Fatal("changed evidence published terminal")
			}
			if _, err := c.TextCompletion(); err == nil {
				t.Fatal("changed terminal granted report")
			}
		})
	}
}
