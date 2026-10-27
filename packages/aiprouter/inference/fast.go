// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"net/http"
	"strings"
)

type FastWireKind string

const (
	FastServiceTier    FastWireKind = "service-tier"
	FastAnthropicSpeed FastWireKind = "anthropic-speed"
	FastCursorVariant  FastWireKind = "cursor-variant"
	FastModelVariant   FastWireKind = "model-variant"
)

// FastPolicy is the immutable authority resolved by routing AFTER final model,
// wire, auth and opt-in policy. Priority is canonicalToWire.priority. Model is
// the concrete OAuth lane only for model-variant; it never replaces logical routing.
type FastPolicy struct {
	Kind     FastWireKind
	Eligible bool
	Priority string
	Betas    []string
	Model    string
}

func applyFastBody(t Target, body object) error {
	if t.Policy != nil {
		metadata, _ := decode(t.Policy.Metadata())
		if supported, ok := metadata["supportsServiceTier"].(bool); ok && !supported {
			delete(body, "service_tier")
			if t.Fast {
				return &Error{400, "fast_not_supported", 0}
			}
		}
	}
	if !t.Fast {
		return nil
	}
	p := t.FastPolicy
	if p == nil || !p.Eligible {
		return &Error{400, "fast_not_supported", 0}
	}
	switch p.Kind {
	case FastServiceTier:
		if t.Wire != Chat && t.Wire != Responses || p.Priority == "" {
			return ErrUnsupported
		}
		body["service_tier"] = p.Priority
	case FastAnthropicSpeed:
		if t.Wire != Anthropic || p.Priority == "" {
			return ErrUnsupported
		}
		body["speed"] = p.Priority
		delete(body, "service_tier")
	case FastCursorVariant:
		if t.Wire != Wire("cursor-agent") {
			return ErrUnsupported
		}
		body["cursor_fast"] = true
		delete(body, "service_tier")
	case FastModelVariant:
		if t.Auth != OAuth || t.Wire != Chat && t.Wire != Responses || p.Model == "" {
			return ErrUnsupported
		}
		body["model"] = p.Model
		delete(body, "service_tier")
	default:
		return ErrUnsupported
	}
	return nil
}
func applyFastHeaders(t Target, headers http.Header) error {
	if !t.Fast || t.FastPolicy == nil || t.FastPolicy.Kind != FastAnthropicSpeed {
		return nil
	}
	seen := map[string]bool{}
	betas := []string{}
	for _, value := range strings.Split(headers.Get("anthropic-beta"), ",") {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			betas = append(betas, value)
		}
	}
	for _, value := range t.FastPolicy.Betas {
		if value == "" || strings.ContainsAny(value, ",\r\n\t ") {
			return &Error{400, "invalid_fast_policy", 0}
		}
		if !seen[value] {
			seen[value] = true
			betas = append(betas, value)
		}
	}
	if len(betas) > 0 {
		headers.Set("anthropic-beta", strings.Join(betas, ","))
	}
	return nil
}
