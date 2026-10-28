// SPDX-License-Identifier: Apache-2.0
package inference

import "context"

type modelDispatchKey struct{}
type modelDispatchContext struct {
	ordinal int
	state   *physicalBudgetState
	marked  bool
}

// ModelDispatchAttempt returns the upcoming physical model send ordinal (1-based)
// inside the authoritative Dispatch gate. Zero means this is not a model gate.
// Refused gates and pre-write failures do not advance the next ordinal.
func ModelDispatchAttempt(ctx context.Context) int {
	if v, ok := ctx.Value(modelDispatchKey{}).(*modelDispatchContext); ok && v != nil {
		return v.ordinal
	}
	return 0
}

type modelDispatchAuthorizer struct {
	Authorizer
	record *UsageRecord
}

func (a modelDispatchAuthorizer) Dispatch(ctx context.Context, p Principal, start func(context.Context) error) error {
	state := budgetFor(a.record)
	// This lock covers only authorization and the bounded first write, not dialing
	// or response headers. It makes concurrent hosted model legs receive distinct ordinals.
	state.modelMu.Lock()
	defer state.modelMu.Unlock()
	attempt := &modelDispatchContext{ordinal: state.modelSends + 1, state: state}
	ctx = context.WithValue(ctx, modelDispatchKey{}, attempt)
	return a.Authorizer.(Dispatcher).Dispatch(ctx, p, start)
}
func markModelDispatch(ctx context.Context) {
	if v, ok := ctx.Value(modelDispatchKey{}).(*modelDispatchContext); ok && v != nil && !v.marked {
		v.marked = true
		v.state.modelSends++
	}
}
