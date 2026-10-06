// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"net/http"
)

type oauthProjectKey struct{}

// InspectOAuth retains the ordinary inspection and outbound route ownership.
// Only a server-resolved OAuth connection supplies this non-secret project.
func InspectOAuth(ctx context.Context, p domain.Provider, key []byte, project string, routing ...outbound.Resolver) (Observation, error) {
	if project != "" && !googleOAuthProvider(p, project) {
		return Observation{}, domain.Fail(domain.PermissionDenied, "OAuth project belongs to another provider.", "Use the original account connection.")
	}
	return Inspect(context.WithValue(ctx, oauthProjectKey{}, project), p, key, routing...)
}
func googleOAuthProvider(p domain.Provider, project string) bool {
	return domain.ValidGoogleProjectID(project) && p.PresetID != nil && *p.PresetID == domain.PresetGemini && p.EnabledValue() && p.Endpoint == "https://generativelanguage.googleapis.com/v1beta/openai" && p.Protocol == domain.OpenAIChat && p.Authentication == domain.BearerAuth
}

// ApplyOAuthProject never accepts a downstream request header as authority.
// It independently verifies the exact provider and destination before billing.
func ApplyOAuthProject(r *http.Request, p domain.Provider, project string) error {
	if project == "" {
		return nil
	}
	if !googleOAuthProvider(p, project) || r.URL.Scheme != "https" || r.URL.Host != "generativelanguage.googleapis.com" || r.URL.User != nil {
		return domain.Fail(domain.PermissionDenied, "OAuth project destination does not match.", "Preserve the original Google connection.")
	}
	r.Header.Set("x-goog-user-project", project)
	return nil
}
