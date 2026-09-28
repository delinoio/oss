package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func newGrokBindingFixture(t *testing.T, mode domain.SessionMode) (*GrokBindingPublisher, []grokClaim, grok.SessionBinding, *openCodeBindingRPC) {
	t.Helper()
	p, j, claims := newGrokClaimsFixtureForMode(t, mode)
	client := &openCodeBindingRPC{t: t, publisher: p}
	p.config.Client = client
	c, err := newGrokBindingPublisher(p, j)
	if err != nil {
		t.Fatal(err)
	}
	binding := grok.SessionBinding{OwnerID: p.job, ProductSessionID: p.input.SessionID, CreationRequestID: p.input.ThreadRequestID, NativeSessionID: claims[1].Creation.NativeSessionID, ConfigurationDigest: claims[1].Creation.ConfigurationDigest, Model: p.input.Configuration.NativeModel, Mode: grok.NativeDefaultMode}
	if mode == domain.PlanMode {
		modes := grokModeClaimsFixture(p, claims[1].Creation)
		binding.Mode, binding.ModeBinding = grok.NativePlanMode, modes[1].Mode
		claims = append(append(append([]grokClaim{}, claims[:2]...), modes...), claims[2:]...)
	}
	return c, claims, binding, client
}

func recordBindingClaim(ctx context.Context, c *GrokBindingPublisher, claim grokClaim) error {
	if claim.Creation != nil {
		return c.Creation(ctx, *claim.Creation)
	}
	if claim.Mode != nil {
		return c.Mode(ctx, *claim.Mode)
	}
	return c.Input(ctx, *claim.Input)
}

func bindingInputObservation(c *GrokBindingPublisher, claims []grokClaim) grok.InputObservation {
	return grok.InputObservation{Kind: grok.InputAccepted, InputID: c.reference.InputRequestID, NativePromptID: claims[len(claims)-1].Input.NativePromptID}
}

func TestGrokBindingRequiresPublicationBeforeOriginalInput(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			c, claims, binding, client := newGrokBindingFixture(t, mode)
			ctx := context.Background()
			input := claims[len(claims)-2:]
			if c.Input(ctx, *input[0].Input) == nil {
				t.Fatal("unpublished session accepted original input")
			}
			for _, claim := range claims[:len(claims)-2] {
				if err := recordBindingClaim(ctx, c, claim); err != nil {
					t.Fatal(err)
				}
			}
			client.lose = true
			if c.BindSession(ctx, binding) == nil || c.Input(ctx, *input[0].Input) == nil {
				t.Fatal("lost session publication authorized input")
			}
			original, _ := security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
			client.lose = false
			if err := c.ReplayPending(ctx); err != nil {
				t.Fatal(err)
			}
			after, _ := security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
			if !bytes.Equal(original, after) || client.requests[0] != client.requests[1] || !bytes.Equal(client.events[0], client.events[1]) {
				t.Fatal("binding replay changed original native claims or receipt")
			}
			for _, claim := range input {
				if err := recordBindingClaim(ctx, c, claim); err != nil {
					t.Fatal(err)
				}
			}
			client.lose = true
			observation := bindingInputObservation(c, claims)
			if c.AcceptInput(ctx, observation) == nil || c.Input(ctx, *input[0].Input) == nil {
				t.Fatal("uncertain acceptance gained another input")
			}
			original, _ = security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
			client.lose = false
			if err := c.ReplayPending(ctx); err != nil {
				t.Fatal(err)
			}
			after, _ = security.ReadPrivate(c.journal.path, maxGrokClaimBytes)
			if !bytes.Equal(original, after) || client.requests[2] != client.requests[3] || !bytes.Equal(client.events[2], client.events[3]) || c.publisher.state.LastSequence != 2 || c.publisher.state.Pending != nil {
				t.Fatal("acceptance replay changed original authority")
			}
			if c.AcceptInput(ctx, observation) == nil || c.ReplayPending(ctx) == nil || c.BindSession(ctx, binding) == nil || len(client.requests) != 4 {
				t.Fatal("settled binding acquired another publication")
			}
			var retained domain.ExecutionEvent
			if domain.Decode(client.events[3], &retained) != nil || retained.GrokUserMessageID.Validate() != nil || retained.GrokUserMessageID != c.userMessageID || retained.NativeThreadID != string(binding.NativeSessionID) || retained.NativeTurnID != observation.NativePromptID {
				t.Fatal("Grok input was replaced with product identity")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if c.Creation(ctx, *claims[0].Creation) == nil || c.ReplayPending(ctx) == nil {
				t.Fatal("closed coordinator regained send authority")
			}
			if _, err := OpenGrokBindingPublisher(c.publisher); err == nil {
				t.Fatal("retained original journal was adopted by another publisher")
			}
		})
	}
}

func TestGrokBindingRejectsUnconfirmedOrForeignNativeEvidence(t *testing.T) {
	for _, mutation := range []string{"missing-claims", "model", "mode", "owner", "product", "creation", "session", "configuration", "mode-event", "mode-claim"} {
		t.Run(mutation, func(t *testing.T) {
			c, claims, binding, client := newGrokBindingFixture(t, domain.PlanMode)
			ctx := context.Background()
			if mutation != "missing-claims" {
				for _, claim := range claims[:4] {
					if err := recordBindingClaim(ctx, c, claim); err != nil {
						t.Fatal(err)
					}
				}
			}
			switch mutation {
			case "model":
				binding.Model = "foreign-model"
			case "mode":
				binding.Mode = grok.NativeDefaultMode
			case "owner":
				binding.OwnerID = domain.NewID()
			case "product":
				binding.ProductSessionID = domain.NewID()
			case "creation":
				binding.CreationRequestID = domain.NewID()
			case "session":
				binding.NativeSessionID = domain.NewID()
			case "configuration":
				binding.ConfigurationDigest = strings.Repeat("ab", 32)
			case "mode-event":
				b := *binding.ModeBinding
				b.EventID = string(binding.NativeSessionID) + "-3"
				binding.ModeBinding = &b
			case "mode-claim":
				binding.ModeBinding = nil
			}
			if c.BindSession(ctx, binding) == nil || len(client.events) != 0 || c.publisher.state.Pending != nil || c.stage != grokBindingBlocked {
				t.Fatal("foreign binding acquired durable publication")
			}
		})
	}
	for _, mutation := range []string{"missing-claim", "input", "prompt", "kind", "mixed", "empty"} {
		t.Run("input-"+mutation, func(t *testing.T) {
			c, claims, binding, client := newGrokBindingFixture(t, domain.ExecuteMode)
			ctx := context.Background()
			for _, claim := range claims[:2] {
				if err := recordBindingClaim(ctx, c, claim); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.BindSession(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if mutation != "missing-claim" {
				for _, claim := range claims[2:] {
					if err := recordBindingClaim(ctx, c, claim); err != nil {
						t.Fatal(err)
					}
				}
			}
			v := bindingInputObservation(c, claims)
			switch mutation {
			case "input":
				v.InputID = domain.NewID()
			case "prompt":
				v.NativePromptID = "526452fa-1956-42dd-b5f4-60e2b23dfe92"
			case "kind":
				v.Kind = grok.InputCompleted
			case "mixed":
				v.Title = "foreign title"
			case "empty":
				v.NativePromptID = ""
			}
			if c.AcceptInput(ctx, v) == nil || len(client.events) != 1 || c.publisher.state.Pending != nil {
				t.Fatal("unconfirmed native input acquired public acceptance")
			}
		})
	}
}

func TestGrokBindingReplayCannotReplaceOriginalProof(t *testing.T) {
	for _, mutation := range []string{"deleted-claims", "changed-claims", "failed-journal", "closed-journal", "changed-request", "changed-event"} {
		t.Run(mutation, func(t *testing.T) {
			c, claims, binding, client := newGrokBindingFixture(t, domain.ExecuteMode)
			ctx := context.Background()
			for _, claim := range claims[:2] {
				if err := recordBindingClaim(ctx, c, claim); err != nil {
					t.Fatal(err)
				}
			}
			client.lose = true
			if c.BindSession(ctx, binding) == nil {
				t.Fatal("fixture did not lose publication")
			}
			switch mutation {
			case "deleted-claims":
				if err := os.Remove(c.journal.path); err != nil {
					t.Fatal(err)
				}
			case "changed-claims":
				state := c.journal.state
				copy := *state.Claims[1].Creation
				copy.NativeSessionID = domain.NewID()
				state.Claims = append([]grokClaim{}, state.Claims...)
				state.Claims[1].Creation = &copy
				raw, _ := json.Marshal(state)
				if err := security.WriteAtomic(c.journal.path, raw); err != nil {
					t.Fatal(err)
				}
			case "failed-journal":
				c.journal.failed = true
			case "closed-journal":
				if err := c.journal.Close(); err != nil {
					t.Fatal(err)
				}
			case "changed-request":
				c.publisher.state.Pending.RequestID = domain.NewID()
			case "changed-event":
				c.publisher.state.Pending.Event.Observed.Model = "foreign model"
			}
			client.lose = false
			if c.ReplayPending(ctx) == nil || len(client.events) != 1 || c.Input(ctx, *claims[2].Input) == nil {
				t.Fatal("uncertain original proof acquired replay or input authority")
			}
		})
	}
}
