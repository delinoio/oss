package domain

import (
	"reflect"
	"testing"
	"time"
)

func routeFixture() (ID, Agent, Model, map[ID]Account) {
	provider := NewID()
	a, b, c := NewID(), NewID(), NewID()
	agent := Agent{Harness: Codex, Accounts: []WeightedAccount{{a, 1}, {b, 1}, {c, 1}}}
	model := Model{ProviderID: provider, Harnesses: []Harness{Codex}}
	accounts := map[ID]Account{}
	for _, id := range []ID{a, b, c} {
		accounts[id] = Account{ProviderID: provider, Enabled: true, Health: AccountReady}
	}
	return NewID(), agent, model, accounts
}
func TestSequentialCursorDoesNotMoveBackOnRecovery(t *testing.T) {
	id, agent, model, accounts := routeFixture()
	state := RoutingState{}
	now := time.Now()
	selectNext := func() ID {
		t.Helper()
		r, next, err := RouteAccount(id, agent, model, nil, accounts, SequentialExhaustion, state, now)
		if err != nil {
			t.Fatal(err)
		}
		state = next
		return r.Selected
	}
	a, b, c := agent.Accounts[0].ID, agent.Accounts[1].ID, agent.Accounts[2].ID
	if got := selectNext(); got != a {
		t.Fatal(got)
	}
	account := accounts[a]
	account.ConfirmedExhausted = true
	accounts[a] = account
	if got := selectNext(); got != b {
		t.Fatal(got)
	}
	account.ConfirmedExhausted = false
	accounts[a] = account
	if got := selectNext(); got != b {
		t.Fatal("recovered earlier account moved cursor backward", got)
	}
	account = accounts[b]
	account.ConfirmedExhausted = true
	accounts[b] = account
	if got := selectNext(); got != c {
		t.Fatal(got)
	}
	account = accounts[c]
	account.ConfirmedExhausted = true
	accounts[c] = account
	if got := selectNext(); got != a {
		t.Fatal("did not wrap after traversal", got)
	}
}
func TestWeightedRoundRobinAndPurePreview(t *testing.T) {
	id, agent, model, accounts := routeFixture()
	agent.Accounts = agent.Accounts[:2]
	agent.Accounts[0].Weight = 3
	state := RoutingState{Rotation: map[ID]int64{}}
	counts := map[ID]int{}
	for i := 0; i < 40; i++ {
		before := map[ID]int64{}
		for k, v := range state.Rotation {
			before[k] = v
		}
		a, next, err := RouteAccount(id, agent, model, nil, accounts, RoundRobin, state, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := RouteAccount(id, agent, model, nil, accounts, RoundRobin, state, time.Now())
		if err != nil || a.Selected != b.Selected || !reflect.DeepEqual(before, state.Rotation) {
			t.Fatal("preview mutates rotation")
		}
		counts[a.Selected]++
		state = next
	}
	if counts[agent.Accounts[0].ID] != 30 || counts[agent.Accounts[1].ID] != 10 {
		t.Fatalf("wrong weights: %v", counts)
	}
}
func TestEligibilityRestrictionsAndFixedExclusion(t *testing.T) {
	id, agent, model, accounts := routeFixture()
	a := agent.Accounts[0].ID
	value := accounts[a]
	value.ExcludeAutomatic = true
	accounts[a] = value
	project := &Project{Accounts: Restriction{Configured: true, IDs: []ID{a}}}
	if _, _, err := RouteAccount(id, agent, model, project, accounts, Priority, RoutingState{}, time.Now()); err == nil {
		t.Fatal("automatic exclusion ignored")
	}
	agent.Accounts = agent.Accounts[:1]
	route, _, err := RouteAccount(id, agent, model, project, accounts, Fixed, RoutingState{}, time.Now())
	if err != nil || route.Selected != a {
		t.Fatalf("explicit Fixed excluded: %v", err)
	}
	project.Agents = Restriction{Configured: true}
	if _, _, err := RouteAccount(id, agent, model, project, accounts, Fixed, RoutingState{}, time.Now()); err == nil {
		t.Fatal("empty configured restriction treated as unrestricted")
	}
}
func TestMinimumWindowComparableQuotaAndStaleReset(t *testing.T) {
	id, agent, model, accounts := routeFixture()
	now := time.Now()
	reset := now.Add(time.Hour)
	set := func(id ID, x, y float64) {
		a := accounts[id]
		a.Quota = []QuotaWindow{{ID: "short", Blocking: true, ComparisonGroup: "same-provider", Remaining: &x, ObservedAt: now, ResetAt: &reset, State: Observed}, {ID: "weekly", Blocking: true, ComparisonGroup: "same-provider", Remaining: &y, ObservedAt: now, ResetAt: &reset, State: Observed}}
		accounts[id] = a
	}
	a, b, c := agent.Accounts[0].ID, agent.Accounts[1].ID, agent.Accounts[2].ID
	set(a, .9, .1)
	set(b, .4, .4)
	set(c, .3, .8)
	route, next, err := RouteAccount(id, agent, model, nil, accounts, RemainingQuota, RoutingState{}, now)
	if err != nil || route.Selected != b {
		t.Fatalf("did not use minimum blocking fraction: %+v %v", route, err)
	}
	set(c, .4, .4)
	route, _, err = RouteAccount(id, agent, model, nil, accounts, RemainingQuota, next, now)
	if err != nil || route.Selected != c {
		t.Fatalf("quota ties did not rotate: %+v %v", route, err)
	}
	route, _, err = RouteAccount(id, agent, model, nil, accounts, RemainingQuota, next, now.Add(6*time.Minute))
	if err != nil || route.Selected != a || !route.Fallback {
		t.Fatalf("stale quota should use priority: %+v %v", route, err)
	}
	account := accounts[a]
	account.ConfirmedExhausted = true
	accounts[a] = account
	route, _, err = RouteAccount(id, agent, model, nil, accounts, ResetWindow, RoutingState{}, now.Add(2*time.Hour))
	if err != nil || route.Selected == a || !route.Fallback {
		t.Fatalf("passed reset falsely recovered exhausted account: %+v %v", route, err)
	}
}
