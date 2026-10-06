// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

// SourceRouteInput is resolved from one store snapshot. Provider/profile failures
// are blockers, never evidence that a subscription quota has been exhausted.
type SourceRouteInput struct {
	Model         Model
	ModelRevision uint64
	Source        string
	Accounts      map[ID]Account
	Blocked       map[ID]Eligibility
	Problem       *Error
}

// RouteSources never changes routing state on a skipped or blocked source. Only
// a successful first-execution claim may persist the returned state.
func RouteSources(agentID ID, agent Agent, project *Project, sources []SourceRouteInput, defaultPolicy RoutingPolicy, state RoutingState, now time.Time) (Route, RoutingState, error) {
	if len(agent.Routes) == 0 {
		if len(sources) != 1 {
			return Route{}, state, Fail(InvalidArgument, "Incomplete source selection.", "Resolve the current model and accounts.")
		}
		return RouteAccount(agentID, agent, sources[0].Model, project, sources[0].Accounts, defaultPolicy, state, now)
	}
	if len(sources) != len(agent.Routes) {
		return Route{}, state, Fail(InvalidArgument, "Incomplete account source selection.", "Resolve every configured source.")
	}
	if err := agent.Validate(); err != nil {
		return Route{}, state, err
	}
	result := Route{Sources: make([]SourceSelection, len(sources))}
	nextStates := make([]RoutingState, len(sources))
	failures := make([]error, len(sources))
	seen := map[string]bool{}
	for i, source := range sources {
		if source.Source == "" || seen[source.Source] {
			return result, state, Fail(InvalidArgument, "Duplicate or unknown account source.", "Configure each source once.")
		}
		seen[source.Source] = true
		cursor := state.Sources[source.Source]
		inner := RoutingState{Current: cursor.Current, Rotation: cursor.Rotation, Tie: cursor.Tie}
		policy := defaultPolicy
		if agent.Routes[i].Routing != nil {
			policy = *agent.Routes[i].Routing
		}
		if policy == Fixed && len(agent.Routes[i].Accounts) != 1 {
			source.Problem = Fail(InvalidArgument, "Fixed routing requires one account per source.", "Configure one account or another policy before execution.")
		}
		route, next, err := routeAccount(agentID, agent.WithSource(agent.Routes[i]), source.Model, project, source.Accounts, defaultPolicy, inner, now, source.Blocked)
		if source.Problem != nil {
			err = source.Problem
			route.Selected = ""
		}
		result.Sources[i] = SourceSelection{Source: source.Source, ModelID: agent.Routes[i].ModelID, ModelRevision: source.ModelRevision, NativeModel: source.Model.NativeID, Route: route}
		if err != nil {
			result.Sources[i].Problem = SafeError(err)
		}
		nextStates[i], failures[i] = next, err
	}
	for i, source := range result.Sources {
		exhausted := sources[i].Problem == nil && (failures[i] == nil || SafeError(failures[i]).Code == MissingInput) && len(source.Route.Candidates) > 0
		for _, candidate := range source.Route.Candidates {
			exhausted = exhausted && candidate.Eligibility == ExhaustedAccount
		}
		if exhausted {
			continue
		}
		result.Policy, result.Candidates, result.Fallback = source.Route.Policy, source.Route.Candidates, source.Route.Fallback
		if failures[i] != nil {
			return result, state, failures[i]
		}
		index := uint32(i)
		result.SourceIndex, result.Selected = &index, source.Route.Selected
		next := state
		next.Sources = map[string]RoutingCursor{}
		for key, value := range state.Sources {
			if seen[key] {
				next.Sources[key] = value
			}
		}
		chosen := nextStates[i]
		next.Sources[source.Source] = RoutingCursor{Current: chosen.Current, Rotation: chosen.Rotation, Tie: chosen.Tie}
		return result, next, nil
	}
	return result, state, Fail(MissingInput, "All configured account sources have confirmed quota exhaustion.", "Wait for an observed quota recovery or explicitly configure another source.")
}
