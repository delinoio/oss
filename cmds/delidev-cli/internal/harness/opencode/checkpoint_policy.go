package opencode

import (
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Policy closure has an original native event, but no direct reply mutation.
// Sources preserve observed prior direct acceptance, not inferred rule owners.
type checkpointPolicyPermission struct {
	InteractionID  string    `json:"interaction_id"`
	ArrivalID      string    `json:"arrival_id"`
	ReplyEventID   string    `json:"reply_event_id"`
	InputRequestID domain.ID `json:"input_request_id"`
	SessionID      string    `json:"session_id"`
	MessageID      string    `json:"message_id"`
	PartID         string    `json:"part_id"`
	CallID         string    `json:"call_id"`
	Sources        []string  `json:"always_observations"`
}

func (p checkpointPolicyPermission) valid() bool {
	if !nativeID(p.InteractionID, "per") || !nativeID(p.ArrivalID, "evt") || !nativeID(p.ReplyEventID, "evt") || p.ArrivalID == p.ReplyEventID || p.InputRequestID.Validate() != nil || !nativeID(p.SessionID, "ses") || !nativeID(p.MessageID, "msg") || !nativeID(p.PartID, "prt") || domain.Text(p.CallID, "native tool call", 1024, true) != nil || len(p.Sources) == 0 || len(p.Sources) > maxObservedInteractions {
		return false
	}
	for index, id := range p.Sources {
		if !nativeID(id, "per") || id == p.InteractionID || index > 0 && p.Sources[index-1] >= id {
			return false
		}
	}
	return true
}

func (o *inputObserver) checkpointPolicy(value *observedInteraction) (checkpointPolicyPermission, bool) {
	var empty checkpointPolicyPermission
	if value == nil || value.value.Kind != PermissionInteraction || value.value.Tool == nil || value.value.Permission == nil || value.value.Permission.Name != "read" || !value.closed || value.attempt != nil || value.alwaysAccepted || value.rejected || value.canceled || value.pendingAbsent || value.rejectionReserved || len(value.rejectionSources) != 0 {
		return empty, false
	}
	tool := value.value.Tool
	result := checkpointPolicyPermission{InteractionID: value.value.ID, ArrivalID: value.arrival, ReplyEventID: value.replyEvent, InputRequestID: o.input.receipt.RequestID, SessionID: value.value.SessionID, MessageID: tool.MessageID, PartID: o.calls[tool.CallID], CallID: tool.CallID, Sources: slices.Clone(value.alwaysObservations)}
	part := o.parts[result.PartID]
	if !result.valid() || result.SessionID != o.input.receipt.SessionID || part == nil || part.value.MessageID != result.MessageID || !checkpointInlineTool(part.value.Tool) || part.value.Tool.Name != "read" || part.value.Tool.CallID != result.CallID {
		return empty, false
	}
	for _, id := range result.Sources {
		prior := o.interactions[id]
		approval, valid := o.checkpointAlways(prior)
		if !valid || approval.Claim.InputRequestID != result.InputRequestID || approval.Claim.SessionID != result.SessionID || !nativeID(prior.replyEvent, "evt") || prior.replyEvent == result.ReplyEventID {
			return empty, false
		}
	}
	return result, true
}
