package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

// This prepares requested settings only. OpenAPIStream must independently
// verify native permission/account initialization and applied model/effort;
// neither this mapping nor saved options authorizes public dispatch.
func claudeExecutionSettings(c domain.ExecutionConfiguration, mode domain.SessionMode) (claude.NativePermission, claude.NativeEffort, error) {
	if err := c.Validate(); err != nil {
		return "", "", err
	}
	o := c.Options
	if c.Harness != domain.ClaudeCode || o.SubagentModel != "" || o.SubagentEffort != "" || o.MaxConcurrency != 0 || o.ApprovalReviewModel != "" || o.ServiceTier != "" {
		return "", "", domain.Fail(domain.Unsupported, "The selected Claude options need an additional native settings adapter.", "Preserve the explicit selection; unsupported settings cannot be omitted or translated.")
	}
	selected, err := o.ClaudePermissionForInput(mode)
	if err != nil {
		return "", "", err
	}
	permissions := map[domain.ClaudePermissionMode]claude.NativePermission{
		domain.ClaudePermissionDefault:     claude.DefaultPermission,
		domain.ClaudePermissionPlan:        claude.PlanPermission,
		domain.ClaudePermissionAcceptEdits: claude.AcceptEditsPermission,
		domain.ClaudePermissionDontAsk:     claude.DontAskPermission,
		domain.ClaudePermissionBypass:      claude.BypassPermission,
	}
	effort := claude.NativeEffort(c.Effort)
	switch effort {
	case "", claude.LowEffort, claude.MediumEffort, claude.HighEffort, claude.XHighEffort, claude.MaxEffort:
	default:
		return "", "", domain.Fail(domain.Unsupported, "The selected effort is not supported by this Claude profile.", "Use an exact supported native effort or leave it unspecified.")
	}
	return permissions[selected], effort, nil
}
