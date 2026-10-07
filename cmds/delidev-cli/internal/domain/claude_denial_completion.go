package domain

// This is the callback-owned stopped execution boundary. The initiating input
// remains distinct from the native session result's absent input identity.
type ClaudeDenialCompletion struct {
	InputID         ID     `json:"input_id"`
	InteractionID   ID     `json:"interaction_id"`
	ArrivalID       ID     `json:"arrival_id"`
	ContextID       ID     `json:"context_id"`
	ResultID        ID     `json:"result_id"`
	CommandNativeID string `json:"command_native_id"`
	IdleNativeID    string `json:"idle_native_id"`
	NativeInputID   *ID    `json:"native_input_id"`
	CleanupVerified bool   `json:"cleanup_verified"`
}

func (v ClaudeDenialCompletion) Validate() error {
	seen := map[string]bool{}
	for _, id := range []ID{v.InputID, v.InteractionID, v.ArrivalID, v.ContextID, v.ResultID} {
		if id.Validate() != nil || seen[string(id)] {
			return invalidClaudeInterruption()
		}
		seen[string(id)] = true
	}
	for _, id := range []string{v.CommandNativeID, v.IdleNativeID} {
		if NativeIdentity(id).Validate(ClaudeCode, NativeTurnIdentity) != nil || seen[id] {
			return invalidClaudeInterruption()
		}
		seen[id] = true
	}
	if v.NativeInputID != nil {
		return invalidClaudeInterruption()
	}
	return nil
}
