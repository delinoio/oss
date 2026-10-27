// SPDX-License-Identifier: Apache-2.0
// Protocols adapted from the pinned reference; see snapshot/NOTICE and LICENSE-MIT.
package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"google.golang.org/protobuf/encoding/protowire"
)

type Model struct {
	Provider                          string
	Raw                               json.RawMessage
	ID                                string
	Name                              string
	ContextWindow, MaxOutputTokens    int
	MaxMode                           bool
	InputModalities, ReasoningEfforts []string
}
type ModelCatalog struct {
	State              ValidationState
	Models             []Model
	CodexCatalog       []json.RawMessage
	CodexClientVersion string
}

// Discover returns account-scoped evidence. The caller owns freshness, generation
// fencing and encrypted credential CAS (Zed can return a renewed credential).
func (a *Auth) Discover(ctx context.Context, s core.Scope, c Connection) (ModelCatalog, *Credential, error) {
	if !validScope(s) || (c.Credential != nil && c.Credential.Scope != s) {
		return ModelCatalog{}, nil, ErrScope
	}
	if err := ValidateSavedConnection(c); err != nil {
		return ModelCatalog{}, nil, err
	}
	cr := c.Credential
	switch c.Provider {
	case "openai":
		if cr != nil {
			return a.codexModels(ctx, s, *cr, c.CodexClientVersion, c.Headers)
		}
	case "zed":
		if cr == nil {
			return ModelCatalog{}, nil, ErrReauthenticate
		}
		z, fresh, err := a.zedModels(ctx, s, *cr, c.Headers)
		if err != nil {
			return ModelCatalog{}, nil, err
		}
		out := ModelCatalog{State: ValidationAccepted}
		for _, m := range z.Models {
			out.Models = append(out.Models, Model{ID: m.ID, Name: m.Name, Provider: m.Provider, Raw: append(json.RawMessage(nil), m.Raw...), ContextWindow: int(m.ContextLength), MaxOutputTokens: int(m.MaxOutputTokens), ReasoningEfforts: m.SupportedEffortLevels})
		}
		return out, &fresh, nil
	case "cursor", "kiro", "google-antigravity", "workbuddy-ai", "devin":
		if cr == nil {
			return ModelCatalog{}, nil, ErrReauthenticate
		}
		models, err := a.specialModels(ctx, s, *cr, c.Headers)
		if err != nil {
			return ModelCatalog{}, nil, err
		}
		return ModelCatalog{State: ValidationAccepted, Models: models}, nil, nil
	default:
		v, err := a.ValidateRemote(ctx, s, c)
		out := ModelCatalog{State: v.State}
		for _, id := range v.Models {
			out.Models = append(out.Models, Model{ID: id})
		}
		return out, nil, err
	}
	v, err := a.ValidateRemote(ctx, s, c)
	out := ModelCatalog{State: v.State}
	for _, id := range v.Models {
		out.Models = append(out.Models, Model{ID: id})
	}
	return out, nil, err
}

func modelID(id string) bool {
	return safeString(id) && len(id) <= 1024 && strings.TrimSpace(id) == id && !strings.ContainsFunc(id, func(r rune) bool { return r < 32 || (r >= 127 && r <= 159) || r == 0x2028 || r == 0x2029 }) && utf8.ValidString(id)
}
func positive(m map[string]any, k string) int {
	n, ok := m[k].(float64)
	if !ok || n <= 0 || n > 1<<31 || n != float64(int(n)) {
		return 0
	}
	return int(n)
}
func workBuddyHeaders(c Credential) (http.Header, error) {
	h := bearer(c.AccessToken)
	for k, v := range map[string]string{"User-Agent": "WorkBuddy/5.5.6", "X-Product": "SaaS", "X-IDE-Type": "WorkBuddy", "X-IDE-Name": "WorkBuddy", "X-IDE-Version": "5.5.6", "X-Requested-With": "XMLHttpRequest", "X-Agent-Intent": "craft", "X-Agent-Purpose": "conversation", "X-Agent-Type": "main"} {
		h.Set(k, v)
	}
	for key, header := range map[string]string{"uid": "X-User-Id", "domain": "X-Domain", "enterpriseId": "X-Enterprise-Id"} {
		v := c.Metadata[key]
		if v == "" && key == "enterpriseId" {
			continue
		}
		if !safeString(v) {
			return nil, ErrReauthenticate
		}
		h.Set(header, v)
	}
	if v := c.Metadata["enterpriseId"]; v != "" {
		h.Set("X-Tenant-Id", v)
	}
	return h, nil
}

const kiroBuilderProfile = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"

func kiroManagement(c Credential, usage bool) (string, map[string]any, http.Header, error) {
	region := c.Metadata["apiRegion"]
	if region == "" {
		region = c.Metadata["ssoRegion"]
	}
	if region == "" {
		region = "us-east-1"
	}
	profile := c.Metadata["profileArn"]
	if profile == "" && c.Metadata["clientId"] != "" {
		profile = kiroBuilderProfile
	}
	if profile == "" || !safeString(profile) {
		return "", nil, nil, ErrReauthenticate
	}
	if profile != kiroBuilderProfile {
		parts := strings.Split(profile, ":")
		if len(parts) < 6 || parts[0] != "arn" || parts[2] != "codewhisperer" {
			return "", nil, nil, ErrResponse
		}
		region = parts[3]
	}
	if !kiroRegionPattern.MatchString(region) {
		return "", nil, nil, ErrResponse
	}
	q := url.Values{"origin": {"AI_EDITOR"}, "profileArn": {profile}}
	b := map[string]any{"origin": "AI_EDITOR", "profileArn": profile}
	h := bearer(c.AccessToken)
	h.Set("Content-Type", "application/x-amz-json-1.0")
	h.Set("X-Amzn-Codewhisperer-Optout", "true")
	h.Set("X-Amz-Target", "KiroControlPlaneBearerService.ListAvailableModels")
	if usage {
		q.Set("isEmailRequired", "true")
		b["isEmailRequired"] = true
		h.Set("X-Amz-Target", "AmazonCodeWhispererService.GetUsageLimits")
	}
	return "https://management." + region + ".kiro.dev/?" + q.Encode(), b, h, nil
}
func (a *Auth) specialModels(ctx context.Context, s core.Scope, c Credential, custom ...map[string]string) ([]Model, error) {
	h := bearer(c.AccessToken)
	var headers map[string]string
	if len(custom) > 0 {
		headers = custom[0]
	}
	overlayHeaders(h, headers)
	method, endpoint := "POST", ""
	var body any
	switch c.Provider {
	case "devin":
		return a.devinModels(ctx, s, c, headers)
	case "cursor":
		h.Set("Accept", "application/proto")
		h.Set("Content-Type", "application/proto")
		h.Set("Connect-Protocol-Version", "1")
		h.Set("X-Ghost-Mode", "true")
		h.Set("X-Cursor-Client-Version", "cli-2026.02.13-41ac335")
		h.Set("X-Cursor-Client-Type", "cli")
		h.Set("X-Session-Id", core.ID())
		b, status, _, err := a.requestBytes(ctx, s, "POST", "https://api2.cursor.sh/agent.v1.AgentService/GetUsableModels", []byte{}, h, 4<<20)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, authFailure(status, nil)
		}
		return cursorModels(b)
	case "kiro":
		var err error
		endpoint, body, h, err = kiroManagement(c, false)
		if err != nil {
			return nil, err
		}
	case "google-antigravity":
		if !safeString(c.ProjectID) {
			return nil, ErrReauthenticate
		}
		endpoint = "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
		body = map[string]string{"project": c.ProjectID}
	case "workbuddy-ai":
		var err error
		h, err = workBuddyHeaders(c)
		if err != nil {
			return nil, err
		}
		method = "GET"
		endpoint = "https://www.workbuddy.ai/v3/config"
	}
	overlayHeaders(h, headers)
	b, status, _, err := a.requestBytes(ctx, s, method, endpoint, body, h, 4<<20)
	var m map[string]any
	if err == nil && status == 200 && json.Unmarshal(b, &m) != nil {
		err = ErrResponse
	}
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, authFailure(status, m)
	}
	var rows []any
	switch c.Provider {
	case "google-antigravity":
		models := obj(m, "models")
		if models == nil || len(models) > 2000 {
			return nil, ErrResponse
		}
		ids := make([]string, 0, len(models))
		for id := range models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			r, ok := models[id].(map[string]any)
			if !ok {
				continue
			}
			copy := map[string]any{}
			for k, v := range r {
				copy[k] = v
			}
			copy["id"] = id
			rows = append(rows, copy)
		}
	case "workbuddy-ai":
		return workBuddyModels(m)
	default:
		rows, _ = m["models"].([]any)
	}
	if len(rows) == 0 || len(rows) > 2000 {
		return nil, ErrResponse
	}
	out := []Model{}
	seen := map[string]bool{}
	for _, raw := range rows {
		r, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := str(r, "id")
		if c.Provider == "kiro" {
			id = str(r, "modelId")
		}
		if !modelID(id) || seen[id] {
			continue
		}
		seen[id] = true
		limit := positive(r, "maxTokens")
		if c.Provider == "kiro" {
			limit = positive(obj(r, "tokenLimits"), "maxInputTokens")
		}
		out = append(out, Model{ID: id, Name: str(r, "displayName"), ContextWindow: limit})
	}
	if len(out) == 0 {
		return nil, ErrResponse
	}
	return out, nil
}
func workBuddyModels(m map[string]any) ([]Model, error) {
	if m["code"] != float64(0) && m["code"] != "0" {
		return nil, ErrResponse
	}
	d := obj(m, "data")
	agents, ok := d["agents"].([]any)
	if !ok || len(agents) > 2000 {
		return nil, ErrResponse
	}
	rows, ok := d["models"].([]any)
	if !ok || len(rows) > 2000 {
		return nil, ErrResponse
	}
	defs := map[string]map[string]any{}
	for _, v := range rows {
		if r, ok := v.(map[string]any); ok {
			defs[str(r, "id")] = r
		}
	}
	var roster []any
	for _, v := range agents {
		if r, ok := v.(map[string]any); ok && str(r, "name") == "cli" {
			roster, _ = r["models"].([]any)
			break
		}
	}
	if roster == nil || len(roster) > 2000 {
		return nil, ErrResponse
	}
	out := []Model{}
	seen := map[string]bool{}
	for _, v := range roster {
		id, ok := v.(string)
		if !ok || !modelID(id) {
			return nil, ErrResponse
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		r := defs[id]
		model := Model{ID: id, Name: str(r, "name"), ContextWindow: positive(r, "maxInputTokens"), MaxOutputTokens: positive(r, "maxOutputTokens"), InputModalities: []string{"text"}}
		if r["supportsImages"] == true && !strings.HasPrefix(id, "glm-") {
			model.InputModalities = append(model.InputModalities, "image")
		}
		reason := obj(r, "reasoning")
		efforts, ok := reason["supportedEfforts"].([]any)
		if !ok && r["supportsReasoning"] == true {
			efforts = []any{"low", "high", "max"}
		}
		for _, v := range efforts {
			if e, ok := v.(string); ok && (e == "low" || e == "medium" || e == "high" || e == "xhigh" || e == "max") {
				model.ReasoningEfforts = append(model.ReasoningEfforts, e)
			}
		}
		if len(model.ReasoningEfforts) > 0 && r["onlyReasoning"] != true && reason["canDisableThinking"] != false {
			model.ReasoningEfforts = append([]string{"none"}, model.ReasoningEfforts...)
		}
		out = append(out, model)
	}
	return out, nil
}
func cursorModels(b []byte) ([]Model, error) {
	out := []Model{}
	seen := map[string]bool{}
	count := 0
	for len(b) > 0 {
		n, t, k := protowire.ConsumeTag(b)
		if k < 0 {
			return nil, ErrResponse
		}
		b = b[k:]
		if n != 1 || t != protowire.BytesType {
			k = protowire.ConsumeFieldValue(n, t, b)
			if k < 0 {
				return nil, ErrResponse
			}
			b = b[k:]
			continue
		}
		row, k := protowire.ConsumeBytes(b)
		if k < 0 {
			return nil, ErrResponse
		}
		b = b[k:]
		count++
		if count > 2000 {
			return nil, ErrResponse
		}
		m := Model{}
		for len(row) > 0 {
			f, t, k := protowire.ConsumeTag(row)
			if k < 0 {
				return nil, ErrResponse
			}
			row = row[k:]
			switch {
			case f == 1 && t == protowire.BytesType:
				var v []byte
				v, k = protowire.ConsumeBytes(row)
				m.ID = string(v)
			case f == 7 && t == protowire.VarintType:
				var v uint64
				v, k = protowire.ConsumeVarint(row)
				m.MaxMode = v != 0
			default:
				k = protowire.ConsumeFieldValue(f, t, row)
			}
			if k < 0 {
				return nil, ErrResponse
			}
			row = row[k:]
		}
		if modelID(m.ID) && !seen[m.ID] && len(out) < 500 {
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, ErrResponse
	}
	return out, nil
}
