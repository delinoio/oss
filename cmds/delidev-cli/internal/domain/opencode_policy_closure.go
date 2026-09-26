package domain

// Sources are observed prior direct responses in the original native session.
// They provide context, never a synthesized response or an inferred exact rule
// owner for the target request that native core closed automatically.
type OpenCodePolicySource struct {
	InteractionID   ID     `json:"interaction_id"`
	NativeRequestID string `json:"native_request_id"`
}

type OpenCodePolicyClosure struct {
	NativeEventID   string                     `json:"native_event_id"`
	ProposalEventID string                     `json:"proposal_event_id"`
	Decision        OpenCodePermissionDecision `json:"decision"`
	Sources         []OpenCodePolicySource     `json:"sources"`
}

func (p OpenCodePolicyClosure) Validate() error {
	if NativeIdentity(p.NativeEventID).Validate(OpenCode, NativeEventIdentity) != nil || NativeIdentity(p.ProposalEventID).Validate(OpenCode, NativeEventIdentity) != nil || p.NativeEventID == p.ProposalEventID || p.Decision != OpenCodePermissionAlways && p.Decision != OpenCodePermissionReject || len(p.Sources) == 0 || len(p.Sources) > 1024 {
		return invalidInteraction()
	}
	ids := map[ID]bool{}
	natives := map[string]bool{}
	for _, source := range p.Sources {
		if source.InteractionID.Validate() != nil || NativeIdentity(source.NativeRequestID).Validate(OpenCode, NativePermissionIdentity) != nil || ids[source.InteractionID] || natives[source.NativeRequestID] {
			return invalidInteraction()
		}
		ids[source.InteractionID], natives[source.NativeRequestID] = true, true
	}
	return nil
}
