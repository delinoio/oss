// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"net/http"
	"net/url"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/artifacts"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/hosted"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

type hostedConfiguredAdapter struct {
	*hosted.Adapter
	inner    inference.Adapter
	resolver *hostedResolver
	recovery inference.HTTPRecoveryPolicy
}

func hostedProviderAdapters(adapters map[inference.Wire]inference.Adapter, auth *inferenceAdapter, art *artifacts.Service) map[inference.Wire]inference.Adapter {
	originals := map[inference.Wire]inference.Adapter{}
	for w, a := range adapters {
		originals[w] = a
	}
	for _, w := range []inference.Wire{inference.Chat, inference.Responses, inference.Anthropic} {
		if originals[w] == nil {
			originals[w] = hostedStandardAdapter{}
		}
	}
	resolver := &hostedResolver{auth: auth, artifacts: art, adapters: originals}
	policies := providerRecoveryPolicies(originals)
	out := map[inference.Wire]inference.Adapter{}
	for w, a := range originals {
		base, _ := hosted.New(a, hosted.Config{Resolver: resolver, EnabledTools: map[hosted.Kind]bool{}, RecoveryPolicy: policies[w]})
		out[w] = &hostedConfiguredAdapter{Adapter: base, inner: a, resolver: resolver, recovery: policies[w]}
	}
	return out
}
func hostedWrapperConfig(c *v1.HostedSettings, in inference.AdapterInput, r hosted.Resolver) hosted.Config {
	out := hosted.Config{Resolver: r, EnabledTools: map[hosted.Kind]bool{}, NativeTools: map[hosted.Kind]bool{}}
	if x := c.Search; x != nil && x.Target != nil && (x.Enabled == nil || x.GetEnabled()) && (x.MaxSearchesPerTurn == nil || x.GetMaxSearchesPerTurn() > 0) {
		out.EnabledTools[hosted.Search] = true
		out.MaxSearches = int(x.GetMaxSearchesPerTurn())
		out.SearchTimeout = time.Duration(x.GetTimeoutMs()) * time.Millisecond
	}
	if x := c.Images; x != nil && x.Target != nil {
		out.EnabledTools[hosted.Image] = x.BridgeEnabled
		out.EnabledTools[hosted.Video] = x.VideoBridgeEnabled
		out.EnableVideo = x.VideoBridgeEnabled
	}
	if x := c.Vision; x != nil && (x.GetTarget() != nil || x.GetRouteModel() != "") && (x.Enabled == nil || x.GetEnabled()) && (x.MaxDescriptionsPerTurn == nil || x.GetMaxDescriptionsPerTurn() > 0) {
		out.DescribeImages = true
		out.MaxDescriptions = int(x.GetMaxDescriptionsPerTurn())
		out.VisionTimeout = time.Duration(x.GetTimeoutMs()) * time.Millisecond
	}
	// Native hosted tools must retain their provider wire. The helper declarations
	// only activate on transports that need a bridge; settings do not replace native tools.
	u, _ := url.Parse(in.Target.BaseURL)
	if u != nil && u.Hostname() == "api.openai.com" {
		out.NativeTools[hosted.Search] = true
		out.NativeTools[hosted.Image] = true
		out.EnableVideo = false
	}
	if in.Target.Wire == inference.Anthropic {
		out.NativeTools[hosted.Search] = true
	}
	return out
}
func (a *hostedConfiguredAdapter) Execute(ctx context.Context, in inference.AdapterInput, e inference.AdapterExecutor, s inference.AdapterStateStore) (*http.Response, inference.Wire, error) {
	c, err := readHostedSettings(ctx, a.resolver.auth.s, scopeOf(in.Principal))
	if err != nil {
		return nil, "", inferenceError(err)
	}
	resolver := &hostedResolver{auth: a.resolver.auth, artifacts: a.resolver.artifacts, adapters: a.resolver.adapters}
	cfg := hostedWrapperConfig(c, in, resolver)
	if cfg.DescribeImages {
		d, err := a.resolver.auth.provider(ctx, scopeOf(in.Principal), in.Credential.AccountID)
		if err != nil {
			return nil, "", inferenceError(err)
		}
		view, _ := modelEvidence(d, in.Target.Model, time.Now())
		accepts := false
		for _, modality := range view.Modalities {
			if modality == "image" {
				accepts = true
			}
		}
		if preset, ok := providers.Lookup(d.Metadata.ProviderId); ok {
			for _, model := range preset.NoVisionModels {
				if model == in.Target.Model {
					accepts = false
					break
				}
			}
		}
		cfg.DescribeImages = !accepts
	}
	cfg.RecoveryPolicy = a.recovery
	wrapper, err := hosted.New(a.inner, cfg)
	if err != nil {
		return nil, "", err
	}
	return wrapper.Execute(ctx, in, e, s)
}

type hostedVisionDepth struct{}

// hostedVisionPrepare is also suitable for Cursor's no-vision model hook. It
// changes only active image parts and uses the independent resolver for each one.
func hostedVisionPrepare(auth *inferenceAdapter, art *artifacts.Service, adapters map[inference.Wire]inference.Adapter) func(context.Context, inference.AdapterInput, inference.AdapterExecutor) (json.RawMessage, error) {
	return func(ctx context.Context, in inference.AdapterInput, _ inference.AdapterExecutor) (json.RawMessage, error) {
		if ctx.Value(hostedVisionDepth{}) != nil {
			return nil, inference.ErrUnsupported
		}
		r := &hostedResolver{auth: auth, artifacts: art, adapters: adapters}
		raw, err := inference.TranslateRequestJSON(in.Body, in.Wire, inference.Chat)
		if err != nil {
			return nil, err
		}
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			return nil, inference.ErrUnsupported
		}
		count := 0
		cfg, err := readHostedSettings(ctx, auth.s, scopeOf(in.Principal))
		if err != nil {
			return nil, err
		}
		max := 8
		if cfg.Vision == nil || cfg.Vision.Enabled != nil && !cfg.Vision.GetEnabled() {
			return nil, inference.ErrUnsupported
		}
		if cfg.Vision.MaxDescriptionsPerTurn != nil {
			max = int(cfg.Vision.GetMaxDescriptionsPerTurn())
		}
		messages, _ := obj["messages"].([]any)
		for _, value := range messages {
			msg, _ := value.(map[string]any)
			parts, _ := msg["content"].([]any)
			for i, value := range parts {
				part, _ := value.(map[string]any)
				if part["type"] != "image_url" {
					continue
				}
				count++
				if count > max {
					return nil, inference.ErrUnsupported
				}
				args, _ := json.Marshal(map[string]any{"image_url": part["image_url"]})
				req := hosted.OperationRequest{Principal: in.Principal, RequestID: in.RequestID, Kind: hosted.Vision, Arguments: args}
				op, err := r.Resolve(ctx, req)
				if err != nil {
					return nil, err
				}
				result, err := op.Execute(ctx, req)
				if err != nil {
					return nil, err
				}
				if result.Failed {
					return nil, inference.ErrUnavailable
				}
				parts[i] = map[string]any{"type": "text", "text": result.Output}
			}
		}
		raw, err = json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		return inference.TranslateRequestJSON(raw, inference.Chat, in.Wire)
	}
}
