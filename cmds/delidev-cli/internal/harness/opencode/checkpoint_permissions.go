package opencode

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const checkpointOnceBody = `{"reply":"once"}`

// Permission acceptance is an original live fact, never reconstructed from a
// completed tool or an empty native pending-request list after replacement.
func (o *inputObserver) checkpointOnce(value *observedInteraction) (SessionClaim, bool) {
	return o.checkpointPermission(value, PermissionOnce)
}

func (o *inputObserver) checkpointPermission(value *observedInteraction, decision PermissionDecision) (SessionClaim, bool) {
	var empty SessionClaim
	body := checkpointOnceBody
	if decision == PermissionAlways {
		body = checkpointAlwaysBody
	} else if decision != PermissionOnce {
		return empty, false
	}
	if value == nil || value.value.Kind != PermissionInteraction || value.value.Tool == nil || value.value.Permission == nil || !value.closed || value.rejected || value.canceled || value.pendingAbsent || value.alwaysAccepted != (decision == PermissionAlways) || value.rejectionReserved || len(value.rejectionSources) != 0 || len(value.alwaysObservations) != 0 {
		return empty, false
	}
	a := value.attempt
	if a == nil || !a.sent || a.permission == nil || *a.permission != decision || a.correction || !a.receipt.HTTPAccepted || !a.receipt.NativeAccepted || a.receipt.FeedbackRequested || string(a.body) != body {
		return empty, false
	}
	c, r, tool := a.claim, a.receipt, value.value.Tool
	if c.Validate() != nil || c.Kind != ReplyPermissionMutation || c.RequestID != r.RequestID || c.InputRequestID != r.InputRequestID || c.InputRequestID != o.input.receipt.RequestID || c.InteractionID != r.InteractionID || c.InteractionID != value.value.ID || c.ArrivalID != r.ArrivalID || c.ArrivalID != value.arrival || c.SessionID != value.value.SessionID || c.SessionID != o.input.receipt.SessionID || c.MessageID != tool.MessageID || c.CallID != tool.CallID || c.PartID != o.calls[tool.CallID] || c.BodyDigest != mutationDigest([]byte(body)) {
		return empty, false
	}
	part := o.parts[c.PartID]
	if part == nil || part.value.MessageID != c.MessageID || part.value.Tool == nil || part.value.Tool.CallID != c.CallID || !checkpointInlineTool(part.value.Tool) {
		return empty, false
	}
	return c, true
}

func validCheckpointOnce(value nativeCheckpoint) bool {
	p := value.Tools
	if p.Version == 1 {
		return p.InteractionFree && len(p.Once) == 0 && len(p.Always) == 0 && len(p.Policy) == 0 && p.AppliedAlways == 0
	}
	if !validCheckpointPermissionProfile(p) {
		return false
	}
	requests := map[domain.ID]bool{value.Reference.CreationRequestID: true, value.Reference.OwnerID: true}
	type toolOwner struct {
		request          domain.ID
		message, session string
	}
	owners := map[string]toolOwner{}
	for _, history := range checkpointHistories(value) {
		requests[history.RequestID] = true
		for _, message := range history.Messages {
			for _, part := range message.Parts {
				if part.Kind == ToolPartKind {
					owners[part.ID] = toolOwner{history.RequestID, message.ID, history.SessionID}
				}
			}
		}
	}
	names := map[string]checkpointToolName{}
	for _, part := range p.Parts {
		names[part.ID] = part.Name
	}
	seen := map[string]bool{}
	always := map[string]SessionClaim{}
	claims := append([]SessionClaim(nil), p.Once...)
	for _, approval := range p.Always {
		claims = append(claims, approval.Claim)
		always[approval.Claim.InteractionID] = approval.Claim
	}
	for index, claim := range claims {
		body := checkpointOnceBody
		if index >= len(p.Once) {
			if names[claim.PartID] != checkpointReadTool {
				return false
			}
			body = checkpointAlwaysBody
		}
		owner, found := owners[claim.PartID]
		if claim.Validate() != nil || claim.Kind != ReplyPermissionMutation || claim.BodyDigest != mutationDigest([]byte(body)) || !found || owner.request != claim.InputRequestID || owner.message != claim.MessageID || owner.session != claim.SessionID || requests[claim.RequestID] || seen[claim.InteractionID] || seen[claim.ArrivalID] || index > 0 && index < len(p.Once) && p.Once[index-1].RequestID >= claim.RequestID {
			return false
		}
		requests[claim.RequestID] = true
		seen[claim.InteractionID], seen[claim.ArrivalID] = true, true
	}
	for index, policy := range p.Policy {
		owner, found := owners[policy.PartID]
		if !policy.valid() || !found || names[policy.PartID] != checkpointReadTool || owner.request != policy.InputRequestID || owner.message != policy.MessageID || owner.session != policy.SessionID || seen[policy.InteractionID] || seen[policy.ArrivalID] || seen[policy.ReplyEventID] || index > 0 && p.Policy[index-1].InteractionID >= policy.InteractionID {
			return false
		}
		for _, id := range policy.Sources {
			claim, exists := always[id]
			if !exists || claim.InputRequestID != policy.InputRequestID || claim.SessionID != policy.SessionID {
				return false
			}
		}
		seen[policy.InteractionID], seen[policy.ArrivalID], seen[policy.ReplyEventID] = true, true, true
	}
	return true
}
