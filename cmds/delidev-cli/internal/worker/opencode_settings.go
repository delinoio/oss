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
	if err := domain.Text(title, "native session title", 1024, true); err != nil {
		return openCodeRequestedSettings{}, err
	}
	agent, err := c.OpenCodePrimaryForInput(mode)
	if err != nil {
		return openCodeRequestedSettings{}, err
	}
	return openCodeRequestedSettings{
		Session:      opencode.SessionSettings{Effort: c.Effort, Title: title, Agent: agent, Provider: openCodeAPIProvider, Model: c.NativeModel, Permission: []opencode.PermissionRule{}},
		Instructions: c.Instructions,
		Rejection:    opencode.StopOnInteractionRejection,
	}, nil
}
