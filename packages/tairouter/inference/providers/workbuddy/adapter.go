// SPDX-License-Identifier: Apache-2.0
// WorkBuddy wire policy adapted from opencodex 88aaa754; see LICENSE.upstream.
package workbuddy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const Wire inference.Wire = "workbuddy-ai"
const BaseURL = "https://www.workbuddy.ai/v2"
const maxBytes = 32 << 20

// Adapter holds only a process-lifetime privacy salt, never account or request state.
type Adapter struct{ salt [32]byte }

// The server may construct adapters per HTTP request. Share only this immutable
// random salt so a tool continuation keeps its conversation/user-turn identifiers.
// No account identity, token, prompt, or alias table is stored at package scope.
var sessionSalt = func() (salt [32]byte) { rand.Read(salt[:]); return }()

func New() *Adapter { return &Adapter{salt: sessionSalt} }

var _ inference.Adapter = (*Adapter)(nil)
var _ inference.ContextualAdapter = (*Adapter)(nil)

func bad(code string) error { return &inference.Error{Status: 400, Code: code} }
func valid(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for _, c := range []byte(s) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func destination(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil {
		return false
	}
	path := strings.TrimRight(u.Path, "/")
	return u.Scheme == "https" && strings.EqualFold(u.Hostname(), "www.workbuddy.ai") && (u.Port() == "" || u.Port() == "443") && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawPath == "" && (path == "" || path == "/v2" || path == "/v2/chat/completions")
}
func decode(data []byte) (object, error) {
	if len(data) > maxBytes {
		return nil, bad("workbuddy_request_limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var m object
	if d.Decode(&m) != nil || m == nil {
		return nil, bad("invalid_json")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, bad("invalid_json")
	}
	return m, nil
}

func (a *Adapter) opaque(in inference.AdapterInput, v any) string {
	// Bind opaque identifiers to the selected owner/account, including identity metadata.
	b, _ := json.Marshal([]any{in.Principal.OrganizationID, in.Principal.UserID, in.Credential.AccountID, in.Credential.StateBinding, in.Credential.Headers.Get("X-User-Id"), in.Credential.Headers.Get("X-Domain"), in.Credential.Headers.Get("X-Enterprise-Id"), v})
	h := hmac.New(sha256.New, a.salt[:])
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func (a *Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	if e := ctx.Err(); e != nil {
		return inference.AdapterRequest{}, e
	}
	if in.Operation != inference.Generate {
		return inference.AdapterRequest{}, inference.ErrUnsupported
	}
	if !destination(in.Target.BaseURL) {
		return inference.AdapterRequest{}, inference.ErrDestination
	}
	if !valid(in.Credential.Secret, 32768) || !valid(in.Credential.Headers.Get("X-User-Id"), 1024) || !valid(in.Credential.Headers.Get("X-Domain"), 1024) {
		return inference.AdapterRequest{}, inference.ErrNoCredential
	}
	team := in.Credential.Headers.Get("X-Enterprise-Id")
	if team != "" && !valid(team, 1024) {
		return inference.AdapterRequest{}, inference.ErrNoCredential
	}
	if _, e := toolMetadata(in); e != nil {
		return inference.AdapterRequest{}, e
	}
	body, _, e := requestBody(in)
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	h := make(http.Header)
	// Core merges headers before adapter overrides. Nil entries remove every inherited
	// header, so configuration cannot introduce a second credential or client channel.
	for k := range in.Target.Headers {
		h[http.CanonicalHeaderKey(k)] = nil
	}
	for k := range in.Credential.Headers {
		h[http.CanonicalHeaderKey(k)] = nil
	}
	for k, v := range map[string]string{"Content-Type": "application/json", "Accept": "text/event-stream", "Authorization": "Bearer " + in.Credential.Secret, "User-Agent": "WorkBuddy/5.5.6", "X-Product": "SaaS", "X-IDE-Type": "WorkBuddy", "X-IDE-Name": "WorkBuddy", "X-IDE-Version": "5.5.6", "X-Requested-With": "XMLHttpRequest", "X-Agent-Intent": "craft", "X-Agent-Purpose": "conversation", "X-Agent-Type": "main", "X-User-Id": in.Credential.Headers.Get("X-User-Id"), "X-Domain": in.Credential.Headers.Get("X-Domain")} {
		h.Set(k, v)
	}
	if team != "" {
		h.Set("X-Enterprise-Id", team)
		h.Set("X-Tenant-Id", team)
	}
	var id [16]byte
	rand.Read(id[:])
	id[6] = (id[6] & 15) | 64
	id[8] = (id[8] & 63) | 128
	requestID := hex.EncodeToString(id[:])
	h.Set("X-Request-ID", requestID)
	h.Set("X-Conversation-Message-ID", requestID)
	var conversation any
	session := strings.TrimSpace(in.ClientHeaders.Get("thread-id"))
	if session == "" {
		session = strings.TrimSpace(in.ClientHeaders.Get("x-codex-parent-thread-id"))
	}
	original, _ := decode(in.Body)
	if session == "" {
		session = str(original["prompt_cache_key"])
	}
	if session == "" {
		session = in.ClientHeaders.Get("session_id")
	}
	if session == "" {
		session = in.ClientHeaders.Get("x-session-id")
	}
	if session != "" {
		conversation = session
	}
	latest := -1
	var content any
	for i, v := range arr(body["messages"]) {
		m := obj(v)
		if m["role"] == "user" {
			if conversation == nil {
				conversation = m["content"]
			}
			latest = i
			content = m["content"]
		}
	}
	if conversation != nil && conversation != "" {
		h.Set("X-Conversation-ID", a.opaque(in, conversation))
	}
	if latest >= 0 {
		h.Set("X-Conversation-Request-ID", a.opaque(in, []any{conversation, latest, content}))
	} else {
		h.Set("X-Conversation-Request-ID", requestID)
	}
	data, e := json.Marshal(body)
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	if len(data) > maxBytes {
		return inference.AdapterRequest{}, bad("workbuddy_request_limit")
	}
	return inference.AdapterRequest{Method: http.MethodPost, URL: BaseURL + "/chat/completions", Headers: h, Body: data, ResponseWire: inference.Chat}, nil
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func requestBody(in inference.AdapterInput) (object, map[string]string, error) {
	original, e := decode(in.Body)
	if e != nil {
		return nil, nil, e
	}
	// Cache/thread controls are consumed locally and never sent to WorkBuddy.
	delete(original, "prompt_cache_key")
	delete(original, "prompt_cache_retention")
	data, e := json.Marshal(original)
	if e != nil {
		return nil, nil, e
	}
	from := in.Wire
	if from == inference.Chat {
		for _, v := range arr(original["tools"]) {
			if obj(v)["type"] != "function" {
				data, e = inference.TranslateRequestJSON(data, from, inference.Responses)
				if e != nil {
					return nil, nil, e
				}
				from = inference.Responses
				break
			}
		}
	}
	data, e = inference.TranslateRequestJSON(data, from, inference.Chat)
	if e != nil {
		return nil, nil, e
	}
	b, e := decode(data)
	if e != nil {
		return nil, nil, e
	}
	if str(b["previous_response_id"]) != "" {
		return nil, nil, inference.ErrContinuation
	}
	b["model"] = in.Target.Model
	b["stream"] = true
	messages := arr(b["messages"])
	for _, v := range messages {
		m := obj(v)
		if m["role"] == "developer" {
			m["role"] = "system"
		}
	}
	if len(messages) == 0 || obj(messages[0])["role"] != "system" {
		messages = append([]any{object{"role": "system", "content": "You are a helpful assistant."}}, messages...)
	}
	b["messages"] = messages
	if v, ok := b["max_completion_tokens"]; ok {
		b["max_tokens"] = v
		delete(b, "max_completion_tokens")
	}
	if b["tool_choice"] == "none" {
		delete(b, "tools")
		delete(b, "tool_choice")
	}
	aliases := map[string]string{}
	forward := map[string]string{}
	// Reserve all valid names before assigning aliases, including names that resemble hashes.
	for _, v := range arr(b["tools"]) {
		name := str(obj(obj(v)["function"])["name"])
		if safeName.MatchString(name) {
			aliases[name] = name
			forward[name] = name
		}
	}
	for _, v := range arr(b["tools"]) {
		t := obj(v)
		if t["type"] != "function" {
			return nil, nil, inference.ErrUnsupported
		}
		f := obj(t["function"])
		name := str(f["name"])
		if name == "" {
			return nil, nil, bad("workbuddy_tool_name")
		}
		alias := forward[name]
		if alias == "" {
			sum := sha256.Sum256([]byte(name))
			alias = "wb_" + hex.EncodeToString(sum[:])[:48]
			for aliases[alias] != "" && aliases[alias] != name {
				alias += "_"
			}
			if len(alias) > 64 {
				return nil, nil, bad("workbuddy_tool_collision")
			}
			forward[name] = alias
			aliases[alias] = name
		}
		f["name"] = alias
		p, e := ToolParameters(f["parameters"])
		if e != nil {
			return nil, nil, e
		}
		f["parameters"] = p
	}
	for _, v := range messages {
		m := obj(v)
		for _, call := range arr(m["tool_calls"]) {
			f := obj(obj(call)["function"])
			if alias := forward[str(f["name"])]; alias != "" {
				f["name"] = alias
			}
		}
	}
	if b["tool_choice"] == "none" {
		delete(b, "tools")
		delete(b, "tool_choice")
	} else if c := obj(b["tool_choice"]); c["type"] == "function" {
		name := str(obj(c["function"])["name"])
		if alias := forward[name]; alias != "" {
			name = alias
		}
		b["tool_choice"] = name
	}
	return b, aliases, nil
}

func (a *Adapter) Decode(ctx context.Context, r *http.Response) (*http.Response, error) {
	return decodeStream(ctx, r, nil, nil)
}
func (a *Adapter) DecodeWithInput(ctx context.Context, in inference.AdapterInput, r *http.Response) (*http.Response, error) {
	_, names, e := requestBody(in)
	if e != nil {
		return nil, e
	}
	metadata, e := toolMetadata(in)
	if e != nil {
		return nil, e
	}
	return decodeStream(ctx, r, names, metadata)
}
