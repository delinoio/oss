// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

const normalQuotaCollectionCadence = 5 * time.Minute
const normalQuotaCollectionTimeout = 8 * time.Second

// normalQuotaCollectionBudget is the maximum number of physical support exchanges
// for the pinned collector. Reserve it before the first request send. A zero value
// means the source has no subscription-window publication into the JEV sink.
func normalQuotaCollectionBudget(d providerData) int {
	if d.Metadata == nil || d.Metadata.State != v1.ConnectionState_CONNECTION_STATE_READY {
		return 0
	}
	if d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH {
		if d.Metadata.ProviderId == "google-antigravity" && d.Credential != nil && d.Credential.ProjectID != "" {
			return 3
		}
		return 0
	}
	if d.Metadata.AuthKind != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY || d.Credential != nil {
		return 0
	}
	r, err := providers.Resolve(providerConnection(d), "__health__", providers.InboundResponses)
	if err != nil {
		return 0
	}
	// Keep this destination list aligned with providers.keyHealthProbe and pinned
	// vendor-probes-key.ts keyReport publishers. Display-only reports do not qualify.
	switch strings.TrimRight(r.BaseURL, "/") {
	case "https://opencode.ai/zen/go/v1", "https://api.cline.bot", "https://api.cline.bot/api/v1",
		"https://api.z.ai", "https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/anthropic", "https://api.z.ai/api/v1",
		"https://open.bigmodel.cn", "https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/v1",
		"https://api.synthetic.new/v2", "https://api.synthetic.new/openai/v1", "https://api.kimi.com/coding/v1":
		return 1
	case "https://ollama.com", "https://ollama.com/v1", "https://ollama.com/api":
		return 2
	case "https://api.commandcode.ai", "https://api.commandcode.ai/provider/v1":
		return 4
	}
	return 0
}

// Project only measured subscription windows, as quota-decision-publication.ts
// does. Balances, custom display counters and Antigravity model rows never imply
// family quota. Preserve the producer's clock instead of refreshing old evidence.
func normalQuotaCollectionSnapshot(provider string, health providers.Health) nativeQuotaSnapshot {
	out := nativeQuotaSnapshot{}
	if health.State != providers.HealthAvailable || health.ObservedAt.IsZero() {
		return out
	}
	for _, w := range health.Windows {
		if math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) || w.UsedPercent < 0 || w.UsedPercent > 100 {
			continue
		}
		row := nativeQuotaWindow{Percent: w.UsedPercent, ObservedAt: health.ObservedAt}
		switch w.Kind {
		case "five-hour":
			row.Window = "5h"
		case "weekly", "monthly":
			row.Window = w.Kind
		default:
			continue
		}
		if provider == "google-antigravity" {
			switch w.Model {
			case "gemini":
				row.Family = "gem"
			case "claude":
				row.Family = "cla"
			default:
				continue
			}
			if row.Window == "monthly" {
				continue
			}
		} else if w.Model != "" {
			continue
		}
		if !w.ResetAt.IsZero() {
			reset := w.ResetAt
			row.ResetAt = &reset
		}
		out.Windows = append(out.Windows, row)
		if len(out.Windows) > 64 {
			return nativeQuotaSnapshot{}
		}
	}
	return out
}

// collectNormalQuota is called only after a normal physical model response. JEV
// decision reads must never call this producer. The original request budget and
// account version authorize every support exchange and the resulting publication.
func (a *inferenceAdapter) collectNormalQuota(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential) error {
	return a.collectNormalQuotaWithExecutor(ctx, p, t, c, nil)
}

// The provider library's general auth timeout is longer than a quota read. Keep
// the quota deadline alive through body decoding, then release it on Close.
type normalQuotaExecutor struct{ providers.Executor }
type normalQuotaBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b normalQuotaBody) Close() error { b.cancel(); return b.ReadCloser.Close() }
func (e normalQuotaExecutor) Do(ctx context.Context, scope core.Scope, req *http.Request) (*http.Response, error) {
	bounded, cancel := context.WithTimeout(ctx, normalQuotaCollectionTimeout)
	response, err := e.Executor.Do(bounded, scope, req.Clone(bounded))
	if err != nil || response == nil || response.Body == nil {
		cancel()
		return response, err
	}
	response.Body = normalQuotaBody{response.Body, cancel}
	return response, nil
}

func (a *inferenceAdapter) collectNormalQuotaWithExecutor(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, test providers.Executor) error {
	if c.AccountID == "" || c.CredentialVersion == "" || !inference.HasPhysicalSendBudget(ctx) {
		return nil
	}
	d, version, err := a.providerVersion(ctx, scopeOf(p), c.AccountID)
	if err != nil {
		return inferenceError(err)
	}
	if version != c.CredentialVersion {
		return inference.ErrNoCredential
	}
	allowance := normalQuotaCollectionBudget(d)
	if allowance == 0 {
		return nil
	}
	lease, err := a.current(p)
	if err != nil {
		return err
	}
	claim, err := a.s.ClaimNormalQuotaCollection(ctx, lease, c.AccountID, version, 45*time.Second)
	if errors.Is(err, store.ErrBusy) {
		return nil
	}
	if err != nil {
		return inferenceError(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(allowance)*normalQuotaCollectionTimeout)
	defer cancel()
	var authorityErr error
	executor := &providerExecutor{store: a.s, scope: lease.Principal.Scope, test: test, gate: func(cctx context.Context, start func(context.Context) error) error {
		e := a.s.WithAccountDispatch(cctx, lease, c.AccountID, version, start)
		// Provider protocol wrappers intentionally hide transport details. Preserve a
		// current authorization refusal independently of those coarse probe errors.
		if errors.Is(e, store.ErrForbidden) || errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrExpired) || errors.Is(e, store.ErrReauthentication) {
			authorityErr = e
		}
		return e
	}}
	health, probeErr := providers.NewAuth(normalQuotaExecutor{executor}).ProbeConnectionHealth(bounded, lease.Principal.Scope, providerConnection(d))
	if authorityErr != nil {
		return inferenceError(authorityErr)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var update []byte
	snapshot := normalQuotaCollectionSnapshot(d.Metadata.ProviderId, health)
	if probeErr == nil && len(snapshot.Windows) > 0 {
		update, err = json.Marshal(snapshot)
		if err != nil {
			return inference.ErrUnavailable
		}
	}
	// A transient failure or display-only fallback retains the old producer clocks.
	// A rejected key report withdraws old evidence; it does not revoke credentials.
	var refusal *providers.AuthError
	terminal := health.State == providers.HealthRejected || errors.As(probeErr, &refusal) && refusal.Status >= 400 && refusal.Status < 500 && refusal.Status != 408 && refusal.Status != 429
	if d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY && terminal {
		update = []byte(`{"windows":[]}`)
	}
	if probeErr != nil && a.telemetry != nil {
		a.telemetry.Event(ctx, "normal_quota_collection_unavailable")
	}
	return inferenceError(a.s.CompleteNormalQuotaCollection(ctx, lease, c.AccountID, version, claim, normalQuotaCollectionCadence, update))
}
