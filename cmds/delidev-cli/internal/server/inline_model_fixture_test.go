// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Fixture source changes precede execution admission. They modify the inline
// Worker route rather than creating an independent persistent Model registry.
func fixtureInlineSource(tx *store.Tx, agentID domain.ID, provider domain.ID, service domain.SubscriptionService) (domain.ID, error) {
	row, err := tx.Get(domain.AgentKind, agentID)
	if err != nil {
		return "", err
	}
	agent, err := store.Decode[domain.Agent](row)
	if err != nil {
		return "", err
	}
	m := *agent.Routes[0].Model
	m.ProviderID, m.SubscriptionService = provider, service
	agent.Routes[0].Model = &m
	_, err = tx.Put(domain.AgentKind, row.ID, row.Revision, "", "", agent)
	return m.ModelIdentity.Key(), err
}

// Test selections are explicit fixtures, not assumed native defaults.
func fixtureHarnessSettings(effort string, o domain.AgentOptions) *domain.HarnessSettings {
	return &domain.HarnessSettings{Version: 1, Effort: domain.OverrideHarnessSetting(effort), Permission: domain.OverrideHarnessSetting(o.Permission), ClaudePermission: domain.OverrideHarnessSetting(o.ClaudePermission), ApprovalPolicy: domain.OverrideHarnessSetting(o.ApprovalPolicy), ApprovalsReviewer: domain.OverrideHarnessSetting(o.ApprovalsReviewer), SubagentModel: domain.OverrideHarnessSetting(o.SubagentModel), SubagentEffort: domain.OverrideHarnessSetting(o.SubagentEffort), MaxConcurrency: domain.OverrideHarnessSetting(o.MaxConcurrency), ApprovalReviewModel: domain.OverrideHarnessSetting(o.ApprovalReviewModel), ServiceTier: domain.OverrideHarnessSetting(o.ServiceTier)}
}
func fixtureProjectHarnessSelectors(a domain.Agent) domain.Agent {
	if a.HarnessSettings == nil {
		return a
	}
	s := a.HarnessSettings
	if s.Effort != nil && s.Effort.Value != nil {
		a.Effort = *s.Effort.Value
	}
	o := &a.Options
	if s.Permission != nil && s.Permission.Value != nil {
		o.Permission = *s.Permission.Value
	}
	if s.ClaudePermission != nil && s.ClaudePermission.Value != nil {
		o.ClaudePermission = *s.ClaudePermission.Value
	}
	if s.ApprovalPolicy != nil && s.ApprovalPolicy.Value != nil {
		o.ApprovalPolicy = *s.ApprovalPolicy.Value
	}
	if s.ApprovalsReviewer != nil && s.ApprovalsReviewer.Value != nil {
		o.ApprovalsReviewer = *s.ApprovalsReviewer.Value
	}
	if s.SubagentModel != nil && s.SubagentModel.Value != nil {
		o.SubagentModel = *s.SubagentModel.Value
	}
	if s.SubagentEffort != nil && s.SubagentEffort.Value != nil {
		o.SubagentEffort = *s.SubagentEffort.Value
	}
	if s.MaxConcurrency != nil && s.MaxConcurrency.Value != nil {
		o.MaxConcurrency = *s.MaxConcurrency.Value
	}
	if s.ApprovalReviewModel != nil && s.ApprovalReviewModel.Value != nil {
		o.ApprovalReviewModel = *s.ApprovalReviewModel.Value
	}
	if s.ServiceTier != nil && s.ServiceTier.Value != nil {
		o.ServiceTier = *s.ServiceTier.Value
	}
	return a
}
func fixtureCaptureHarnessSelectors(a *domain.Agent) {
	a.HarnessSettings = fixtureHarnessSettings(a.Effort, a.Options)
	a.Effort = ""
	a.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	for i := range a.Routes {
		a.Routes[i].ModelSelection = domain.OverrideHarnessSetting(a.Routes[i].Model.NativeID)
	}
}
