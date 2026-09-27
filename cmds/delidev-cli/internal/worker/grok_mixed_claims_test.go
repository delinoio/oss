package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
)

func TestGrokMixedRepliesShareOriginalNamespacesAndBound(t *testing.T) {
	p, journal, claims := newGrokClaimsFixture(t)
	ctx := context.Background()
	for _, c := range claims {
		if err := recordGrokClaim(ctx, journal, c); err != nil {
			t.Fatal(err)
		}
	}
	first := fileReplyClaimFixture(p, claims[3].Input, 0)
	if err := journal.FileReply(ctx, first); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(*grok.QuestionClaim){
		func(c *grok.QuestionClaim) { c.RequestID = first.RequestID },
		func(c *grok.QuestionClaim) { c.ArrivalID = first.ArrivalID },
		func(c *grok.QuestionClaim) { c.RequestID = first.ArrivalID },
		func(c *grok.QuestionClaim) { c.ArrivalID = first.RequestID },
		func(c *grok.QuestionClaim) { c.ToolID = first.ToolID },
		func(c *grok.QuestionClaim) { c.RequestDigest = first.RequestDigest },
	} {
		c := questionReplyClaimFixture(p, claims[3].Input, 1)
		mutation(&c)
		if journal.QuestionReply(ctx, c) == nil || len(journal.state.Claims) != 5 {
			t.Fatal("question reused file response identity")
		}
	}
	second := questionReplyClaimFixture(p, claims[3].Input, 1)
	if err := journal.QuestionReply(ctx, second); err != nil {
		t.Fatal("original mixed reply rejected", err)
	}
	for _, mutation := range []func(*grok.FilePermissionClaim){
		func(c *grok.FilePermissionClaim) { c.RequestID = second.RequestID },
		func(c *grok.FilePermissionClaim) { c.ArrivalID = second.ArrivalID },
		func(c *grok.FilePermissionClaim) { c.RequestID = second.ArrivalID },
		func(c *grok.FilePermissionClaim) { c.ArrivalID = second.RequestID },
		func(c *grok.FilePermissionClaim) { c.ToolID = second.ToolID },
		func(c *grok.FilePermissionClaim) { c.RequestDigest = second.RequestDigest },
	} {
		c := fileReplyClaimFixture(p, claims[3].Input, 2)
		mutation(&c)
		if journal.FileReply(ctx, c) == nil || len(journal.state.Claims) != 6 {
			t.Fatal("file response reused original question identity")
		}
	}
	for i := 2; i < 128; i++ {
		var err error
		if i%2 == 0 {
			err = journal.FileReply(ctx, fileReplyClaimFixture(p, claims[3].Input, i))
		} else {
			err = journal.QuestionReply(ctx, questionReplyClaimFixture(p, claims[3].Input, i))
		}
		if err != nil {
			t.Fatal("bounded mixed response rejected", i, err)
		}
	}
	if journal.FileReply(ctx, fileReplyClaimFixture(p, claims[3].Input, 128)) == nil || journal.QuestionReply(ctx, questionReplyClaimFixture(p, claims[3].Input, 128)) == nil {
		t.Fatal("tool family change bypassed combined reply bound")
	}
	retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(retained) != 132 || *retained[4].FileReply != first || *retained[5].QuestionReply != second {
		t.Fatal("mixed original response lineage lost", err)
	}
	if p.state.Pending != nil || p.state.LastSequence != 0 {
		t.Fatal("mixed claims granted publication")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("mixed response journal reopened send authority")
	}
}
