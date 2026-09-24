package domain

import (
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

type Eligibility string

const (
	Eligible               Eligibility = "eligible"
	MissingAccount         Eligibility = "missing-account"
	ProjectRestricted      Eligibility = "project-restricted"
	AgentRestricted        Eligibility = "agent-restricted"
	IncompatibleAccount    Eligibility = "incompatible-account"
	DisabledAccount        Eligibility = "disabled"
	UnauthenticatedAccount Eligibility = "unauthenticated"
	ExhaustedAccount       Eligibility = "exhausted"
	AutomaticExcluded      Eligibility = "automatic-excluded"
)

type Candidate struct {
	ID              ID               `json:"id"`
	Weight          uint32           `json:"weight"`
	Eligibility     Eligibility      `json:"eligibility"`
	QuotaState      ObservationState `json:"quota_state"`
	Score           *float64         `json:"score,omitempty"`
	ResetAt         *time.Time       `json:"reset_at,omitempty"`
	ComparisonGroup string           `json:"comparison_group,omitempty"`
}
type RoutingState struct {
	Current  ID           `json:"current,omitempty"`
	Rotation map[ID]int64 `json:"rotation"`
	Tie      uint64       `json:"tie"`
}
type Route struct {
	Policy     RoutingPolicy `json:"policy"`
	Selected   ID            `json:"selected,omitempty"`
	Candidates []Candidate   `json:"candidates"`
	Fallback   bool          `json:"fallback"`
}

// RouteAccount is pure. Preview discards next; dispatch must atomically persist
// next together with the session snapshot/account and actual selection record.
func RouteAccount(agentID ID, agent Agent, model Model, project *Project, accounts map[ID]Account, defaultPolicy RoutingPolicy, state RoutingState, now time.Time) (Route, RoutingState, error) {
	policy := defaultPolicy
	if agent.Routing != nil {
		policy = *agent.Routing
	}
	route := Route{Policy: policy, Candidates: make([]Candidate, 0, len(agent.Accounts))}
	next := RoutingState{Current: state.Current, Tie: state.Tie, Rotation: map[ID]int64{}}
	for id, weight := range state.Rotation {
		next.Rotation[id] = weight
	}
	if !policy.Valid() {
		return route, state, Fail(InvalidArgument, "Unknown routing policy.", "Configure a supported policy.")
	}
	if !slices.Contains(model.Harnesses, agent.Harness) {
		return route, state, Fail(Unsupported, "The model is incompatible with this harness.", "Select a compatible Agent Worker model.")
	}
	eligible := make([]Candidate, 0)
	for _, link := range agent.Accounts {
		c := Candidate{ID: link.ID, Weight: link.Weight, Eligibility: Eligible, QuotaState: ObservationUnknown}
		account, ok := accounts[link.ID]
		switch {
		case project != nil && !project.Agents.Allows(agentID):
			c.Eligibility = AgentRestricted
		case !ok:
			c.Eligibility = MissingAccount
		case project != nil && !project.Accounts.Allows(link.ID):
			c.Eligibility = ProjectRestricted
		case account.ProviderID != model.ProviderID:
			c.Eligibility = IncompatibleAccount
		case !account.Enabled:
			c.Eligibility = DisabledAccount
		case account.Health != AccountReady:
			c.Eligibility = UnauthenticatedAccount
		case account.ConfirmedExhausted:
			c.Eligibility = ExhaustedAccount
		case account.ExcludeAutomatic && policy != Fixed:
			c.Eligibility = AutomaticExcluded
		}
		if ok {
			c.QuotaState, c.Score, c.ResetAt, c.ComparisonGroup = quotaEvidence(account.Quota, now)
		}
		route.Candidates = append(route.Candidates, c)
		if c.Eligibility == Eligible {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		return route, state, Fail(MissingInput, "No eligible account is available for execution.", "Inspect routing preview, project restrictions, model compatibility, and account health.")
	}
	choose := eligible[0].ID
	switch policy {
	case Fixed:
		if len(agent.Accounts) != 1 {
			return route, state, Fail(MissingInput, "Fixed routing requires exactly one account.", "Configure one eligible account on the Agent Worker.")
		}
	case Priority:
	case SequentialExhaustion:
		current := -1
		for i, c := range route.Candidates {
			if c.ID == state.Current {
				current = i
				break
			}
		}
		if current >= 0 {
			if route.Candidates[current].Eligibility == Eligible {
				choose = state.Current
			} else {
				for step := 1; step <= len(route.Candidates); step++ {
					c := route.Candidates[(current+step)%len(route.Candidates)]
					if c.Eligibility == Eligible {
						choose = c.ID
						break
					}
				}
			}
		}
		next.Current = choose
	case RoundRobin:
		// Smooth weighted rotation is bounded by the sum of configured weights and
		// persists the entire accumulator vector, avoiding process-local randomness.
		total := int64(0)
		var best int64
		first := true
		active := map[ID]bool{}
		for _, c := range eligible {
			active[c.ID] = true
			weight := int64(c.Weight)
			if weight < 1 || weight > 1000 {
				return route, state, Fail(InvalidArgument, "Invalid routing weight.", "Use weights from 1 through 1000.")
			}
			total += weight
			next.Rotation[c.ID] += weight
			if first || next.Rotation[c.ID] > best {
				first = false
				choose = c.ID
				best = next.Rotation[c.ID]
			}
		}
		for id := range next.Rotation {
			if !active[id] {
				delete(next.Rotation, id)
			}
		}
		next.Rotation[choose] -= total
	case RemainingQuota, ResetWindow:
		// Comparison groups encode all applicable native blocking windows. Never
		// compare unrelated providers/windows or infer recovery from an expired reset.
		groups := map[string][]Candidate{}
		order := []string{}
		for _, c := range eligible {
			if c.QuotaState != Observed || c.Score == nil || (policy == ResetWindow && c.ResetAt == nil) {
				continue
			}
			if _, ok := groups[c.ComparisonGroup]; !ok {
				order = append(order, c.ComparisonGroup)
			}
			groups[c.ComparisonGroup] = append(groups[c.ComparisonGroup], c)
		}
		var comparable []Candidate
		for _, group := range order {
			if len(groups[group]) >= 2 {
				comparable = groups[group]
				break
			}
		}
		if len(comparable) < 2 {
			route.Fallback = true
			break
		}
		best := comparable[0]
		ties := []ID{best.ID}
		for _, c := range comparable[1:] {
			better, equal := false, false
			if policy == RemainingQuota {
				better = *c.Score > *best.Score
				equal = *c.Score == *best.Score
			} else {
				better = c.ResetAt.Before(*best.ResetAt)
				equal = c.ResetAt.Equal(*best.ResetAt)
			}
			if better {
				best = c
				ties = []ID{c.ID}
			} else if equal {
				ties = append(ties, c.ID)
			}
		}
		choose = ties[state.Tie%uint64(len(ties))]
		next.Tie++
	}
	route.Selected = choose
	return route, next, nil
}

func quotaEvidence(windows []QuotaWindow, now time.Time) (ObservationState, *float64, *time.Time, string) {
	minimum := 1.0
	var reset *time.Time
	groups := []string{}
	for _, w := range windows {
		if !w.Blocking {
			continue
		}
		if w.State != Observed {
			return w.State, nil, nil, ""
		}
		if w.ObservedAt.After(now) || now.Sub(w.ObservedAt) > 5*time.Minute || (w.ResetAt != nil && !w.ResetAt.After(now)) {
			return ObservationStale, nil, nil, ""
		}
		if w.Remaining == nil || math.IsNaN(*w.Remaining) || *w.Remaining < 0 || *w.Remaining > 1 || w.ComparisonGroup == "" {
			return ObservationUnknown, nil, nil, ""
		}
		if *w.Remaining < minimum {
			minimum = *w.Remaining
		}
		if w.ResetAt != nil && (reset == nil || w.ResetAt.Before(*reset)) {
			value := *w.ResetAt
			reset = &value
		}
		groups = append(groups, w.ComparisonGroup+":"+w.ID)
	}
	if len(groups) == 0 {
		return ObservationUnknown, nil, nil, ""
	}
	sort.Strings(groups)
	return Observed, &minimum, reset, strings.Join(groups, "|")
}
