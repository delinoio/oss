// SPDX-License-Identifier: Apache-2.0
// Kiro wire behavior adapted from opencodex; see LICENSE.upstream.
package kiro

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const Wire inference.Wire = "kiro"
const DefaultAttemptTimeout = 200 * time.Second
const builderProfile = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
const completionTool = "codex_kiro_final_answer"
const maxRequestBytes = 32 << 20
const maxRetainedBytes = 32 << 20

type object = map[string]any
type stateKey struct{}
type requestState struct {
	mu          sync.Mutex
	model       string
	binding     [32]byte
	streamID    string
	aliases     map[string]string
	completion  bool
	built       bool
	owner       inference.Principal
	metadata    Metadata
	definitions map[string]ToolDefinition
}

// Adapter contains no credentials, mutable request state, or HTTP client.
type Adapter struct{}

var _ inference.Adapter = (*Adapter)(nil)

func New() *Adapter { return &Adapter{} }

// WithRequestState creates an isolated Build/Decode pair. The executor must retain
// this context through Decode; each new attempt must receive fresh state.
func WithRequestState(ctx context.Context) context.Context {
	return context.WithValue(ctx, stateKey{}, &requestState{})
}

func (*Adapter) PrepareContext(ctx context.Context) context.Context { return WithRequestState(ctx) }

func failure(status int, code string) error { return &inference.Error{Status: status, Code: code} }
func obj(v any) object                      { m, _ := v.(map[string]any); return m }
func arr(v any) []any                       { a, _ := v.([]any); return a }
func str(v any) string                      { s, _ := v.(string); return s }

func identifier() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

func (*Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	var empty inference.AdapterRequest
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	state, _ := ctx.Value(stateKey{}).(*requestState)
	if state == nil {
		return empty, failure(500, "kiro_request_context_required")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.built {
		return empty, failure(500, "kiro_request_context_reused")
	}
	if in.Operation != inference.Generate {
		return empty, inference.ErrUnsupported
	}
	if len(in.Body) > maxRequestBytes {
		return empty, failure(413, "kiro_request_too_large")
	}
	var body object
	if json.Unmarshal(in.Body, &body) != nil || body == nil {
		return empty, failure(400, "invalid_json")
	}
	endpoint, err := url.Parse(in.Target.BaseURL)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return empty, inference.ErrDestination
	}
	secret := strings.TrimSpace(in.Credential.Secret)
	if secret == "" || strings.ContainsAny(secret, "\r\n") {
		return empty, inference.ErrNoCredential
	}
	profile := in.Credential.Headers.Get("X-Amzn-Kiro-Profile-Arn")
	builder := in.Credential.Headers.Get("X-Tairouter-Kiro-Builder-Id") == "true"
	apiKey := strings.HasPrefix(secret, "ksk_")
	if builder {
		profile = builderProfile
	}
	if apiKey {
		profile = ""
	}
	if strings.ContainsAny(profile, "\r\n") {
		return empty, failure(400, "kiro_invalid_profile")
	}
	cli := apiKey || builder || profile == ""
	model := normalizeModel(in.Target.Model)
	if model == "" {
		model = normalizeModel(str(body["model"]))
	}
	if model == "" {
		return empty, failure(400, "kiro_model_required")
	}
	registry := newRegistry()
	payload, completion, err := buildPayload(body, in.Wire, model, profile, cli, registry)
	if err != nil {
		return empty, err
	}
	if seed, ok := ctx.Value(continuationKey{}).(continuationSeed); ok {
		if seed.owner != in.Principal {
			return empty, inference.ErrUnauthorized
		}
		obj(payload["conversationState"])["conversationId"] = seed.id
	}
	if seed, ok := ctx.Value(imagePositionsKey{}).(imagePositionSeed); ok && seed.owner != in.Principal {
		return empty, inference.ErrUnauthorized
	}
	if err := normalizePayloadImages(ctx, payload); err != nil {
		return empty, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return empty, err
	}
	if len(encoded) > maxRequestBytes {
		return empty, failure(413, "kiro_request_too_large")
	}
	id, err := identifier()
	if err != nil {
		return empty, err
	}
	headers := http.Header{
		"Authorization":               {"Bearer " + secret},
		"Content-Type":                {"application/x-amz-json-1.0"},
		"Accept":                      {"application/vnd.amazon.eventstream"},
		"X-Amz-Target":                {"AmazonCodeWhispererStreamingService.GenerateAssistantResponse"},
		"X-Amzn-Codewhisperer-Optout": {"true"},
		"Amz-Sdk-Invocation-Id":       {id},
	}
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	if cli {
		ua := "aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.17975 os/" + platform + " lang/rust/1.92.0"
		headers.Set("User-Agent", ua+" md/appVersion-2.14.2 m/F app/AmazonQ-For-CLI")
		headers.Set("X-Amz-User-Agent", ua+" m/F app/AmazonQ-For-CLI")
		headers.Set("Accept", "*/*")
		headers.Set("Amz-Sdk-Request", "attempt=1; max=3")
		if apiKey {
			headers.Set("Tokentype", "API_KEY")
		}
	} else {
		// Random per-request fingerprint avoids exposing server/user identity.
		fp := strings.ReplaceAll(id, "-", "")
		headers.Set("User-Agent", "aws-sdk-js/1.0.27 ua/2.1 os/"+platform+" lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-1.0.0-"+fp)
		headers.Set("X-Amz-User-Agent", "aws-sdk-js/1.0.27 KiroIDE-1.0.0-"+fp)
		headers.Set("X-Amzn-Kiro-Agent-Mode", "vibe")
	}
	if profile != "" {
		headers.Set("X-Amzn-Kiro-Profile-Arn", profile)
	}
	state.model, state.aliases, state.completion, state.built = model, registry.advertised, completion, true
	state.owner, state.definitions = in.Principal, registry.definitions
	state.binding = inputBinding(in)
	state.metadata.ConversationID = str(obj(payload["conversationState"])["conversationId"])
	state.metadata.Tools = append([]ToolDefinition(nil), registry.catalog...)
	return inference.AdapterRequest{URL: endpoint.String(), Headers: headers, Body: encoded, ResponseWire: inference.Chat, AttemptTimeout: DefaultAttemptTimeout}, nil
}
