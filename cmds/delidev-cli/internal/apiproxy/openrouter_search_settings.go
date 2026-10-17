// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Translate only documented server-tool parameters. Native caller restrictions
// and cache-only access have no equivalent remote enforcement; rejecting them
// before credential access prevents a successful search with weaker settings.
func openRouterSearchParameters(raw json.RawMessage) (map[string]any, error) {
	parameters := map[string]any{}
	if !externalJSON(raw) {
		return parameters, nil
	}
	var settings struct {
		Location *struct {
			Type     string  `json:"type"`
			Country  *string `json:"country,omitempty"`
			Region   *string `json:"region,omitempty"`
			City     *string `json:"city,omitempty"`
			Timezone *string `json:"timezone,omitempty"`
		} `json:"user_location"`
		Context *string `json:"search_context_size"`
		Filters *struct {
			Allowed json.RawMessage `json:"allowed_domains"`
			Blocked json.RawMessage `json:"blocked_domains"`
		} `json:"filters"`
		Image *struct {
			MaxResults *uint64 `json:"max_results"`
			Caption    *bool   `json:"caption"`
		} `json:"image_settings"`
		Callers  *[]string       `json:"allowed_callers"`
		External json.RawMessage `json:"external_web_access"`
	}
	if domain.DecodeBounded(raw, &settings, maxBody) != nil || settings.Image != nil && (settings.Image.MaxResults != nil || settings.Image.Caption != nil) || settings.Callers != nil && !slices.Equal(*settings.Callers, []string{"direct"}) {
		return nil, openRouterToolError()
	}
	if externalJSON(settings.External) && string(settings.External) != "true" && string(settings.External) != `"live"` {
		return nil, openRouterToolError()
	}
	if settings.Location != nil {
		if settings.Location.Type != "approximate" {
			return nil, openRouterToolError()
		}
		parameters["user_location"] = settings.Location
	}
	if settings.Context != nil {
		if !slices.Contains([]string{"low", "medium", "high"}, *settings.Context) {
			return nil, openRouterToolError()
		}
		parameters["search_context_size"] = *settings.Context
	}
	if settings.Filters != nil {
		for name, value := range map[string]json.RawMessage{"allowed_domains": settings.Filters.Allowed, "excluded_domains": settings.Filters.Blocked} {
			if !validSearchField(value, searchField{kind: searchStrings}) {
				return nil, openRouterToolError()
			}
			if externalJSON(value) {
				parameters[name] = value
			}
		}
	}
	return parameters, nil
}
