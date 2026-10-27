// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"net/http"
	"strings"
	"time"
)

// ZedAccount is encrypted private state. NativeToken is NEVER an inference bearer.
// LLMToken duplicates Credential.AccessToken to prevent an unprepared account token
// from passing the registry's inference handoff validation.
type ZedAccount struct {
	NativeToken    string `json:"nativeToken"`
	OrganizationID string `json:"organizationId"`
	SystemID       string `json:"systemId"`
	LLMToken       string `json:"llmToken"`
}
type ZedModel struct {
	ID                        string          `json:"id"`
	Name                      string          `json:"name"`
	Provider                  string          `json:"provider"`
	ContextLength             int64           `json:"contextLength,omitempty"`
	ContextLengthInMaxMode    int64           `json:"contextLengthInMaxMode,omitempty"`
	MaxOutputTokens           int64           `json:"maxOutputTokens,omitempty"`
	SupportsTools             bool            `json:"supportsTools"`
	SupportsImages            bool            `json:"supportsImages"`
	SupportsThinking          bool            `json:"supportsThinking"`
	SupportsDisablingThinking bool            `json:"supportsDisablingThinking"`
	SupportsFastMode          bool            `json:"supportsFastMode"`
	SupportedEffortLevels     []string        `json:"supportedEffortLevels"`
	Raw                       json.RawMessage `json:"raw"`
}
type ZedCatalog struct {
	Models                         []ZedModel
	DefaultModel, DefaultFastModel string
	RecommendedModels              []string
	ExpiresAt                      time.Time
}

func zedID(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		if len(x) > 0 {
			return zedID(x[0])
		}
	case map[string]any:
		return zedID(x["id"])
	}
	return ""
}
func zedOrganization(user map[string]any) string {
	for _, key := range []string{"default_organization_id", "defaultOrganizationId", "organization_id", "organizationId", "default_organization"} {
		if v := zedID(user[key]); v != "" {
			return v
		}
	}
	rows, _ := user["organizations"].([]any)
	first := ""
	for _, v := range rows {
		r, _ := v.(map[string]any)
		id := zedID(r["id"])
		if first == "" {
			first = id
		}
		if r["is_personal"] == true {
			return id
		}
	}
	return first
}
func validZed(c Credential) bool {
	return c.Provider == "zed" && c.Zed != nil && safeString(c.Zed.NativeToken) && safeString(c.Zed.OrganizationID) && safeString(c.AccountID) && safeString(c.AccessToken) && equal(c.AccessToken, c.Zed.LLMToken)
}

// PrepareZed performs account lookup followed by token exchange. It does not cache
// globally or persist. Commit the returned credential with the original private-data
// version and scope fence before using it. Calling again explicitly forces renewal.
func (a *Auth) PrepareZed(ctx context.Context, s core.Scope, c Credential) (Credential, error) {
	if !validScope(s) || c.Scope != s || c.Provider != "zed" {
		return Credential{}, ErrScope
	}
	if c.Zed == nil || !safeString(c.Zed.NativeToken) || !safeString(c.AccountID) {
		return Credential{}, ErrReauthenticate
	}
	z := *c.Zed
	h := http.Header{"Authorization": {c.AccountID + " " + z.NativeToken}}
	if z.SystemID != "" {
		if !safeString(z.SystemID) {
			return Credential{}, ErrResponse
		}
		h.Set("x-zed-system-id", z.SystemID)
	}
	user, e := a.send(ctx, s, "GET", "https://cloud.zed.dev/client/users/me", nil, h)
	if e != nil {
		return Credential{}, e
	}
	organization := zedOrganization(user)
	if !safeString(organization) {
		return Credential{}, ErrResponse
	}
	m, e := a.send(ctx, s, "POST", "https://cloud.zed.dev/client/llm_tokens", map[string]string{"organization_id": organization}, h)
	if e != nil {
		return Credential{}, e
	}
	token := str(m, "token")
	if token == "" {
		token = str(obj(m, "token"), "value")
	}
	for _, k := range []string{"access_token", "llm_token", "llmToken"} {
		if token == "" {
			token = str(m, k)
		}
	}
	if !safeString(token) {
		return Credential{}, ErrResponse
	}
	z.OrganizationID = organization
	z.LLMToken = token
	c.Zed = &z
	c.AccessToken = token
	c.RefreshToken = ""
	c.ExpiresAt = a.now().Add(50 * time.Minute)
	return c, nil
}

// ShouldRefreshZed must be checked before decoding any response bytes. The caller
// owns one recovery attempt and the shared physical-send budget, even for HTTP 200.
func ShouldRefreshZed(status int, h http.Header) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	for k := range h {
		if strings.EqualFold(k, "x-zed-expired-token") || strings.EqualFold(k, "x-zed-outdated-token") {
			return true
		}
	}
	return false
}

// ZedModels fetches the account-specific catalog, with at most one token recovery.
// Every support call and retry uses Executor, and thus the same admission/send budget.
// The returned credential can differ from input; persist it with compare-and-swap.
func (a *Auth) ZedModels(ctx context.Context, s core.Scope, c Credential) (ZedCatalog, Credential, error) {
	return a.zedModels(ctx, s, c, nil)
}
func (a *Auth) zedModels(ctx context.Context, s core.Scope, c Credential, custom map[string]string) (ZedCatalog, Credential, error) {
	if !validScope(s) || s != c.Scope {
		return ZedCatalog{}, Credential{}, ErrScope
	}
	if !validZed(c) {
		return ZedCatalog{}, Credential{}, ErrReauthenticate
	}
	var e error
	if !a.now().Before(c.ExpiresAt) {
		c, e = a.PrepareZed(ctx, s, c)
		if e != nil {
			return ZedCatalog{}, Credential{}, e
		}
	}
	var body []byte
	for attempt := 0; attempt < 2; attempt++ {
		h := bearer(c.AccessToken)
		overlayHeaders(h, custom)
		h.Set("x-zed-client-supports-x-ai", "true")
		b, status, headers, err := a.requestBytes(ctx, s, "GET", "https://cloud.zed.dev/models", nil, h, 4*1024*1024)
		if err != nil {
			return ZedCatalog{}, Credential{}, err
		}
		if ShouldRefreshZed(status, headers) {
			if attempt == 1 {
				return ZedCatalog{}, Credential{}, ErrReauthenticate
			}
			c, e = a.PrepareZed(ctx, s, c)
			if e != nil {
				return ZedCatalog{}, Credential{}, e
			}
			continue
		}
		if status < 200 || status >= 300 {
			return ZedCatalog{}, Credential{}, authFailure(status, nil)
		}
		body = b
		break
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return ZedCatalog{}, Credential{}, ErrResponse
	}
	var rows []any
	record, _ := value.(map[string]any)
	switch x := value.(type) {
	case []any:
		rows = x
	case map[string]any:
		for _, key := range []string{"models", "data", "available_models"} {
			if r, ok := x[key].([]any); ok {
				rows = r
				break
			}
		}
	}
	if rows == nil || len(rows) > 2000 {
		return ZedCatalog{}, Credential{}, ErrResponse
	}
	catalog := ZedCatalog{ExpiresAt: a.now().Add(time.Hour), DefaultModel: zedID(record["default_model"]), DefaultFastModel: zedID(record["default_fast_model"])}
	if catalog.DefaultModel == "" {
		catalog.DefaultModel = zedID(record["defaultModel"])
	}
	if catalog.DefaultFastModel == "" {
		catalog.DefaultFastModel = zedID(record["defaultFastModel"])
	}
	recommended, _ := record["recommended_models"].([]any)
	if recommended == nil {
		recommended, _ = record["recommendedModels"].([]any)
	}
	for _, v := range recommended {
		if id := zedID(v); safeString(id) {
			catalog.RecommendedModels = append(catalog.RecommendedModels, id)
		}
	}
	for _, v := range rows {
		r, ok := v.(map[string]any)
		if !ok {
			continue
		}
		id := zedID(r["id"])
		if !safeString(id) || r["is_disabled"] == true || r["isDisabled"] == true {
			continue
		}
		name := str(r, "display_name")
		if name == "" {
			name = str(r, "displayName")
		}
		if name == "" {
			name = id
		}
		raw, _ := json.Marshal(r)
		model := ZedModel{ID: id, Name: name, Provider: str(r, "provider"), Raw: raw, ContextLength: zedPositive(r, "max_token_count", "maxTokenCount"), ContextLengthInMaxMode: zedPositive(r, "max_token_count_in_max_mode", "maxTokenCountInMaxMode"), MaxOutputTokens: zedPositive(r, "max_output_tokens", "maxOutputTokens"), SupportsTools: zedBool(r, "supports_tools", "supportsTools"), SupportsImages: zedBool(r, "supports_images", "supportsImages"), SupportsThinking: zedBool(r, "supports_thinking", "supportsThinking"), SupportsDisablingThinking: zedBool(r, "supports_disabling_thinking", "supportsDisablingThinking"), SupportsFastMode: zedBool(r, "supports_fast_mode", "supportsFastMode")}
		efforts, _ := r["supported_effort_levels"].([]any)
		if efforts == nil {
			efforts, _ = r["supportedEffortLevels"].([]any)
		}
		if efforts != nil {
			model.SupportedEffortLevels = make([]string, 0, len(efforts))
		}
		for _, v := range efforts {
			if s, ok := v.(string); ok && safeString(s) {
				model.SupportedEffortLevels = append(model.SupportedEffortLevels, s)
			}
		}
		catalog.Models = append(catalog.Models, model)
	}
	return catalog, c, nil
}
func zedPositive(r map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if n, ok := r[k].(float64); ok && n > 0 && n < 9007199254740992 && n == float64(int64(n)) {
			return int64(n)
		}
	}
	return 0
}
func zedBool(r map[string]any, keys ...string) bool {
	for _, k := range keys {
		if r[k] == true {
			return true
		}
	}
	return false
}
