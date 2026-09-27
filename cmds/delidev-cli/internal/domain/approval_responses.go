package domain

import (
	"encoding/json"
	"reflect"
	"slices"
	"time"
)

const MaxApprovalResponseBytes = 256 << 10

// The original pinned request supplies harness/version and scope. A response
// selects one original decision or a bounded native permission grant, never
// another request, command, question answer or execution policy.
type ApprovalResponseInput struct {
	Claude   *ClaudePermissionResponse   `json:"claude,omitempty"`
	OpenCode *OpenCodePermissionResponse `json:"opencode,omitempty"`
	Decision *CodexApprovalDecision      `json:"decision,omitempty"`
	Grant    *CodexPermissionGrant       `json:"grant,omitempty"`
}

func (r *ApprovalResponseInput) UnmarshalJSON(raw []byte) error {
	type plain ApprovalResponseInput
	var value plain
	var fields map[string]json.RawMessage
	if Decode(raw, &value) != nil || Decode(raw, &fields) != nil {
		return invalidApprovalResponse()
	}
	if _, exists := fields["claude"]; exists && (len(fields) != 1 || value.Claude == nil) {
		return invalidApprovalResponse()
	}
	*r = ApprovalResponseInput(value)
	return nil
}

func invalidApprovalResponse() error {
	return Fail(InvalidArgument, "The response does not match its original approval.", "Select an offered native decision or an explicit grant within the original permission request.")
}

func (r ApprovalResponseInput) Validate(original *ApprovalRequest) error {
	if r.Claude != nil || r.OpenCode != nil || original == nil || original.Validate() != nil {
		return invalidApprovalResponse()
	}
	a := original.Codex
	switch a.Kind {
	case CodexCommandApproval, CodexFileApproval:
		if r.Grant != nil || r.Decision == nil || r.Decision.Validate() != nil {
			return invalidApprovalResponse()
		}
		if a.Kind == CodexCommandApproval {
			if a.Command.AvailableDecisions == nil {
				return Fail(Unsupported, "Native approval choices are unavailable.", "Retain the original request without inventing native decisions.")
			}
			if !slices.ContainsFunc(a.Command.AvailableDecisions, func(offered CodexApprovalDecision) bool { return reflect.DeepEqual(offered, *r.Decision) }) {
				return invalidApprovalResponse()
			}
		} else if !slices.Contains([]CodexApprovalDecisionKind{CodexApprovalAccept, CodexApprovalAcceptSession, CodexApprovalDecline, CodexApprovalCancel}, r.Decision.Kind) {
			return invalidApprovalResponse()
		}
	case CodexPermissionsApproval:
		if r.Decision != nil || r.Grant == nil {
			return invalidApprovalResponse()
		}
		if err := r.Grant.Validate(a.Permissions); err != nil {
			return err
		}
	default:
		return invalidApprovalResponse()
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxApprovalResponseBytes {
		return Fail(ResourceExhausted, "The complete approval response exceeds its bound.", "Keep the original decision and selected permission scope intact within the response limit.")
	}
	return nil
}

type ApprovalResponseState string
type ApprovalDelivery string

const (
	ApprovalResponseQueued      ApprovalResponseState = "queued"
	ApprovalResponseClaimed     ApprovalResponseState = "claimed"
	ApprovalResponseTransmitted ApprovalResponseState = "transmitted"
	ApprovalResponseUncertain   ApprovalResponseState = "uncertain"
	ApprovalResponseCanceled    ApprovalResponseState = "canceled"
	ApprovalResponseAccepted    ApprovalResponseState = "accepted"
	ApprovalNotSent             ApprovalDelivery      = "not-sent"
	ApprovalTransmitted         ApprovalDelivery      = "transmitted"
	ApprovalDeliveryUncertain   ApprovalDelivery      = "uncertain"
)

// A claim's metadata shape is shared, but each native response kind validates
// its own original interaction, response and claim before any send or report.
type ApprovalResponseClaim = QuestionResponseClaim
type ApprovalResponse struct {
	ClaudeEcho *ClaudeReplyEcho               `json:"claude_echo,omitempty"`
	ID         ID                             `json:"id"`
	State      ApprovalResponseState          `json:"state"`
	Input      ApprovalResponseInput          `json:"input"`
	AcceptedAt time.Time                      `json:"accepted_at"`
	Claim      *ApprovalResponseClaim         `json:"claim,omitempty"`
	Delivery   *ApprovalDeliveryObservation   `json:"delivery,omitempty"`
	Acceptance *ApprovalAcceptanceObservation `json:"acceptance,omitempty"`
}
type ApprovalDeliveryObservation struct {
	State    ApprovalDelivery `json:"state"`
	Sequence uint64           `json:"sequence"`
}
type ExecutionApprovalResponseUpdate struct {
	InteractionID ID               `json:"interaction_id"`
	ResponseID    ID               `json:"response_id"`
	ClaimID       ID               `json:"claim_id"`
	NativeItemID  string           `json:"native_item_id"`
	Delivery      ApprovalDelivery `json:"delivery"`
}

func (u ExecutionApprovalResponseUpdate) Validate() error {
	for _, id := range []ID{u.InteractionID, u.ResponseID, u.ClaimID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if Text(u.NativeItemID, "native approval item", 1024, true) != nil || !slices.Contains([]ApprovalDelivery{ApprovalNotSent, ApprovalTransmitted, ApprovalDeliveryUncertain}, u.Delivery) {
		return invalidApprovalResponse()
	}
	return nil
}

// Acceptance is a separately correlated native processing fact. The initial
// profiles distinguish exact permission output from pinned single-use approval
// execution. Generic completion and native closure cannot substitute for them.
type ApprovalAcceptanceEvidence string

const (
	NativeOpenCodePermissionReply ApprovalAcceptanceEvidence = "native-opencode-permission-reply"
	NativePermissionsOutput       ApprovalAcceptanceEvidence = "native-permissions-output"
	NativeApprovedCommand         ApprovalAcceptanceEvidence = "native-approved-command"
	NativeApprovedPatch           ApprovalAcceptanceEvidence = "native-approved-patch"
)

type ApprovalAcceptanceObservation struct {
	OpenCode *OpenCodeReplyEvidence     `json:"opencode,omitempty"`
	Evidence ApprovalAcceptanceEvidence `json:"evidence"`
	Sequence uint64                     `json:"sequence"`
}
type ExecutionApprovalAcceptanceUpdate struct {
	OpenCode      *OpenCodeReplyEvidence     `json:"opencode,omitempty"`
	InteractionID ID                         `json:"interaction_id"`
	ResponseID    ID                         `json:"response_id"`
	ClaimID       ID                         `json:"claim_id"`
	NativeItemID  string                     `json:"native_item_id"`
	Evidence      ApprovalAcceptanceEvidence `json:"evidence"`
}

func (u ExecutionApprovalAcceptanceUpdate) Validate() error {
	for _, id := range []ID{u.InteractionID, u.ResponseID, u.ClaimID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if Text(u.NativeItemID, "native approval item", 1024, true) != nil {
		return invalidApprovalResponse()
	}
	if u.Evidence == NativeOpenCodePermissionReply && u.OpenCode != nil {
		return u.OpenCode.Validate(NativeApprovalInteraction)
	}
	if u.OpenCode != nil || u.Evidence != NativePermissionsOutput {
		return Fail(InvalidArgument, "Unknown native approval acceptance evidence.", "Retain exact owned permission-tool output; transmission, closure and tool completion cannot replace it.")
	}
	return nil
}
