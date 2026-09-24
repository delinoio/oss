package server

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
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

func configurationValue(kind domain.Kind, raw []byte) (validatable, error) {
	var value validatable
	switch kind {
	case domain.ProjectKind:
		value = &domain.Project{}
	case domain.RepositoryKind:
		value = &domain.Repository{}
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
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return value, nil
}
func SaveConfiguration(ctx context.Context, s *store.Store, input ConfigurationMutation) (store.Result, error) {
	value, err := configurationValue(input.Kind, input.Document)
	if err != nil {
		return store.Result{}, err
	}
	if input.ID == "" && input.ExpectedRevision != 0 {
		return store.Result{}, domain.Fail(domain.InvalidArgument, "A new entity has no expected revision.", "Use revision zero when creating configuration.")
	}
	return s.Mutate(ctx, input.RequestID, "configuration.save", input, func(tx *store.Tx) (any, error) {
		id := input.ID
		if id == "" {
			id = domain.NewID()
		}
		if err := validateRelationships(tx, input.Kind, id, input.ExpectedRevision, value); err != nil {
			return nil, err
		}
		return tx.Put(input.Kind, id, input.ExpectedRevision, "", "", value)
	})
}
func mustExist(tx *store.Tx, kind domain.Kind, ids ...domain.ID) error {
	for _, id := range ids {
		if _, err := tx.Get(kind, id); err != nil {
			return err
		}
	}
	return nil
}
func all(tx *store.Tx, kind domain.Kind) ([]store.Record, error) {
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
func validateRelationships(tx *store.Tx, kind domain.Kind, id domain.ID, expected uint64, value validatable) error {
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
		// Repository inspection/revalidation is Worker-owned. Unverified checkouts
		// cannot be saved through a configuration write without an inspection result.
		return domain.Fail(domain.MissingInput, "Repository configuration requires Worker inspection.", "Inspect the repository on its execution Worker before saving it.")
	case *domain.Agent:
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
			if account.ProviderID != model.ProviderID {
				return domain.Fail(domain.InvalidArgument, "An account is incompatible with the Agent Worker model.", "Choose accounts from the configured model provider.")
			}
		}
		return mustExist(tx, domain.TemplateKind, v.Templates...)
	case *domain.Provider:
		if expected > 0 {
			old, err := tx.Get(kind, id)
			if err != nil {
				return err
			}
			previous, err := store.Decode[domain.Provider](old)
			if err != nil {
				return err
			}
			if previous.Endpoint != v.Endpoint || previous.Protocol != v.Protocol || previous.Authentication != v.Authentication {
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
	case *domain.Model:
		if err := mustExist(tx, domain.ProviderKind, v.ProviderID); err != nil {
			return err
		}
		models, err := all(tx, domain.ModelKind)
		if err != nil {
			return err
		}
		for _, record := range models {
			if record.ID == id {
				continue
			}
			m, err := store.Decode[domain.Model](record)
			if err != nil {
				return err
			}
			if (v.Alias != "" && (m.Alias == v.Alias || string(record.ID) == v.Alias)) || (m.ProviderID == v.ProviderID && m.NativeID == v.NativeID) {
				return domain.Fail(domain.Conflict, "The model identity or CLI alias already exists.", "Choose a unique alias and canonical provider/model identity.")
			}
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
			if old.ProviderID != v.ProviderID || old.NativeID != v.NativeID {
				return domain.Fail(domain.Conflict, "Canonical model identity is immutable.", "Register a new model instead of relabeling historical usage.")
			}
		}
	case *domain.Account:
		record, err := tx.Get(domain.ProviderKind, v.ProviderID)
		if err != nil {
			return err
		}
		provider, err := store.Decode[domain.Provider](record)
		if err != nil {
			return err
		}
		if (v.Type == domain.SubscriptionAccount) != (provider.Protocol == domain.NativeSubscription) {
			return domain.Fail(domain.InvalidArgument, "Account type does not match the provider.", "Use the provider's authentication type.")
		}
		if expected == 0 {
			if v.Health != domain.AccountDisconnected || len(v.Quota) > 0 || v.ConfirmedExhausted {
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
				Health    domain.AccountHealth
				Quota     []domain.QuotaWindow
				Exhausted bool
			}{old.Health, old.Quota, old.ConfirmedExhausted})
			newObservations, _ := json.Marshal(struct {
				Health    domain.AccountHealth
				Quota     []domain.QuotaWindow
				Exhausted bool
			}{v.Health, v.Quota, v.ConfirmedExhausted})
			if old.ProviderID != v.ProviderID || old.Type != v.Type || string(oldObservations) != string(newObservations) {
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
		if v.Remediation.AgentID != "" {
			if err := mustExist(tx, domain.AgentKind, v.Remediation.AgentID); err != nil {
				return err
			}
		}
		if v.Remediation.MachineID != "" {
			if err := mustExist(tx, domain.MachineKind, v.Remediation.MachineID); err != nil {
				return err
			}
		}
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
	// Routing records have independent UUID-v7 IDs; their owning Agent Worker ID
	// is an indexed project-independent association in the data document.
	records, err := all(tx, domain.RoutingKind)
	if err != nil {
		return domain.RoutingState{}, err
	}
	for _, record := range records {
		var v struct {
			AgentID domain.ID           `json:"agent_id"`
			State   domain.RoutingState `json:"state"`
		}
		if err := domain.Decode(record.Data, &v); err != nil {
			return v.State, err
		}
		if v.AgentID == agentID {
			return v.State, nil
		}
	}
	return domain.RoutingState{}, nil
}
