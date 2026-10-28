// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf16"
)

// CompactionHandoff contains a currently authorized concrete route and credential,
// never a policy/combo selector. The service revalidates config, key and account version.
type CompactionHandoff struct {
	Target     Target
	Credential Credential
}
type CompactionHandoffs interface {
	Capture(context.Context, Principal, Target, Credential) error
	Lookup(context.Context, Principal, Target, HTTPFailure) (*CompactionHandoff, error)
	Clear(context.Context, Principal) error
}
type compactionHandoffKey struct{}

func validNativeCompact(out object) bool {
	if status := str(out["status"]); status != "" && status != "completed" {
		return false
	}
	if out["error"] != nil {
		return false
	}
	meaningful := false
	for _, v := range arr(out["output"]) {
		m := obj(v)
		switch str(m["type"]) {
		case "compaction", "compaction_summary", "context_compaction":
			if strings.TrimSpace(str(m["encrypted_content"])) == "" {
				return false
			}
			meaningful = true
		case "reasoning":
		case "message", "":
			role := str(m["role"])
			if role != "user" && role != "assistant" && role != "system" && role != "developer" {
				return false
			}
			if text, ok := m["content"].(string); ok {
				meaningful = meaningful || strings.TrimSpace(text) != ""
			} else {
				for _, part := range arr(m["content"]) {
					if strings.TrimSpace(str(obj(part)["text"])) != "" {
						meaningful = true
					}
				}
			}
		default:
			return false
		}
	}
	return meaningful
}

func (h *Handler) tryCompactionHandoff(ctx context.Context, p Principal, t Target, f HTTPFailure, b object, route Route, rec *UsageRecord) (*compactBuffer, error) {
	if h.c.CompactionHandoffs == nil || ctx.Value(compactionHandoffKey{}) != nil || rec.Sends >= route.MaxSends {
		return nil, nil
	}
	// The service classifies body-inferred quota on any refusal. Native 404
	// retains its separate same-route synthetic fallback.
	if f.Status < 400 || f.Status == 404 {
		return nil, nil
	}
	next, e := h.c.CompactionHandoffs.Lookup(ctx, p, t, f)
	if e != nil {
		return nil, e
	}
	if next == nil {
		return nil, nil
	}
	if next.Target.Provider == t.Provider && compactionServingModel(next.Target) == compactionServingModel(t) && next.Target.BaseURL == t.BaseURL {
		return nil, nil
	}
	if next.Credential.AccountID == "" || next.Credential.CredentialVersion == "" || next.Target.Model == "" || len(utf16.Encode([]rune(next.Target.Model))) > 512 || compactionServingModel(next.Target) == "" || len(utf16.Encode([]rune(compactionServingModel(next.Target)))) > 512 {
		return nil, ErrContinuation
	}
	if e = h.guard(ctx, p); e != nil {
		return nil, e
	}
	child := context.WithValue(ctx, compactionHandoffKey{}, *next)
	child = context.WithValue(child, compactionRouteKey{}, Route{Targets: []Target{next.Target}, MaxSends: route.MaxSends})
	child = context.WithValue(child, continuationKey{}, Continuation{Provider: next.Target.Provider, AccountID: next.Credential.AccountID, RoutedModel: next.Target.Model, Model: str(b["model"])})
	output := &compactBuffer{header: make(map[string][]string)}
	err := h.execute(child, output, p, Responses, Compact, b, rec)
	if err != nil {
		// Cancellation, authority and storage refusal remain fatal. An unsuccessful
		// authorized upstream fallback preserves the original native refusal.
		pe := publicError(err)
		if ctx.Err() != nil || (pe.Code != "upstream_refused" && pe.Code != "upstream_rate_limited" && pe.Code != "no_eligible_credential") {
			return nil, err
		}
		return nil, nil
	}
	var parsed object
	if json.Unmarshal(output.body.Bytes(), &parsed) != nil || !validNativeCompact(parsed) {
		return nil, nil
	}
	return output, nil
}

func compactionServingModel(t Target) string {
	if t.Fast && t.FastPolicy != nil && t.FastPolicy.Kind == FastModelVariant {
		return t.FastPolicy.Model
	}
	return t.Model
}
