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
	var value any
	if question != nil && permission == nil && question.Answers != nil {
		value = struct {
			Answers [][]string `json:"answers"`
		}{question.Answers}
	} else if permission != nil && question == nil && permission.Decision == OpenCodePermissionOnce {
		value = struct {
			Reply OpenCodePermissionDecision `json:"reply"`
		}{permission.Decision}
	} else {
		return "", invalidInteraction()
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", invalidInteraction()
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
