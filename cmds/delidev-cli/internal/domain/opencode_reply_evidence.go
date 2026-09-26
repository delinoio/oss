package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// This observation confirms an original native reply event plus the exact HTTP
// body. Neither fact proves tool completion, process cleanup or a policy change.
type OpenCodeReplyEvidence struct {
	NativeEventID   string `json:"native_event_id"`
	ProposalEventID string `json:"proposal_event_id"`
	NativeRequestID string `json:"native_request_id"`
	BodyDigest      string `json:"body_digest"`
	HTTPAccepted    bool   `json:"http_accepted"`
}

func (e OpenCodeReplyEvidence) Validate(kind InteractionType) error {
	namespace := NativeQuestionIdentity
	if kind == NativeApprovalInteraction {
		namespace = NativePermissionIdentity
	} else if kind != UserQuestionInteraction {
		return invalidInteraction()
	}
	if NativeIdentity(e.NativeEventID).Validate(OpenCode, NativeEventIdentity) != nil || NativeIdentity(e.ProposalEventID).Validate(OpenCode, NativeEventIdentity) != nil || e.NativeEventID == e.ProposalEventID || NativeIdentity(e.NativeRequestID).Validate(OpenCode, namespace) != nil || !e.HTTPAccepted {
		return invalidInteraction()
	}
	raw, err := hex.DecodeString(e.BodyDigest)
	if err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != e.BodyDigest {
		return invalidInteraction()
	}
	return nil
}

// The pinned native protocol uses these exact bodies. This digest binds the
// server's accepted response to the original Worker claim without duplicating
// answers in delivery/acceptance metadata. The native adapter independently
// compares this digest before its single HTTP mutation.
func OpenCodeResponseDigest(question *OpenCodeQuestionResponse, permission *OpenCodePermissionResponse) (string, error) {
	var raw []byte
	var err error
	if question != nil && permission == nil {
		if question.Reject {
			if question.Answers != nil {
				return "", invalidInteraction()
			}
			// The native rejection endpoint receives no JSON body.
		} else {
			if question.Answers == nil {
				return "", invalidInteraction()
			}
			raw, err = json.Marshal(struct {
				Answers [][]string `json:"answers"`
			}{question.Answers})
		}
	} else if permission != nil && question == nil && permission.validateValue() == nil {
		raw, err = json.Marshal(struct {
			Reply   OpenCodePermissionDecision `json:"reply"`
			Message *string                    `json:"message,omitempty"`
		}{permission.Decision, permission.Feedback})
	} else {
		return "", invalidInteraction()
	}
	if err != nil {
		return "", invalidInteraction()
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
