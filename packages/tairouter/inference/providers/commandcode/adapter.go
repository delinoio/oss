// SPDX-License-Identifier: Apache-2.0
// Protocol behavior adapted from opencodex; see LICENSE.upstream.
package commandcode

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const Wire inference.Wire = "command-code"
const maxBytes = 32 << 20

type object = map[string]any
type Adapter struct{ options Options }

func New() *Adapter                           { return &Adapter{} }
func NewWithOptions(options Options) *Adapter { return &Adapter{options: options} }

var _ inference.Adapter = (*Adapter)(nil)
var _ inference.ContextualAdapter = (*Adapter)(nil)
var _ inference.HTTPRecoveryPolicy = (*Adapter)(nil)
var _ inference.RequestRepairer = (*Adapter)(nil)

func obj(v any) object                   { m, _ := v.(map[string]any); return m }
func arr(v any) []any                    { a, _ := v.([]any); return a }
func str(v any) string                   { s, _ := v.(string); return s }
func fail(status int, code string) error { return &inference.Error{Status: status, Code: code} }
func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func sessionID(ctx context.Context, in inference.AdapterInput) string {
	session := inference.AffinitySession(ctx)
	if session == "" {
		return uuid()
	}
	// Scope upstream affinity to the exact selected account and tenant. A public
	// thread ID or shared prompt-cache cohort is not an ownership boundary.
	b, _ := json.Marshal([]string{in.Principal.OrganizationID, in.Principal.UserID, in.Target.Provider, in.Credential.AccountID, in.Credential.StateBinding, session})
	sum := sha256.Sum256(b)
	s := hex.EncodeToString(sum[:])
	return s[:8] + "-" + s[8:12] + "-4" + s[13:16] + "-8" + s[17:20] + "-" + s[20:32]
}

var safeIdentity = regexp.MustCompile(`^[A-Za-z0-9._/@:+\[\]~-]+$`)
var identity = regexp.MustCompile(`You are Codex, (?:a coding agent|an agent) based on GPT-[0-9]+(?:\.[0-9]+)*\.`)

func chatInput(in inference.AdapterInput) (object, error) {
	if len(in.Body) > maxBytes {
		return nil, fail(413, "command_code_request_too_large")
	}
	b, err := inference.TranslateRequestJSON(in.Body, in.Wire, inference.Chat)
	if err != nil {
		return nil, err
	}
	var m object
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&m) != nil || m == nil {
		return nil, fail(400, "invalid_json")
	}
	// Same-wire Chat translation intentionally preserves native custom tools. Lower
	// those tools here because /alpha/generate accepts only function-shaped tools.
	for _, raw := range arr(m["tools"]) {
		t := obj(raw)
		if t["type"] == "custom" {
			c := obj(t["custom"])
			if c == nil {
				c = t
			}
			t["type"] = "function"
			t["function"] = object{"name": c["name"], "description": c["description"], "parameters": object{"type": "object", "properties": object{"input": object{"type": "string"}}, "required": []any{"input"}, "additionalProperties": false}, "extra_content": object{"tairouter": object{"custom_tool": true}}}
		}
	}
	for _, raw := range arr(m["messages"]) {
		msg := obj(raw)
		for _, raw := range arr(msg["tool_calls"]) {
			call := obj(raw)
			if call["type"] == "custom" {
				c := obj(call["custom"])
				args, _ := json.Marshal(object{"input": c["input"]})
				call["type"] = "function"
				call["function"] = object{"name": c["name"], "arguments": string(args)}
			}
		}
	}
	return m, nil
}
func (a *Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	var empty inference.AdapterRequest
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if in.Operation != inference.Generate {
		return empty, inference.ErrUnsupported
	}
	if in.Credential.Secret == "" {
		return empty, inference.ErrNoCredential
	}
	u, err := url.Parse(in.Target.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return empty, inference.ErrDestination
	}
	m, err := chatInput(in)
	if err != nil {
		return empty, err
	}
	p, err := a.resolvePolicy(ctx, in)
	if err != nil {
		return empty, err
	}
	model := CanonicalModel(in.Target.Model)
	if model == "" {
		model = CanonicalModel(str(m["model"]))
	}
	messages, system, err := wireMessages(arr(m["messages"]))
	if err != nil {
		return empty, err
	}
	tools, instruction := wireTools(m)
	if nudge := toolNudge(tools); nudge != "" {
		system += "\n\n" + nudge
	}
	if instruction != "" {
		system += "\n\n" + instruction
	}
	system = identity.ReplaceAllStringFunc(system, func(string) string {
		if safeIdentity.MatchString(model) && len(model) <= 128 {
			return "You are a coding agent powered by the " + model + ". If asked which model you are, identify as " + model + ". Do not claim to be a different model or to have a different creator."
		}
		return "You are a coding agent powered by the configured model. If asked which model you are, identify as configured model. Do not claim to be GPT-5 or made by OpenAI."
	})
	limit := any(64000)
	if p.DefaultMaxOutputTokens > 0 {
		limit = p.DefaultMaxOutputTokens
	}
	if v, ok := m["max_tokens"]; ok {
		limit = v
	}
	if v, ok := m["max_completion_tokens"]; ok {
		limit = v
	}
	params := object{"model": model, "messages": messages, "tools": tools, "system": system, "max_tokens": limit, "stream": true}
	if v, ok := m["temperature"]; ok {
		params["temperature"] = v
	}
	effort := str(m["reasoning_effort"])
	if effort == "" {
		effort = str(obj(m["reasoning"])["effort"])
	}
	if effort = SupportedEffort(p, model, effort); effort != "" {
		params["reasoning_effort"] = effort
	}
	// Remote service mode intentionally provides no working directory, repository,
	// memory files, skills or local tool execution context.
	body := object{"config": object{"date": time.Now().UTC().Format("2006-01-02"), "environment": runtime.GOOS, "structure": []string{}}, "memory": "", "taste": nil, "skills": nil, "permissionMode": "standard", "mode": "agent", "params": params}
	data, err := json.Marshal(body)
	if err != nil {
		return empty, err
	}
	if len(data) > maxBytes {
		return empty, fail(413, "command_code_request_too_large")
	}
	h := in.Target.Headers.Clone()
	if h == nil {
		h = make(http.Header)
	}
	for k, v := range in.Credential.Headers {
		h[k] = append([]string(nil), v...)
	}
	for _, k := range []string{"X-Api-Key", "Api-Key", "Cookie", "X-Project-Slug", "Content-Length"} {
		h.Del(k)
	}
	h.Set("Authorization", "Bearer "+in.Credential.Secret)
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "cli")
	version := p.Version
	if version == "" {
		version = "0.52.1"
	}
	h.Set("X-Command-Code-Version", version)
	h.Set("X-Cli-Environment", "production")
	h.Set("X-Taste-Learning", "false")
	h.Set("X-Co-Flag", "false")
	h.Set("X-Session-Id", sessionID(ctx, in))
	u.Path = strings.TrimRight(u.Path, "/") + "/alpha/generate"
	u.RawPath = ""
	return inference.AdapterRequest{Method: http.MethodPost, URL: u.String(), Headers: h, Body: data, ResponseWire: inference.Chat, AttemptTimeout: 200 * time.Second}, nil
}
