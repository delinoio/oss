package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func acceptedGrokContentFixture(t *testing.T) (*GrokBindingPublisher, *openCodeBindingRPC) {
	t.Helper()
	return acceptedGrokContentFixtureWithNativeSession(t, "")
}

func acceptedGrokContentFixtureWithNativeSession(t *testing.T, session domain.ID) (*GrokBindingPublisher, *openCodeBindingRPC) {
	t.Helper()
	c, claims, binding, client := newGrokBindingFixture(t, domain.ExecuteMode)
	if session != "" {
		if err := session.Validate(); err != nil {
			t.Fatal(err)
		}
		digest, err := grok.TextInputClaimDigest(session, c.publisher.input.Input.Prompt)
		if err != nil {
			t.Fatal(err)
		}
		binding.NativeSessionID, claims[1].Creation.NativeSessionID = session, session
		for _, claim := range claims[2:] {
			claim.Input.NativeSessionID, claim.Input.BodyDigest = session, digest
		}
	}
	ctx := context.Background()
	for _, claim := range claims[:2] {
		if err := recordBindingClaim(ctx, c, claim); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.BindSession(ctx, binding); err != nil {
		t.Fatal(err)
	}
	for _, claim := range claims[2:] {
		if err := recordBindingClaim(ctx, c, claim); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AcceptInput(ctx, bindingInputObservation(c, claims)); err != nil {
		t.Fatal(err)
	}
	return c, client
}
func grokContentText(c *GrokBindingPublisher) grok.InputObservation {
	chunk := &grok.TextChunk{Session: c.thread}
	chunk.Update.Kind = "agent_message_chunk"
	chunk.Update.Content.Type = "text"
	chunk.Update.Content.Text = "Original <script>private</script> 한글"
	chunk.Meta.Event = string(c.thread) + "-10"
	chunk.Meta.Prompt = c.turn
	chunk.Meta.Type = "AgentMessageChunk"
	chunk.Meta.Chunk = 1
	chunk.Meta.ContextTokens = 18446744073709551615
	return grok.InputObservation{Kind: grok.InputText, InputID: c.reference.InputRequestID, NativePromptID: c.turn, Chunk: chunk}
}
func grokContentResponse(t *testing.T, c *GrokBindingPublisher) grok.InputObservation {
	t.Helper()
	v := grok.InputObservation{Kind: grok.InputResponse, InputID: c.reference.InputRequestID, NativePromptID: c.turn}
	if err := json.Unmarshal([]byte(`{"Response":{"input_tokens":11,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"reasoning_tokens":0}}`), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestGrokContentLostReceiptsPreserveOriginalStateAndClaims(t *testing.T) {
	c, client := acceptedGrokContentFixture(t)
	ctx := context.Background()
	text := grokContentText(c)
	usage := grokContentResponse(t, c)
	for _, v := range []grok.InputObservation{text, usage, usage} {
		prior := c.content
		original, _ := security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
		client.lose = true
		if c.ObserveContent(ctx, v) == nil || c.content != prior || c.stage != grokContentPending {
			t.Fatal("lost receipt advanced original content")
		}
		if c.ObserveContent(ctx, v) == nil {
			t.Fatal("pending receipt was replaced")
		}
		index := len(client.events) - 1
		client.lose = false
		if err := c.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		retained, _ := security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
		if !bytes.Equal(original, retained) || !bytes.Equal(client.events[index], client.events[index+1]) || client.requests[index] != client.requests[index+1] || c.stage != grokInputAccepted || c.publisher.state.Pending != nil {
			t.Fatal("replay changed original publication or native claims")
		}
	}
	if c.content.Responses != 2 || c.content.MessageID != "" || c.sequence != 5 {
		t.Fatal("response ordinals changed")
	}
	var event domain.ExecutionEvent
	if domain.Decode(client.events[2], &event) != nil || event.GrokText == nil || event.GrokText.Metadata.ContextTokens != "18446744073709551615" || event.GrokText.Metadata.EventID != text.Chunk.Meta.Event {
		t.Fatal("text provenance lost")
	}
}
func TestGrokContentRejectsForeignClaimsPayloadsAndOrdering(t *testing.T) {
	for _, mutation := range []string{"input", "prompt", "session", "native-prompt", "kind", "mixed", "missing", "schema", "event", "duplicate", "regression", "missing-journal", "closed-journal"} {
		t.Run(mutation, func(t *testing.T) {
			c, client := acceptedGrokContentFixture(t)
			ctx := context.Background()
			v := grokContentText(c)
			if mutation == "duplicate" || mutation == "regression" {
				if err := c.ObserveContent(ctx, v); err != nil {
					t.Fatal(err)
				}
			}
			switch mutation {
			case "input":
				v.InputID = domain.NewID()
			case "prompt":
				v.NativePromptID = "526452fa-1956-42dd-b5f4-60e2b23dfe92"
			case "session":
				v.Chunk.Session = domain.NewID()
			case "native-prompt":
				v.Chunk.Meta.Prompt = "526452fa-1956-42dd-b5f4-60e2b23dfe92"
			case "kind":
				v.Kind = grok.InputCompleted
			case "mixed":
				v.Title = "not text"
			case "missing":
				v.Chunk = nil
			case "schema":
				v.Chunk.Update.Content.Type = "image"
			case "event":
				v.Chunk.Meta.Event = string(c.thread) + "-01"
			case "regression":
				v.Chunk.Meta.Event = string(c.thread) + "-11"
				v.Chunk.Meta.Chunk = 1
			case "missing-journal":
				if err := os.Remove(c.journal.path); err != nil {
					t.Fatal(err)
				}
			case "closed-journal":
				if err := c.journal.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, requests := c.content, len(client.requests)
			if c.ObserveContent(ctx, v) == nil || c.content != before || len(client.requests) != requests || c.stage != grokBindingBlocked {
				t.Fatal("invalid content changed retained facts")
			}
		})
	}
}
func TestGrokContentReplayRejectsReplacedEvidence(t *testing.T) {
	for _, mutation := range []string{"text", "request", "claims", "closed"} {
		t.Run(mutation, func(t *testing.T) {
			c, client := acceptedGrokContentFixture(t)
			ctx := context.Background()
			client.lose = true
			if c.ObserveContent(ctx, grokContentText(c)) == nil {
				t.Fatal("lost response expected")
			}
			switch mutation {
			case "text":
				c.publisher.state.Pending.Event.GrokText.Text = "changed"
			case "request":
				c.publisher.state.Pending.RequestID = domain.NewID()
			case "claims":
				if err := os.Remove(c.journal.path); err != nil {
					t.Fatal(err)
				}
			case "closed":
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := len(client.requests)
			client.lose = false
			if c.ReplayPending(ctx) == nil || len(client.requests) != before || c.content.MessageID != "" {
				t.Fatal("changed pending evidence replayed")
			}
		})
	}
}
