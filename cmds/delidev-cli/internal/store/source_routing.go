// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"time"
)

type SourceRoutingPreview struct {
	Route         domain.Route
	Agent         domain.Agent
	Model         domain.Model
	ModelRevision uint64
	Accounts      map[domain.ID]domain.Account
	routingRecord Record
	nextRouting   domain.RoutingState
}

// PreviewSourceRouting is shared by read-only preview and atomic first dispatch.
// It reads metadata only; it cannot validate credentials or refresh native quota.
func (t *Tx) PreviewSourceRouting(agentID domain.ID, agent domain.Agent, project *domain.Project, defaultPolicy domain.RoutingPolicy) (SourceRoutingPreview, error) {
	var result SourceRoutingPreview
	if err := agent.Validate(); err != nil {
		return result, err
	}
	if agent.ReconfigurationRequired {
		return result, domain.SubscriptionReconfigurationRequired()
	}
	record, state, err := t.Routing(agentID)
	if err != nil {
		return result, err
	}
	sources := make([]domain.SourceRouteInput, 0, len(agent.SourceRoutes()))
	for _, route := range agent.SourceRoutes() {
		mr, model, err := decodeEntity[domain.Model](t, domain.ModelKind, route.ModelID)
		if err != nil {
			return result, err
		}
		if err := model.Validate(); err != nil {
			return result, err
		}
		source := domain.SourceRouteInput{Model: model, ModelRevision: mr.Revision, Accounts: map[domain.ID]domain.Account{}, Blocked: map[domain.ID]domain.Eligibility{}}
		var provider domain.Provider
		if model.SourceKind == domain.SubscriptionModel {
			source.Source = "subscription:" + string(model.SubscriptionService)
			if len(agent.Routes) > 0 && agent.Harness != domain.Codex {
				source.Problem = domain.Fail(domain.Unsupported, "This subscription execution profile is unsupported.", "Choose a supported account source and harness.")
			}
		} else {
			source.Source = "api:" + string(model.ProviderID)
			_, provider, err = decodeEntity[domain.Provider](t, domain.ProviderKind, model.ProviderID)
			if err != nil {
				return result, err
			}
			if err := provider.Validate(); err != nil {
				return result, err
			}
			if !provider.EnabledValue() {
				source.Problem = domain.Fail(domain.ProviderDisabled, "The selected API provider is off.", "Enable this provider before starting another turn.")
			}
		}
		for _, link := range route.Accounts {
			_, account, err := decodeEntity[domain.Account](t, domain.AccountKind, link.ID)
			if err != nil {
				if domain.SafeError(err).Code == domain.NotFound {
					continue
				}
				return result, err
			}
			source.Accounts[link.ID] = account
			selected := provider
			if account.Type == domain.APIAccount {
				selected, err = providers.ResolveAccountProfile(provider, account)
				if err != nil || !providers.HarnessMatches(agent.Harness, selected.Protocol) {
					source.Blocked[link.ID] = domain.IncompatibleAccount
					continue
				}
			}
			if len(agent.Routes) > 0 {
				blocked := false
				if account.Type == domain.SubscriptionAccount {
					managed := account.Subscription
					blocked = managed == nil || managed.Generation == "" || managed.RecoveryRequired || managed.Lease != nil || managed.Pending != nil && managed.Pending.Action != domain.SubscriptionRefresh || account.Connection == nil || account.Connection.Authentication != domain.SubscriptionAuth
				} else {
					validation, connection := account.Validation, account.Connection
					blocked = validation == nil || connection == nil
					if !blocked {
						blocked = validation.ConnectionID != connection.ID || validation.State != domain.Observed || validation.Problem != nil || validation.ObservedAt.IsZero() || validation.ObservedAt.Before(connection.ConnectedAt) || validation.ObservedAt.After(t.now.Add(time.Second)) || connection.Authentication != selected.Authentication ||
							validation.Authentication != domain.CredentialAccepted && validation.Authentication != domain.KeylessEndpoint || (selected.Authentication == domain.KeylessAuth) != (validation.Authentication == domain.KeylessEndpoint)
					}
				}
				if blocked {
					source.Blocked[link.ID] = domain.UnauthenticatedAccount
				}
			}
		}
		sources = append(sources, source)
	}
	route, next, err := domain.RouteSources(agentID, agent, project, sources, defaultPolicy, state, t.now)
	result.Route, result.routingRecord, result.nextRouting = route, record, next
	if err != nil {
		return result, err
	}
	index := 0
	if route.SourceIndex != nil {
		index = int(*route.SourceIndex)
	}
	result.Agent = agent.WithSource(agent.SourceRoutes()[index])
	result.Model, result.ModelRevision, result.Accounts = sources[index].Model, sources[index].ModelRevision, sources[index].Accounts
	if sources[index].Problem != nil {
		return result, sources[index].Problem
	}
	return result, nil
}

// DefaultRoutingPolicy rejects ambiguous singleton ownership consistently for
// read-only previews and all first-execution admission paths.
func (t *Tx) DefaultRoutingPolicy() (domain.RoutingPolicy, error) {
	records, err := t.List(Filter{Kind: domain.SettingsKind, Limit: 2})
	if err != nil {
		return "", err
	}
	if len(records) > 1 {
		return "", domain.Fail(domain.RecoveryRequired, "Global settings ownership is ambiguous.", "Reconcile duplicate settings before dispatch.")
	}
	settings := domain.DefaultSettings()
	if len(records) == 1 {
		settings, err = Decode[domain.Settings](records[0])
		if err != nil {
			return "", err
		}
	}
	if !settings.DefaultRouting.Valid() {
		return "", domain.Fail(domain.InvalidArgument, "Invalid server routing policy.", "Select one of the supported policies.")
	}
	return settings.DefaultRouting, nil
}
