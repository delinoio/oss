package worker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func grokStoppedPublicationFixture(t *testing.T, kind domain.GrokStopKind) (*GrokBindingPublisher, *openCodeBindingRPC, grok.StoppedTextObservation) {
	t.Helper()
	c, client := acceptedGrokContentFixture(t)
	ctx := context.Background()
	// Lose the original text receipt before the independent Stop claim. The
	// Stop tail must not prevent acknowledging exactly that pending receipt.
	if kind != domain.GrokInterruptedBeforeText {
		client.lose = true
		if c.ObserveContent(ctx, grokContentText(c)) == nil {
			t.Fatal("text receipt unexpectedly acknowledged")
		}
	}
	digest, _ := grok.ClosureClaimDigest(c.thread)
	claim := grok.StopClaim{Version: 1, OwnerID: c.reference.JobID, ProductSessionID: c.reference.SessionID, InputRequestID: c.reference.InputRequestID, RequestID: domain.NewID(), NativeSessionID: c.thread, NativePromptID: c.turn, BodyDigest: digest}
	c.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- c.Stop(ctx, claim) }()
	select {
	case err := <-done:
		if err != nil {
			c.mu.Unlock()
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		c.mu.Unlock()
		t.Fatal("publication lock blocked original Stop")
	}
	c.mu.Unlock()
	if c.Stop(ctx, claim) == nil {
		t.Fatal("Stop claim reopened")
	}
	client.lose = false
	if kind != domain.GrokInterruptedBeforeText {
		if err := c.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(client.events[2], client.events[3]) || client.requests[2] != client.requests[3] {
			t.Fatal("Stop changed pending original receipt")
		}
	}
	if kind == domain.GrokCompletedDuringStop {
		if err := c.ObserveContent(ctx, grokContentResponse(t, c)); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := c.readClaims()
	if err != nil || len(claims) != 5 {
		t.Fatal(err)
	}
	v := grok.StoppedTextObservation{CreationRequestID: c.reference.CreationRequestID, InputDigest: claims[2].Input.BodyDigest, OutputDigest: hex.EncodeToString(c.textOutput.Sum(nil)), ChunkDigests: append([]string{}, c.textChunks...), Stop: grok.StopObservation{Claim: claim, Claimed: true, Attempted: true, Delivered: true, Idle: true, CleanupJoined: true}}
	load := func(name string, target any) {
		raw, err := os.ReadFile("../harness/grok/testdata/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		text := strings.NewReplacer("019f6de0-a760-7000-8000-000000000071", string(c.thread), "e5833c4a-d764-4428-8bd8-6c2968a34b1b", c.turn, "fixture-model", c.publisher.input.Configuration.NativeModel).Replace(string(raw))
		if err := json.Unmarshal([]byte(text), target); err != nil {
			t.Fatal(err)
		}
	}
	if kind == domain.GrokCompletedDuringStop {
		v.Stop.NativeReason = grok.EndTurn
		v.Completed = &grok.TextTerminal{}
		load("prompt-result", &v.Completed.Result)
		load("turn-completed", &v.Completed.Turn)
		load("prompt-completed", &v.Completed.Prompt)
		v.Completed.Turn.Meta.Event = string(c.thread) + "-12"
	} else {
		v.Stop.NativeReason, v.Stop.Category = grok.Cancelled, grok.MidTurnAbort
		v.Interrupted = &grok.InterruptedTextTerminal{}
		load("cancel-result", &v.Interrupted.Result)
		load("cancel-turn", &v.Interrupted.Turn)
		load("cancel-completed", &v.Interrupted.Prompt)
		v.Interrupted.Turn.Meta.Event = string(c.thread) + "-12"
		v.Retries = []grok.RetryObservation{{Session: c.thread, Event: string(c.thread) + "-11", TimestampMS: 1, Kind: grok.Retrying, Error: grok.HTTPRetry, Attempt: 1, MaxRetries: 3}}
	}
	if err := v.Validate(c.publisher.input.Configuration.NativeModel); err != nil {
		t.Fatal(err)
	}
	return c, client, v
}

func TestGrokStopPublishesOriginalTerminalAndReplaysOnlyReceipt(t *testing.T) {
	for _, kind := range []domain.GrokStopKind{domain.GrokInterruptedBeforeText, domain.GrokInterruptedText, domain.GrokCompletedDuringStop} {
		c, client, v := grokStoppedPublicationFixture(t, kind)
		before, _ := security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
		client.lose = true
		if c.publishStoppedText(context.Background(), v) == nil || c.stage != grokTerminalPending {
			t.Fatal("lost Stop receipt completed execution")
		}
		if _, err := c.TextCompletion(); err == nil {
			t.Fatal("pending Stop granted report")
		}
		client.lose = false
		if err := c.ReplayPending(context.Background()); err != nil {
			t.Fatal(err)
		}
		after, _ := security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
		n := len(client.events)
		if !bytes.Equal(before, after) || !bytes.Equal(client.events[n-2], client.events[n-1]) || client.requests[n-2] != client.requests[n-1] {
			t.Fatal("receipt replay repeated native Stop")
		}
		proof, err := c.TextCompletion()
		outcome := domain.ExecutionStopped
		if kind == domain.GrokCompletedDuringStop {
			outcome = domain.ExecutionSucceeded
		}
		if err != nil || proof.Outcome != outcome || proof.Version != 1 || proof.NativeCheckpointDigest != "" || c.stopped == nil || c.stopped.Kind != kind {
			t.Fatal("Stop lost original outcome or acquired history", err)
		}
		if kind != domain.GrokCompletedDuringStop && (c.stopped.Completed != nil || c.stopped.ContextTokens == nil || len(c.stopped.Retries) != 1) {
			t.Fatal("interrupted context/retry became usage")
		}
		if kind == domain.GrokInterruptedBeforeText && (c.stopped.MessageID != "" || c.stopped.TextChunks != 0 || c.content != (domain.GrokContentState{})) {
			t.Fatal("pre-text Stop fabricated output")
		}
	}
}

func TestGrokStopRejectsChangedOriginalComparison(t *testing.T) {
	for _, name := range []string{"input", "creation", "output", "chunk", "stop", "model", "terminal", "retry", "journal", "usage", "hidden-output", "pending-metadata", "pending-message"} {
		t.Run(name, func(t *testing.T) {
			kind := domain.GrokInterruptedText
			if strings.HasPrefix(name, "pending-") {
				kind = domain.GrokInterruptedBeforeText
			}
			c, client, v := grokStoppedPublicationFixture(t, kind)
			switch name {
			case "input":
				v.InputDigest = strings.Repeat("ab", 32)
			case "creation":
				v.CreationRequestID = domain.NewID()
			case "output":
				v.OutputDigest = strings.Repeat("ab", 32)
			case "chunk":
				v.ChunkDigests[0] = strings.Repeat("ab", 32)
			case "stop":
				v.Stop.Claim.RequestID = domain.NewID()
			case "model":
				v.Interrupted.Result.Meta.Model = "foreign"
			case "terminal":
				v.Interrupted.Prompt.Category = "foreign"
			case "retry":
				v.Retries[0].Attempt = 2
			case "journal":
				c.journal.closed = true
			case "usage":
				c.content.Responses = 1
			case "hidden-output":
				v.ChunkDigests, v.OutputDigest = nil, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
			case "pending-metadata":
				c.content.LastEvent = string(c.thread) + "-10"
			case "pending-message":
				c.firstTextID, c.content.MessageID = domain.NewID(), domain.NewID()
			}
			n := len(client.events)
			if c.publishStoppedText(context.Background(), v) == nil || len(client.events) != n {
				t.Fatal("changed Stop proof published")
			}
			if _, err := c.TextCompletion(); err == nil {
				t.Fatal("changed Stop granted report")
			}
		})
	}
}
