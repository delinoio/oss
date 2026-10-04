package opencode

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Validate checks this metadata-only claim's closed native shape. It cannot
// prove durable assignment ownership, original observation or native delivery;
// those remain the owning Worker journal and live adapter's separate duties.
func (c SessionClaim) Validate() error {
	digest, err := hex.DecodeString(c.BodyDigest)
	if c.RequestID.Validate() != nil || err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != c.BodyDigest {
		return sessionInvalid()
	}
	if c.Kind == CreateSessionMutation {
		if c.SessionID != "" || c.MessageID != "" || c.PartID != "" || c.InputRequestID != "" || c.StopRequestID != "" || c.InteractionID != "" || c.ArrivalID != "" || c.CallID != "" {
			return sessionInvalid()
		}
		return nil
	}
	if !nativeID(c.SessionID, "ses") || !nativeID(c.MessageID, "msg") || !nativeID(c.PartID, "prt") {
		return sessionInvalid()
	}
	if c.Kind == SubmitInputMutation {
		if c.InputRequestID != "" || c.StopRequestID != "" || c.InteractionID != "" || c.ArrivalID != "" || c.CallID != "" {
			return sessionInvalid()
		}
		return nil
	}
	if c.InputRequestID.Validate() != nil || c.InputRequestID == c.RequestID {
		return sessionInvalid()
	}
	switch c.Kind {
	case ResumeSessionMutation, CompactSessionMutation, ForkSessionMutation, MoveForkMutation, MarkForkMutation, DeleteForkSourceMutation:
		if c.StopRequestID != "" || c.InteractionID != "" || c.ArrivalID != "" || c.CallID != "" {
			return sessionInvalid()
		}
	case StopInputMutation, StopOwnedRuntimeMutation, RecoverStoppedRuntimeMutation:
		if c.InteractionID != "" || c.ArrivalID != "" || c.CallID != "" || c.BodyDigest != mutationDigest(nil) {
			return sessionInvalid()
		}
		if c.Kind == RecoverStoppedRuntimeMutation {
			if c.StopRequestID.Validate() != nil || c.StopRequestID == c.RequestID || c.StopRequestID == c.InputRequestID {
				return sessionInvalid()
			}
		} else if c.StopRequestID != "" {
			return sessionInvalid()
		}
	case ReplyPermissionMutation, ReplyQuestionMutation, RejectQuestionMutation:
		prefix := "que"
		if c.Kind == ReplyPermissionMutation {
			prefix = "per"
		}
		if c.StopRequestID != "" || !nativeID(c.InteractionID, prefix) || !nativeID(c.ArrivalID, "evt") || domain.Text(c.CallID, "native tool call", 1024, true) != nil || c.Kind == RejectQuestionMutation && c.BodyDigest != mutationDigest(nil) {
			return sessionInvalid()
		}
	default:
		return sessionInvalid()
	}
	return nil
}
