// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type webSearchItem struct {
	ID          string
	Observation domain.CodexWebSearchObservation
}

func decodeWebSearch(raw json.RawMessage) (webSearchItem, error) {
	var item struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Query   *string         `json:"query"`
		Action  json.RawMessage `json:"action"`
		Results json.RawMessage `json:"results"`
	}
	if domain.Decode(raw, &item) != nil || item.Type != "webSearch" || domain.Text(item.ID, "native search identity", 1024, true) != nil || item.Query == nil || domain.Text(*item.Query, "native search query", 16<<10, false) != nil || len(item.Action) == 0 || len(item.Results) == 0 {
		return webSearchItem{}, incompatible()
	}
	observed := domain.CodexWebSearchObservation{Query: *item.Query}
	if !bytes.Equal(bytes.TrimSpace(item.Action), []byte("null")) {
		var action domain.CodexWebSearchAction
		if domain.Decode(item.Action, &action) != nil || action.Validate() != nil {
			return webSearchItem{}, incompatible()
		}
		// The pinned union permits omission, but never explicit null action fields.
		var fields map[string]json.RawMessage
		if domain.Decode(item.Action, &fields) != nil {
			return webSearchItem{}, incompatible()
		}
		for key, value := range fields {
			if key != "type" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return webSearchItem{}, incompatible()
			}
		}
		observed.Action = &action
	}
	if !bytes.Equal(bytes.TrimSpace(item.Results), []byte("null")) {
		var results []json.RawMessage
		if len(item.Results) > 384<<10 || domain.DecodeBounded(item.Results, &results, 384<<10) != nil || results == nil || len(results) > 1024 {
			return webSearchItem{}, incompatible()
		}
		value := string(item.Results)
		observed.Results = &value
	}
	return webSearchItem{ID: item.ID, Observation: observed}, nil
}
func webSearchUnavailable() error {
	return domain.Fail(domain.Unsupported, "Native web search support is unavailable for this account source.", "Keep the original mode and account. No fallback search or native input was sent.")
}
func configureWebSearch(config *Config) error {
	if config.WebSearch == "" {
		return nil
	}
	if !config.WebSearch.Valid() || config.Mode != ThreadProtocol {
		return unsupportedSettings()
	}
	if config.Sidechat != "" && config.WebSearch != domain.WebSearchDisabled {
		return sidechatUnavailable()
	}
	if config.WebSearch != domain.WebSearchDisabled && (!config.ManagedAuthentication || config.API != nil) {
		return webSearchUnavailable()
	}
	config.Process.Args = append(config.Process.Args, "-c", "web_search="+strconv.Quote(string(config.WebSearch)))
	return nil
}

// Process-owned CLI overrides and the cwd-specific effective config/policy read
// establish mode. A thread item supplies no mode and cannot replace this proof.
func (c *Client) verifyWebSearch(ctx context.Context, settings ThreadSettings) error {
	mode := settings.Options.WebSearch
	if mode == "" {
		if c.webSearch != "" {
			return incompatible()
		}
		return nil
	}
	if mode != c.webSearch || !mode.Valid() {
		return incompatible()
	}
	if mode != domain.WebSearchDisabled && (c.managedHome == "" || settings.Provider != "openai" || c.api != nil || c.sidechat != "") {
		return webSearchUnavailable()
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "config/read", map[string]any{"cwd": settings.Cwd, "includeLayers": false})
	if err != nil {
		return err
	}
	if response.ErrorCode != nil {
		return nativeRejected(*response.ErrorCode)
	}
	requirements, err := c.wire.Call(ctx, domain.NewID(), "configRequirements/read", nativewire.OmittedParams{})
	if err != nil {
		return err
	}
	if requirements.ErrorCode != nil {
		return nativeRejected(*requirements.ErrorCode)
	}
	if err := validateWebSearchProfile(mode, response.Result, requirements.Result); err != nil {
		return err
	}
	if c.logger != nil {
		c.logger.DebugContext(ctx, "Codex web search settings verified", "owner_id", c.ownerID, "stage", "native-web-search-config", "mode", mode)
	}
	return nil
}

// Each input uses the original process selector and native effective policy.
// Keeping this decoder separate lets fixtures prove denial without a native account.
func validateWebSearchProfile(mode domain.WebSearchMode, configResult, requirementsResult json.RawMessage) error {
	var read struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers,omitempty"`
	}
	var effective domain.WebSearchMode
	if domain.Decode(configResult, &read) != nil || json.Unmarshal(read.Config["web_search"], &effective) != nil || !effective.Valid() {
		return incompatible()
	}
	var policy struct {
		Requirements                  map[string]json.RawMessage `json:"requirements"`
		SupportsIndependentSpeedModes *bool                      `json:"supportsIndependentSpeedModes,omitempty"`
	}
	var policyFields map[string]json.RawMessage
	if domain.Decode(requirementsResult, &policyFields) != nil || len(policyFields["requirements"]) == 0 || domain.Decode(requirementsResult, &policy) != nil {
		return incompatible()
	}
	// This wire profile serializes requirements in camelCase. A TOML-shaped
	// requirement must not silently bypass its native allow-list.
	if _, stale := policy.Requirements["allowed_web_search_modes"]; stale {
		return incompatible()
	}
	if raw, ok := policy.Requirements["allowedWebSearchModes"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var allowed []domain.WebSearchMode
		if domain.Decode(raw, &allowed) != nil || allowed == nil || len(allowed) > 3 {
			return incompatible()
		}
		for i, value := range allowed {
			if !value.Valid() || slices.Contains(allowed[:i], value) {
				return incompatible()
			}
		}
		if !slices.Contains(allowed, mode) {
			return domain.Fail(domain.PermissionDenied, "Native policy denies the selected web search mode.", "Keep the draft and original policy. No native input was sent.")
		}
	}
	if effective != mode {
		return incompatible()
	}
	return nil
}
