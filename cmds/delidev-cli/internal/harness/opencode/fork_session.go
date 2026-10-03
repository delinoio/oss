// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Absence is a native fork preparation fact, never an observation of effective
// agent/model selection. Normalize only this explicit closed comparison shape;
// no normalized bytes are written back to native storage or published.
func validateForkSession(raw []byte, cwd string, creation *sessionCreation, permission bool) (sessionIdentity, error) {
	fields, err := object(raw)
	if err != nil {
		return sessionIdentity{}, err
	}
	if _, present := fields["agent"]; present {
		return sessionIdentity{}, sessionProblem()
	}
	if _, present := fields["model"]; present {
		return sessionIdentity{}, sessionProblem()
	}
	if _, present := fields["permission"]; present != permission {
		return sessionIdentity{}, sessionProblem()
	}
	if !permission {
		fields["permission"] = json.RawMessage("[]")
	}
	fields["agent"], _ = json.Marshal(creation.settings.Agent)
	fields["model"], _ = json.Marshal(sessionModel{creation.settings.Model, creation.settings.Provider})
	normalized, _ := json.Marshal(fields)
	return validateSession(normalized, cwd, creation, false)
}

func (s *sessionAPI) validateOriginalSession(raw []byte, creation *sessionCreation) (sessionIdentity, error) {
	if !s.forkSelectionPending {
		return validateSession(raw, s.cwd, creation, false)
	}
	if s.input != nil {
		if identity, err := validateSession(raw, s.cwd, creation, false); err == nil {
			s.forkSelectionPending = false
			return identity, nil
		}
	}
	return validateForkSession(raw, s.cwd, creation, true)
}

// VerifiedForkSettings cannot project requested settings from preparation.
// The first original stored input must independently establish native selection
// before this method returns any effective public settings.
func (a *OwnedAPI) VerifiedForkSettings(ctx context.Context) (domain.ObservedExecutionSettings, error) {
	if a == nil || a.session == nil {
		return domain.ObservedExecutionSettings{}, sessionInvalid()
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return domain.ObservedExecutionSettings{}, err
	}
	if s.forkOrigin == nil || s.forkSelectionPending || s.input == nil || !s.input.receipt.Recorded {
		s.leave()
		return domain.ObservedExecutionSettings{}, sessionUncertain()
	}
	_, err := s.readSession(ctx)
	s.leave()
	if err != nil {
		return domain.ObservedExecutionSettings{}, err
	}
	return a.InitialSettings(ctx)
}
