package domain

// OpenCode primary agents are native policies, not filesystem sandbox modes.
// The native adapter must independently compare their full ordered rules.
type OpenCodePrimaryAgent string

const (
	OpenCodeBuildAgent OpenCodePrimaryAgent = "build"
	OpenCodePlanAgent  OpenCodePrimaryAgent = "plan"
)

func (a OpenCodePrimaryAgent) Valid() bool {
	return a == OpenCodeBuildAgent || a == OpenCodePlanAgent
}

// Retain the original default selection; Plan selects the native primary agent
// for this input without rewriting the immutable Agent configuration.
func (o AgentOptions) OpenCodePrimaryForInput(mode SessionMode) (OpenCodePrimaryAgent, error) {
	if !mode.Valid() {
		return "", Fail(InvalidArgument, "Invalid input mode.", "Select Execute or Plan.")
	}
	if o.Permission != PermissionDefault || o.ClaudePermission != "" || o.ApprovalPolicy != "" {
		return "", Fail(Unsupported, "The permission selection is not supported by this OpenCode profile.", "Retain the explicit selection; another harness's policy cannot be translated or omitted.")
	}
	if mode == PlanMode {
		return OpenCodePlanAgent, nil
	}
	return OpenCodeBuildAgent, nil
}
