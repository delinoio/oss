package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func TestOpenCodeResponseClaimRequiresExactLiveProductAuthorization(t *testing.T) {
	for _, changed := range []string{"unclaimed", "digest", "request", "arrival", "part", "message", "call", "kind", "input", "session", "duplicate"} {
		t.Run(changed, func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			b := f.c.binding
			expected := opencode.SessionClaim{RequestID: domain.NewID(), Kind: opencode.ReplyPermissionMutation, SessionID: f.input.SessionID, MessageID: textAssistantID, PartID: textPartOneID, InputRequestID: f.input.RequestID, InteractionID: "per_01960dcbe1faABCDEFGHIJKLMN", ArrivalID: "evt_01960dcbe1faABCDEFGHIJKLMN", CallID: "original", BodyDigest: strings.Repeat("ab", 32)}
			value := expected
			if changed != "unclaimed" {
				b.expectedReply = &expected
			}
			switch changed {
			case "digest":
				value.BodyDigest = strings.Repeat("cd", 32)
			case "request":
				value.RequestID = domain.NewID()
			case "arrival":
				value.ArrivalID = "evt_01960dcbe1fbABCDEFGHIJKLMN"
			case "part":
				value.PartID = textPartTwoID
			case "message":
				value.MessageID = f.input.MessageID
			case "call":
				value.CallID = "foreign"
			case "kind":
				value.Kind = opencode.RejectQuestionMutation
			case "input":
				value.InputRequestID = domain.NewID()
			case "session":
				value.SessionID = "ses_01960dcbe1fbABCDEFGHIJKLMN"
			case "duplicate":
				if err := b.Claim(context.Background(), value); err != nil {
					t.Fatal(err)
				}
				claims, err := b.readClaims()
				if err != nil || !b.validPublicationClaims(claims) || len(claims) != 3 || b.expectedReply != nil {
					t.Fatal("original reply authorization was not consumed")
				}
			}
			if b.Claim(context.Background(), value) == nil {
				t.Fatal("unclaimed, changed or repeated response regained send authority")
			}
		})
	}
}

func TestOpenCodePublicationCannotAdoptAnUncoordinatedReplyClaim(t *testing.T) {
	f := newOpenCodeTextFixture(t)
	b := f.c.binding
	claim := opencode.SessionClaim{RequestID: domain.NewID(), Kind: opencode.ReplyPermissionMutation, SessionID: f.input.SessionID, MessageID: textAssistantID, PartID: textPartOneID, InputRequestID: f.input.RequestID, InteractionID: "per_01960dcbe1faABCDEFGHIJKLMN", ArrivalID: "evt_01960dcbe1faABCDEFGHIJKLMN", CallID: "original", BodyDigest: strings.Repeat("ab", 32)}
	if b.journal.Claim(context.Background(), claim) != nil {
		t.Fatal("fixture journal claim")
	}
	claims, err := b.readClaims()
	if err != nil || b.validPublicationClaims(claims) {
		t.Fatal("uncoordinated journal claim acquired terminal authority")
	}
}
