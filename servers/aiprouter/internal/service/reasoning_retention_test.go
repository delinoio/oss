// SPDX-License-Identifier: Apache-2.0
package service

import (
	"math"
	"testing"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/policy"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

func TestReasoningRetentionProjection(t *testing.T) {
	resolved, err := policy.Resolve(policy.Config{ProviderID: "custom", Model: "fixture", BaseURL: "https://provider.example/v1", Wire: providers.Chat, Overrides: []byte(`{"contextWindow":32000}`)})
	if err != nil {
		t.Fatal(err)
	}
	target := inference.Target{BaseURL: "https://provider.example/v1", Policy: &resolved}
	cfg := &v1.ReasoningRetentionConfig{Enabled: true}
	if err = applyReasoningRetention(cfg, &target); err != nil {
		t.Fatal(err)
	}
	got := target.ReasoningRetention
	if got == nil || !got.Enabled || got.ContextWindow != 32000 || got.MaxContextPercent != 0 || got.MaxTokens != 0 {
		t.Fatalf("trusted context or default semantics lost: %+v", got)
	}
	target.Policy = nil
	if err = applyReasoningRetention(cfg, &target); err != nil || target.ReasoningRetention.ContextWindow != 0 {
		t.Fatal("unknown context invented")
	}
	pct, tokens := 12.5, uint32(42)
	cfg.MaxContextPercent = &pct
	cfg.MaxTokens = &tokens
	if err = applyReasoningRetention(cfg, &target); err != nil || target.ReasoningRetention.MaxContextPercent != 12.5 || target.ReasoningRetention.MaxTokens != 42 {
		t.Fatal("explicit limits lost")
	}
	if err = applyReasoningRetention(nil, &target); err != nil || target.ReasoningRetention != nil {
		t.Fatal("omitted replacement retained prior policy")
	}
	for _, origin := range []string{"https://chatgpt.com/backend-api/codex", "https://chat.openai.com/backend-api/codex"} {
		target.BaseURL = origin
		if err = applyReasoningRetention(cfg, &target); err != nil || target.ReasoningRetention != nil {
			t.Fatal("native compaction acquired gateway retention")
		}
	}
	for _, percent := range []float64{-1, 101, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cfg.MaxContextPercent = &percent
		if validateReasoningRetention(cfg) == nil {
			t.Fatal("invalid percent accepted")
		}
	}
	for _, percent := range []float64{0, 0.1, 20, 100} {
		cfg.MaxContextPercent = &percent
		if err = validateReasoningRetention(cfg); err != nil {
			t.Fatal(err)
		}
	}
}
