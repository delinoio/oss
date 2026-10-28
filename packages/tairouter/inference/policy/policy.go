// SPDX-License-Identifier: Apache-2.0
// Policy algorithms follow the pinned opencodex source; see NOTICE and LICENSE-MIT.
package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/providers"
)

const Revision = "88aaa754566d119b6b8fc898f8fb47602387d82e"

var ErrConfig = errors.New("invalid provider model policy")

// Config contains no credentials. Registry is the selected Preset.Metadata.
// Overrides uses pinned camelCase field names and retains explicit false values.
type Config struct {
	ProviderID, Model, BaseURL string
	Wire                       providers.Wire
	Inbound                    providers.Inbound
	Auth                       providers.AuthKind
	Registry, Overrides        json.RawMessage
}

// Resolved is detached from its inputs. Its private policy is read-only and can be
// shared across requests. Resolve once per selected destination/model/auth/inbound.
type Resolved struct {
	ProviderID, Model, BaseURL, Endpoint string
	Wire                                 providers.Wire
	Inbound                              providers.Inbound
	Auth                                 providers.AuthKind
	TransportMatchedRegistry             bool
	fields                               map[string]any
	fastOff                              bool
}

func decode(raw []byte) (map[string]any, error) {
	out := map[string]any{}
	if len(raw) == 0 {
		return out, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&out); err != nil || out == nil {
		return nil, ErrConfig
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrConfig
	}
	return out, nil
}
func str(v any) string         { s, _ := v.(string); return s }
func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func list(v any) []any         { a, _ := v.([]any); return a }
func flag(v any) bool          { b, _ := v.(bool); return b }
func endpoint(s string) string {
	u, e := url.Parse(strings.TrimSpace(s))
	if e != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/"))
}
func validBase(s string) bool {
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(s, "{}\r\n")
}
func generic(w providers.Wire) bool { return w == providers.Chat || w == providers.Responses }

// Resolve applies destination-scoped defaults, explicit overrides, then hard wire pins.
func Resolve(c Config) (Resolved, error) {
	r, e := decode(c.Registry)
	if e != nil {
		return Resolved{}, e
	}
	o, e := decode(c.Overrides)
	if e != nil {
		return Resolved{}, e
	}
	if c.BaseURL == "" {
		c.BaseURL = str(r["baseUrl"])
	}
	if !validBase(c.BaseURL) {
		return Resolved{}, ErrConfig
	}
	if c.Wire == "" {
		c.Wire = providers.Wire(str(r["adapter"]))
	}
	if c.Wire == "" {
		c.Wire = providers.Chat
	}
	if c.Auth == "" {
		c.Auth = providers.AuthKind(str(r["authKind"]))
	}
	if c.Inbound == "" {
		c.Inbound = providers.InboundResponses
	}
	matched := endpoint(c.BaseURL) == endpoint(str(r["baseUrl"])) && string(c.Wire) == str(r["adapter"])
	for _, a := range list(r["destinationAliases"]) {
		v := obj(a)
		if endpoint(c.BaseURL) == endpoint(str(v["baseUrl"])) && string(c.Wire) == str(v["adapter"]) {
			matched = true
		}
	}
	for _, choice := range list(r["baseUrlChoices"]) {
		if endpoint(c.BaseURL) == endpoint(str(obj(choice)["baseUrl"])) && string(c.Wire) == str(r["adapter"]) {
			matched = true
		}
	}
	f := map[string]any{}
	if matched {
		for k, v := range r {
			f[k] = v
		}
	}
	// Classification lists are additive; scalar false and empty opt-in lists are not defaults.
	for k, v := range o {
		if isUnion(k) {
			seen := map[string]bool{}
			a := []any{}
			for _, part := range [][]any{list(f[k]), list(v)} {
				for _, x := range part {
					s := str(x)
					if !seen[s] {
						a = append(a, x)
						seen[s] = true
					}
				}
			}
			f[k] = a
		} else if rm, om := obj(f[k]), obj(v); rm != nil && om != nil && (strings.HasPrefix(k, "model") || k == "reasoningEffortMap") {
			f[k] = mergeMap(rm, om)
		} else {
			f[k] = v
		}
	}

	if x, ok := o["responsesItemIdRepair"]; ok && obj(x) == nil {
		return Resolved{}, ErrConfig
	}
	if matched && c.Auth == providers.Key && flag(r["allowKeyAuthOverride"]) {
		for k, v := range obj(r["keyAuthServiceTier"]) {
			if _, explicit := o[k]; !explicit {
				if old := obj(f[k]); old != nil && obj(v) != nil {
					f[k] = mergeMap(old, obj(v))
				} else {
					f[k] = v
				}
			}
		}
	}
	fastOff := o["fastEnabled"] == false || (o["fastEnabled"] == nil && flag(r["fastOptIn"]))
	if fastOff {
		f["supportsServiceTier"] = false
	}

	if c.ProviderID == "openai-apikey" && matched {
		for _, key := range []string{"modelContextWindows", "modelMaxInputTokens"} {
			for model, v := range obj(f[key]) {
				n, ok := number(v)
				if !ok {
					continue
				}
				for original, cap := range obj(r[key]) {
					if strings.EqualFold(model, original) {
						limit, ok := number(cap)
						if ok && n > limit {
							obj(f[key])[model] = cap
						}
					}
				}
			}
		}
	}
	w := c.Wire
	if matched && generic(w) {
		var d providers.ModelWireDefault
		if raw, ok := obj(r["modelWireDefaults"])[strings.ToLower(strings.TrimSpace(c.Model))]; ok {
			b, _ := json.Marshal(raw)
			if json.Unmarshal(b, &d) == nil && generic(d.Wire) && (len(d.Inbound) == 0 || hasInbound(d.Inbound, c.Inbound)) && (len(d.AuthModes) == 0 || hasAuth(d.AuthModes, c.Auth)) {
				w = d.Wire
			}
		}
	}
	if v := providers.Wire(str(obj(o["modelAdapters"])[c.Model])); v != "" {
		if !generic(v) {
			return Resolved{}, ErrConfig
		}
		w = v
	}
	if c.Auth == providers.Forward && endpoint(c.BaseURL) == "https://chatgpt.com/backend-api/codex" {
		w = c.Wire
	}
	switch c.ProviderID {
	case "workbuddy-ai", "dsh-account":
		w = providers.Wire(c.ProviderID)
	case "commandcode", "tokenlab":
		want := "https://api.commandcode.ai/provider/v1"
		if c.ProviderID == "tokenlab" {
			want = "https://api.tokenlab.sh/v1"
		}
		if endpoint(c.BaseURL) == want && strings.HasPrefix(strings.ToLower(c.Model), "claude-") {
			w = providers.Anthropic
		}
	case "opencode-go":
		c.BaseURL = "https://opencode.ai/zen/go/v1"
		switch c.Model {
		case "claude-haiku-5-5", "minimax-m2.5", "minimax-m2.7", "minimax-m3", "union-alpha":
			w = providers.Anthropic
		case "qwen3.7-plus", "qwen3.8-max", "qwen3.8-flash":
			if c.Auth == providers.Key || c.Auth == "" {
				w = providers.Anthropic
			}
		case "gemini-4-argon-contributor":
			if c.Auth == providers.Key || c.Auth == "" {
				w = providers.Google
			}
		}
	case "opencode-zen":
		if (c.Auth == providers.Key || c.Auth == "") && (endpoint(c.BaseURL) == "https://opencode.ai/zen/v1" || endpoint(c.BaseURL) == "https://opencode.ai/inference/v1") {
			if strings.HasPrefix(strings.ToLower(c.Model), "claude-") {
				w = providers.Anthropic
			}
			if strings.HasPrefix(strings.ToLower(c.Model), "gemini-") && (f["googleMode"] == nil || f["googleMode"] == "ai-studio") {
				w = providers.Google
			}
		}
	}
	path := ""
	switch w {
	case providers.Chat:
		path = str(f["chatCompletionsPath"])
		if path == "" {
			path = "/chat/completions"
		}
	case providers.Responses:
		path = str(f["responsesPath"])
		if path == "" {
			path = "/responses"
		}
	case providers.Anthropic:
		path = "/v1/messages"
	}
	if path != "" {
		u, e := url.Parse(path)
		if e != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || u.RawQuery != "" || u.Fragment != "" {
			return Resolved{}, ErrConfig
		}
	}
	destination := strings.TrimRight(c.BaseURL, "/") + path
	if w == providers.Anthropic {
		destination = anthropicMessagesURL(c.BaseURL)
	}
	return Resolved{ProviderID: c.ProviderID, Model: c.Model, BaseURL: c.BaseURL, Endpoint: destination, Wire: w, Inbound: c.Inbound, Auth: c.Auth, TransportMatchedRegistry: matched, fields: f, fastOff: fastOff}, nil
}

// Keep proxy path prefixes, but normalize versioned endpoint suffixes exactly as
// the pinned Anthropic adapter does. BaseURL remains the selected route identity.
func anthropicMessagesURL(base string) string {
	root := strings.TrimRight(strings.TrimSpace(base), "/")
	for _, suffix := range []string{"/v1/messages", "/v1"} {
		if strings.HasSuffix(strings.ToLower(root), suffix) {
			root = root[:len(root)-len(suffix)]
		}
	}
	return strings.TrimRight(root, "/") + "/v1/messages"
}

func hasInbound(a []providers.Inbound, v providers.Inbound) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
func hasAuth(a []providers.AuthKind, v providers.AuthKind) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
func isUnion(k string) bool {
	switch k {
	case "noVisionModels", "noReasoningModels", "noTemperatureModels", "noTopPModels", "noStopModels", "noPenaltyModels", "noJsonSchemaModels", "noStructuredOutputModels", "autoToolChoiceOnlyModels", "preserveReasoningContentModels", "requiresReasoningPlaceholderModels", "reasoningSplitModels", "reasoningDetailsModels", "thinkingToggleModels", "thinkingBudgetModels":
		return true
	}
	return false
}
func mergeMap(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		owned := false
		for j := range b {
			if strings.EqualFold(k, j) {
				owned = true
			}
		}
		if !owned {
			out[k] = v
		}
	}
	for k, v := range b {
		old := obj(a[k])
		if old == nil {
			for key, value := range a {
				if strings.EqualFold(key, k) {
					old = obj(value)
					break
				}
			}
		}
		if old != nil && obj(v) != nil {
			out[k] = mergeMap(old, obj(v))
		} else {
			out[k] = v
		}
	}
	return out
}
func (p Resolved) in(k string, exact bool) bool {
	for _, v := range list(p.fields[k]) {
		s := str(v)
		if s == p.Model || (!exact && strings.Contains(p.Model, ":") && s == strings.SplitN(p.Model, ":", 2)[0]) {
			return true
		}
	}
	return false
}
func (p Resolved) modelValue(k string) any {
	m := obj(p.fields[k])
	if v, ok := m[p.Model]; ok {
		return v
	}
	if i := strings.IndexByte(p.Model, ':'); i > 0 {
		if v, ok := m[p.Model[:i]]; ok {
			return v
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.EqualFold(k, p.Model) {
			return m[k]
		}
	}
	return nil
}

// Metadata returns a detached snapshot for capability consumers; never includes credentials.
func (p Resolved) Metadata() json.RawMessage { b, _ := json.Marshal(p.fields); return b }
