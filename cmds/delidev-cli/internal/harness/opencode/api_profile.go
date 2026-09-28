package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const nativeAPIProviderName = "DeliDev execution"
const nativeAPIProviderPackage = "@ai-sdk/openai-compatible"

// This initial private profile describes explicit text/tool Chat Completions
// settings. It does not validate provider credentials or grant account/model
// readiness. Its production owner must separately validate the registered
// execution relay, managed policy and canonical private runtime/workspace.
type nativeAPIProfile struct {
	Settings            SessionSettings      `json:"-"`
	BaseURL             string               `json:"-"`
	Token               string               `json:"-"`
	ContextLimit        int64                `json:"-"`
	OutputLimit         int64                `json:"-"`
	Rejection           RejectionPolicy      `json:"-"`
	Instructions        string               `json:"-"`
	InstructionsPath    string               `json:"-"`
	ProjectInstructions *projectInstructions `json:"-"`
	ProjectConfig       *projectConfigScope  `json:"-"`
	References          []WorkspaceReference `json:"-"`
	ReferencePath       string               `json:"-"`
	ReferenceWorkspace  string               `json:"-"`
}

func (p nativeAPIProfile) config() (map[string]any, error) {
	// Zero retains the pinned native unknown-limit representation. Native
	// output fallback remains native policy, not catalog capability evidence.
	if !validSessionSettings(p.Settings) || domain.Text(p.BaseURL, "relay", 8192, true) != nil || domain.Text(p.Token, "credential", 16384, true) != nil || !validRejectionPolicy(p.Rejection) || p.ContextLimit < 0 || p.ContextLimit > 9007199254740991 || p.OutputLimit < 0 || p.OutputLimit > 9007199254740991 || p.ContextLimit > 0 && p.OutputLimit > p.ContextLimit {
		return nil, sessionInvalid()
	}
	if domain.Text(p.Instructions, "native instructions", 256<<10, false) != nil || (p.Instructions == "") != (p.InstructionsPath == "") || p.InstructionsPath != "" && (!filepath.IsAbs(p.InstructionsPath) || filepath.Clean(p.InstructionsPath) != p.InstructionsPath || strings.ContainsAny(p.InstructionsPath, "\r\n\x00") || filepath.Base(p.InstructionsPath) != "instructions.txt") {
		return nil, sessionInvalid()
	}
	model := p.Settings.Provider + "/" + p.Settings.Model
	result := map[string]any{
		"autoupdate": false, "share": "disabled", "username": "delidev",
		"model": model, "small_model": model, "enabled_providers": []string{p.Settings.Provider},
		"experimental": map[string]any{"continue_loop_on_deny": p.Rejection == ContinueOnInteractionRejection},
		"provider": map[string]any{p.Settings.Provider: map[string]any{
			"npm": nativeAPIProviderPackage, "name": nativeAPIProviderName,
			"options": map[string]any{"baseURL": p.BaseURL, "apiKey": p.Token},
			"models": map[string]any{p.Settings.Model: map[string]any{
				"name": p.Settings.Model, "limit": map[string]any{"context": p.ContextLimit, "output": p.OutputLimit},
				"temperature": false, "reasoning": false, "attachment": false, "tool_call": true, "interleaved": false,
				"modalities": map[string]any{"input": []string{"text"}, "output": []string{"text"}},
			}},
		}},
	}
	var instructionPaths []string
	if p.ProjectInstructions != nil {
		for _, source := range p.ProjectInstructions.Sources {
			instructionPaths = append(instructionPaths, source.Path)
		}
	}
	if p.InstructionsPath != "" {
		// Native instructions are additive. Do not replace the native agent's
		// prompt or the system field of an individual user message.
		instructionPaths = append(instructionPaths, p.InstructionsPath)
	}
	if len(instructionPaths) > 0 {
		result["instructions"] = instructionPaths
	}
	if len(p.References) > 0 {
		result["$schema"] = referenceConfigSchema
		result["references"] = p.referenceConfig()
	}
	return result, nil
}

func (p nativeAPIProfile) configBytes() ([]byte, error) {
	value, err := p.config()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxHTTPBody {
		return nil, sessionInvalid()
	}
	return raw, nil
}

func exactPrivateJSON(raw []byte, expected any) error {
	var fields map[string]json.RawMessage
	if len(raw) > maxHTTPBody || domain.Decode(raw, &fields) != nil || fields == nil {
		return sessionProblem()
	}
	want, err := json.Marshal(expected)
	if err != nil || !bytes.Equal(canonicalNative(raw), canonicalNative(want)) {
		return sessionProblem()
	}
	return nil
}

func (p nativeAPIProfile) validateConfig(raw []byte) error {
	expected, err := p.config()
	if err != nil {
		return err
	}
	// These empty containers are added by the pinned native config loader.
	// Compare the complete effective object, including otherwise unknown keys,
	// so project/global/plugin overrides cannot become execution authority.
	for _, field := range []string{"agent", "mode", "command"} {
		expected[field] = map[string]any{}
	}
	expected["plugin"] = []any{}
	return exactPrivateJSON(raw, expected)
}

func (p nativeAPIProfile) provider() map[string]any {
	modalities := map[string]any{"text": true, "audio": false, "image": false, "video": false, "pdf": false}
	return map[string]any{
		"all": []any{map[string]any{
			"id": p.Settings.Provider, "name": nativeAPIProviderName, "source": "config", "env": []any{},
			"options": map[string]any{"baseURL": p.BaseURL, "apiKey": p.Token},
			"models": map[string]any{p.Settings.Model: map[string]any{
				"id": p.Settings.Model, "providerID": p.Settings.Provider, "name": p.Settings.Model,
				"api":    map[string]any{"id": p.Settings.Model, "npm": nativeAPIProviderPackage, "url": ""},
				"status": "active", "family": "", "release_date": "",
				"capabilities": map[string]any{
					"temperature": false, "reasoning": false, "attachment": false, "toolcall": true,
					"input": modalities, "output": modalities, "interleaved": false,
				},
				// Native zero defaults are not prices, charges or free-use proof.
				"cost":    map[string]any{"input": 0, "output": 0, "cache": map[string]any{"read": 0, "write": 0}},
				"limit":   map[string]any{"context": p.ContextLimit, "output": p.OutputLimit},
				"options": map[string]any{}, "headers": map[string]any{}, "variants": map[string]any{},
			}},
		}},
		"default": map[string]any{p.Settings.Provider: p.Settings.Model}, "connected": []string{p.Settings.Provider},
	}
}

func (p nativeAPIProfile) validateProvider(raw []byte) error {
	return exactPrivateJSON(raw, p.provider())
}

func equalSessionSettings(a, b SessionSettings) bool {
	return a.Title == b.Title && a.Agent == b.Agent && a.Provider == b.Provider && a.Model == b.Model && slices.Equal(a.Permission, b.Permission)
}

func (s *sessionAPI) verifyAPIProfile(ctx context.Context) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	if s.apiProfile == nil || s.creation != nil || s.input != nil || s.problem != nil || s.rejectionPolicy != s.apiProfile.Rejection {
		return sessionInvalid()
	}
	s.apiVerified = false
	if err := s.verifyInstructions(ctx, "initialization"); err != nil {
		return err
	}
	s.runtimeRead = true
	defer func() { s.runtimeRead = false }()
	for _, step := range []struct {
		path     string
		phase    string
		validate func([]byte) error
	}{
		{"/config", "configuration-before-provider", s.apiProfile.validateConfig},
		{"/provider", "provider-selection", s.apiProfile.validateProvider},
		{"/config", "configuration-after-provider", s.apiProfile.validateConfig},
	} {
		raw, status, err := s.request(ctx, http.MethodGet, step.path, nil, http.StatusOK)
		if err == nil {
			err = step.validate(raw)
		}
		if err != nil {
			if domain.SafeError(err).Code == domain.Unsupported {
				s.problem = sessionProblem()
			}
			if s.logger != nil {
				s.logger.WarnContext(ctx, "OpenCode effective API profile verification failed", "owner_id", s.owner, "phase", step.phase, "http_status", status, "code", domain.SafeError(err).Code)
			}
			return err
		}
	}
	s.apiVerified = true
	return nil
}
