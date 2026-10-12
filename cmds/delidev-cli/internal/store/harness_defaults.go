// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

// This is a configuration document upgrade within the current schema, not a
// SQLite migration. One transaction fences the entire live Agent set before
// serving any client. Failed reads/writes roll back every Agent and event.
func upgradeHarnessDefaults(ctx context.Context, db *sql.DB) error {
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer sqlTx.Rollback()
	tx := &Tx{tx: sqlTx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	count, err := tx.upgradeAgentHarnessDocuments()
	if err != nil {
		return err
	}
	for _, kind := range []domain.Kind{domain.ProjectKind, domain.SettingsKind} {
		var after domain.ID
		for {
			rows, err := tx.List(Filter{Kind: kind, After: after, Limit: MaxPage})
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(row.Data, &fields); err != nil {
					return err
				}
				if _, present := fields["harness_defaults"]; !present {
					fields["harness_defaults"], _ = json.Marshal(domain.HarnessDefaults{Version: 1, Entries: []domain.HarnessDefaultEntry{}})
					if _, err := tx.Put(kind, row.ID, row.Revision, row.SessionID, row.ProjectID, fields); err != nil {
						return err
					}
				}
				after = row.ID
			}
		}
	}
	if err := sqlTx.Commit(); err != nil {
		return storageError(err)
	}
	if count > 0 {
		slog.InfoContext(ctx, "agent_harness_defaults_upgraded", "agents", count, "shape", 1)
	}
	return nil
}
func (t *Tx) upgradeAgentHarnessDocuments() (int, error) {
	count := 0
	var after domain.ID
	for {
		rows, err := t.List(Filter{Kind: domain.AgentKind, After: after, Limit: MaxPage})
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			agent, err := Decode[domain.Agent](row)
			if err != nil {
				return 0, err
			}
			if domain.UpgradeAgentHarnessSettings(&agent) {
				if err := agent.Validate(); err != nil {
					return 0, err
				}
				// Patch only these owned fields. Unknown companion selections stay
				// byte-for-byte within their JSON values across the upgrade.
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(row.Data, &fields); err != nil {
					return 0, err
				}
				fields["harness_settings"], _ = json.Marshal(agent.HarnessSettings)
				fields["options"], _ = json.Marshal(agent.Options)
				delete(fields, "effort")
				var routes []map[string]json.RawMessage
				if err := json.Unmarshal(fields["routes"], &routes); err != nil || len(routes) != len(agent.Routes) {
					return 0, domain.Fail(domain.RecoveryRequired, "The live Agent route document cannot be upgraded.", "Preserve the complete configuration and repair its original source routes.")
				}
				for i := range routes {
					routes[i]["model"], _ = json.Marshal(agent.Routes[i].Model)
					routes[i]["model_selection"], _ = json.Marshal(agent.Routes[i].ModelSelection)
				}
				fields["routes"], _ = json.Marshal(routes)
				if _, err := t.Put(domain.AgentKind, row.ID, row.Revision, row.SessionID, row.ProjectID, fields); err != nil {
					return 0, err
				}
				count++
			} else if err := agent.Validate(); err != nil {
				return 0, err
			}
			after = row.ID
		}
	}
	return count, nil
}

func (t *Tx) resolveAgentHarnessDefaults(agentRecord, projectRecord, settingsRecord Record, agent domain.Agent, project *domain.Project, settings domain.Settings, preview SourceRoutingPreview, machineID domain.ID) (domain.Agent, domain.Model, *domain.HarnessSettingsProvenance, error) {
	if agent.HarnessSettings == nil {
		return agent, preview.Model, nil, nil
	}
	selected := preview.Accounts[preview.Route.Selected]
	if selected.Connection == nil {
		return agent, preview.Model, nil, domain.Fail(domain.Conflict, "The selected account is disconnected.", "Reconnect the exact source before resolving harness defaults.")
	}
	source := domain.HarnessDefaultScope{Harness: agent.Harness, Source: domain.HarnessSubscriptionSource, SubscriptionService: selected.SubscriptionService}
	provenance := domain.HarnessSettingsProvenance{AccountID: preview.Route.Selected, ConnectionID: selected.Connection.ID}
	if selected.Type == domain.APIAccount {
		_, provider, err := decodeEntity[domain.Provider](t, domain.ProviderKind, selected.ProviderID)
		if err != nil {
			return agent, preview.Model, nil, err
		}
		profile, err := providers.ResolveAccountProfile(provider, selected)
		if err != nil {
			return agent, preview.Model, nil, err
		}
		source = domain.HarnessDefaultScope{Harness: agent.Harness, Source: domain.HarnessAPISource, ProviderID: selected.ProviderID, APIProtocol: profile.Protocol}
		provenance.ProfileDigest = domain.HarnessProfileDigest(profile.LegacyAPIFormat())
	}
	provenance.Source = source
	layers := []domain.HarnessSettingsLayer{{Settings: domain.HarnessSettings{Version: 1, Model: preview.SourceRoute.ModelSelection}, Origin: domain.HarnessSettingOrigin{Layer: domain.HarnessAgentLayer, ID: agentRecord.ID, Revision: agentRecord.Revision}}, {Settings: *agent.HarnessSettings, Origin: domain.HarnessSettingOrigin{Layer: domain.HarnessAgentLayer, ID: agentRecord.ID, Revision: agentRecord.Revision}}}
	if project != nil {
		layers = append(layers, domain.HarnessDefaultLayers(project.HarnessDefaults, source, domain.HarnessSettingOrigin{Layer: domain.HarnessProjectLayer, ID: projectRecord.ID, Revision: projectRecord.Revision})...)
	}
	layers = append(layers, domain.HarnessDefaultLayers(settings.HarnessDefaults, source, domain.HarnessSettingOrigin{Layer: domain.HarnessServerLayer, ID: settingsRecord.ID, Revision: settingsRecord.Revision})...)
	modelName, err := domain.ResolveHarnessModel(layers)
	if err != nil {
		return agent, preview.Model, nil, err
	}
	native, err := t.nativeHarnessDefaultLayer(machineID, preview.Route.Selected, selected, modelName, agent.Harness)
	if err != nil {
		return agent, preview.Model, nil, err
	}
	if native != nil {
		layers = append(layers, *native)
	}
	modelName, effort, options, provenance, err := domain.ResolveHarnessSettings(layers, provenance)
	if err != nil {
		return agent, preview.Model, nil, err
	}
	model := preview.Model
	model.NativeID, model.Name = modelName, modelName
	// Advisory metadata belongs only to the same explicit model override.
	if preview.SourceRoute.Model.NativeID != modelName {
		model.ContextLimit, model.InputModalities, model.MetadataSource = nil, nil, domain.Unknown
	}
	agent.Model = &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: model.ProviderID, SubscriptionService: model.SubscriptionService, NativeID: modelName}, Name: model.Name, ContextLimit: model.ContextLimit, InputModalities: model.InputModalities, MetadataSource: model.MetadataSource}
	agent.ModelID = agent.Model.ModelIdentity.Key()
	agent.HarnessSettings = nil
	agent.Effort, agent.Options = effort, options
	return agent, model, &provenance, nil
}

// Existing accepted Codex model observations provide exact per-model reasoning
// and service-tier defaults. They do not prove a default model or permissions.
// Dispatch reads them only; it never queues an observation/probe automatically.
func (t *Tx) nativeHarnessDefaultLayer(machineID, accountID domain.ID, account domain.Account, model string, harness domain.Harness) (*domain.HarnessSettingsLayer, error) {
	if harness != domain.Codex || account.Type != domain.APIAccount || account.Connection == nil {
		return nil, nil
	}
	mr, machine, err := decodeEntity[domain.Machine](t, domain.MachineKind, machineID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.NativeModelsV1) {
		return nil, nil
	}
	ar, err := t.Get(domain.AccountKind, accountID)
	if err != nil {
		return nil, err
	}
	for _, installation := range machine.Installations {
		if installation.Harness != domain.Codex || installation.State != domain.InstallationDetected {
			continue
		}
		for _, includeHidden := range []bool{false, true} {
			scope := domain.NativeModelScope{Version: 1, MachineID: machineID, MachineRevision: mr.Revision, InstallationGeneration: machine.DiscoveryRevision, NativeVersion: installation.Version, Executable: installation.ResolvedPath, ExecutableSHA256: installation.ExecutableSHA256, AccountID: accountID, AccountRevision: ar.Revision, ConnectionID: account.Connection.ID, ProviderID: account.ProviderID, Actor: domain.Principal{Type: domain.OwnerDevice}, IncludeHidden: includeHidden}
			if scope.Validate() != nil {
				continue
			}
			row, err := t.LastNativeModelSuccess(scope)
			if err != nil {
				return nil, err
			}
			if row.ID == "" {
				continue
			}
			job, err := Decode[domain.Job](row)
			if err != nil {
				return nil, err
			}
			if job.Type != domain.NativeModelsJob || job.State != domain.JobSucceeded || job.MachineID != machineID || job.ParentID != accountID {
				continue
			}
			var original domain.NativeModelScope
			var observation domain.NativeModelObservation
			if domain.Decode(job.Input, &original) != nil || domain.Decode(job.Output, &observation) != nil || original.Validate() != nil || observation.Validate(original.IncludeHidden) != nil || original.IncludeHidden != includeHidden || original.MachineID != machineID || original.AccountID != accountID || original.ConnectionID != account.Connection.ID || original.InstallationGeneration != machine.DiscoveryRevision || original.ExecutableSHA256 != installation.ExecutableSHA256 || original.MachineRevision != mr.Revision || original.AccountRevision != ar.Revision || original.ProviderID != account.ProviderID || original.NativeVersion != installation.Version || original.Executable != installation.ResolvedPath || observation.ObservedAt.Before(account.Connection.ConnectedAt) || observation.ObservedAt.After(t.now.Add(time.Second)) {
				continue
			}
			if original.Actor.Type == domain.ClientDevice {
				dr, err := t.Get(domain.DeviceKind, original.Actor.DeviceID)
				if err != nil {
					if domain.SafeError(err).Code == domain.NotFound {
						continue
					}
					return nil, err
				}
				device, err := Decode[domain.Device](dr)
				if err != nil {
					return nil, err
				}
				if device.Revoked || device.Type != domain.ClientDevice {
					continue
				}
			}
			for _, entry := range observation.Models {
				if entry.Model != model {
					continue
				}
				settings := domain.HarnessSettings{Version: 1, Effort: domain.OverrideHarnessSetting(string(entry.DefaultReasoning))}
				if entry.DefaultServiceTier != nil {
					settings.ServiceTier = domain.OverrideHarnessSetting(*entry.DefaultServiceTier)
				}
				return &domain.HarnessSettingsLayer{Settings: settings, Origin: domain.HarnessSettingOrigin{Layer: domain.HarnessNativeLayer, ID: row.ID, Revision: row.Revision, EvidenceDigest: domain.HarnessEvidenceDigest(job.Output)}}, nil
			}
		}
	}
	return nil, nil
}
