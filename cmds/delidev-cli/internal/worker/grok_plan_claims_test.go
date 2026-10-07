package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func planReplyClaimFixture(p *ExecutionPublisher, running *grok.InputClaim, index int, revision uint64, outcome grok.PlanOutcome) grok.PlanClaim {
	digest := func(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
	body, _ := json.Marshal(struct {
		Outcome grok.PlanOutcome `json:"outcome"`
	}{outcome})
	return grok.PlanClaim{Version: 1, OwnerID: p.job, ProductSessionID: p.input.SessionID, InputRequestID: p.input.TurnRequestID, RequestID: domain.NewID(), NativeSessionID: running.NativeSessionID, NativePromptID: running.NativePromptID, ArrivalID: domain.NewID(), ToolID: fmt.Sprintf("plan-exit-%d", index), RequestDigest: digest(fmt.Sprintf("s:plan-request-%d", index)), ProposalDigest: digest(fmt.Sprintf("plan-proposal-%d", index)), EntryToolID: "original-plan-entry", EntryEventID: string(running.NativeSessionID) + "-6", Revision: revision, WriteToolID: fmt.Sprintf("original-plan-write-%d", revision), ContentDigest: digest(fmt.Sprintf("original plan revision %d", revision)), Outcome: outcome, BodyDigest: digest(string(body))}
}

func TestGrokPlanClaimsRetainRevisionOwnershipAndBound(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			p, j, input := newGrokClaimsFixtureForMode(t, mode)
			claims := input
			if mode == domain.PlanMode {
				claims = append(append(append([]grokClaim{}, input[:2]...), grokModeClaimsFixture(p, input[1].Creation)...), input[2:]...)
			}
			ctx := context.Background()
			first := planReplyClaimFixture(p, input[3].Input, 0, 1, grok.PlanCancelled)
			if j.PlanReply(ctx, first) == nil {
				t.Fatal("Plan response preceded original input")
			}
			for _, c := range claims {
				if err := recordGrokClaim(ctx, j, c); err != nil {
					t.Fatal(err)
				}
			}
			if err := j.PlanReply(ctx, first); err != nil {
				t.Fatal(err)
			}
			before, _ := security.ReadPrivate(j.path, maxGrokClaimBytes)
			for _, mutate := range []func(*grok.PlanClaim){
				func(c *grok.PlanClaim) { c.OwnerID = "" },
				func(c *grok.PlanClaim) { c.ProductSessionID = "" },
				func(c *grok.PlanClaim) { c.InputRequestID = domain.NewID() },
				func(c *grok.PlanClaim) { c.NativeSessionID = domain.NewID() },
				func(c *grok.PlanClaim) { c.NativePromptID = "526452fa-1956-42dd-b5f4-60e2b23dfe92" },
				func(c *grok.PlanClaim) { c.RequestID = first.RequestID },
				func(c *grok.PlanClaim) { c.RequestID = p.input.ThreadRequestID },
				func(c *grok.PlanClaim) { c.RequestID = first.ArrivalID },
				func(c *grok.PlanClaim) { c.ArrivalID = first.RequestID },
				func(c *grok.PlanClaim) { c.ArrivalID = first.ArrivalID },
				func(c *grok.PlanClaim) { c.ToolID = first.ToolID },
				func(c *grok.PlanClaim) { c.RequestDigest = first.RequestDigest },
				func(c *grok.PlanClaim) { c.EntryToolID = "foreign-entry" },
				func(c *grok.PlanClaim) { c.EntryEventID = string(c.NativeSessionID) + "-7" },
				func(c *grok.PlanClaim) { c.Revision = 0 },
				func(c *grok.PlanClaim) { c.Revision = 129 },
				func(c *grok.PlanClaim) { c.Revision = 1 },
				func(c *grok.PlanClaim) { c.WriteToolID = first.WriteToolID },
				func(c *grok.PlanClaim) { c.ToolID = first.EntryToolID },
				func(c *grok.PlanClaim) { c.ToolID = first.WriteToolID },
				func(c *grok.PlanClaim) { c.BodyDigest = strings.Repeat("ab", 32) },
			} {
				c := planReplyClaimFixture(p, input[3].Input, 1, 2, grok.PlanCancelled)
				mutate(&c)
				if j.PlanReply(ctx, c) == nil {
					t.Fatal("foreign Plan revision acquired authority")
				}
				after, _ := security.ReadPrivate(j.path, maxGrokClaimBytes)
				if !bytes.Equal(before, after) {
					t.Fatal("rejected Plan changed original journal")
				}
			}
			if mode == domain.PlanMode {
				c := planReplyClaimFixture(p, input[3].Input, 1, 2, grok.PlanCancelled)
				c.RequestID = claims[2].Mode.RequestID
				if j.PlanReply(ctx, c) == nil {
					t.Fatal("Plan response reused native mode operation")
				}
				if j.FileReply(ctx, fileReplyClaimFixture(p, input[3].Input, 1)) == nil {
					t.Fatal("cancelled Plan granted default file authority")
				}
			}
			// Re-proposing the unchanged original revision is distinct from
			// reusing an old response. A later revision must own a new Write.
			second := planReplyClaimFixture(p, input[3].Input, 1, 1, grok.PlanCancelled)
			if err := j.PlanReply(ctx, second); err != nil {
				t.Fatal("unchanged original revision rejected", err)
			}
			for i := 2; i < 128; i++ {
				if i > 2 {
					stale := planReplyClaimFixture(p, input[3].Input, i, uint64(i), grok.PlanCancelled)
					stale.WriteToolID = first.WriteToolID
					if j.PlanReply(ctx, stale) == nil {
						t.Fatal("later Plan revision reused an earlier Write identity")
					}
				}
				if err := j.PlanReply(ctx, planReplyClaimFixture(p, input[3].Input, i, uint64(i), grok.PlanCancelled)); err != nil {
					t.Fatal(i, err)
				}
			}
			if j.PlanReply(ctx, planReplyClaimFixture(p, input[3].Input, 128, 128, grok.PlanApproved)) == nil || j.QuestionReply(ctx, questionReplyClaimFixture(p, input[3].Input, 128)) == nil {
				t.Fatal("Plan bypassed combined response bound")
			}
			retained, err := readGrokClaims(p.config.Root, j.state.Reference)
			if err != nil || len(retained) != len(claims)+128 || *retained[len(claims)].PlanReply != first {
				t.Fatal("original Plan journal changed", err)
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := openGrokClaims(p); err == nil {
				t.Fatal("retained Plan journal granted another sender")
			}
			if p.state.Pending != nil || p.state.LastSequence != 0 {
				t.Fatal("Plan claim fabricated public publication")
			}
		})
	}
}

func TestGrokPlanClaimsSeparateClosedPlanAndFileAuthority(t *testing.T) {
	for _, outcome := range []grok.PlanOutcome{grok.PlanApproved, grok.PlanAbandoned} {
		t.Run(string(outcome), func(t *testing.T) {
			p, j, input := newGrokClaimsFixtureForMode(t, domain.PlanMode)
			claims := append(append(append([]grokClaim{}, input[:2]...), grokModeClaimsFixture(p, input[1].Creation)...), input[2:]...)
			ctx := context.Background()
			for _, c := range claims {
				if err := recordGrokClaim(ctx, j, c); err != nil {
					t.Fatal(err)
				}
			}
			question := questionReplyClaimFixture(p, input[3].Input, 0)
			if err := j.QuestionReply(ctx, question); err != nil {
				t.Fatal(err)
			}
			original := planReplyClaimFixture(p, input[3].Input, 1, 1, outcome)
			for _, mutate := range []func(*grok.PlanClaim){
				func(c *grok.PlanClaim) { c.ToolID = question.ToolID },
				func(c *grok.PlanClaim) { c.EntryToolID = question.ToolID },
				func(c *grok.PlanClaim) { c.WriteToolID = question.ToolID },
				func(c *grok.PlanClaim) { c.RequestID = question.RequestID },
				func(c *grok.PlanClaim) { c.ArrivalID = question.ArrivalID },
				func(c *grok.PlanClaim) { c.RequestDigest = question.RequestDigest },
			} {
				c := original
				mutate(&c)
				if j.PlanReply(ctx, c) == nil {
					t.Fatal("Plan reused original question ownership")
				}
			}
			if j.claim(ctx, grokClaim{PlanReply: &original, QuestionReply: &question}) == nil {
				t.Fatal("ambiguous Plan claim accepted")
			}
			if err := j.PlanReply(ctx, original); err != nil {
				t.Fatal(err)
			}
			if j.PlanReply(ctx, planReplyClaimFixture(p, input[3].Input, 2, 2, grok.PlanApproved)) == nil {
				t.Fatal("closed original Plan accepted another proposal")
			}
			for _, id := range []string{original.EntryToolID, original.WriteToolID, original.ToolID} {
				q := questionReplyClaimFixture(p, input[3].Input, 2)
				q.ToolID = id
				f := fileReplyClaimFixture(p, input[3].Input, 2)
				f.ToolID = id
				if j.QuestionReply(ctx, q) == nil || j.FileReply(ctx, f) == nil {
					t.Fatal("another family reused Plan artifact identity")
				}
			}
			file := fileReplyClaimFixture(p, input[3].Input, 2)
			if err := j.FileReply(ctx, file); err != nil {
				t.Fatal("independently claimed post-Plan file rejected", err)
			}
			retained, err := readGrokClaims(p.config.Root, j.state.Reference)
			if err != nil || len(retained) != 9 || *retained[8].FileReply != file {
				t.Fatal("ordinary file reply lost its independent original claim", err)
			}
		})
	}
}
