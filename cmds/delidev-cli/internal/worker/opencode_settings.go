package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

const openCodeAPIProvider = "delidev"

type openCodeRequestedSettings struct {
	Session      opencode.SessionSettings `json:"-"`
	Instructions string                   `json:"-"`
	Rejection    opencode.RejectionPolicy `json:"-"`
}

// Requested immutable settings are not native observations. The owned API
// initializer and original session must independently validate their effect;
// neither this mapping nor advisory model metadata authorizes public dispatch.
func openCodeExecutionSettings(c domain.ExecutionConfiguration, mode domain.SessionMode, title string) (openCodeRequestedSettings, error) {
	if err := c.Validate(); err != nil {
		return openCodeRequestedSettings{}, err
	}
	if err := domain.Text(title, "native session title", 1024, true); err != nil {
		return openCodeRequestedSettings{}, err
	}
	o := c.Options
	if c.Harness != domain.OpenCode || c.Effort != "" || o.SubagentModel != "" || o.SubagentEffort != "" || o.MaxConcurrency != 0 || o.ApprovalReviewModel != "" || o.ServiceTier != "" {
		return openCodeRequestedSettings{}, domain.Fail(domain.Unsupported, "The selected OpenCode options need an additional native settings adapter.", "Preserve every explicit selection; unsupported settings cannot be omitted or translated.")
	}
	agent, err := o.OpenCodePrimaryForInput(mode)
	if err != nil {
		return openCodeRequestedSettings{}, err
	}
	return openCodeRequestedSettings{
		Session:      opencode.SessionSettings{Title: title, Agent: agent, Provider: openCodeAPIProvider, Model: c.NativeModel, Permission: []opencode.PermissionRule{}},
		Instructions: c.Instructions,
		Rejection:    opencode.StopOnInteractionRejection,
	}, nil
}
