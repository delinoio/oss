package opencode

import (
	"bytes"

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
	if value == nil || value.value.Kind != PermissionInteraction || value.value.Permission == nil || value.alwaysAccepted != (decision == PermissionAlways) || value.attempt == nil || value.attempt.permission == nil || *value.attempt.permission != decision {
		return empty, false
	}
	claim, valid := o.checkpointDirectClaim(value, ReplyPermissionMutation, []byte(body))
	if !valid || o.parts[claim.PartID].value.Tool.Name == "question" {
		return empty, false
	}
	return claim, true
}

type checkpointReplyProfile struct {
	Kind              SessionMutation
	Body              []byte
	Rejected          bool
	FeedbackRequested bool
	Correction        bool
}

func (o *inputObserver) checkpointDirectClaim(value *observedInteraction, kind SessionMutation, body []byte) (SessionClaim, bool) {
	return o.checkpointAcceptedClaim(value, checkpointReplyProfile{Kind: kind, Body: body, Rejected: kind == RejectQuestionMutation})
}

func (o *inputObserver) checkpointAcceptedClaim(value *observedInteraction, expected checkpointReplyProfile) (SessionClaim, bool) {
	kind, body := expected.Kind, expected.Body
	var empty SessionClaim
	if value == nil || value.value.Tool == nil || !value.closed || value.rejected != expected.Rejected || value.canceled || value.pendingAbsent || value.rejectionReserved || len(value.rejectionSources) != 0 || len(value.alwaysObservations) != 0 {
		return empty, false
	}
	a := value.attempt
	if a == nil || !a.sent || a.correction != expected.Correction || !a.receipt.HTTPAccepted || !a.receipt.NativeAccepted || a.receipt.FeedbackRequested != expected.FeedbackRequested || !bytes.Equal(a.body, body) {
		return empty, false
	}
	c, r, tool := a.claim, a.receipt, value.value.Tool
	if c.Validate() != nil || c.Kind != kind || c.RequestID != r.RequestID || c.InputRequestID != r.InputRequestID || c.InputRequestID != o.input.receipt.RequestID || c.InteractionID != r.InteractionID || c.InteractionID != value.value.ID || c.ArrivalID != r.ArrivalID || c.ArrivalID != value.arrival || c.SessionID != value.value.SessionID || c.SessionID != o.input.receipt.SessionID || c.MessageID != tool.MessageID || c.CallID != tool.CallID || c.PartID != o.calls[tool.CallID] || c.BodyDigest != mutationDigest(body) {
		return empty, false
	}
	part := o.parts[c.PartID]
	if part == nil || part.value.MessageID != c.MessageID || part.value.Tool == nil || part.value.Tool.CallID != c.CallID || !checkpointInlineTool(part.value.Tool) {
		return empty, false
	}
	return c, true
}

func validCheckpointInteractions(value nativeCheckpoint) bool {
	p := value.Tools
	if p.Version == 1 {
		return p.InteractionFree && len(p.Once) == 0 && len(p.Always) == 0 && len(p.Policy) == 0 && len(p.Questions) == 0 && len(p.Rejections) == 0 && len(p.RejectionPolicy) == 0 && p.AppliedAlways == 0
	}
	if !validCheckpointInteractionProfile(p) {
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
	failed := map[string]bool{}
	rejectedParts := map[string]bool{}
	rejected := map[string]SessionClaim{}
	for _, part := range p.Parts {
		names[part.ID] = part.Name
		failed[part.ID] = part.Failed
	}
	seen := map[string]bool{}
	always := map[string]SessionClaim{}
	claims := append([]SessionClaim(nil), p.Once...)
	for _, approval := range p.Always {
		claims = append(claims, approval.Claim)
		always[approval.Claim.InteractionID] = approval.Claim
	}
	questionStart := len(claims)
	questionParts := map[string]bool{}
	for _, reply := range p.Questions {
		claims = append(claims, reply.Claim)
	}
	rejectionStart := len(claims)
	for _, rejection := range p.Rejections {
		claims = append(claims, rejection.Claim)
	}
	for index, claim := range claims {
		body := checkpointOnceBody
		kind := ReplyPermissionMutation
		digest := mutationDigest([]byte(body))
		if index >= rejectionStart {
			rejection := p.Rejections[index-rejectionStart]
			if names[claim.PartID] != checkpointReadTool || !failed[claim.PartID] || !nativeID(rejection.ReplyEventID, "evt") || rejection.ReplyEventID == claim.ArrivalID || seen[rejection.ReplyEventID] || index > rejectionStart && p.Rejections[index-rejectionStart-1].Claim.RequestID >= claim.RequestID {
				return false
			}
			digest = claim.BodyDigest
			seen[rejection.ReplyEventID], rejectedParts[claim.PartID] = true, true
			rejected[claim.InteractionID] = claim
		} else if index >= questionStart {
			reply := p.Questions[index-questionStart]
			if names[claim.PartID] != checkpointQuestionTool || questionParts[claim.PartID] || !nativeID(reply.ReplyEventID, "evt") || reply.ReplyEventID == claim.ArrivalID || seen[reply.ReplyEventID] || index > questionStart && p.Questions[index-questionStart-1].Claim.RequestID >= claim.RequestID {
				return false
			}
			kind, digest = ReplyQuestionMutation, claim.BodyDigest
			if claim.Kind == RejectQuestionMutation && p.Version >= 8 {
				kind, digest = RejectQuestionMutation, mutationDigest(nil)
			}
			seen[reply.ReplyEventID], questionParts[claim.PartID] = true, true
		} else if index >= len(p.Once) {
			if names[claim.PartID] != checkpointReadTool {
				return false
			}
			digest = mutationDigest([]byte(checkpointAlwaysBody))
		} else if names[claim.PartID] != checkpointReadTool && names[claim.PartID] != checkpointShellTool && !checkpointSearchOrTodo(names[claim.PartID]) && !checkpointFileTool(names[claim.PartID]) {
			return false
		}
		owner, found := owners[claim.PartID]
		if claim.Validate() != nil || claim.Kind != kind || claim.BodyDigest != digest || !found || owner.request != claim.InputRequestID || owner.message != claim.MessageID || owner.session != claim.SessionID || requests[claim.RequestID] || seen[claim.InteractionID] || seen[claim.ArrivalID] || index > 0 && index < len(p.Once) && p.Once[index-1].RequestID >= claim.RequestID {
			return false
		}
		requests[claim.RequestID] = true
		seen[claim.InteractionID], seen[claim.ArrivalID] = true, true
	}
	for _, part := range p.Parts {
		if part.Name == checkpointQuestionTool && !questionParts[part.ID] {
			return false
		}
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
	for index, policy := range p.RejectionPolicy {
		owner, found := owners[policy.PartID]
		if !policy.identity().valid() || !found || names[policy.PartID] != checkpointReadTool || !failed[policy.PartID] || owner.request != policy.InputRequestID || owner.message != policy.MessageID || owner.session != policy.SessionID || seen[policy.InteractionID] || seen[policy.ArrivalID] || seen[policy.ReplyEventID] || index > 0 && p.RejectionPolicy[index-1].InteractionID >= policy.InteractionID {
			return false
		}
		for _, id := range policy.Sources {
			claim, exists := rejected[id]
			if !exists || claim.InputRequestID != policy.InputRequestID || claim.SessionID != policy.SessionID {
				return false
			}
		}
		seen[policy.InteractionID], seen[policy.ArrivalID], seen[policy.ReplyEventID] = true, true, true
		rejectedParts[policy.PartID] = true
	}
	for _, part := range p.Parts {
		if part.Failed && !rejectedParts[part.ID] {
			return false
		}
	}
	return true
}
