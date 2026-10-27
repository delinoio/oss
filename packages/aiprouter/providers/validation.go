// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"net/url"
	"strings"
)

type ValidationState string

const (
	ValidationAccepted ValidationState = "accepted"
	ValidationRejected ValidationState = "rejected"
	ValidationUnknown  ValidationState = "unknown"
)

type Validation struct {
	State  ValidationState
	Models []string
}
type DiscoveryPredicate struct {
	Path            []string `json:"path"`
	EqualsAny       []any    `json:"equalsAny"`
	ContainsAny     []any    `json:"containsAny"`
	ContainsAll     []any    `json:"containsAll"`
	CaseInsensitive bool     `json:"caseInsensitive"`
}
type DiscoveryFilter struct {
	AllOf  []DiscoveryPredicate `json:"allOf"`
	AnyOf  []DiscoveryPredicate `json:"anyOf"`
	NoneOf []DiscoveryPredicate `json:"noneOf"`
}
type DiscoverySpec struct {
	URL           string            `json:"url"`
	Path          string            `json:"path"`
	Query         map[string]string `json:"query"`
	EnvelopeKey   string            `json:"envelopeKey"`
	IDField       string            `json:"idField"`
	StripIDPrefix string            `json:"stripIdPrefix"`
	Filter        *DiscoveryFilter  `json:"filter"`
	Format        string            `json:"format"`
	MaxModels     int               `json:"maxModels"`
}

// ValidateRemote uses a non-billable model-discovery request. Accepted records an
// authenticated discovery response, not inference/entitlement or live acceptance.
// Public catalogs and specialized wires return Unknown instead of false success.
func (a *Auth) ValidateRemote(ctx context.Context, s core.Scope, c Connection) (Validation, error) {
	if !validScope(s) {
		return Validation{}, ErrScope
	}
	if c.Credential != nil && c.Credential.Scope != s {
		return Validation{}, ErrScope
	}
	if p, ok := Lookup(c.Provider); (ok && p.CredentialOnly) || (c.Provider == "custom" && c.Wire == JEVDecision) {
		if err := ValidateConnection(c); err != nil {
			return Validation{}, err
		}
		return Validation{State: ValidationUnknown}, nil
	}
	if c.Provider == "openai" && c.Credential != nil {
		catalog, _, err := a.Discover(ctx, s, c)
		if err != nil {
			var ae *AuthError
			if errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403) {
				return Validation{State: ValidationRejected}, nil
			}
			return Validation{}, err
		}
		out := Validation{State: catalog.State}
		for _, m := range catalog.Models {
			out.Models = append(out.Models, m.ID)
		}
		return out, nil
	}
	switch c.Provider {
	case "cursor", "kiro", "google-antigravity", "workbuddy-ai", "devin":
		catalog, _, err := a.Discover(ctx, s, c)
		if err != nil {
			var ae *AuthError
			if errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403) {
				return Validation{State: ValidationRejected}, nil
			}
			return Validation{}, err
		}
		result := Validation{State: catalog.State}
		for _, m := range catalog.Models {
			result.Models = append(result.Models, m.ID)
		}
		return result, nil
	}
	r, e := Resolve(c, "__validation__", InboundResponses)
	if e != nil {
		return Validation{}, e
	}
	p := r.Preset
	unknown := Validation{State: ValidationUnknown, Models: append([]string(nil), p.Models...)}
	if p.APIKeyValidation == "unknown" || p.CredentialOnly || (p.LiveModels != nil && !*p.LiveModels) {
		return unknown, nil
	}
	var data struct {
		ModelDiscovery DiscoverySpec `json:"modelDiscovery"`
	}
	if len(p.Metadata) > 0 {
		if e := json.Unmarshal(p.Metadata, &data); e != nil {
			return Validation{}, ErrResponse
		}
	}
	d := data.ModelDiscovery
	// A changed destination must not send its credential to a catalog-owned absolute URL.
	if p.BaseURL != "" && strings.TrimRight(r.BaseURL, "/") != strings.TrimRight(p.BaseURL, "/") {
		d = DiscoverySpec{}
	}
	if d.Format != "" {
		return unknown, nil
	}
	base := strings.TrimRight(r.BaseURL, "/")
	endpoint := base + "/models"
	switch r.Wire {
	case Chat, Responses:
	case Anthropic:
		if !strings.HasSuffix(base, "/v1") {
			endpoint = base + "/v1/models"
		}
	case Google:
		if strings.Contains(base, "generativelanguage.googleapis.com") {
			if strings.TrimRight(base, "/") == "https://generativelanguage.googleapis.com" {
				endpoint = base + "/v1beta/models"
			} else {
				endpoint = base + "/models"
			}
		} else {
			return unknown, nil
		}
	case "ollama-native":
		endpoint = base + "/api/tags"
		d.EnvelopeKey = "models"
		d.IDField = "name"
	default:
		return unknown, nil
	}
	// Absolute catalog endpoints are registry-owned and never inherited by custom destinations.
	if d.URL != "" {
		endpoint = d.URL
	} else if d.Path != "" {
		u, _ := url.Parse(base + "/")
		v, e := url.Parse(d.Path)
		if e != nil {
			return Validation{}, ErrResponse
		}
		endpoint = u.ResolveReference(v).String()
	}
	u, e := url.Parse(endpoint)
	if e != nil {
		return Validation{}, ErrResponse
	}
	q := u.Query()
	for k, v := range d.Query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	m, status, e := a.request(ctx, s, "GET", u.String(), nil, r.Headers)
	if e != nil {
		return Validation{}, e
	}
	if status == 401 || status == 403 {
		return Validation{State: ValidationRejected}, nil
	}
	if status < 200 || status >= 300 {
		return Validation{}, authFailure(status, m)
	}
	envelope := d.EnvelopeKey
	if envelope == "" {
		envelope = "data"
	}
	field := d.IDField
	if field == "" {
		field = "id"
	}
	if r.Wire == Google {
		envelope = "models"
		field = "name"
	}
	rows, ok := m[envelope].([]any)
	if !ok || len(rows) > 10000 {
		return Validation{}, ErrResponse
	}
	models := []string{}
	seen := map[string]bool{}
	for _, v := range rows {
		row, ok := v.(map[string]any)
		if !ok || !discoveryEligible(row, d.Filter) {
			continue
		}
		id := str(row, field)
		if d.StripIDPrefix != "" {
			id = strings.TrimPrefix(id, d.StripIDPrefix)
		}
		if r.Wire == Google {
			id = strings.TrimPrefix(id, "models/")
		}
		if !safeString(id) || len(id) > 512 || strings.TrimSpace(id) != id || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
		if d.MaxModels > 0 && len(models) >= d.MaxModels {
			break
		}
	}
	return Validation{State: ValidationAccepted, Models: models}, nil
}
func discoveryEligible(row map[string]any, f *DiscoveryFilter) bool {
	if f == nil {
		return true
	}
	for _, p := range f.AllOf {
		if !predicate(row, p) {
			return false
		}
	}
	if len(f.AnyOf) > 0 {
		match := false
		for _, p := range f.AnyOf {
			match = match || predicate(row, p)
		}
		if !match {
			return false
		}
	}
	for _, p := range f.NoneOf {
		if predicate(row, p) {
			return false
		}
	}
	return true
}
func predicate(row map[string]any, p DiscoveryPredicate) bool {
	values := []any{row}
	for _, key := range p.Path {
		next := []any{}
		for _, v := range values {
			if key == "*" {
				if a, ok := v.([]any); ok {
					if len(a) > 256 {
						a = a[:256]
					}
					next = append(next, a...)
				}
			} else if m, ok := v.(map[string]any); ok {
				next = append(next, m[key])
			}
		}
		values = next
	}
	eq := func(a, b any) bool {
		as, aok := a.(string)
		bs, bok := b.(string)
		if aok && bok {
			if p.CaseInsensitive {
				return strings.EqualFold(as, bs)
			}
			return as == bs
		}
		switch av := a.(type) {
		case bool:
			bv, ok := b.(bool)
			return ok && av == bv
		case float64:
			bv, ok := b.(float64)
			return ok && av == bv
		}
		return false
	}
	matches := func(target any, substring bool) bool {
		for _, v := range values {
			if eq(v, target) {
				return true
			}
			if substring {
				if a, ok := v.([]any); ok {
					for _, x := range a {
						if eq(x, target) {
							return true
						}
					}
				}
				vs, vok := v.(string)
				ts, tok := target.(string)
				if vok && tok {
					if p.CaseInsensitive {
						vs = strings.ToLower(vs)
						ts = strings.ToLower(ts)
					}
					if strings.Contains(vs, ts) {
						return true
					}
				}
			}
		}
		return false
	}
	if len(p.EqualsAny) > 0 {
		for _, v := range p.EqualsAny {
			if matches(v, false) {
				return true
			}
		}
		return false
	}
	if len(p.ContainsAny) > 0 {
		for _, v := range p.ContainsAny {
			if matches(v, true) {
				return true
			}
		}
		return false
	}
	for _, v := range p.ContainsAll {
		if !matches(v, true) {
			return false
		}
	}
	return len(p.ContainsAll) > 0
}
