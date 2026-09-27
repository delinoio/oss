package domain

// Grok modes are original ACP observations, not filesystem sandbox policies.
type GrokMode string

const (
	GrokDefaultMode GrokMode = "default"
	GrokPlanMode    GrokMode = "plan"
)

func (m GrokMode) Valid() bool { return m == GrokDefaultMode || m == GrokPlanMode }

func (c ExecutionConfiguration) GrokModeForInput(mode SessionMode) (GrokMode, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if !mode.Valid() {
		return "", Fail(InvalidArgument, "Invalid input mode.", "Select Execute or Plan.")
	}
	o := c.Options
	if c.Harness != GrokBuild || c.Effort != "" || c.Instructions != "" || len(c.Templates) != 0 || o.Permission != PermissionDefault || o.ClaudePermission != "" || o.ApprovalPolicy != "" || o.SubagentModel != "" || o.SubagentEffort != "" || o.MaxConcurrency != 0 || o.ApprovalReviewModel != "" || o.ServiceTier != "" {
		return "", Fail(Unsupported, "The selected Grok Build settings require an additional native profile.", "Preserve explicit settings and instructions; unsupported selections cannot be omitted or translated.")
	}
	if mode == PlanMode {
		return GrokPlanMode, nil
	}
	return GrokDefaultMode, nil
}
