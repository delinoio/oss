package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func questionReplyClaimFixture(p *ExecutionPublisher, running *grok.InputClaim, index int) grok.QuestionClaim {
	digest := func(value string) string { v := sha256.Sum256([]byte(value)); return hex.EncodeToString(v[:]) }
	return grok.QuestionClaim{Version: 1, OwnerID: p.job, ProductSessionID: p.input.SessionID, InputRequestID: p.input.TurnRequestID, RequestID: domain.NewID(), NativeSessionID: running.NativeSessionID, NativePromptID: running.NativePromptID, ArrivalID: domain.NewID(), ToolID: fmt.Sprintf("original-tool-%d", index), RequestDigest: digest(fmt.Sprintf("s:request-%d", index)), ProposalDigest: digest(fmt.Sprintf("proposal-%d", index)), Outcome: grok.QuestionAccepted, BodyDigest: digest(`{"outcome":"accepted","answers":{"Original question?":"One"},"annotations":{}}`)}
}

func TestGrokQuestionReplyClaimsRetainOriginalOwnershipAndBound(t *testing.T) {
	p, journal, claims := newGrokClaimsFixture(t)
	ctx := context.Background()
	original := questionReplyClaimFixture(p, claims[3].Input, 0)
	if journal.QuestionReply(ctx, original) == nil {
		t.Fatal("question reply preceded original input binding")
	}
	for _, claim := range claims {
		if err := recordGrokClaim(ctx, journal, claim); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*grok.QuestionClaim){
		func(c *grok.QuestionClaim) { c.OwnerID = domain.NewID() },
		func(c *grok.QuestionClaim) { c.ProductSessionID = domain.NewID() },
		func(c *grok.QuestionClaim) { c.InputRequestID = domain.NewID() },
		func(c *grok.QuestionClaim) { c.RequestID = p.input.ThreadRequestID },
		func(c *grok.QuestionClaim) { c.NativeSessionID = domain.NewID() },
		func(c *grok.QuestionClaim) { c.NativePromptID = "e5833c4a-d764-4428-8bd8-6c2968a34b1c" },
		func(c *grok.QuestionClaim) { c.Outcome = grok.QuestionCancelled },
		func(c *grok.QuestionClaim) { c.ProposalDigest = strings.Repeat("AB", 32) },
	} {
		changed := original
		mutate(&changed)
		before, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
		if journal.QuestionReply(ctx, changed) == nil {
			t.Fatal("foreign question reply claimed")
		}
		after, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
		if !bytes.Equal(before, after) {
			t.Fatal("invalid question reply changed journal")
		}
	}
	if err := journal.QuestionReply(ctx, original); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*grok.QuestionClaim){
		func(c *grok.QuestionClaim) { c.RequestID = original.RequestID },
		func(c *grok.QuestionClaim) { c.ArrivalID = original.ArrivalID },
		func(c *grok.QuestionClaim) { c.ToolID = original.ToolID },
		func(c *grok.QuestionClaim) { c.RequestDigest = original.RequestDigest },
	} {
		c := questionReplyClaimFixture(p, claims[3].Input, 1)
		mutate(&c)
		if journal.QuestionReply(ctx, c) == nil {
			t.Fatal("original question reply identity reused")
		}
	}
	for i := 1; i < 128; i++ {
		if err := journal.QuestionReply(ctx, questionReplyClaimFixture(p, claims[3].Input, i)); err != nil {
			t.Fatal("bounded original reply refused", i, err)
		}
	}
	if journal.QuestionReply(ctx, questionReplyClaimFixture(p, claims[3].Input, 128)) == nil {
		t.Fatal("unbounded question replies accepted")
	}
	retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(retained) != 132 || *retained[4].QuestionReply != original {
		t.Fatal("original replies not retained", err)
	}
	if p.state.Pending != nil || p.state.LastSequence != 0 {
		t.Fatal("question claims granted public publication")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("question replies regained send authority on reopen")
	}
}

func TestGrokQuestionRepliesExcludeTextStopAndClosure(t *testing.T) {
	for _, preceding := range []string{"reply", "stop", "closure"} {
		t.Run(preceding, func(t *testing.T) {
			p, journal, claims := newGrokClaimsFixture(t)
			ctx := context.Background()
			for _, claim := range claims {
				if err := recordGrokClaim(ctx, journal, claim); err != nil {
					t.Fatal(err)
				}
			}
			running := claims[3].Input
			digest, _ := grok.ClosureClaimDigest(running.NativeSessionID)
			stop := grok.StopClaim{Version: 1, OwnerID: p.job, ProductSessionID: p.input.SessionID, InputRequestID: p.input.TurnRequestID, RequestID: domain.NewID(), NativeSessionID: running.NativeSessionID, NativePromptID: running.NativePromptID, BodyDigest: digest}
			closure := grok.ClosureClaim{Phase: grok.ClaimClosure, RequestID: domain.NewID(), ProductSessionID: p.input.SessionID, NativeSessionID: running.NativeSessionID, NativePromptID: running.NativePromptID, BodyDigest: digest}
			reply := questionReplyClaimFixture(p, running, 0)
			switch preceding {
			case "reply":
				if err := journal.QuestionReply(ctx, reply); err != nil {
					t.Fatal(err)
				}
				if journal.Stop(ctx, stop) == nil || journal.Closure(ctx, closure) == nil {
					t.Fatal("question reply granted text-only claims")
				}
			case "stop":
				if err := journal.Stop(ctx, stop); err != nil {
					t.Fatal(err)
				}
				if journal.QuestionReply(ctx, reply) == nil {
					t.Fatal("stopped text granted question reply")
				}
			case "closure":
				if err := journal.Closure(ctx, closure); err != nil {
					t.Fatal(err)
				}
				if journal.QuestionReply(ctx, reply) == nil {
					t.Fatal("closed text granted question reply")
				}
			}
		})
	}
}
