package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

// This prepares requested settings only. OpenAPIStream must independently
// verify native permission/account initialization and applied model/effort;
// neither this mapping nor saved options authorizes public dispatch.
func claudeExecutionSettings(c domain.ExecutionConfiguration, mode domain.SessionMode) (claude.NativePermission, claude.NativeEffort, error) {
	selected, err := c.ClaudeAPIInputPermission(mode)
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
	return permissions[selected], claude.NativeEffort(c.Effort), nil
}
