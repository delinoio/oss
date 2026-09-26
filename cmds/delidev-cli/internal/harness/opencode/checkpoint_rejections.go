package opencode

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type checkpointPermissionRejection struct {
	Claim        SessionClaim                  `json:"claim"`
	ReplyEventID string                        `json:"reply_event_id"`
	Permission   checkpointRejectionPermission `json:"permission,omitempty"`
}

type checkpointPolicyRejection struct {
	Permission     checkpointRejectionPermission `json:"permission,omitempty"`
	InteractionID  string                        `json:"interaction_id"`
	ArrivalID      string                        `json:"arrival_id"`
	ReplyEventID   string                        `json:"reply_event_id"`
	InputRequestID domain.ID                     `json:"input_request_id"`
	SessionID      string                        `json:"session_id"`
	MessageID      string                        `json:"message_id"`
	PartID         string                        `json:"part_id"`
	CallID         string                        `json:"call_id"`
	Sources        []string                      `json:"rejection_observations"`
}

// Share only identity validation with the allowance profile. Rejection context
// has its own wire field and cannot contribute an allowance or direct reply.
func (p checkpointPolicyRejection) identity() checkpointPolicyPermission {
	return checkpointPolicyPermission{InteractionID: p.InteractionID, ArrivalID: p.ArrivalID, ReplyEventID: p.ReplyEventID, InputRequestID: p.InputRequestID, SessionID: p.SessionID, MessageID: p.MessageID, PartID: p.PartID, CallID: p.CallID, Sources: p.Sources}
}

func checkpointRejectedTool(tool *NativeToolPart) bool {
	if tool == nil || !checkpointRejectableTool(checkpointToolName(tool.Name)) || tool.State != ToolError || tool.Error == nil || tool.Output != nil {
		return false
	}
	// These pinned tool permission checks precede native result/auxiliary state.
	// An error shape alone never proves rejection; every failed part also needs
	// its original independently accepted closure evidence.
	if tool.Metadata == nil {
		return true
	}
	_, err := shape(tool.Metadata, nil, nil)
	return err == nil
}

func (o *inputObserver) checkpointRejectedPermission(value *observedInteraction) (checkpointPermissionRejection, bool) {
	var empty checkpointPermissionRejection
	if value == nil || value.value.Kind != PermissionInteraction || value.value.Permission == nil || value.alwaysAccepted || value.attempt == nil || value.attempt.permission == nil || *value.attempt.permission != PermissionReject || !nativeID(value.replyEvent, "evt") || value.replyEvent == value.arrival {
		return empty, false
	}
	var response permissionResponse
	if domain.Decode(value.attempt.body, &response) != nil || response.Reply != PermissionReject || response.Message != nil && domain.Text(*response.Message, "native correction", 64<<10, false) != nil {
		return empty, false
	}
	body, err := json.Marshal(response)
	if err != nil {
		return empty, false
	}
	claim, valid := o.checkpointAcceptedClaim(value, checkpointReplyProfile{Kind: ReplyPermissionMutation, Body: body, Rejected: true, FeedbackRequested: response.Message != nil, Correction: response.Message != nil && *response.Message != ""})
	permission := checkpointRejectionPermission(value.value.Permission.Name)
	if !valid {
		return empty, false
	}
	part := o.parts[claim.PartID].value.Tool
	if !permission.supports(checkpointToolName(part.Name)) || !checkpointRejectedTool(part) {
		return empty, false
	}
	return checkpointPermissionRejection{Claim: claim, ReplyEventID: value.replyEvent, Permission: permission.stored()}, true
}

func (o *inputObserver) checkpointRejectedPolicy(value *observedInteraction) (checkpointPolicyRejection, bool) {
	var empty checkpointPolicyRejection
	if value == nil || value.value.Kind != PermissionInteraction || value.value.Permission == nil || value.value.Tool == nil || !value.closed || !value.rejected || !value.rejectionReserved || value.attempt != nil || value.alwaysAccepted || value.canceled || value.pendingAbsent || len(value.alwaysObservations) != 0 {
		return empty, false
	}
	tool := value.value.Tool
	permission := checkpointRejectionPermission(value.value.Permission.Name)
	result := checkpointPolicyRejection{Permission: permission.stored(), InteractionID: value.value.ID, ArrivalID: value.arrival, ReplyEventID: value.replyEvent, InputRequestID: o.input.receipt.RequestID, SessionID: value.value.SessionID, MessageID: tool.MessageID, PartID: o.calls[tool.CallID], CallID: tool.CallID, Sources: slices.Clone(value.rejectionSources)}
	part := o.parts[result.PartID]
	if !result.identity().valid() || result.SessionID != o.input.receipt.SessionID || part == nil || part.value.MessageID != result.MessageID || !checkpointInlineTool(part.value.Tool) || !checkpointRejectedTool(part.value.Tool) || !permission.supports(checkpointToolName(part.value.Tool.Name)) || part.value.Tool.CallID != result.CallID {
		return empty, false
	}
	for _, id := range result.Sources {
		prior := o.interactions[id]
		rejection, valid := o.checkpointRejectedPermission(prior)
		if !valid || rejection.Claim.InputRequestID != result.InputRequestID || rejection.Claim.SessionID != result.SessionID || rejection.ReplyEventID == result.ReplyEventID {
			return empty, false
		}
	}
	return result, true
}
