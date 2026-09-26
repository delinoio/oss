package opencode

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This initial observation profile covers native primary-agent defaults only.
// Explicit ordered session overrides need their own full typed observation,
// never a misleading default/sandbox label. It grants no publication authority.
func (s *sessionAPI) initialObservedSettings(ctx context.Context) (domain.ObservedExecutionSettings, error) {
	if err := s.enter(ctx); err != nil {
		return domain.ObservedExecutionSettings{}, err
	}
	defer s.leave()
	if s.problem != nil || !s.apiVerified || s.apiProfile == nil || s.runtimeRoot == "" || s.apiProfile.Settings.Permission == nil || len(s.apiProfile.Settings.Permission) != 0 || s.alive == nil {
		return domain.ObservedExecutionSettings{}, sessionProblem()
	}
	if err := s.alive(); err != nil {
		return domain.ObservedExecutionSettings{}, launchError(err)
	}
	if err := s.verifyInstructions(ctx, "observed-settings"); err != nil {
		return domain.ObservedExecutionSettings{}, err
	}
	return domain.ObservedExecutionSettings{Model: s.apiProfile.Settings.Model, Permission: domain.PermissionDefault, OpenCodeAgent: s.apiProfile.Settings.Agent}, nil
}
