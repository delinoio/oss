// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"log/slog"
	"time"
)

// This is a configuration-document upgrade, not a database schema migration.
// All Agents move together; a failed validation or crash rolls back the batch.
func upgradeHarnessAgents(ctx context.Context, db *sql.DB) error {
	sqltx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer sqltx.Rollback()
	tx := &Tx{tx: sqltx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	records, _, err := tx.sessionPage(100001, "SELECT "+recordColumns+" FROM entities WHERE kind='agent' AND json_type(body,'$.harness_settings') IS NULL ORDER BY id LIMIT 100001")
	if err != nil {
		return err
	}
	if len(records) > 100000 {
		return domain.Fail(domain.ResourceExhausted, "Too many legacy Agents for an atomic upgrade.", "Preserve the original database and reduce the configuration batch.")
	}
	for _, r := range records {
		agent, err := Decode[domain.Agent](r)
		if err != nil {
			return err
		}
		agent.HarnessSettings = domain.NewInheritedAgentSettings(len(agent.SourceRoutes()))
		if err := agent.Validate(); err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(r.Data, &fields); err != nil {
			return err
		}
		fields["harness_settings"], err = json.Marshal(agent.HarnessSettings)
		if err != nil {
			return err
		}
		if _, err := tx.Put(domain.AgentKind, r.ID, r.Revision, r.SessionID, r.ProjectID, fields); err != nil {
			return err
		}
	}
	if err := sqltx.Commit(); err != nil {
		return storageError(err)
	}
	if len(records) > 0 {
		slog.Info("harness_configuration_upgraded", "agents", len(records), "version", 1)
	}
	return nil
}

func (t *Tx) resolveHarnessSource(agent domain.Agent, route domain.AgentSourceRoute, index int, project *domain.Project) (domain.Agent, error) {
	single := agent.WithSource(route)
	if agent.HarnessSettings == nil {
		return single, nil
	}
	_, anchor, err := decodeEntity[domain.Model](t, domain.ModelKind, route.ModelID)
	if err != nil {
		return domain.Agent{}, err
	}
	var protocol domain.APIProtocol
	for i, link := range route.Accounts {
		_, account, err := decodeEntity[domain.Account](t, domain.AccountKind, link.ID)
		if err != nil {
			return domain.Agent{}, err
		}
		if !anchor.MatchesAccount(account, agent.Harness) {
			return domain.Agent{}, domain.Fail(domain.InvalidArgument, "An inherited harness source changed.", "Retain the original route account and provider/service identity.")
		}
		if account.Type == domain.APIAccount {
			_, provider, err := decodeEntity[domain.Provider](t, domain.ProviderKind, account.ProviderID)
			if err != nil {
				return domain.Agent{}, err
			}
			profile, err := providers.ResolveAccountProfile(provider, account)
			if err != nil {
				return domain.Agent{}, err
			}
			if i > 0 && protocol != profile.Protocol {
				return domain.Agent{}, domain.Fail(domain.MissingInput, "The inherited harness API profile is ambiguous.", "Use accounts with one selected API profile in this source route.")
			}
			protocol = profile.Protocol
		}
	}
	_, settings, err := t.SessionDefaultSettings()
	if err != nil {
		return domain.Agent{}, err
	}
	var projectValues []domain.HarnessDefault
	if project != nil {
		projectValues = project.HarnessDefaults
	}
	values := domain.EffectiveHarnessValues(agent.Harness, anchor.ProviderID, anchor.SubscriptionService, protocol, settings.HarnessDefaults, projectValues, agent.HarnessSettings.Values)
	if selected := agent.HarnessSettings.Models[index]; selected.State == domain.HarnessOverride {
		values.Model = selected
	}
	result, err := values.Apply(single)
	if err != nil {
		return domain.Agent{}, err
	}
	_, model, err := decodeEntity[domain.Model](t, domain.ModelKind, result.ModelID)
	if err != nil {
		return domain.Agent{}, err
	}
	if model.ProviderID != anchor.ProviderID || model.SubscriptionService != anchor.SubscriptionService || model.SourceKind != anchor.SourceKind {
		return domain.Agent{}, domain.Fail(domain.InvalidArgument, "The inherited harness model uses another source.", "Select a default model from the original account source.")
	}
	return result, nil
}
