// SPDX-License-Identifier: Apache-2.0
package server

import (
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Only the first native publication can decide policy. The enclosing publication
// receipt atomically retains this decision and its ordinary durable response.
func (s *Service) decideAutomaticPlanApproval(tx *store.Tx, id domain.ID) error {
	r, err := tx.Get(domain.InteractionKind, id)
	if err != nil {
		return err
	}
	v, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return err
	}
	response, eligible := nativePlanApproval(v)
	if !eligible || v.PlanApprovalPolicy == domain.PlanApprovalManual {
		return nil
	}
	if v.PlanApprovalPolicy == domain.PlanApprovalUnavailable {
		s.logger.Warn("automatic_plan_approval", "phase", "policy", "outcome", "unavailable")
		return nil
	}
	if v.PlanApprovalPolicy != domain.PlanApprovalAutomatic || v.ApprovalResponse != nil {
		return nil
	}
	// Reuse the original native response and live actor/account/Worker scope.
	// Both writes remain inside the first publication transaction; manual policy
	// is stored with the original revision and does not add a checkpoint revision.
	if _, err := s.approvalResponseScope(tx, r, v); err == nil {
		// Admission is checked before acceptance. Every error from acceptance,
		// including its durable write, must roll back the publication transaction.
		_, err := s.acceptApprovalResponse(tx, domain.NewID(), id, r.Revision, response)
		return err
	} else {
		var admission *domain.Error
		if !errors.As(err, &admission) || (admission.Code != domain.Conflict && admission.Code != domain.NotFound && admission.Code != domain.PermissionDenied && admission.Code != domain.RecoveryRequired) {
			return err
		}
	}
	v.PlanApprovalPolicy = domain.PlanApprovalUnavailable
	s.logger.Warn("automatic_plan_approval", "phase", "admission", "outcome", "unavailable")
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, v)
	return err
}

func effectivePlanApproval(tx *store.Tx, projectID domain.ID) (bool, error) {
	settings := domain.DefaultSettings()
	rows, err := tx.List(store.Filter{Kind: domain.SettingsKind, Limit: 2})
	if err != nil {
		return false, err
	}
	if len(rows) > 1 {
		return false, executionEventConflict()
	}
	if len(rows) == 1 {
		settings, err = store.Decode[domain.Settings](rows[0])
		if err != nil {
			return false, err
		}
	}
	if err := settings.Validate(); err != nil {
		return false, err
	}
	var project *domain.Project
	if projectID != "" {
		row, err := tx.Get(domain.ProjectKind, projectID)
		if err != nil {
			return false, err
		}
		value, err := store.Decode[domain.Project](row)
		if err != nil {
			return false, err
		}
		if err := value.Validate(); err != nil {
			return false, err
		}
		project = &value
	}
	return project.EffectivePlanApproval(settings.AutomaticPlanApproval), nil
}
func nativePlanApproval(v domain.ExecutionInteraction) (domain.ApprovalResponseInput, bool) {
	if v.Type != domain.NativeApprovalInteraction {
		return domain.ApprovalResponseInput{}, false
	}
	if v.Claude != nil && v.Claude.Kind == domain.ClaudePlanApproval {
		return domain.ApprovalResponseInput{Claude: &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow}}, true
	}
	if v.Grok != nil && v.Grok.Event.Method == domain.GrokPlanMethod {
		return domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Outcome: domain.GrokPlanApproved}}, true
	}
	return domain.ApprovalResponseInput{}, false
}
