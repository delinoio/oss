package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
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
	var value validatable
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
		value = &domain.Settings{}
	default:
		return nil, domain.Fail(domain.InvalidArgument, "This entity is not editable configuration.", "Use the entity's dedicated product operation.")
	}
	if err := domain.Decode(raw, value); err != nil {
		return nil, err
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
			for _, ref := range prior.Accounts {
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
		modelChanged := input.ExpectedRevision == 0
		if !modelChanged {
			record, err := tx.Get(input.Kind, id)
			if err != nil {
				return err
			}
			prior, err := store.Decode[domain.Agent](record)
			if err != nil {
				return err
			}
			modelChanged = prior.ModelID != selected.ModelID
		}
		if modelChanged {
			record, err := tx.Get(domain.ModelKind, selected.ModelID)
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
		for _, ref := range selected.Accounts {
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
			modelRecord, err := tx.Get(domain.ModelKind, agent.ModelID)
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
	return provider.Name == canonical.Name && provider.Endpoint == canonical.Endpoint && provider.Protocol == canonical.Protocol && provider.Authentication == canonical.Authentication && provider.Discovery == canonical.Discovery
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
		if v.ReconfigurationRequired {
			return domain.SubscriptionReconfigurationRequired()
		}
		record, err := tx.Get(domain.ModelKind, v.ModelID)
		if err != nil {
			return err
		}
		model, err := store.Decode[domain.Model](record)
		if err != nil {
			return err
		}
		if !slices.Contains(model.Harnesses, v.Harness) {
			return domain.Fail(domain.Unsupported, "The model does not support the selected harness.", "Select an explicitly compatible model.")
		}
		for _, link := range v.Accounts {
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
			if previous.PresetID != nil && (v.PresetID == nil || *v.PresetID != *previous.PresetID) || previous.PresetID == nil && v.PresetID != nil {
				return domain.Fail(domain.Conflict, "Provider preset identity is immutable.", "Keep the managed provider identity or create a new custom copy.")
			}
			harnessChanged := (previous.SubscriptionHarness == nil) != (v.SubscriptionHarness == nil)
			if previous.SubscriptionHarness != nil && v.SubscriptionHarness != nil {
				harnessChanged = *previous.SubscriptionHarness != *v.SubscriptionHarness
			}
			if previous.Endpoint != v.Endpoint || previous.Protocol != v.Protocol || previous.Authentication != v.Authentication || harnessChanged {
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
						return domain.Fail(domain.Conflict, "A connected provider's authority cannot be replaced through configuration.", "Disconnect and remove its account references before changing authority.")
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
			record, err := tx.Get(domain.ProviderKind, v.ProviderID)
			if err != nil {
				return err
			}
			provider, err := store.Decode[domain.Provider](record)
			if err != nil {
				return err
			}
			if provider.Protocol == domain.NativeSubscription {
				return domain.Fail(domain.InvalidArgument, "API accounts require an API provider.", "Select an API endpoint or create an independent subscription account.")
			}
		}
		if expected == 0 {
			if v.Health != domain.AccountDisconnected || len(v.Quota) > 0 || v.ConfirmedExhausted || v.Connection != nil || v.Removal != nil || v.Validation != nil || v.Catalog != nil || v.Subscription != nil {
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
		record, err = tx.Get(domain.ModelKind, agent.ModelID)
		if err != nil {
			return err
		}
		model, err := store.Decode[domain.Model](record)
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
		accounts := map[domain.ID]domain.Account{}
		for _, link := range agent.Accounts {
			record, err := tx.Get(domain.AccountKind, link.ID)
			if err != nil {
				if domain.SafeError(err).Code == domain.NotFound {
					continue
				}
				return err
			}
			a, err := store.Decode[domain.Account](record)
			if err != nil {
				return err
			}
			accounts[link.ID] = a
		}
		settings := domain.DefaultSettings()
		records, err := tx.List(store.Filter{Kind: domain.SettingsKind, Limit: 1})
		if err != nil {
			return err
		}
		if len(records) > 0 {
			settings, err = store.Decode[domain.Settings](records[0])
			if err != nil {
				return err
			}
		}
		state, err := routingState(tx, agentID)
		if err != nil {
			return err
		}
		route, _, err = domain.RouteAccount(agentID, agent, model, project, accounts, settings.DefaultRouting, state, time.Now().UTC())
		// Preview is useful precisely when execution is blocked. Candidate reasons
		// remain readable; actual dispatch still returns the actionable typed error.
		if err != nil && domain.SafeError(err).Code == domain.MissingInput {
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
