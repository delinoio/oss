// SPDX-License-Identifier: Apache-2.0
// Endpoint selection follows google-vertex-endpoint.ts; see UPSTREAM-LICENSE.
package google

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const (
	WireVertex inference.Wire = "google-vertex"
	// LocationHeader is selected-account metadata and is never forwarded upstream.
	LocationHeader = "X-Tairouter-Google-Location"
)

// NewVertex uses explicit selected project/location metadata and an already
// acquired OAuth access token or API key. Token acquisition belongs to the
// credential owner; this adapter never reads ambient credentials or sends I/O.
func NewVertex(options Options) *Adapter {
	a := newAdapter(false, options)
	a.vertex = true
	a.options.DisableDirectModelRenames = true
	// Vertex has no inferred effort ladder in the pin. Explicit options can add one.
	if a.options.ReasoningEfforts == nil {
		a.options.ReasoningEfforts = []string{}
	}
	return a
}

var vertexLocation = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func selectedMetadata(in inference.AdapterInput, header, key string) string {
	if values, ok := in.Credential.Headers[http.CanonicalHeaderKey(header)]; ok {
		return strings.TrimSpace(strings.Join(values, ""))
	}
	if values, ok := in.Target.Headers[http.CanonicalHeaderKey(header)]; ok {
		return strings.TrimSpace(strings.Join(values, ""))
	}
	if in.Target.Policy != nil {
		if m, e := decodeJSON(in.Target.Policy.Metadata()); e == nil {
			return strings.TrimSpace(text(m[key]))
		}
	}
	return ""
}

func vertexSelection(in inference.AdapterInput) (project, location, base string, err error) {
	project = selectedMetadata(in, ProjectHeader, "project")
	location = selectedMetadata(in, LocationHeader, "location")
	if strings.EqualFold(project, "auto") {
		return "", "", "", failure(400, "google_vertex_project_unresolved")
	}
	if len(project) > 1024 || strings.ContainsAny(project, "\x00\r\n") || project == "." || project == ".." {
		return "", "", "", invalid()
	}
	if in.Target.Auth == inference.OAuth {
		if project == "" {
			return "", "", "", failure(400, "google_project_required")
		}
		if location == "" {
			return "", "", "", failure(400, "google_vertex_location_required")
		}
	} else if in.Target.Auth != inference.APIKey {
		return "", "", "", inference.ErrUnsupported
	}
	if project == "" {
		return "", "", "https://aiplatform.googleapis.com", nil
	}
	if location == "" {
		location = "global"
	}
	if !vertexLocation.MatchString(location) {
		return "", "", "", failure(400, "google_vertex_location_invalid")
	}
	base = "https://aiplatform.googleapis.com"
	if location != "global" {
		base = "https://" + location + "-aiplatform.googleapis.com"
	}
	return
}

func vertexPath(u *url.URL, project, location, model, method string) error {
	if strings.Contains(model, "..") {
		return invalid()
	}
	if u.Path != "" && u.Path != "/" && u.Path != "/v1" {
		return inference.ErrDestination
	}
	suffix := "/publishers/google/models/" + model + ":" + method
	u.Path = "/v1" + suffix
	u.RawPath = ""
	if project != "" {
		u.Path = "/v1/projects/" + project + "/locations/" + location + suffix
		u.RawPath = "/v1/projects/" + url.PathEscape(project) + "/locations/" + location + suffix
	}
	return nil
}
