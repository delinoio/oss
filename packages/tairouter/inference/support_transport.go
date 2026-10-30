// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"net/http"
	"sync"
)

// SendGate holds the authoritative revocation fence only around start's bounded write.
type SendGate func(context.Context, func(context.Context) error) error
type supportAuthorizer struct{ gate SendGate }

func (a supportAuthorizer) Admit(context.Context, string) (Principal, func(), error) {
	return Principal{}, nil, ErrUnsupported
}
func (a supportAuthorizer) BeforeSend(ctx context.Context, _ Principal) error { return ctx.Err() }
func (a supportAuthorizer) Dispatch(ctx context.Context, _ Principal, start func(context.Context) error) error {
	return a.gate(ctx, start)
}

// Do executes one support request, retaining its query while validating and pinning
// the origin. No redirects or retries are performed. gate must be non-nil. onSend
// is a notification, not authorization; use WithPhysicalSendBudget for budget refusal.
// Do waits for active dispatch callbacks and prevents new callbacks before returning.
func (p DestinationPolicy) Do(ctx context.Context, req *http.Request, gate SendGate, onSend func()) (*http.Response, error) {
	if req == nil || req.URL == nil || gate == nil || req.Host != "" && req.Host != req.URL.Host {
		return nil, ErrDestination
	}
	ctx = context.WithValue(ctx, modelDispatchKey{}, (*modelDispatchContext)(nil))
	ctx, stopFirstByte, cancel := supportFirstByteContext(ctx)
	defer stopFirstByte()
	keepContext := false
	var dispatchMu sync.Mutex
	dispatchFinished := false
	defer func() {
		if !keepContext {
			cancel()
		}
		// net/http can return a response before its write goroutine finishes
		// the authorization transaction. Join that callback before callers read
		// captured outcomes, and reject dispatches that arrive after Do returns.
		dispatchMu.Lock()
		dispatchFinished = true
		dispatchMu.Unlock()
	}()
	u := *req.URL
	u.RawQuery = ""
	u.ForceQuery = false
	client, _, e := p.client(u.String())
	if e != nil {
		return nil, e
	}
	wrapped := supportAuthorizer{gate: func(c context.Context, start func(context.Context) error) error {
		dispatchMu.Lock()
		defer dispatchMu.Unlock()
		if dispatchFinished {
			return ErrUnavailable
		}
		return gate(c, func(locked context.Context) error { return physicalSend(locked, start) })
	}}
	if onSend == nil {
		onSend = func() {}
	}
	sent := gateClient(client, ctx, Principal{}, wrapped, onSend)
	resp, e := client.Do(req.Clone(ctx))
	if e != nil {
		if sent.Load() {
			return nil, ErrAmbiguous
		}
		return nil, e
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != 304 {
		resp.Body.Close()
		return nil, ErrDestination
	}
	resp.Body = &executorBody{ReadCloser: resp.Body, cancel: cancel}
	keepContext = true
	return resp, nil
}

type physicalBudgetKey struct{}

// WithPhysicalSendBudget attaches a gate invoked at the physical write boundary.
// The callback must invoke start synchronously once, or return a refusal without it.
func WithPhysicalSendBudget(ctx context.Context, gate SendGate) context.Context {
	return context.WithValue(ctx, physicalBudgetKey{}, gate)
}
func physicalSend(ctx context.Context, start func(context.Context) error) error {
	if gate, ok := ctx.Value(physicalBudgetKey{}).(SendGate); ok {
		return gate(ctx, start)
	}
	return start(ctx)
}

// PhysicalSend applies the enclosing request's budget to an externally owned transport.
// Call only inside its authorization gate, immediately before a bounded physical write.
func PhysicalSend(ctx context.Context, start func(context.Context) error) error {
	return physicalSend(ctx, start)
}
func requestSupportBudget(ctx context.Context, rec *UsageRecord, max int) context.Context {
	state := budgetFor(rec)
	return WithPhysicalSendBudget(ctx, func(c context.Context, start func(context.Context) error) error {
		state.mu.Lock()
		defer state.mu.Unlock()
		if rec.Sends+state.reserved >= max {
			return &Error{429, "request_send_budget_exhausted", 0}
		}
		rec.Sends++
		rec.UnmeasuredAttempts++
		return start(c)
	})
}

type inboundWireKey struct{}

// InboundWire identifies the caller protocol while Router.Resolve selects a target.
func InboundWire(ctx context.Context) Wire { w, _ := ctx.Value(inboundWireKey{}).(Wire); return w }

// RequestHeaders returns a copy of bounded, allowlisted session/thread headers.
func RequestHeaders(ctx context.Context) http.Header {
	m, _ := ctx.Value(requestMetadataKey{}).(requestMetadata)
	return m.headers.Clone()
}
func AffinitySession(ctx context.Context) string {
	h := RequestHeaders(ctx)
	for _, k := range []string{"session-id", "session_id", "thread-id"} {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return ""
}

// HasPhysicalSendBudget lets preselection support helpers fail closed outside an admitted request budget.
func HasPhysicalSendBudget(ctx context.Context) bool {
	_, ok := ctx.Value(physicalBudgetKey{}).(SendGate)
	return ok
}
