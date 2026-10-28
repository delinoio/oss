// SPDX-License-Identifier: Apache-2.0
package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/pricing"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func TestOverrideValidationPreservesUnknownRates(t *testing.T) {
	id := core.ID()
	override := &v1.PriceOverride{Id: id, ProviderId: "custom", ModelId: "fixture", Rates: &v1.PriceRates{Currency: "USD", Input: ptr("0"), Output: ptr("0"), CacheRead: ptr("0"), CacheWrite: ptr("0")}}
	snapshot := func() *pricing.Snapshot {
		t.Helper()
		raw, err := json.Marshal(map[string]*v1.PriceOverride{id: override})
		acceptanceOK(t, err)
		overrides, err := priceOverrides(raw)
		acceptanceOK(t, err)
		result, err := pricing.NewSnapshot(overrides)
		acceptanceOK(t, err)
		return result
	}
	request := pricing.Request{Provider: "custom", Model: "fixture", Status: pricing.Reported, Usage: &pricing.Usage{InputTokens: ptr(int64(1)), OutputTokens: ptr(int64(1))}}
	estimate := snapshot().Estimate(request, time.Now())
	if !estimate.Known || estimate.Cost.Total != 0 {
		t.Fatal("confirmed zero rates lost")
	}
	override.Rates = &v1.PriceRates{Currency: "USD", Input: ptr("0")}
	estimate = snapshot().Estimate(request, time.Now())
	if estimate.Known {
		t.Fatal("missing output rate became known zero")
	}
	if estimate.EffectiveOptionalRates == nil || estimate.EffectiveOptionalRates.Output != nil {
		t.Fatal("missing rate presence lost")
	}
}
