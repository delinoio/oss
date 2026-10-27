// SPDX-License-Identifier: Apache-2.0
package kiro

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

const maxStateBytes = 512 << 10
const StateTTL = 24 * time.Hour

type savedState struct {
	Version                            int
	Owner                              inference.Principal
	Provider, Account, Model, Endpoint string
	Metadata                           Metadata
}

var (
	_ inference.ContextPreparer    = (*Adapter)(nil)
	_ inference.ContextualAdapter  = (*Adapter)(nil)
	_ inference.StatefulAdapter    = (*Adapter)(nil)
	_ inference.HTTPRecoveryPolicy = (*Adapter)(nil)
)

func (a *Adapter) DecodeWithInput(ctx context.Context, in inference.AdapterInput, response *http.Response) (*http.Response, error) {
	if !matchesInput(ctx, in) {
		return nil, inference.ErrUnauthorized
	}
	return a.Decode(ctx, response)
}

// RestoreState accepts only the executor's account/credential-scoped store. No
// missing, expired or invalid continuation may silently become a fresh turn.
func (a *Adapter) RestoreState(ctx context.Context, in inference.AdapterInput, store inference.AdapterStateStore) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.PreviousResponseID == "" {
		return ctx, nil
	}
	if store == nil {
		return nil, inference.ErrContinuation
	}
	data, err := store.Load(ctx, in.PreviousResponseID)
	if err != nil {
		return nil, err
	}
	var state savedState
	if len(data) > maxStateBytes || json.Unmarshal(data, &state) != nil || state.Version != 1 || !state.Metadata.Terminal || state.Metadata.FailureCode != "" || state.Owner != in.Principal || state.Provider != in.Target.Provider || state.Account != in.Credential.AccountID || state.Model != in.Target.Model || state.Endpoint != in.Target.BaseURL {
		return nil, inference.ErrContinuation
	}
	ctx, err = WithContinuation(ctx, in.Principal, state.Metadata.ConversationID)
	if err != nil {
		return nil, inference.ErrContinuation
	}
	ctx, err = WithImagePositions(ctx, in.Principal, state.Metadata.ImagePositions)
	if err != nil {
		return nil, inference.ErrContinuation
	}
	return ctx, ctx.Err()
}

func (a *Adapter) CaptureState(ctx context.Context, in inference.AdapterInput, responseID string, store inference.AdapterStateStore) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !matchesInput(ctx, in) {
		return inference.ErrUnauthorized
	}
	m, ok := MetadataFor(ctx, in.Principal)
	if !ok || !m.Terminal || m.FailureCode != "" {
		return inference.ErrContinuation
	}
	if store == nil || !conversationPattern.MatchString(responseID) {
		return inference.ErrContinuation
	}
	data, err := json.Marshal(savedState{1, in.Principal, in.Target.Provider, in.Credential.AccountID, in.Target.Model, in.Target.BaseURL, m})
	if err != nil {
		return err
	}
	if len(data) > maxStateBytes {
		return failure(413, "kiro_state_limit")
	}
	return store.Save(ctx, responseID, data, StateTTL)
}

// RecoverHTTP is advice only: the executor owns authorization, refresh, delay,
// origin transfer and the original send budget. It never retries transport loss.
func (a *Adapter) RecoverHTTP(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if err := ctx.Err(); err != nil {
		return inference.RecoveryDecision{}, err
	}
	if f.Status >= 200 && f.Status < 400 {
		return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
	}
	plan := PlanHTTPRecovery(in.Target.BaseURL, f.Status, f.Header, f.Body, f.Attempt, f.EndpointHops > 0, in.PreviousResponseID == "", time.Now())
	d := inference.RecoveryDecision{Action: inference.RecoveryStop, Error: &inference.Error{Status: plan.Failure.Status, Code: plan.Failure.Code}}
	switch plan.Kind {
	case RecoveryEndpoint:
		d.Action, d.Endpoint = inference.RecoveryRetry, plan.URL
	case RecoveryThrottle:
		d.Action, d.Delay = inference.RecoveryRetry, plan.Delay
	default:
		if f.Status == 401 && !f.Refreshed && in.Target.Auth == inference.OAuth && in.Target.Retry.RefreshOn401 {
			d.Action = inference.RecoveryRefresh
		}
	}
	return d, nil
}

func inputBinding(in inference.AdapterInput) [32]byte {
	// Length-delimited JSON avoids ambiguous separators without retaining secrets.
	data, _ := json.Marshal([]any{in.Principal, in.Target.Provider, in.Target.BaseURL, in.Target.Model, in.Credential.AccountID, in.Credential.Secret})
	return sha256.Sum256(data)
}
func matchesInput(ctx context.Context, in inference.AdapterInput) bool {
	state, _ := ctx.Value(stateKey{}).(*requestState)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.built && state.owner == in.Principal && state.binding == inputBinding(in)
}
