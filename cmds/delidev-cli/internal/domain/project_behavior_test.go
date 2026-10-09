// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"testing"
	"time"
)

func TestProjectBehaviorContinuousInheritanceAndExplicitDisabled(t *testing.T) {
	p := &Project{Settings: &ProjectBehavior{}}
	for _, value := range []bool{false, true, false} {
		if p.EffectiveFetch(value) != value || p.EffectivePlanApproval(value) != value {
			t.Fatal("inheritance did not follow current global value")
		}
	}
	p.Settings.AutomaticFetch, p.Settings.AutomaticPlanApproval = DisabledBoolean, DisabledBoolean
	if p.EffectiveFetch(true) || p.EffectivePlanApproval(true) {
		t.Fatal("explicit false was lost")
	}
	routing := Priority
	p.Settings.Routing = &routing
	if p.EffectiveRouting(RoundRobin) != Priority {
		t.Fatal("project routing was lost")
	}
	explicit := DefaultRemediationPolicy()
	p.Settings.Remediation = &explicit
	global := DefaultRemediationPolicy()
	global.CIFailure = true
	if p.EffectiveRemediation(global).CIFailure {
		t.Fatal("explicit policy was deep-merged")
	}
	p.Settings = nil
	if p.EffectiveRouting(RoundRobin) != RoundRobin || !p.EffectiveRemediation(global).CIFailure {
		t.Fatal("legacy project behavior changed")
	}
}
func TestProjectBehaviorRejectsUnknownStates(t *testing.T) {
	for _, value := range []ProjectBehavior{{AutomaticFetch: "sometimes"}, {AutomaticPlanApproval: "true"}, {Routing: func() *RoutingPolicy { v := RoutingPolicy("new"); return &v }()}} {
		if value.Validate() == nil {
			t.Fatal("unknown behavior accepted")
		}
	}
}

func TestProjectBehaviorRoutingPrecedenceAndIndependentProjects(t *testing.T) {
	id, agent, model, accounts := routeFixture()
	projectPolicy := Priority
	first := &Project{Settings: &ProjectBehavior{Routing: &projectPolicy}}
	secondPolicy := RoundRobin
	second := &Project{Settings: &ProjectBehavior{Routing: &secondPolicy}}
	for _, p := range []*Project{first, second, nil} {
		route, _, err := RouteAccount(id, agent, model, p, accounts, SequentialExhaustion, RoutingState{}, time.Now())
		if err != nil || route.Policy != p.EffectiveRouting(SequentialExhaustion) {
			t.Fatal("project routing failed", err)
		}
	}
	explicit := RemainingQuota
	agent.Routing = &explicit
	route, _, err := RouteAccount(id, agent, model, first, accounts, SequentialExhaustion, RoutingState{}, time.Now())
	if err != nil || route.Policy != explicit {
		t.Fatal("Agent override lost", err)
	}
}
func TestProjectBehaviorFetchRequiresEveryGate(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, repo := range []bool{false, true} {
			for _, override := range []BooleanOverride{InheritBoolean, EnabledBoolean, DisabledBoolean} {
				p := &Project{Settings: &ProjectBehavior{AutomaticFetch: override}}
				got := global && p.EffectiveFetch(global) && repo
				want := global && repo && override != DisabledBoolean
				if got != want {
					t.Fatal("fetch gate changed", global, repo, override)
				}
			}
		}
	}
}
