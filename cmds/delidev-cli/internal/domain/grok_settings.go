package domain

// The native custom-model profile requires an explicit context window. This
// copies selected model metadata; it does not turn a declaration into measured
// provider capability or infer a default for older execution snapshots.
type GrokModelContext struct {
	Tokens uint64         `json:"tokens"`
	Source EvidenceSource `json:"source"`
}

func (c GrokModelContext) Validate() error {
	if c.Tokens < 1024 || c.Tokens > 1_000_000_000 || (c.Source != Known && c.Source != UserDeclared) {
		return Fail(Unsupported, "Grok Build needs an explicit supported model context window.", "Record a known or user-declared model context limit from 1,024 to 1,000,000,000 tokens before execution.")
	}
	return nil
}

func (c ExecutionConfiguration) GrokFirstTextContext(mode SessionMode) (uint64, error) {
	selected, err := c.GrokModeForInput(mode)
	if err != nil {
		return 0, err
	}
	if selected != GrokDefaultMode || c.GrokContext == nil {
		return 0, Fail(Unsupported, "This Grok runner requires first Execute text and explicit model context metadata.", "Preserve Plan and other unsupported selections for their separate native profiles.")
	}
	if err := c.GrokContext.Validate(); err != nil {
		return 0, err
	}
	return c.GrokContext.Tokens, nil
}

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
	if c.Harness != GrokBuild || c.Effort != "" || (mode == PlanMode && len(c.Templates) != 0) || o.Permission != PermissionDefault || o.ClaudePermission != "" || o.ApprovalPolicy != "" || o.SubagentModel != "" || o.SubagentEffort != "" || o.MaxConcurrency != 0 || o.ApprovalReviewModel != "" || o.ServiceTier != "" {
		return "", Fail(Unsupported, "The selected Grok Build settings require an additional native profile.", "Preserve explicit settings and instructions; unsupported selections cannot be omitted or translated.")
	}
	if mode == PlanMode {
		return GrokPlanMode, nil
	}
	return GrokDefaultMode, nil
}
