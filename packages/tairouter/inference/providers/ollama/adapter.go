// SPDX-License-Identifier: Apache-2.0
// Native protocol behavior is adapted from opencodex; see LICENSE.upstream.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const Wire inference.Wire = "ollama-native"
const MaxRequestBytes = 32 << 20
const MaxFrameBytes = 32 << 20
const MaxCallArgumentBytes = 2 << 20
const MaxPendingCalls = 128
const MaxLateAttributionBytes = 256 << 10
const MaxRetainedBytes = 32 << 20

type object = map[string]any

// Adapter is immutable. Each decoder owns its tool identities and bounded buffers.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ inference.Adapter = (*Adapter)(nil)
var _ inference.ContextualAdapter = (*Adapter)(nil)

func obj(v any) object      { m, _ := v.(map[string]any); return m }
func arr(v any) []any       { a, _ := v.([]any); return a }
func str(v any) string      { s, _ := v.(string); return s }
func bad(code string) error { return &inference.Error{Status: 400, Code: code} }
func malformed() error      { return &inference.Error{Status: 502, Code: "invalid_ollama_native_payload"} }
func decode(b []byte) (object, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m object
	if e := d.Decode(&m); e != nil || m == nil {
		return nil, malformed()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, malformed()
	}
	return m, nil
}

func endpoint(base string) (*url.URL, string, error) {
	u, e := url.Parse(strings.TrimSpace(base))
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", inference.ErrDestination
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	kind := "custom"
	switch host {
	case "localhost", "127.0.0.1", "::1":
		kind = "local"
	case "ollama.com":
		kind = "cloud"
	case "www.ollama.com":
		return nil, "", inference.ErrDestination
	}
	if kind == "cloud" {
		if u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
			return nil, "", inference.ErrDestination
		}
		u.Host = "ollama.com"
	}
	p := strings.TrimRight(u.EscapedPath(), "/")
	if p != "" && p != "/api" && p != "/api/chat" && !(kind != "custom" && (p == "/v1" || p == "/v1/chat/completions")) {
		return nil, "", inference.ErrDestination
	}
	u.Path = "/api/chat"
	u.RawPath = ""
	return u, kind, nil
}

var sensitiveHeader = regexp.MustCompile(`(?i)^(?:authorization|proxy-authorization|cookie|set-cookie|set-cookie2|api[-_]?key|x-api-key|x-goog-api-key|x-amz-security-token|x-dsh-auth-token|x-openai-agent-assertion|copilot[-_]?session[-_]?token|session[-_]?token|access[-_]?token|refresh[-_]?token|id[-_]?token|token|secret|client[-_]?secret|password|profile[-_]?arn|exa[-_]?api[-_]?key)$`)

func sensitive(k string) bool { return sensitiveHeader.MatchString(strings.TrimSpace(k)) }

func (a *Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	if e := ctx.Err(); e != nil {
		return inference.AdapterRequest{}, e
	}
	if in.Operation != inference.Generate {
		return inference.AdapterRequest{}, inference.ErrUnsupported
	}
	if len(in.Body) > MaxRequestBytes {
		return inference.AdapterRequest{}, bad("ollama_request_limit")
	}
	u, kind, e := endpoint(in.Target.BaseURL)
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	if in.Target.Auth != "" && in.Target.Auth != inference.APIKey && in.Target.Auth != inference.OAuth && in.Target.Auth != inference.Anonymous {
		return inference.AdapterRequest{}, bad("ollama_forward_auth_unsupported")
	}
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, application/x-ndjson"}}
	if kind != "local" && strings.TrimSpace(in.Credential.Secret) != "" {
		h.Set("Authorization", "Bearer "+strings.TrimSpace(in.Credential.Secret))
	}
	hasCredential := strings.TrimSpace(in.Credential.Secret) != ""
	for _, headers := range []http.Header{in.Target.Headers, in.Credential.Headers} {
		for k, vs := range headers {
			if sensitive(k) {
				hasCredential = true
				if kind == "local" {
					h[http.CanonicalHeaderKey(k)] = nil
					continue
				}
			}
			h[http.CanonicalHeaderKey(k)] = append([]string(nil), vs...)
		}
	}
	if kind == "local" {
		h["Authorization"] = nil
	} else if u.Scheme == "http" && hasCredential {
		return inference.AdapterRequest{}, bad("ollama_plaintext_credentials_refused")
	} else if in.Target.Auth != inference.Anonymous && strings.TrimSpace(in.Credential.Secret) == "" {
		return inference.AdapterRequest{}, inference.ErrNoCredential
	}
	data, e := inference.TranslateRequestJSON(in.Body, in.Wire, inference.Chat)
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	b, e := decode(data)
	if e != nil {
		return inference.AdapterRequest{}, bad("invalid_json")
	}
	if e = normalizeTools(b); e != nil {
		return inference.AdapterRequest{}, e
	}
	policy, e := policyFor(in)
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	if e = applyPolicy(b, policy); e != nil {
		return inference.AdapterRequest{}, e
	}
	h[http.CanonicalHeaderKey(PolicyHeader)] = nil
	model := in.Target.Model
	if model == "" {
		model = str(b["model"])
	}
	if policy.StripModelSuffix {
		end := strings.TrimRight(model, " \t\r\n")
		if strings.HasSuffix(end, "]") {
			if i := strings.LastIndex(end, "["); i > 0 {
				model = end[:i]
			}
		}
	}
	body, e := buildBody(b, kind, model)
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	data, e = json.Marshal(body)
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	if len(data) > MaxRequestBytes {
		return inference.AdapterRequest{}, bad("ollama_request_limit")
	}
	return inference.AdapterRequest{URL: u.String(), Method: http.MethodPost, Headers: h, Body: data, ResponseWire: inference.Chat}, nil
}
