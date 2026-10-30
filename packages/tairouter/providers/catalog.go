// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const Revision = "88aaa754566d119b6b8fc898f8fb47602387d82e"

type Wire string

const (
	Chat        Wire = "openai-chat"
	Responses   Wire = "openai-responses"
	Anthropic   Wire = "anthropic"
	Google      Wire = "google"
	JEVDecision Wire = "jev-decision"
	ExaSearch   Wire = "exa-search"
)

type AuthKind string

const (
	Key     AuthKind = "key"
	OAuth   AuthKind = "oauth"
	Forward AuthKind = "forward"
	Local   AuthKind = "local"
)

type Inbound string

const (
	InboundChat      Inbound = "chat"
	InboundResponses Inbound = "responses"
	InboundAnthropic Inbound = "anthropic"
)

var (
	ErrProvider    = errors.New("provider unavailable")
	ErrConnection  = errors.New("invalid provider connection")
	ErrUnsupported = errors.New("provider authentication method unsupported")
)

// Optional booleans distinguish unsupported from unobserved capabilities.
type ResponsesItemIDRepairPolicy struct {
	Message                  []string `json:"message,omitempty"`
	Reasoning                []string `json:"reasoning,omitempty"`
	RepairInvalidIDs         *bool    `json:"repairInvalidIds,omitempty"`
	RepairMissingTerminalIDs *bool    `json:"repairMissingTerminalIds,omitempty"`
}

type Capabilities struct {
	ResponsesItemIDRepair                *ResponsesItemIDRepairPolicy `json:"responsesItemIdRepair,omitempty"`
	ShowThinkingSummary                  *bool                        `json:"showThinkingSummary,omitempty"`
	EscapeBuiltinToolNames               *bool                        `json:"escapeBuiltinToolNames,omitempty"`
	SupportsServiceTier                  *bool                        `json:"supportsServiceTier,omitempty"`
	SupportsVerbosity                    *bool                        `json:"supportsVerbosity,omitempty"`
	SupportsResponsesCustomTools         *bool                        `json:"supportsResponsesCustomTools,omitempty"`
	SupportsOpenAIWebSearchToolFields    *bool                        `json:"supportsOpenAiWebSearchToolFields,omitempty"`
	ParallelToolCalls                    *bool                        `json:"parallelToolCalls,omitempty"`
	StatelessResponses                   *bool                        `json:"statelessResponses,omitempty"`
	RequiresAdjacentResponsesToolResults *bool                        `json:"requiresAdjacentResponsesToolResults,omitempty"`
	RequiresPairedResponsesToolResults   *bool                        `json:"requiresPairedResponsesToolResults,omitempty"`
	PreserveResponsesReasoningContent    *bool                        `json:"preserveResponsesReasoningContent,omitempty"`
	DropResponsesReasoningItems          *bool                        `json:"dropResponsesReasoningItems,omitempty"`
	FoldDeveloperRoleToSystem            *bool                        `json:"foldDeveloperRoleToSystem,omitempty"`
	InputModalities                      []string                     `json:"inputModalities,omitempty"`
	ModelInputModalities                 map[string][]string          `json:"modelInputModalities,omitempty"`
	ModelContextWindows                  map[string]int               `json:"modelContextWindows,omitempty"`
	ModelMaxInputTokens                  map[string]int               `json:"modelMaxInputTokens,omitempty"`
	ModelMaxOutputTokens                 map[string]int               `json:"modelMaxOutputTokens,omitempty"`
	ModelSupportsServiceTier             map[string]bool              `json:"modelSupportsServiceTier,omitempty"`
	ModelReasoningEfforts                map[string][]string          `json:"modelReasoningEfforts,omitempty"`
	ReasoningEfforts                     []string                     `json:"reasoningEfforts,omitempty"`
	NoVisionModels                       []string                     `json:"noVisionModels,omitempty"`
	NoReasoningModels                    []string                     `json:"noReasoningModels,omitempty"`
	NoStructuredOutputModels             []string                     `json:"noStructuredOutputModels,omitempty"`
	ContextWindow                        int                          `json:"contextWindow,omitempty"`
	DefaultMaxOutputTokens               int                          `json:"defaultMaxOutputTokens,omitempty"`
}
type ModelWireDefault struct {
	Wire                     Wire       `json:"wire"`
	Inbound                  []Inbound  `json:"inbound,omitempty"`
	AuthModes                []AuthKind `json:"authModes,omitempty"`
	ForwardCallerServiceTier bool       `json:"forwardCallerServiceTier,omitempty"`
}

func (m *ModelWireDefault) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &m.Wire)
	}
	type plain ModelWireDefault
	return json.Unmarshal(b, (*plain)(m))
}

type Preset struct {
	ID                   string                      `json:"id"`
	Label                string                      `json:"label"`
	Wire                 Wire                        `json:"adapter"`
	BaseURL              string                      `json:"baseUrl"`
	Auth                 AuthKind                    `json:"authKind"`
	OAuthID              string                      `json:"oauthId,omitempty"`
	CredentialOnly       bool                        `json:"credentialOnly,omitempty"`
	KeyOptional          bool                        `json:"keyOptional,omitempty"`
	AllowKeyAuthOverride bool                        `json:"allowKeyAuthOverride,omitempty"`
	AllowBaseURLOverride bool                        `json:"allowBaseUrlOverride,omitempty"`
	APIKeyValidation     string                      `json:"apiKeyValidation,omitempty"`
	Deprecated           string                      `json:"deprecated,omitempty"`
	DefaultModel         string                      `json:"defaultModel,omitempty"`
	Models               []string                    `json:"models,omitempty"`
	LiveModels           *bool                       `json:"liveModels,omitempty"`
	StaticHeaders        map[string]string           `json:"staticHeaders,omitempty"`
	ResponsesPath        string                      `json:"responsesPath,omitempty"`
	ChatCompletionsPath  string                      `json:"chatCompletionsPath,omitempty"`
	ModelWireDefaults    map[string]ModelWireDefault `json:"modelWireDefaults,omitempty"`
	Capabilities
	// Metadata retains every pinned field, including discovery filters, effort maps and wire repair policies.
	Metadata json.RawMessage `json:"-"`
	Excluded bool            `json:"excluded,omitempty"`
}

//go:embed snapshot/catalog.json
var catalogJSON []byte

// Catalog returns independent copies. Callers cannot mutate the shared registry.
func Catalog() []Preset {
	var raw []json.RawMessage
	if err := json.Unmarshal(catalogJSON, &raw); err != nil {
		panic(err)
	}
	// Service-owned hosted credentials are additive; the upstream snapshot stays
	// exactly reproducible from Revision and never claims these rows as upstream.
	raw = append(raw, json.RawMessage(exaPresetJSON))
	out := make([]Preset, 0, len(raw))
	for _, b := range raw {
		out = append(out, decodePreset(b))
	}
	return out
}

func decodePreset(b json.RawMessage) Preset {
	var p Preset
	if err := json.Unmarshal(b, &p); err != nil {
		panic(err)
	}
	p.Metadata = bytes.Clone(b)
	switch p.Wire {
	case "qoder", "codebuddy", "claude-cli":
		p.Excluded = true
	}
	return p
}

// Index immutable snapshot bytes once. Decoding only the requested row avoids
// repeatedly decoding unrelated providers during route reconstruction, while
// each returned preset still owns all of its maps, slices and pointers.
var lookupRows = sync.OnceValue(func() map[string]json.RawMessage {
	rows := make(map[string]json.RawMessage)
	for _, p := range Catalog() {
		if _, exists := rows[p.ID]; !exists {
			rows[p.ID] = p.Metadata
		}
	}
	return rows
})

func Lookup(id string) (Preset, bool) {
	b, ok := lookupRows()[id]
	if !ok {
		return Preset{}, false
	}
	return decodePreset(b), true
}

// Connection contains private data. Never serialize it into a public catalog response.
type Connection struct {
	CodexClientVersion string            `json:"codexClientVersion,omitempty"`
	Provider           string            `json:"provider"`
	BaseURL            string            `json:"baseUrl,omitempty"`
	Wire               Wire              `json:"wire,omitempty"`
	Auth               AuthKind          `json:"auth,omitempty"`
	APIKey             string            `json:"apiKey,omitempty"`
	Credential         *Credential       `json:"credential,omitempty"`
	DefaultModel       string            `json:"defaultModel,omitempty"`
	ModelWires         map[string]Wire   `json:"modelWires,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
}
type Route struct {
	Preset                             Preset
	BaseURL                            string
	Wire                               Wire
	Model                              string
	Headers                            http.Header
	ResponsesPath, ChatCompletionsPath string
	ForwardCallerServiceTier           bool
}

func validURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(raw, "{}\r\n")
}
func supportedCustom(w Wire) bool {
	return w == Chat || w == Responses || w == Anthropic || w == Google || w == JEVDecision
}

// ValidateConnection validates new enrollment, including the deprecation policy.
func ValidateConnection(c Connection) error { return validateConnection(c, true) }

// ValidateSavedConnection retains every security check but permits a deprecated
// saved preset to reach its own adapter. Deprecation is not evidence of live availability.
func ValidateSavedConnection(c Connection) error { return validateConnection(c, false) }

func validateConnection(c Connection, enrollment bool) error {
	p, ok := Lookup(c.Provider)
	if c.Provider == "custom" {
		p = Preset{ID: "custom", Wire: c.Wire, Auth: Key, AllowBaseURLOverride: true, CredentialOnly: c.Wire == JEVDecision}
		ok = supportedCustom(c.Wire)
	}
	if !ok || p.Excluded || (enrollment && p.Deprecated != "") {
		return ErrProvider
	}
	if p.ID == "exa" {
		if c.Credential != nil || !validExaKey(c.APIKey) {
			return ErrConnection
		}
	}
	if err := ValidateHeaders(c.Headers); err != nil {
		return err
	}
	if c.Wire != "" && c.Wire != p.Wire {
		return ErrConnection
	}
	for model, w := range c.ModelWires {
		if !modelID(model) || w == "" || !validModelWireOverride(p, w) {
			return ErrConnection
		}
	}
	base := p.BaseURL
	if c.BaseURL != "" {
		if !p.AllowBaseURLOverride && c.BaseURL != p.BaseURL && !strings.Contains(p.BaseURL, "{") {
			return ErrConnection
		}
		base = c.BaseURL
	}
	if !validURL(base) {
		return ErrConnection
	}
	auth := p.Auth
	if p.ID == "google-vertex" && c.Credential != nil && c.Credential.Google != nil {
		auth = OAuth
	}
	if c.Auth != "" {
		if c.Auth != p.Auth && !(c.Auth == Key && p.AllowKeyAuthOverride) && !(p.ID == "google-vertex" && c.Auth == OAuth && c.Credential != nil && c.Credential.Google != nil) {
			return ErrConnection
		}
		auth = c.Auth
	}
	if strings.ContainsAny(c.APIKey, "\r\n") {
		return ErrConnection
	}
	if auth == Key && !p.KeyOptional && c.APIKey == "" {
		return ErrConnection
	}
	if (auth == OAuth || auth == Forward) && (c.Credential == nil || c.Credential.AccessToken == "") {
		return ErrConnection
	}
	if c.Credential != nil && (c.Credential.Provider != c.Provider || !validScope(c.Credential.Scope) || strings.ContainsAny(c.Credential.AccessToken, "\r\n")) {
		return ErrConnection
	}
	if c.Provider == "zed" && (c.Credential == nil || !validZed(*c.Credential)) {
		return ErrConnection
	}
	if c.Provider == "google-antigravity" && (c.Credential == nil || !safeString(c.Credential.ProjectID)) {
		return ErrConnection
	}
	return nil
}
func contains[T comparable](list []T, v T) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
func Resolve(c Connection, model string, inbound Inbound) (Route, error) {
	return resolve(c, model, inbound, "")
}

// ResolvePolicyWire accepts only trusted server-side policy.Resolve output, never
// a management wire/model_wires field. User configuration is still validated in
// full. Generic protocol changes synthesize auth after selecting the final wire;
// specialized and project-bound provider pins cannot be bypassed.
func ResolvePolicyWire(c Connection, model string, inbound Inbound, wire Wire) (Route, error) {
	if wire == "" {
		return Route{}, ErrConnection
	}
	return resolve(c, model, inbound, wire)
}

func resolve(c Connection, model string, inbound Inbound, policyWire Wire) (Route, error) {
	if err := ValidateSavedConnection(c); err != nil {
		return Route{}, err
	}
	p, _ := Lookup(c.Provider)
	if c.Provider == "custom" {
		p = Preset{ID: "custom", Wire: c.Wire, Auth: Key, CredentialOnly: c.Wire == JEVDecision}
	}
	if p.CredentialOnly {
		return Route{}, ErrProvider
	}
	r := Route{Preset: p, BaseURL: p.BaseURL, Wire: p.Wire, Model: model, Headers: make(http.Header), ResponsesPath: p.ResponsesPath, ChatCompletionsPath: p.ChatCompletionsPath}
	if c.BaseURL != "" {
		r.BaseURL = c.BaseURL
	}
	// The pinned OpenRouter service-tier evidence applies only to its canonical destination.
	if p.ID == "openrouter" && strings.TrimRight(r.BaseURL, "/") != "https://openrouter.ai/api/v1" {
		r.Preset.ModelSupportsServiceTier = nil
	}
	if r.Model == "" {
		r.Model = c.DefaultModel
	}
	if r.Model == "" {
		r.Model = p.DefaultModel
	}
	if r.Model == "" {
		return Route{}, ErrConnection
	}
	auth := p.Auth
	if p.ID == "google-vertex" && c.Credential != nil && c.Credential.Google != nil {
		auth = OAuth
	}
	if c.Auth != "" {
		auth = c.Auth
	}
	if d, ok := p.ModelWireDefaults[r.Model]; ok && (len(d.Inbound) == 0 || contains(d.Inbound, inbound)) && (len(d.AuthModes) == 0 || contains(d.AuthModes, auth)) {
		r.Wire = d.Wire
		r.ForwardCallerServiceTier = d.ForwardCallerServiceTier
	}

	if w := c.ModelWires[r.Model]; w != "" {
		r.Wire = w
		r.ForwardCallerServiceTier = false
	}
	if policyWire != "" {
		generic := func(w Wire) bool { return w == Chat || w == Responses || w == Anthropic || w == Google }
		if policyWire != r.Wire && (!generic(p.Wire) || !generic(policyWire) || p.ID == "google-antigravity" || p.ID == "google-vertex") {
			return Route{}, ErrConnection
		}
		if policyWire != r.Wire {
			r.ForwardCallerServiceTier = false
		}
		r.Wire = policyWire
	}
	for k, v := range p.StaticHeaders {
		r.Headers.Set(k, v)
	}
	token := c.APIKey
	if auth == OAuth || auth == Forward {
		token = c.Credential.AccessToken
	}
	if token != "" {
		switch {
		case auth == Key && r.Wire == Anthropic:
			r.Headers.Set("x-api-key", token)
		case auth == Key && r.Wire == Google:
			r.Headers.Set("x-goog-api-key", token)
		case auth == Key && r.Wire == "azure-openai":
			r.Headers.Set("api-key", token)
		default:
			r.Headers.Set("Authorization", "Bearer "+token)
		}
	}
	if r.Wire == Anthropic {
		r.Headers.Set("anthropic-version", "2023-06-01")
		if auth == OAuth {
			r.Headers.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
		}
	}
	if c.Credential != nil && c.Credential.APIBaseURL != "" {
		if !(p.ID == "github-copilot" && copilotURL(c.Credential.APIBaseURL)) && !(p.ID == "devin" && devinURL(c.Credential.APIBaseURL)) && !(p.ID == "orcarouter-oauth" && validOrcaCredentialURL(c.Credential.APIBaseURL, r.BaseURL)) {
			return Route{}, ErrConnection
		}
		r.BaseURL = c.Credential.APIBaseURL
	}
	if p.ID == "openai" && c.Credential != nil && c.Credential.AccountID != "" {
		r.Headers.Set("ChatGPT-Account-Id", c.Credential.AccountID)
	}
	// Reserved identity/auth headers were rejected during validation. Allowed
	// personal headers therefore take precedence over provider protocol defaults.
	overlayHeaders(r.Headers, c.Headers)
	return r, nil
}
