package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

type ConfigurationMutation struct {
	RequestID        domain.ID       `json:"request_id"`
	ID               domain.ID       `json:"id,omitempty"`
	ExpectedRevision uint64          `json:"expected_revision"`
	Kind             domain.Kind     `json:"kind"`
	Document         json.RawMessage `json:"document"`
}

type validatable interface{ Validate() error }

func configurationValue(kind domain.Kind, raw []byte, requireRepositoryURL bool) (validatable, error) {
	var fields map[string]json.RawMessage
	if err := domain.Decode(raw, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"harness_settings", "harness_defaults"} {
		if field, ok := fields[key]; ok && bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
			return nil, domain.Fail(domain.InvalidArgument, "Harness configuration cannot be null.", "Preserve explicit inherit/override states.")
		}
	}
	var value validatable
	if kind == domain.ProjectKind || kind == domain.SettingsKind {
		var fields map[string]json.RawMessage
		if err := domain.Decode(raw, &fields); err != nil {
			return nil, err
		}
		if v, ok := fields["plan_mode_default"]; kind == domain.SettingsKind && ok && !bytes.Equal(v, []byte("true")) && !bytes.Equal(v, []byte("false")) {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid Plan Mode default.", "Use an explicit boolean.")
		}
		if v, ok := fields["branch_prefix"]; kind == domain.SettingsKind && ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid branch prefix.", "Use a string, including empty to disable.")
		}
		if v, ok := fields["automatic_plan_approval"]; kind == domain.SettingsKind && ok && !bytes.Equal(v, []byte("true")) && !bytes.Equal(v, []byte("false")) {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid automatic plan approval value.", "Use an explicit boolean.")
		}
		if v, ok := fields["settings"]; kind == domain.ProjectKind && ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid project settings.", "Use a typed settings object, including an empty object for inheritance.")
		}
	}
	switch kind {
	case domain.ProjectKind:
		value = &domain.Project{}
	case domain.RepositoryKind:
		value = &domain.Repository{AutoFetch: true}
	case domain.AgentKind:
		value = &domain.Agent{}
	case domain.AccountKind:
		value = &domain.Account{}
	case domain.ProviderKind:
		value = &domain.Provider{}
	case domain.ModelKind:
		value = &domain.Model{}
	case domain.TemplateKind:
		value = &domain.Template{}
	case domain.SettingsKind:
		prefix := domain.DefaultBranchPrefix
		value = &domain.Settings{BranchPrefix: &prefix}
	default:
		return nil, domain.Fail(domain.InvalidArgument, "This entity is not editable configuration.", "Use the entity's dedicated product operation.")
	}
	if err := domain.Decode(raw, value); err != nil {
		return nil, err
	}
	if project, ok := value.(*domain.Project); ok && project.Settings != nil && project.Settings.BranchPrefix != nil && project.Settings.PlanModeDefault == "" {
		project.Settings.PlanModeDefault = domain.InheritBoolean
	}
	if repository, ok := value.(*domain.Repository); ok {
		if repository.RemoteURL == "" {
			if requireRepositoryURL {
				return nil, domain.Fail(domain.InvalidArgument, "Enter a credential-free HTTPS or SSH Git URL.", "Use HTTPS, ssh:// or SCP-style SSH. Local paths, passwords, tokens and helper transports are unsupported.")
			}
		} else {
			parsed, err := domain.ParseRepositoryCloneURL(repository.RemoteURL)
			if err != nil {
				return nil, err
			}
			if repository.Name == "" {
				repository.Name = parsed.DirectoryName
			}
		}
	}
	if _, ok := fields["harness_defaults"]; ok {
		switch v := value.(type) {
		case *domain.Project:
			v.HarnessDefaultsVersion = 1
		case *domain.Settings:
			v.HarnessDefaultsVersion = 1
		}
	}
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return value, nil
}
func SaveConfiguration(ctx context.Context, s *store.Store, input ConfigurationMutation) (store.Result, error) {
	value, err := configurationValue(input.Kind, input.Document, true)
	if err != nil {
		return store.Result{}, err
	}
	if input.ID == "" && input.ExpectedRevision != 0 {
		return store.Result{}, domain.Fail(domain.InvalidArgument, "A new entity has no expected revision.", "Use revision zero when creating configuration.")
	}
	if repository, ok := value.(*domain.Repository); ok {
		return saveRepository(ctx, s, input, *repository)
	}
	return s.Mutate(ctx, input.RequestID, "configuration.save", input, func(tx *store.Tx) (any, error) {
		id := input.ID
		if id == "" {
			id = domain.NewID()
		}
		if input.ExpectedRevision > 0 && (input.Kind == domain.ProjectKind || input.Kind == domain.SettingsKind || input.Kind == domain.AgentKind) {
			previous, err := tx.Get(input.Kind, id)
			if err != nil {
				return nil, err
			}
			if rpc.ResourceSchemaVersion(input.Kind, previous.Data) > rpc.ResourceSchemaVersion(input.Kind, input.Document) {
				return nil, domain.Fail(domain.Unsupported, "Project behavior settings require a current client.", "Preserve the current schema and all behavior settings when editing.")
			}
		}
		if project, ok := value.(*domain.Project); ok {
			if project.Settings == nil {
				project.Settings = &domain.ProjectBehavior{AutomaticFetch: domain.InheritBoolean, AutomaticPlanApproval: domain.InheritBoolean}
			}
			if project.Settings.PlanModeDefault == "" {
				project.Settings.PlanModeDefault = domain.InheritBoolean
			}
		}
		if settings, ok := value.(*domain.Settings); ok && settings.BranchPrefix == nil {
			prefix := domain.DefaultBranchPrefix
			settings.BranchPrefix = &prefix
		}
		if provider, ok := value.(*domain.Provider); ok {
			if err := preserveProviderActivation(tx, input, id, provider); err != nil {
				return nil, err
			}
		}
		if err := validateNewProviderSelections(tx, input, id, value); err != nil {
			return nil, err
		}
		if err := validateRelationships(tx, input.Kind, id, input.ExpectedRevision, value); err != nil {
			return nil, err
		}
		if agent, ok := value.(*domain.Agent); ok && agent.HarnessSettings == nil {
			agent.HarnessSettings = domain.NewInheritedAgentSettings(len(agent.SourceRoutes()))
		}
		return tx.Put(input.Kind, id, input.ExpectedRevision, "", "", value)
	})
}

func validateNewProviderSelections(tx *store.Tx, input ConfigurationMutation, id domain.ID, value validatable) error {
	previousIDs := func(kind domain.Kind) (map[domain.ID]bool, error) {
		ids := map[domain.ID]bool{}
		if input.ExpectedRevision == 0 {
			return ids, nil
		}
		record, err := tx.Get(input.Kind, id)
		if err != nil {
			return nil, err
		}
		switch value.(type) {
		case *domain.Agent:
			prior, err := store.Decode[domain.Agent](record)
			if err != nil {
				return nil, err
			}
			for _, ref := range prior.AllAccounts() {
				ids[ref.ID] = true
			}
		case *domain.Project:
			prior, err := store.Decode[domain.Project](record)
			if err != nil {
				return nil, err
			}
			if kind == domain.AgentKind {
				for _, ref := range prior.Agents.IDs {
					ids[ref] = true
				}
			}
			if kind == domain.AccountKind {
				for _, ref := range prior.Accounts.IDs {
					ids[ref] = true
				}
			}
		case *domain.Account:
			prior, err := store.Decode[domain.Account](record)
			if err != nil {
				return nil, err
			}
			ids[prior.ProviderID] = true
		}
		return ids, nil
	}
	checkProvider := func(providerID domain.ID) error {
		record, err := tx.Get(domain.ProviderKind, providerID)
		if err != nil {
			return err
		}
		provider, err := store.Decode[domain.Provider](record)
		if err != nil {
			return err
		}
		if provider.Protocol != domain.NativeSubscription && !provider.EnabledValue() {
			return providerDisabled()
		}
		return nil
	}
	checkAccount := func(accountID domain.ID) error {
		record, err := tx.Get(domain.AccountKind, accountID)
		if err != nil {
			return err
		}
		account, err := store.Decode[domain.Account](record)
		if err != nil {
			return err
		}
		if account.Type == domain.SubscriptionAccount {
			return nil
		}
		return checkProvider(account.ProviderID)
	}
	switch selected := value.(type) {
	case *domain.Model:
		if selected.SourceKind == domain.SubscriptionModel {
			return nil
		}
		if input.ExpectedRevision == 0 {
			return checkProvider(selected.ProviderID)
		}
	case *domain.Account:
		if selected.Type == domain.SubscriptionAccount {
			return nil
		}
		if input.ExpectedRevision == 0 {
			return checkProvider(selected.ProviderID)
		}
		old, err := previousIDs(domain.ProviderKind)
		if err != nil {
			return err
		}
		if !old[selected.ProviderID] {
			return checkProvider(selected.ProviderID)
		}
	case *domain.Agent:
		previousModels := map[domain.ID]bool{}
		if input.ExpectedRevision > 0 {
			record, err := tx.Get(input.Kind, id)
			if err != nil {
				return err
			}
			prior, err := store.Decode[domain.Agent](record)
			if err != nil {
				return err
			}
			for _, modelID := range prior.ModelIDs() {
				previousModels[modelID] = true
			}
		}
		for _, modelID := range selected.ModelIDs() {
			if previousModels[modelID] {
				continue
			}
			record, err := tx.Get(domain.ModelKind, modelID)
			if err != nil {
				return err
			}
			model, err := store.Decode[domain.Model](record)
			if err != nil {
				return err
			}
			if model.SourceKind != domain.SubscriptionModel {
				if err := checkProvider(model.ProviderID); err != nil {
					return err
				}
			}
		}
		old, err := previousIDs(domain.AccountKind)
		if err != nil {
			return err
		}
		for _, ref := range selected.AllAccounts() {
			if !old[ref.ID] {
				if err := checkAccount(ref.ID); err != nil {
					return err
				}
			}
		}
	case *domain.Project:
		oldAgents, err := previousIDs(domain.AgentKind)
		if err != nil {
			return err
		}
		for _, agentID := range selected.Agents.IDs {
			if oldAgents[agentID] {
				continue
			}
			record, err := tx.Get(domain.AgentKind, agentID)
			if err != nil {
				return err
			}
			agent, err := store.Decode[domain.Agent](record)
			if err != nil {
				return err
			}
			for _, modelID := range agent.ModelIDs() {
				modelRecord, err := tx.Get(domain.ModelKind, modelID)
				if err != nil {
					return err
				}
				model, err := store.Decode[domain.Model](modelRecord)
				if err != nil {
					return err
				}
				if model.SourceKind != domain.SubscriptionModel {
					if err := checkProvider(model.ProviderID); err != nil {
						return err
					}
				}
			}
		}
		oldAccounts, err := previousIDs(domain.AccountKind)
		if err != nil {
			return err
		}
		for _, accountID := range selected.Accounts.IDs {
			if !oldAccounts[accountID] {
				if err := checkAccount(accountID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func preserveProviderActivation(tx *store.Tx, input ConfigurationMutation, id domain.ID, provider *domain.Provider) error {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(input.Document, &fields); err != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid provider configuration.", "Use one complete provider document.")
	}
	var previous *domain.Provider
	if input.ExpectedRevision > 0 {
		record, err := tx.Get(domain.ProviderKind, id)
		if err != nil {
			return err
		}
		value, err := store.Decode[domain.Provider](record)
		if err != nil {
			return err
		}
		previous = &value
	}
	if raw, present := fields["enabled"]; present {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return domain.Fail(domain.InvalidArgument, "Provider availability must be a boolean.", "Set enabled to true or false.")
		}
		var enabled bool
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return domain.Fail(domain.InvalidArgument, "Provider availability must be a boolean.", "Set enabled to true or false.")
		}
		provider.SetEnabled(enabled)
	} else if previous != nil {
		provider.Enabled = previous.Enabled
	} else {
		provider.SetEnabled(true)
	}
	if raw, present := fields["preset_id"]; present {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return domain.Fail(domain.InvalidArgument, "Managed preset identity cannot be cleared.", "Create a custom copy as a new provider instead.")
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return domain.Fail(domain.InvalidArgument, "Invalid managed preset identity.", "Use a supported preset identifier.")
		}
		preset := domain.ProviderPresetID(value)
		provider.PresetID = &preset
	} else if previous != nil {
		provider.PresetID = previous.PresetID
	}
	return nil
}

func providerPresetDefaults(id domain.ProviderPresetID) (domain.Provider, bool) {
	for _, preset := range providers.Presets() {
		if preset.ID == id {
			return preset.Provider, true
		}
	}
	return domain.Provider{}, false
}

func sameProviderPresetDefaults(provider, canonical domain.Provider) bool {
	return provider.Name == canonical.Name && provider.Endpoint == canonical.Endpoint && provider.Protocol == canonical.Protocol && provider.Authentication == canonical.Authentication && provider.Discovery == canonical.Discovery && (provider.APIFormats == nil || slices.Equal(provider.APIFormats, canonical.APIFormats))
}

func accountAPIProvider(tx configurationView, account domain.Account) (domain.Provider, error) {
	record, err := tx.Get(domain.ProviderKind, account.ProviderID)
	if err != nil {
		return domain.Provider{}, err
	}
	provider, err := store.Decode[domain.Provider](record)
	if err != nil {
		return provider, err
	}
	return providers.ResolveAccountProfile(provider, account)
}

func providerDisabled() *domain.Error {
	return domain.Fail(domain.ProviderDisabled, "The selected API provider is off.", "Enable this provider or select an active API provider before starting another turn.")
}
func mustExist(tx configurationView, kind domain.Kind, ids ...domain.ID) error {
	for _, id := range ids {
		if _, err := tx.Get(kind, id); err != nil {
			return err
		}
	}
	return nil
}

type configurationView interface {
	Get(domain.Kind, domain.ID) (store.Record, error)
	List(store.Filter) ([]store.Record, error)
	ValidateModelIdentity(domain.ID, domain.Model) error
	ProviderPresetExists(domain.ProviderPresetID, domain.ID) (bool, error)
}

func all(tx configurationView, kind domain.Kind) ([]store.Record, error) {
	out := []store.Record{}
	f := store.Filter{Kind: kind, Limit: store.MaxPage}
	for {
		page, err := tx.List(f)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(out) > 10000 {
			return nil, domain.Fail(domain.ResourceExhausted, "Configuration relationship validation exceeded its bound.", "Reduce the affected configuration scope before retrying.")
		}
		if len(page) < f.Limit {
			return out, nil
		}
		f.After = page[len(page)-1].ID
	}
}
func validateRelationships(tx configurationView, kind domain.Kind, id domain.ID, expected uint64, value validatable) error {
	switch v := value.(type) {
	case *domain.Project:
		if err := validateHarnessDefaultReferences(tx, v.HarnessDefaults); err != nil {
			return err
		}
		if v.Settings != nil && v.Settings.Remediation != nil {
			if err := validateRemediationRelationships(tx, *v.Settings.Remediation); err != nil {
				return err
			}
		}
		if err := mustExist(tx, domain.RepositoryKind, v.Repositories...); err != nil {
			return err
		}
		if err := mustExist(tx, domain.AgentKind, v.Agents.IDs...); err != nil {
			return err
		}
		return mustExist(tx, domain.AccountKind, v.Accounts.IDs...)
	case *domain.Repository:
		for _, c := range v.Checkouts {
			if err := mustExist(tx, domain.MachineKind, c.MachineID); err != nil {
				return err
			}
		}
		if v.IntegrationID != "" {
			if err := mustExist(tx, domain.IntegrationKind, v.IntegrationID); err != nil {
				return err
			}
		}
		if v.Remediation != nil {
			return validateRemediationRelationships(tx, *v.Remediation)
		}
		return nil
	case *domain.Agent:

		if expected > 0 {
			row, e := tx.Get(domain.AgentKind, id)
			if e != nil {
				return e
			}
			prior, e := store.Decode[domain.Agent](row)
			if e != nil {
				return e
			}
			if v.MCPSelections == nil {
				v.MCPSelections = prior.MCPSelections
			}
		}
		if v.MCPSelections != nil && !v.MCPSelections.RebindingRequired {
			for _, selection := range v.MCPSelections.Selections {
				owner, ok := tx.(interface {
					MCPMetadata(domain.ID, domain.ID) (store.MCPMetadata, bool, error)
				})
				if !ok {
					return domain.Fail(domain.Unsupported, "MCP references require explicit Worker rebinding.", "Select the original Worker catalog before importing Agent selections.")
				}
				meta, exists, e := owner.MCPMetadata(selection.MachineID, selection.ServerID)
				if e != nil {
					return e
				}
				if !exists || meta.Deleted || !meta.Enabled || !meta.AuthenticationReady || meta.PendingID != "" || meta.DeviceID != selection.DeviceID || meta.Revision != selection.Revision {
					return domain.Fail(domain.Conflict, "The selected MCP definition changed or is unavailable.", "Refresh its original Runner catalog and select its current revision.")
				}
			}
		}
		if expected > 0 && len(v.Routes) == 0 {
			previous, err := tx.Get(domain.AgentKind, id)
			if err != nil {
				return err
			}
			prior, err := store.Decode[domain.Agent](previous)
			if err != nil {
				return err
			}
			if len(prior.Routes) > 0 {
				return domain.Fail(domain.Unsupported, "Ordered account sources require a current client.", "Keep schema 3 when editing this Worker, including when one source remains.")
			}
		}

		if v.ReconfigurationRequired {
			return domain.AgentReconfigurationRequired()
		}
		sources := map[string]bool{}
		for index, route := range v.SourceRoutes() {
			record, err := tx.Get(domain.ModelKind, route.ModelID)
			if err != nil {
				return err
			}
			model, err := store.Decode[domain.Model](record)
			if err != nil {
				return err
			}
			if v.HarnessSettings != nil {
				if selected := v.HarnessSettings.Models[index].Value; selected != nil {
					rr, err := tx.Get(domain.ModelKind, *selected)
					if err != nil {
						return err
					}
					var override domain.Model
					if err := domain.Decode(rr.Data, &override); err != nil {
						return err
					}
					if override.ProviderID != model.ProviderID || override.SubscriptionService != model.SubscriptionService || override.SourceKind != model.SourceKind || !slices.Contains(override.Harnesses, v.Harness) {
						return domain.Fail(domain.InvalidArgument, "Agent model override uses another source.", "Preserve the original source/account identity.")
					}
				}
			}
			key := "api:" + string(model.ProviderID)
			if model.SourceKind == domain.SubscriptionModel {
				key = "subscription:" + string(model.SubscriptionService)
			}
			if sources[key] {
				return domain.Fail(domain.InvalidArgument, "Duplicate account source.", "Configure each source once.")
			}
			sources[key] = true
			if !slices.Contains(model.Harnesses, v.Harness) {
				return domain.Fail(domain.Unsupported, "The model does not support the selected harness.", "Select an explicitly compatible model.")
			}
			for _, link := range route.Accounts {
				record, err := tx.Get(domain.AccountKind, link.ID)
				if err != nil {
					return err
				}
				account, err := store.Decode[domain.Account](record)
				if err != nil {
					return err
				}
				if !model.MatchesAccount(account, v.Harness) {
					return domain.Fail(domain.InvalidArgument, "An account is incompatible with the Agent Worker model.", "Choose accounts from the configured model provider.")
				}
				if account.Type == domain.APIAccount {
					provider, err := accountAPIProvider(tx, account)
					if err != nil {
						return err
					}
					// Legacy documents remain writable/importable with their original
					// configuration semantics; execution still enforces the resolved
					// protocol. Explicit selections reject incompatible Worker saves.
					if account.APIProtocol != "" && !providers.HarnessMatches(v.Harness, provider.Protocol) {
						return domain.Fail(domain.Unsupported, "The account API format does not match the Agent Worker.", "Choose Responses for Codex, Messages for Claude Code or Chat Completions for OpenCode and Grok.")
					}
				}
			}
		}
		return mustExist(tx, domain.TemplateKind, v.Templates...)
	case *domain.Provider:
		if v.Protocol == domain.NativeSubscription {
			return domain.Fail(domain.InvalidArgument, "Subscription providers are retired.", "Create a subscription service account and native model instead.")
		}
		if v.PresetID != nil {
			canonical, ok := providerPresetDefaults(*v.PresetID)
			if !ok || !sameProviderPresetDefaults(*v, canonical) {
				return domain.Fail(domain.InvalidArgument, "Managed preset settings are server-owned.", "Use the preset's fixed provider defaults or create a custom copy.")
			}
		}
		if expected > 0 {
			old, err := tx.Get(kind, id)
			if err != nil {
				return err
			}
			previous, err := store.Decode[domain.Provider](old)
			if err != nil {
				return err
			}
			if len(providers.WithAPIFormats(previous).APIFormats) != 0 && len(v.APIFormats) == 0 {
				return domain.Fail(domain.Unsupported, "API format profiles require a current client.", "Keep schema 3 and all declared API format profiles when editing this provider.")
			}
			if previous.PresetID != nil && (v.PresetID == nil || *v.PresetID != *previous.PresetID) || previous.PresetID == nil && v.PresetID != nil {
				return domain.Fail(domain.Conflict, "Provider preset identity is immutable.", "Keep the managed provider identity or create a new custom copy.")
			}
			harnessChanged := (previous.SubscriptionHarness == nil) != (v.SubscriptionHarness == nil)
			if previous.SubscriptionHarness != nil && v.SubscriptionHarness != nil {
				harnessChanged = *previous.SubscriptionHarness != *v.SubscriptionHarness
			}
			if previous.LegacyAPIFormat() != v.LegacyAPIFormat() || !slices.Equal(providers.APIFormats(previous), providers.APIFormats(*v)) || harnessChanged {
				accounts, err := all(tx, domain.AccountKind)
				if err != nil {
					return err
				}
				for _, record := range accounts {
					account, err := store.Decode[domain.Account](record)
					if err != nil {
						return err
					}
					if account.ProviderID == id {
						for _, account := range accountProfileReferences(account) {
							before, beforeErr := providers.ResolveAccountProfile(previous, account)
							after, afterErr := providers.ResolveAccountProfile(*v, account)
							removedDeclaredProfile := slices.Contains(providers.APIFormats(previous), before.LegacyAPIFormat()) && !slices.Contains(providers.APIFormats(*v), before.LegacyAPIFormat())
							if beforeErr != nil || afterErr != nil || before.LegacyAPIFormat() != after.LegacyAPIFormat() || removedDeclaredProfile || harnessChanged {
								return domain.Fail(domain.Conflict, "An account-referenced API profile cannot be replaced or removed.", "Keep its original URL and authentication; create a new provider or remove all references first.")
							}
						}
					}
				}
			}
		}
		if v.PresetID != nil {
			found, err := tx.ProviderPresetExists(*v.PresetID, id)
			if err != nil {
				return err
			}
			if found {
				return domain.Fail(domain.Conflict, "This API provider preset is already saved.", "Refresh the provider inventory and change the existing preset.")
			}
		}
	case *domain.Model:
		if expected == 0 {
			if v.MetadataSource == domain.Known {
				return domain.Fail(domain.InvalidArgument, "Provider-observed model metadata is server-owned.", "Use user-declared for manual advisory metadata or unknown when unavailable.")
			}
			if v.Discovery != nil || v.New {
				return domain.Fail(domain.InvalidArgument, "Model discovery provenance is server-owned.", "Register a manual model without discovery fields.")
			}
			v.Manual = true
		}
		if v.SourceKind != domain.SubscriptionModel {
			if err := mustExist(tx, domain.ProviderKind, v.ProviderID); err != nil {
				return err
			}
		}
		if err := tx.ValidateModelIdentity(id, *v); err != nil {
			return err
		}
		if expected > 0 {
			record, err := tx.Get(kind, id)
			if err != nil {
				return err
			}
			old, err := store.Decode[domain.Model](record)
			if err != nil {
				return err
			}
			oldDiscovery, _ := json.Marshal(old.Discovery)
			newDiscovery, _ := json.Marshal(v.Discovery)
			if string(oldDiscovery) != string(newDiscovery) || old.Manual != v.Manual || (!old.New && v.New) {
				return domain.Fail(domain.InvalidArgument, "Model registration and discovery provenance are server-owned.", "Preserve provenance; NEW may only be acknowledged by clearing it.")
			}
			if !old.SameIdentity(*v) {
				return domain.Fail(domain.Conflict, "Canonical model identity is immutable.", "Register a new model instead of relabeling historical usage.")
			}
			if v.MetadataSource == domain.Known && (old.MetadataSource != domain.Known || modelAdvisoryBytes(old) != modelAdvisoryBytes(*v)) {
				return domain.Fail(domain.InvalidArgument, "Provider-observed model metadata cannot be forged through configuration.", "Use user-declared when changing advisory metadata.")
			}
		}
	case *domain.Account:
		if v.Type == domain.APIAccount {
			if _, err := accountAPIProvider(tx, *v); err != nil {
				return err
			}
		}
		if expected == 0 {
			if v.Health != domain.AccountDisconnected || len(v.Quota) > 0 || v.ConfirmedExhausted || v.Connection != nil || v.Removal != nil || v.Validation != nil || v.Catalog != nil || v.Subscription != nil || len(v.RetainedConnections) != 0 {
				return domain.Fail(domain.InvalidArgument, "New account health must be disconnected.", "Use account connect/login to validate credentials and quota.")
			}
		} else {
			previous, err := tx.Get(kind, id)
			if err != nil {
				return err
			}
			old, err := store.Decode[domain.Account](previous)
			if err != nil {
				return err
			}
			if old.APIProtocol != v.APIProtocol {
				if !v.APIProtocol.API() {
					return domain.Fail(domain.Unsupported, "The account API format cannot be cleared.", "Keep its explicit API format with schema 3.")
				}
				if old.Connection != nil || old.Removal != nil || old.Health != domain.AccountDisconnected {
					return domain.Fail(domain.Conflict, "The account API format is still in use.", "Disconnect and finish credential cleanup before changing its format.")
				}
				before, e := accountAPIProvider(tx, old)
				if e != nil {
					return e
				}
				after, e := accountAPIProvider(tx, *v)
				if e != nil {
					return e
				}
				if (before.Authentication == domain.KeylessAuth) != (after.Authentication == domain.KeylessAuth) {
					return domain.Fail(domain.Conflict, "An account cannot change whether it owns credentials.", "Create a new account for a keyless or key-required profile.")
				}
			}
			if !bytes.Equal(connectionGenerationBytes(old), connectionGenerationBytes(*v)) {
				return domain.Fail(domain.InvalidArgument, "Account connection generations are server-owned.", "Preserve the original connection generations.")
			}
			oldObservations, _ := json.Marshal(struct {
				Health       domain.AccountHealth
				Quota        []domain.QuotaWindow
				Exhausted    bool
				Connection   *domain.AccountConnection
				Removal      *domain.AccountRemoval
				Validation   *domain.AccountValidation
				Catalog      *domain.CatalogObservation
				Subscription *domain.SubscriptionState
			}{old.Health, old.Quota, old.ConfirmedExhausted, old.Connection, old.Removal, old.Validation, old.Catalog, old.Subscription})
			newObservations, _ := json.Marshal(struct {
				Health       domain.AccountHealth
				Quota        []domain.QuotaWindow
				Exhausted    bool
				Connection   *domain.AccountConnection
				Removal      *domain.AccountRemoval
				Validation   *domain.AccountValidation
				Catalog      *domain.CatalogObservation
				Subscription *domain.SubscriptionState
			}{v.Health, v.Quota, v.ConfirmedExhausted, v.Connection, v.Removal, v.Validation, v.Catalog, v.Subscription})
			if old.ProviderID != v.ProviderID || old.Type != v.Type || old.SubscriptionService != v.SubscriptionService || string(oldObservations) != string(newObservations) {
				return domain.Fail(domain.InvalidArgument, "Account identity and observed health are server-owned.", "Use login/connect/refresh to update authentication or quota.")
			}
		}
	case *domain.Settings:
		if err := validateHarnessDefaultReferences(tx, v.HarnessDefaults); err != nil {
			return err
		}
		records, err := tx.List(store.Filter{Kind: domain.SettingsKind, Limit: 2})
		if err != nil {
			return err
		}
		if len(records) > 0 && records[0].ID != id {
			return domain.Fail(domain.Conflict, "The server already has settings.", "Edit the existing settings ID and revision.")
		}
		return validateRemediationRelationships(tx, v.Remediation)
	}
	return nil
}

func validateRemediationRelationships(tx configurationView, policy domain.RemediationPolicy) error {
	if policy.AgentID != "" {
		if err := mustExist(tx, domain.AgentKind, policy.AgentID); err != nil {
			return err
		}
	}
	if policy.MachineID != "" {
		return mustExist(tx, domain.MachineKind, policy.MachineID)
	}
	return nil
}

func PreviewRouting(ctx context.Context, s *store.Store, agentID, projectID domain.ID) (domain.Route, error) {
	// Read-only consistency must not consume a mutation receipt or change routing
	// cursors. The store read transaction gives all candidates one coherent view.
	var route domain.Route
	err := s.Read(ctx, func(tx *store.Tx) error {
		record, err := tx.Get(domain.AgentKind, agentID)
		if err != nil {
			return err
		}
		agent, err := store.Decode[domain.Agent](record)
		if err != nil {
			return err
		}
		var project *domain.Project
		if projectID != "" {
			record, err := tx.Get(domain.ProjectKind, projectID)
			if err != nil {
				return err
			}
			p, err := store.Decode[domain.Project](record)
			if err != nil {
				return err
			}
			project = &p
		}
		policy, err := tx.DefaultRoutingPolicy()
		if err != nil {
			return err
		}
		preview, err := tx.PreviewSourceRouting(agentID, agent, project, policy)
		route = preview.Route
		// Preview is useful precisely when execution is blocked. Candidate reasons
		// remain readable; actual dispatch still returns the actionable typed error.
		if err != nil && (domain.SafeError(err).Code == domain.MissingInput || len(route.Sources) > 0) {
			return nil
		}
		return err
	})
	return route, err
}
func routingState(tx *store.Tx, agentID domain.ID) (domain.RoutingState, error) {
	_, state, err := tx.Routing(agentID)
	return state, err
}

func connectionGenerationBytes(account domain.Account) []byte {
	raw, _ := json.Marshal(account.RetainedConnections)
	return raw
}
