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

// The public coordinator and original Worker share this immutable selection
// gate. Native initialization must independently prove the effective settings.
func (c ExecutionConfiguration) OpenCodePrimaryForInput(mode SessionMode) (OpenCodePrimaryAgent, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	o := c.Options
	if c.Harness != OpenCode || c.Effort != "" || o.SubagentModel != "" || o.SubagentEffort != "" || o.MaxConcurrency != 0 || o.ApprovalReviewModel != "" || o.ServiceTier != "" {
		return "", Fail(Unsupported, "The selected OpenCode options need an additional native settings adapter.", "Preserve every explicit selection; unsupported settings cannot be omitted or translated.")
	}
	return o.OpenCodePrimaryForInput(mode)
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
