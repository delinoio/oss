package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func TestOpenCodeStopClaimRequiresOriginalCoordinatedAuthority(t *testing.T) {
	for _, name := range []string{"unclaimed", "request", "part", "session", "message", "input", "digest", "duplicate", "journal-only", "original"} {
		t.Run(name, func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			b := f.c.binding
			digest := sha256.Sum256(nil)
			expected := opencode.SessionClaim{RequestID: domain.NewID(), Kind: opencode.StopInputMutation, SessionID: b.thread, MessageID: b.turn, PartID: b.inputClaim.PartID, InputRequestID: b.reference.InputRequestID, BodyDigest: hex.EncodeToString(digest[:])}
			claim := expected
			if name != "unclaimed" && name != "journal-only" {
				b.expectedStop = &expected
			}
			switch name {
			case "request":
				claim.RequestID = domain.NewID()
			case "part":
				claim.PartID = textPartTwoID
			case "session":
				claim.SessionID = "ses_01960dcbe1ffABCDEFGHIJKLMN"
			case "message":
				claim.MessageID = textAssistantID
			case "input":
				claim.InputRequestID = domain.NewID()
			case "digest":
				claim.BodyDigest = "00" + claim.BodyDigest[2:]
			case "duplicate":
				if err := b.Claim(context.Background(), claim); err != nil {
					t.Fatal(err)
				}
			case "journal-only":
				if err := b.journal.Claim(context.Background(), claim); err != nil {
					t.Fatal(err)
				}
				claims, err := b.readClaims()
				if err != nil || b.validPublicationClaims(claims) {
					t.Fatal("uncoordinated Stop acquired publication authority")
				}
				return
			}
			err := b.Claim(context.Background(), claim)
			if (err == nil) != (name == "original") {
				t.Fatal("changed or repeated Stop acquired native send authority", err)
			}
			if name == "original" {
				claims, err := b.readClaims()
				if err != nil || !b.validPublicationClaims(claims) || b.expectedStop != nil || b.stopClaim == nil || *b.stopClaim != expected {
					t.Fatal("original Stop lost synchronized ownership")
				}
			}
		})
	}
}
