package opencode

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Native primary-agent permissions remain separate from Codex sandboxes.
// Explicit effort requires matching loaded config/provider observations.
// These observations grant no independent publication authority.
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
	var appliedEffort *string
	if s.apiProfile.Settings.Effort != "" {
		// Re-read both independently loaded native views immediately before binding;
		// a requested value alone cannot become an applied-setting observation.
		s.reconciliationRead = true
		defer func() { s.reconciliationRead = false }()
		for _, read := range []struct {
			path     string
			validate func([]byte) error
		}{
			{"/config", s.apiProfile.validateConfig}, {"/provider", s.apiProfile.validateProvider},
		} {
			raw, _, err := s.request(ctx, http.MethodGet, read.path, nil, http.StatusOK)
			if err != nil {
				return domain.ObservedExecutionSettings{}, err
			}
			if err := read.validate(raw); err != nil {
				return domain.ObservedExecutionSettings{}, err
			}
			if read.path == "/provider" {
				// Project only after the complete native provider object matches.
				// Read the applied value from native bytes, not requested settings.
				var loaded struct {
					All []struct {
						Models map[string]struct{ Options map[string]json.RawMessage }
					}
				}
				if json.Unmarshal(raw, &loaded) != nil || len(loaded.All) != 1 {
					return domain.ObservedExecutionSettings{}, sessionProblem()
				}
				var value string
				if json.Unmarshal(loaded.All[0].Models[s.apiProfile.Settings.Model].Options["reasoningEffort"], &value) != nil {
					return domain.ObservedExecutionSettings{}, sessionProblem()
				}
				appliedEffort = &value
			}
		}
	}
	observed := domain.ObservedExecutionSettings{Model: s.apiProfile.Settings.Model, Permission: domain.PermissionDefault, OpenCodeAgent: s.apiProfile.Settings.Agent}
	observed.Effort = appliedEffort
	return observed, nil
}
