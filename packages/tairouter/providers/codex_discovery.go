// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/core"
	"net/url"
	"regexp"
	"strings"
)

var codexVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*([-+][0-9A-Za-z.-]+)?$`)

// The pinned upstream uses 99.0.0 for discovery, separately from runtime-version
// entitlement. Callers must supply their actual version for entitlement evidence.
const CodexDiscoveryVersion = "99.0.0"

func validCodexVersion(v string) bool {
	if len(v) > 64 || !codexVersionPattern.MatchString(v) {
		return false
	}
	core := strings.FieldsFunc(v, func(r rune) bool { return r == '-' || r == '+' })[0]
	return strings.Trim(core, "0.") != ""
}
func (a *Auth) codexModels(ctx context.Context, s core.Scope, c Credential, version string, headers map[string]string) (ModelCatalog, *Credential, error) {
	if version == "" {
		version = CodexDiscoveryVersion
	}
	if !validCodexVersion(version) {
		return ModelCatalog{}, nil, ErrConnection
	}
	h := bearer(c.AccessToken)
	overlayHeaders(h, headers)
	if c.AccountID != "" {
		if !safeString(c.AccountID) {
			return ModelCatalog{}, nil, ErrResponse
		}
		h.Set("ChatGPT-Account-Id", c.AccountID)
	}
	b, status, _, err := a.requestBytes(ctx, s, "GET", "https://chatgpt.com/backend-api/codex/models?client_version="+url.QueryEscape(version), nil, h, 4<<20)
	if err != nil {
		return ModelCatalog{}, nil, err
	}
	if status != 200 {
		return ModelCatalog{}, nil, authFailure(status, nil)
	}
	var response struct {
		Models []json.RawMessage `json:"models"`
	}
	if json.Unmarshal(b, &response) != nil || response.Models == nil || len(response.Models) > 2000 {
		return ModelCatalog{}, nil, ErrResponse
	}
	out := ModelCatalog{State: ValidationAccepted, CodexClientVersion: version, CodexCatalog: make([]json.RawMessage, 0, len(response.Models)), Models: make([]Model, 0, len(response.Models))}
	seen := map[string]bool{}
	for _, raw := range response.Models {
		var row struct {
			Slug    string `json:"slug"`
			Name    string `json:"display_name"`
			Context int    `json:"context_window"`
			Output  int    `json:"max_output_tokens"`
		}
		if json.Unmarshal(raw, &row) != nil || !modelID(row.Slug) || seen[row.Slug] {
			return ModelCatalog{}, nil, ErrResponse
		}
		seen[row.Slug] = true
		// Preserve the entire authenticated row byte-for-byte, including native prompt
		// templates and unknown fields. Never fabricate a row from a model ID.
		out.CodexCatalog = append(out.CodexCatalog, append(json.RawMessage(nil), raw...))
		out.Models = append(out.Models, Model{ID: row.Slug, Name: row.Name, ContextWindow: row.Context, MaxOutputTokens: row.Output})
	}
	return out, nil, nil
}
