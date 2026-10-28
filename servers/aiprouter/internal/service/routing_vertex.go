// SPDX-License-Identifier: Apache-2.0
package service

import (
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

var selectedVertexLocation = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Resolve the selected private metadata before destination authorization. The
// adapter must not change an authorized global origin into a regional origin.
func selectedVertexMetadata(d providerData) (project, location, base string, err error) {
	if d.Credential != nil {
		project = strings.TrimSpace(d.Credential.ProjectID)
		if project == "" && d.Credential.Google != nil {
			project = strings.TrimSpace(d.Credential.Google.ProjectID)
		}
		location = strings.TrimSpace(d.Credential.Metadata["location"])
	}
	if config := d.Metadata.Configuration; config != nil {
		if config.VertexProject != nil {
			project = *config.VertexProject
		}
		if config.VertexLocation != nil {
			location = *config.VertexLocation
		}
	}
	if project == "auto" && d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY {
		// A logical auto target stays on the fixed express origin until negotiation
		// resolves the captured key. The final regional origin is authorized afresh.
		return "", "", "https://aiplatform.googleapis.com", nil
	}
	if strings.EqualFold(project, "auto") || len(project) > 1024 || strings.ContainsAny(project, "\x00\r\n") || project == "." || project == ".." {
		return "", "", "", inference.ErrUnsupported
	}
	if d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH && (project == "" || location == "") {
		return "", "", "", inference.ErrUnsupported
	}
	base = "https://aiplatform.googleapis.com"
	if project == "" {
		return project, "", base, nil
	}
	if location == "" {
		location = "global"
	}
	if !selectedVertexLocation.MatchString(location) {
		return "", "", "", inference.ErrUnsupported
	}
	if location != "global" {
		base = "https://" + location + "-aiplatform.googleapis.com"
	}
	return
}
