// SPDX-License-Identifier: Apache-2.0
package service

import (
	"math"
	"net/url"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func validateReasoningRetention(c *v1.ReasoningRetentionConfig) error {
	if c == nil {
		return nil
	}
	percent := c.GetMaxContextPercent()
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
		return invalid("reasoning retention context percent must be finite and between 0 and 100")
	}
	// uint32 excludes negative limits. Explicit zero and absence both select the
	// pinned default; keep presence intact for management round trips.
	return nil
}

func applyReasoningRetention(c *v1.ReasoningRetentionConfig, t *inference.Target) error {
	if err := validateReasoningRetention(c); err != nil {
		return err
	}
	t.ReasoningRetention = nil
	if c == nil || !c.Enabled {
		return nil
	}
	// Canonical ChatGPT compaction is forwarded natively and must not gain the
	// gateway's portable reasoning-retention behavior.
	u, err := url.Parse(t.BaseURL)
	if err == nil && (strings.EqualFold(u.Hostname(), "chatgpt.com") || strings.EqualFold(u.Hostname(), "chat.openai.com")) {
		return nil
	}
	window := int64(0)
	if t.Policy != nil {
		if value := t.Policy.ModelPolicy().ContextWindow; value != nil && *value > 0 {
			window = *value
		}
	}
	t.ReasoningRetention = &inference.ReasoningRetentionPolicy{Enabled: true, ContextWindow: window, MaxContextPercent: c.GetMaxContextPercent(), MaxTokens: int64(c.GetMaxTokens())}
	return nil
}
