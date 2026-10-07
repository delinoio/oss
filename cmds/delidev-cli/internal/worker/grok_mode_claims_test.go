package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func grokModeClaimsFixture(p *ExecutionPublisher, created *grok.CreationClaim) []grokClaim {
	body, _ := json.Marshal(struct {
		Session domain.ID       `json:"sessionId"`
		Mode    grok.NativeMode `json:"modeId"`
	}{created.NativeSessionID, grok.NativePlanMode})
	digest := sha256.Sum256(body)
	c := grok.ModeClaim{Phase: grok.ClaimMode, OwnerID: p.job, RequestID: domain.NewID(), ProductSessionID: p.input.SessionID, NativeSessionID: created.NativeSessionID, Mode: grok.NativePlanMode, BodyDigest: hex.EncodeToString(digest[:])}
	b := c
	b.Phase, b.EventID, b.TimestampMS = grok.BindMode, string(created.NativeSessionID)+"-2", 1790536355539
	return []grokClaim{{Mode: &c}, {Mode: &b}}
}

func TestGrokInitialPlanClaimsBindImmutableInputMode(t *testing.T) {
	p, journal, input := newGrokClaimsFixtureForMode(t, domain.PlanMode)
	mode := grokModeClaimsFixture(p, input[1].Creation)
	claims := append(append(append([]grokClaim{}, input[:2]...), mode...), input[2:]...)
	if journal.state.Reference.Version != 2 || journal.state.Reference.InputMode != domain.PlanMode {
		t.Fatal("journal lost original assignment mode")
	}
	ctx := context.Background()
	for i, c := range claims {
		for _, early := range claims[i+1:] {
			if recordGrokClaim(ctx, journal, early) == nil {
				t.Fatal("Plan claim bypassed its original predecessor")
			}
		}
		if err := recordGrokClaim(ctx, journal, c); err != nil {
			t.Fatal(err)
		}
		if err := recordGrokClaim(ctx, journal, c); err == nil {
			t.Fatal("original mode or input claim replayed")
		}
		retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
		if err != nil || !reflect.DeepEqual(retained, claims[:i+1]) {
			t.Fatal("Plan claims changed on read-only inspection", err)
		}
	}
	for _, id := range []domain.ID{mode[0].Mode.RequestID, p.input.ThreadRequestID, p.input.TurnRequestID} {
		reply := questionReplyClaimFixture(p, input[3].Input, 0)
		reply.RequestID = id
		if journal.QuestionReply(ctx, reply) == nil {
			t.Fatal("question reused original operation identity")
		}
	}
	for i := 0; i < 128; i++ {
		if err := journal.QuestionReply(ctx, questionReplyClaimFixture(p, input[3].Input, i)); err != nil {
			t.Fatal(i, err)
		}
	}
	if journal.QuestionReply(ctx, questionReplyClaimFixture(p, input[3].Input, 128)) == nil {
		t.Fatal("Plan expanded original reply count")
	}
	retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(retained) != 134 {
		t.Fatal("Plan journal lost independent mode stages", err)
	}
	if p.state.LastSequence != 0 || p.state.Pending != nil {
		t.Fatal("mode claims fabricated public publication")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("retained mode claims granted another native writer")
	}
	if _, err := readGrokClaims(p.config.Root, journal.state.Reference); err != nil {
		t.Fatal(err)
	}
}

func TestGrokInitialModeClaimRejectsForeignAndChangedAuthority(t *testing.T) {
	p, journal, input := newGrokClaimsFixtureForMode(t, domain.PlanMode)
	ctx := context.Background()
	for _, c := range input[:2] {
		if err := recordGrokClaim(ctx, journal, c); err != nil {
			t.Fatal(err)
		}
	}
	modes := grokModeClaimsFixture(p, input[1].Creation)
	for i, c := range modes {
		changes := []func(*grok.ModeClaim){
			func(c *grok.ModeClaim) { c.ProductSessionID = "" },
			func(c *grok.ModeClaim) { c.NativeSessionID = domain.NewID() },
			func(c *grok.ModeClaim) { c.RequestID = p.input.ThreadRequestID },
			func(c *grok.ModeClaim) { c.RequestID = p.input.TurnRequestID },
			func(c *grok.ModeClaim) { c.Mode = grok.NativeDefaultMode },
			func(c *grok.ModeClaim) { c.BodyDigest = strings.Repeat("ab", 32) },
		}
		if i == 0 {
			changes = append(changes, func(c *grok.ModeClaim) { c.EventID = string(c.NativeSessionID) + "-2" }, func(c *grok.ModeClaim) { c.TimestampMS = 1 })
		} else {
			changes = append(changes, func(c *grok.ModeClaim) { c.RequestID = domain.NewID() }, func(c *grok.ModeClaim) { c.EventID = string(domain.NewID()) + "-2" }, func(c *grok.ModeClaim) { c.TimestampMS = 0 })
		}
		before, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
		for _, mutate := range changes {
			v := *c.Mode
			mutate(&v)
			if journal.Mode(ctx, v) == nil {
				t.Fatal("foreign or changed mode claimed")
			}
			after, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected mode changed journal")
			}
		}
		if journal.claim(ctx, grokClaim{Mode: c.Mode, Input: input[2].Input}) == nil {
			t.Fatal("ambiguous mode claim accepted")
		}
		if err := journal.Mode(ctx, *c.Mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range input[2:] {
		if err := recordGrokClaim(ctx, journal, c); err != nil {
			t.Fatal(err)
		}
	}
	file := fileReplyClaimFixture(p, input[3].Input, 0)
	if journal.FileReply(ctx, file) == nil {
		t.Fatal("initial Plan granted default file reply authority")
	}
	raw, err := security.ReadPrivate(journal.path, maxGrokClaimBytes)
	if err != nil || bytes.Contains(raw, []byte(p.input.Input.Prompt)) {
		t.Fatal("mode claim retained input text", err)
	}
	ref := journal.state.Reference
	ref.Version, ref.InputMode = 1, ""
	if _, err := readGrokClaims(p.config.Root, ref); err == nil {
		t.Fatal("original Plan journal downgraded to Execute")
	}
}

func TestGrokDefaultModeCannotAcquirePlanClaims(t *testing.T) {
	p, journal, input := newGrokClaimsFixture(t)
	ctx := context.Background()
	for _, c := range input[:2] {
		if err := recordGrokClaim(ctx, journal, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range grokModeClaimsFixture(p, input[1].Creation) {
		if journal.Mode(ctx, *c.Mode) == nil {
			t.Fatal("Execute assignment changed mode")
		}
	}
	for _, c := range input[2:] {
		if err := recordGrokClaim(ctx, journal, c); err != nil {
			t.Fatal(err)
		}
	}
}
