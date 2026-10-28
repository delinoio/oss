// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"sync"
)

type physicalBudgetState struct {
	modelMu    sync.Mutex
	modelSends int
	mu         sync.Mutex
	reserved   int
}

var budgetInitialization sync.Mutex

func budgetFor(rec *UsageRecord) *physicalBudgetState {
	budgetInitialization.Lock()
	defer budgetInitialization.Unlock()
	if rec.budget == nil {
		rec.budget = &physicalBudgetState{}
	}
	return rec.budget
}

// The ordinary one-send path shares reservations with support traffic and adapter
// batches. The mutex spans only the bounded physical write inside authoritative
// Dispatch, never DNS/TLS or response waits.
type modelBudgetAuthorizer struct {
	Authorizer
	record *UsageRecord
	max    int
}

func (a modelBudgetAuthorizer) Dispatch(ctx context.Context, p Principal, start func(context.Context) error) error {
	return a.Authorizer.(Dispatcher).Dispatch(ctx, p, func(gated context.Context) error {
		state := budgetFor(a.record)
		state.mu.Lock()
		defer state.mu.Unlock()
		if a.record.Sends+state.reserved >= a.max {
			return &Error{429, "request_send_budget_exhausted", 0}
		}
		return start(gated)
	})
}
