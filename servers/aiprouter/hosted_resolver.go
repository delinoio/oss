// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/pricing"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/google/uuid"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/artifacts"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/hosted"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

type hostedResolver struct {
	auth                   *inferenceAdapter
	artifacts              *artifacts.Service
	adapters               map[inference.Wire]inference.Adapter
	mu                     sync.Mutex
	imageBytes, videoBytes int64
}
type hostedOperation struct {
	resolver   *hostedResolver
	request    hosted.OperationRequest
	transport  *hostedTransport
	target     inference.Target
	credential inference.Credential
	backend    string
	settings   *v1.HostedSettings
	wait       func(context.Context, time.Duration) error
}

func hostedDelay(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (r *hostedResolver) Resolve(ctx context.Context, req hosted.OperationRequest) (hosted.OperationExecutor, error) {
	if e := r.auth.BeforeSend(ctx, req.Principal); e != nil {
		return nil, e
	}
	c, e := readHostedSettings(ctx, r.auth.s, scopeOf(req.Principal))
	if e != nil {
		return nil, inferenceError(e)
	}
	var selection *v1.HostedHelperTarget
	backend, model := "", ""
	switch req.Kind {
	case hosted.Search:
		x := c.Search
		if x == nil || x.Enabled != nil && !x.GetEnabled() || x.MaxSearchesPerTurn != nil && x.GetMaxSearchesPerTurn() == 0 {
			return nil, inference.ErrUnsupported
		}
		backend = hostedSearchBackend(x)
		selection = x.Target
		model = map[string]string{"openai": "gpt-5.6-luna", "anthropic": "claude-sonnet-5", "xai": "grok-4.6", "gemini": "gemini-3.8-flash", "exa": "search", "devin": "search"}[backend]
	case hosted.Vision:
		x := c.Vision
		if x == nil || x.Enabled != nil && !x.GetEnabled() || x.MaxDescriptionsPerTurn != nil && x.GetMaxDescriptionsPerTurn() == 0 {
			return nil, inference.ErrUnsupported
		}
		backend = hostedVisionBackend(x)
		selection = x.GetTarget()
		model = "gpt-5.6-luna"
	case hosted.Image, hosted.Video:
		x := c.Images
		if x == nil || req.Kind == hosted.Image && !x.BridgeEnabled || req.Kind == hosted.Video && !x.VideoBridgeEnabled || r.artifacts == nil {
			return nil, inference.ErrUnsupported
		}
		backend = "media"
		selection = x.Target
		model = "grok-imagine-image-quality"
		if x.BridgeModel != nil {
			model = *x.BridgeModel
		}
		if req.Kind == hosted.Video {
			model = "grok-imagine-video"
			if x.VideoBridgeModel != nil {
				model = *x.VideoBridgeModel
			}
		}
	default:
		return nil, inference.ErrUnsupported
	}
	lease, e := r.auth.current(req.Principal)
	if e != nil {
		return nil, e
	}
	var d providerData
	var version string
	var target inference.Target
	var cred inference.Credential
	if backend == "routed" {
		// A separate router shares only the admission, never the parent's selection state.
		a := &inferenceAdapter{s: r.auth.s, lease: lease, telemetry: r.auth.telemetry, prices: r.auth.prices}
		route, e := a.Resolve(ctx, req.Principal, c.Vision.GetRouteModel(), inference.Generate)
		if e != nil {
			return nil, e
		}
		target = route.Targets[0]
		cred, e = a.Select(ctx, req.Principal, target, "")
		if e != nil {
			return nil, e
		}
		d, version, e = a.providerVersion(ctx, scopeOf(req.Principal), cred.AccountID)
		if e != nil {
			return nil, e
		}
	} else {
		members, pool, e := hostedMembers(ctx, r.auth.s, scopeOf(req.Principal), selection)
		if e != nil {
			return nil, inference.ErrNoCredential
		}
		if selection.ModelId != "" && (req.Kind != hosted.Image && req.Kind != hosted.Video || req.Kind == hosted.Image && c.Images.BridgeModel == nil || req.Kind == hosted.Video && c.Images.VideoBridgeModel == nil) {
			model = selection.ModelId
		}
		choice := store.SelectionRequest{GroupID: "hosted/" + string(req.Kind), Version: selectionVersion(c), Model: model, Strategy: store.SelectionPriority}
		if pool != nil {
			choice.GroupID += "/" + pool.Id
			choice.Version = selectionVersion(pool)
			switch pool.Strategy {
			case v1.PoolStrategy_POOL_STRATEGY_ROUND_ROBIN:
				choice.Strategy = store.SelectionRoundRobin
			case v1.PoolStrategy_POOL_STRATEGY_LEAST_RECENTLY_USED:
				choice.Strategy = store.SelectionLRU
			}
		}
		rows := map[string]providerData{}
		versions := map[string]string{}
		for _, m := range members {
			row, v, e := r.auth.providerVersion(ctx, scopeOf(req.Principal), m.ConnectionId)
			if e != nil {
				return nil, inferenceError(e)
			}
			lane := backend
			if lane == "" {
				lane = "openai"
				if hostedEligible(row, "anthropic") {
					lane = "anthropic"
				}
			}
			if !hostedEligible(row, lane) {
				return nil, inference.ErrNoCredential
			}
			if backend == "" {
				backend = lane
				if lane == "anthropic" && selection.ModelId == "" {
					model = "claude-sonnet-5"
					choice.Model = model
				}
			}
			if backend != "media" && backend != "exa" && backend != "devin" {
				if _, eligible := modelEvidence(row, model, time.Now()); !eligible {
					return nil, inference.ErrNoCredential
				}
			}
			rows[m.ConnectionId] = row
			versions[m.ConnectionId] = v
			weight := int(m.Weight)
			if weight < 1 {
				weight = 1
			}
			choice.Candidates = append(choice.Candidates, store.SelectionCandidate{ID: m.ConnectionId, Version: v, Weight: weight, Priority: int(m.Priority)})
		}
		receipt, e := r.auth.s.SelectAccount(ctx, lease, choice)
		if e != nil {
			return nil, inferenceError(e)
		}
		d = rows[receipt.CandidateID]
		version = versions[receipt.CandidateID]
		if backend == "media" || backend == "exa" {
			base := "https://api.x.ai/v1"
			if backend == "exa" {
				base = "https://api.exa.ai"
			}
			target = inference.Target{Provider: d.Metadata.Id, BaseURL: base, Model: model, Auth: inference.APIKey, Wire: inference.Responses}
			cred = inference.Credential{AccountID: d.Metadata.Id, Secret: d.Secret}
		} else {
			target, e = targetForContext(ctx, d, model)
			if e != nil {
				return nil, e
			}
			cred, e = credentialFor(ctx, d, target)
			if e != nil {
				return nil, e
			}
			if backend == "xai" {
				target.BaseURL = "https://api.x.ai/v1"
				target.Wire = inference.Responses
			}
		}
	}
	if d.Credential != nil && !d.Credential.ExpiresAt.IsZero() && !d.Credential.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		if _, e = refreshProviderCredential(ctx, r.auth.s, lease, cred.AccountID); e != nil {
			return nil, inferenceError(e)
		}
		d, version, e = r.auth.providerVersion(ctx, scopeOf(req.Principal), cred.AccountID)
		if e != nil {
			return nil, inferenceError(e)
		}
		target, e = targetForContext(ctx, d, target.Model)
		if e != nil {
			return nil, e
		}
		cred, e = credentialFor(ctx, d, target)
		if e != nil {
			return nil, e
		}
	}
	if backend == "xai" {
		target.BaseURL = "https://api.x.ai/v1"
		target.Wire = inference.Responses
	}
	if backend == "gemini" {
		preset, ok := providers.Lookup("google-antigravity")
		if !ok {
			return nil, inference.ErrUnsupported
		}
		target.BaseURL = preset.BaseURL
	}
	if strings.TrimSpace(cred.Secret) == "" {
		return nil, inference.ErrNoCredential
	}
	settings, e := r.auth.s.Settings(ctx)
	if e != nil {
		return nil, inferenceError(e)
	}
	tr := &hostedTransport{policy: inference.DestinationPolicy{ApprovedOrigins: settings.Destinations, Rules: settings.DestinationRules}, base: target.BaseURL, provider: d.Metadata.ProviderId, model: target.Model}
	tr.gate = func(c context.Context, start func(context.Context) error) error {
		var sendErr error
		err := r.auth.s.WithAccountDispatch(c, lease, cred.AccountID, version, func(locked context.Context) error { sendErr = start(locked); return sendErr })
		if sendErr != nil {
			return sendErr
		}
		return inferenceError(err)
	}
	tr.price = func(u *store.Usage, usage inference.Usage) {
		r.auth.mu.Lock()
		prices := r.auth.prices
		r.auth.mu.Unlock()
		if prices != nil {
			estimate := prices.Estimate(pricing.Request{Provider: u.Provider, Model: u.Model, Status: pricing.Reported, Usage: &pricing.Usage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CachedInputTokens: usage.CacheReadTokens, CacheCreationInputTokens: usage.CacheWriteTokens}}, time.Now())
			u.Price, _ = json.Marshal(estimate)
			u.Provenance = estimate.SnapshotID
			if estimate.Known {
				cost := strconv.FormatFloat(estimate.Cost.Total, 'f', 12, 64)
				u.Cost = &cost
				u.PriceKnown = true
			}
		}
	}
	tr.record = func(c context.Context, id string, u store.Usage) error {
		if u.InputTokens == nil || u.OutputTokens == nil {
			if r.auth.telemetry != nil {
				r.auth.telemetry.Event(c, "hosted_usage_unknown")
			}
		}
		if !u.PriceKnown && r.auth.telemetry != nil {
			r.auth.telemetry.Event(c, "hosted_cost_unknown")
		}
		e := r.auth.s.RecordOperationUsage(c, lease, id, u)
		if e != nil && r.auth.telemetry != nil {
			r.auth.telemetry.Event(c, "hosted_usage_record_failed")
		}
		return e
	}
	return &hostedOperation{resolver: r, request: req, transport: tr, target: target, credential: cred, backend: backend, settings: c, wait: hostedDelay}, nil
}
func (o *hostedOperation) Execute(ctx context.Context, r hosted.OperationRequest) (result hosted.OperationResult, err error) {
	// A client can cancel while an accepted asynchronous job waits between polls.
	// Preserve that terminal cancellation separately from completed HTTP receipts;
	// no token or cost amount is inferred from the remote job's unknown completion.
	defer func() {
		if !errors.Is(ctx.Err(), context.Canceled) || o.transport == nil {
			return
		}
		o.transport.mu.Lock()
		count, last := o.transport.completed, o.transport.lastOutcome
		o.transport.mu.Unlock()
		if count == 0 || last == store.OutcomeCanceled {
			return
		}
		id, e := uuid.NewV7()
		if e != nil {
			err = errors.Join(err, e)
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if o.transport.record == nil {
			err = errors.Join(err, inference.ErrUnavailable)
			return
		}
		if e = o.transport.record(cleanup, id.String(), store.Usage{Provider: o.transport.provider, Model: o.transport.model, Outcome: store.OutcomeCanceled, ErrorCode: "canceled"}); e != nil {
			err = errors.Join(err, inference.ErrUnavailable)
		}
	}()

	if r.Principal != o.request.Principal || r.RequestID != o.request.RequestID || r.CallID != o.request.CallID || r.Kind != o.request.Kind || string(r.Arguments) != string(o.request.Arguments) || string(r.Declaration) != string(o.request.Declaration) {
		return hosted.OperationResult{}, inference.ErrUnauthorized
	}
	var args map[string]any
	if json.Unmarshal(r.Arguments, &args) != nil {
		return hosted.OperationResult{}, inference.ErrUnsupported
	}
	switch r.Kind {
	case hosted.Image:
		return o.image(ctx, args)
	case hosted.Video:
		return o.video(ctx, args)
	default:
		return o.model(ctx, args)
	}
}
