// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
	"testing"
	"time"
)

func sourceFixture() (ID, Agent, []SourceRouteInput) {
	id, agent, apiModel, accounts := routeFixture()
	agent.Name = "Source Worker"
	apiModel.NativeID = "api-native"
	apiModel.Name = "API"
	agent.Options.Permission = PermissionWorkspaceWrite
	subscription := Model{SourceKind: SubscriptionModel, SubscriptionService: SubscriptionChatGPT, NativeID: "subscription-model", Name: "Subscription", Harnesses: []Harness{Codex}}
	subAccounts := map[ID]Account{}
	links := []WeightedAccount{}
	for range 2 {
		accountID := NewID()
		links = append(links, WeightedAccount{accountID, 1})
		subAccounts[accountID] = Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, Enabled: true, Health: AccountReady, Connection: &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: time.Now().UTC()}}
	}
	priority := Priority
	agent.Routes = []AgentSourceRoute{{Model: &InlineModel{ModelIdentity: ModelIdentity{SubscriptionService: SubscriptionChatGPT, NativeID: subscription.NativeID}, MetadataSource: Unknown}, Accounts: links, Routing: &priority}, {Model: &InlineModel{ModelIdentity: ModelIdentity{ProviderID: apiModel.ProviderID, NativeID: "api-native"}, MetadataSource: Unknown}, Accounts: agent.Accounts, Routing: &priority}}
	agent.ModelID, agent.Accounts, agent.Routing = "", nil, nil
	return id, agent, []SourceRouteInput{{Model: subscription, ModelRevision: 1, Source: "subscription:chatgpt", Accounts: subAccounts}, {Model: apiModel, ModelRevision: 1, Source: "api:" + string(apiModel.ProviderID), Accounts: accounts}}
}

func TestSourcePriorityRequiresCompleteConfirmedExhaustion(t *testing.T) {
	for _, test := range []struct {
		name     string
		change   func(*Agent, []SourceRouteInput)
		selected int
		blocked  bool
	}{
		{name: "subscription first", selected: 0},
		{name: "one exhausted", selected: 0, change: func(a *Agent, sources []SourceRouteInput) {
			id := a.Routes[0].Accounts[0].ID
			account := sources[0].Accounts[id]
			account.ConfirmedExhausted = true
			sources[0].Accounts[id] = account
		}},
		{name: "all exhausted", selected: 1, change: func(a *Agent, sources []SourceRouteInput) {
			for id, account := range sources[0].Accounts {
				account.ConfirmedExhausted = true
				sources[0].Accounts[id] = account
			}
		}},
		{name: "unknown quota", selected: 0},
		{name: "authentication", blocked: true, change: func(a *Agent, sources []SourceRouteInput) {
			for id, account := range sources[0].Accounts {
				account.Health = AccountFailed
				sources[0].Accounts[id] = account
			}
		}},
		{name: "disabled and exhausted", blocked: true, change: func(a *Agent, sources []SourceRouteInput) {
			for id, account := range sources[0].Accounts {
				account.Enabled = false
				account.ConfirmedExhausted = true
				sources[0].Accounts[id] = account
			}
		}},
		{name: "protocol blocker", blocked: true, change: func(a *Agent, sources []SourceRouteInput) {
			sources[0].Problem = Fail(Unsupported, "Protocol mismatch.", "Configure Responses.")
		}},
		{name: "temporary restriction", blocked: true, change: func(a *Agent, sources []SourceRouteInput) {
			for id, account := range sources[0].Accounts {
				account.Health = AccountFailed
				account.Quota = []QuotaWindow{{State: ObservationFailed}}
				sources[0].Accounts[id] = account
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, agent, sources := sourceFixture()
			if test.change != nil {
				test.change(&agent, sources)
			}
			state := RoutingState{}
			route, next, err := RouteSources(id, agent, nil, sources, Priority, state, time.Now().UTC())
			if test.blocked {
				if err == nil || route.Selected != "" || !reflect.DeepEqual(next, state) {
					t.Fatalf("block bypassed: %+v %+v %v", route, next, err)
				}
				return
			}
			if err != nil || route.SourceIndex == nil || int(*route.SourceIndex) != test.selected || len(route.Sources) != 2 {
				t.Fatalf("selection: %+v %v", route, err)
			}
		})
	}
}
func TestSourceRecoveryAndIndependentState(t *testing.T) {
	id, agent, sources := sourceFixture()
	rr := RoundRobin
	agent.Routes[0].Routing = &rr
	agent.Routes[1].Routing = &rr
	route, state, err := RouteSources(id, agent, nil, sources, Priority, RoutingState{}, time.Now().UTC())
	if err != nil || *route.SourceIndex != 0 {
		t.Fatal(err)
	}
	original := state.Sources[sources[0].Source]
	for id, account := range sources[0].Accounts {
		account.ConfirmedExhausted = true
		account.Quota = []QuotaWindow{{State: ObservationFailed, Blocking: true, ResetAt: timePointer(time.Now().Add(-time.Hour))}}
		sources[0].Accounts[id] = account
	}
	route, next, err := RouteSources(id, agent, nil, sources, Priority, state, time.Now().UTC())
	if err != nil || *route.SourceIndex != 1 || !reflect.DeepEqual(next.Sources[sources[0].Source], original) {
		t.Fatalf("skipped state changed: %+v %v", next, err)
	}
	// A past reset and failed observation retained exhaustion above. Only the
	// native observation owner can clear this flag following positive evidence.
	recovered := agent.Routes[0].Accounts[0].ID
	account := sources[0].Accounts[recovered]
	account.ConfirmedExhausted = false
	sources[0].Accounts[recovered] = account
	route, recoveredState, err := RouteSources(id, agent, nil, sources, Priority, next, time.Now().UTC())
	if err != nil || *route.SourceIndex != 0 || route.Selected != recovered || !reflect.DeepEqual(recoveredState.Sources[sources[1].Source], next.Sources[sources[1].Source]) {
		t.Fatalf("recovery: %+v %v", route, err)
	}
	project := Project{Agents: Restriction{Configured: true, IDs: []ID{id}}, Accounts: Restriction{Configured: true, IDs: agent.Routes[1].accountIDs()}}
	route, _, err = RouteSources(id, agent, &project, sources, Priority, next, time.Now().UTC())
	if err == nil || route.Selected != "" {
		t.Fatal("project restriction caused paid fallback")
	}
}
func timePointer(value time.Time) *time.Time { return &value }
func (r AgentSourceRoute) accountIDs() []ID {
	ids := []ID{}
	for _, link := range r.Accounts {
		ids = append(ids, link.ID)
	}
	return ids
}

func TestInheritedFixedPolicyCannotAdvanceInvalidSource(t *testing.T) {
	id, agent, sources := sourceFixture()
	agent.Routes[0].Routing = nil
	for id, account := range sources[0].Accounts {
		account.ConfirmedExhausted = true
		sources[0].Accounts[id] = account
	}
	route, _, err := RouteSources(id, agent, nil, sources, Fixed, RoutingState{}, time.Now().UTC())
	if err == nil || SafeError(err).Code != InvalidArgument || route.Selected != "" {
		t.Fatalf("invalid fixed source advanced: %+v %v", route, err)
	}
}

func TestLegacySourceProblemBlocksRouting(t *testing.T) {
	id, agent, sources := sourceFixture()
	legacy := agent.WithSource(agent.Routes[1])
	source := sources[1]
	source.Problem = Fail(ProviderDisabled, "The selected API provider is off.", "Enable this provider before starting another turn.")

	route, next, err := RouteSources(id, legacy, nil, []SourceRouteInput{source}, Priority, RoutingState{}, time.Now().UTC())
	if err == nil || SafeError(err).Code != ProviderDisabled || route.Selected != "" || !reflect.DeepEqual(next, RoutingState{}) {
		t.Fatalf("legacy source problem was bypassed: route=%+v next=%+v err=%v", route, next, err)
	}
}
