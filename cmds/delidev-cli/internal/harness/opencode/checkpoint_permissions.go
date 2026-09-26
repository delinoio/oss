package opencode

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const checkpointOnceBody = `{"reply":"once"}`

// Permission acceptance is an original live fact, never reconstructed from a
// completed tool or an empty native pending-request list after replacement.
func (o *inputObserver) checkpointOnce(value *observedInteraction) (SessionClaim, bool) {
	var empty SessionClaim
	if value == nil || value.value.Kind != PermissionInteraction || value.value.Tool == nil || value.value.Permission == nil || !value.closed || value.rejected || value.canceled || value.pendingAbsent || value.alwaysAccepted || value.rejectionReserved || len(value.rejectionSources) != 0 || len(value.alwaysObservations) != 0 {
		return empty, false
	}
	a := value.attempt
	if a == nil || !a.sent || a.permission == nil || *a.permission != PermissionOnce || a.correction || !a.receipt.HTTPAccepted || !a.receipt.NativeAccepted || a.receipt.FeedbackRequested || string(a.body) != checkpointOnceBody {
		return empty, false
	}
	c, r, tool := a.claim, a.receipt, value.value.Tool
	if c.Validate() != nil || c.Kind != ReplyPermissionMutation || c.RequestID != r.RequestID || c.InputRequestID != r.InputRequestID || c.InputRequestID != o.input.receipt.RequestID || c.InteractionID != r.InteractionID || c.InteractionID != value.value.ID || c.ArrivalID != r.ArrivalID || c.ArrivalID != value.arrival || c.SessionID != value.value.SessionID || c.SessionID != o.input.receipt.SessionID || c.MessageID != tool.MessageID || c.CallID != tool.CallID || c.PartID != o.calls[tool.CallID] || c.BodyDigest != mutationDigest([]byte(checkpointOnceBody)) {
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
		return p.InteractionFree && len(p.Once) == 0
	}
	if p.Version != 2 || p.InteractionFree || len(p.Once) == 0 || len(p.Once) > maxObservedInteractions {
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
	seen := map[string]bool{}
	for index, claim := range p.Once {
		owner, found := owners[claim.PartID]
		if claim.Validate() != nil || claim.Kind != ReplyPermissionMutation || claim.BodyDigest != mutationDigest([]byte(checkpointOnceBody)) || !found || owner.request != claim.InputRequestID || owner.message != claim.MessageID || owner.session != claim.SessionID || requests[claim.RequestID] || seen[claim.InteractionID] || seen[claim.ArrivalID] || index > 0 && p.Once[index-1].RequestID >= claim.RequestID {
			return false
		}
		requests[claim.RequestID] = true
		seen[claim.InteractionID], seen[claim.ArrivalID] = true, true
	}
	return true
}
