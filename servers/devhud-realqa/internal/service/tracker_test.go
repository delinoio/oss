package service

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
)

func TestValidateAuthorizationTargetRequiresCanonicalOAuthCallback(t *testing.T) {
	t.Parallel()
	authorization, err := realqagithub.NewAuthorization("fixture-realqa-client")
	if err != nil {
		t.Fatal(err)
	}
	target, err := authorization.Target(strings.Repeat("a", 43))
	if err != nil {
		t.Fatal(err)
	}
	if err = validateAuthorizationTarget(target); err != nil {
		t.Fatalf("canonical target was rejected: %v", err)
	}

	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("redirect_uri", "https://example.com/github/oauth/callback")
	parsed.RawQuery = query.Encode()
	if err = validateAuthorizationTarget(parsed.String()); err == nil {
		t.Fatal("substituted OAuth callback was accepted")
	}
}

func TestRepositoryPageCursorPreservesSource(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		source repositoryPageSource
		cursor string
	}{
		{
			name:   "live provider",
			source: repositoryPageSourceLive, cursor: "github-v1:2:10",
		},
		{
			name:   "cached repositories",
			source: repositoryPageSourceCache, cursor: "123456",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded := encodeRepositoryPageCursor(test.source, test.cursor)
			source, cursor, err := decodeRepositoryPageCursor(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if source != test.source || cursor != test.cursor {
				t.Fatalf("decoded cursor = %q %q", source, cursor)
			}
		})
	}

	unnamespaced := base64.RawURLEncoding.EncodeToString([]byte("github-v1:2:10"))
	if _, _, err := decodeRepositoryPageCursor(unnamespaced); err == nil {
		t.Fatal("unnamespaced provider cursor was accepted")
	}
}

func TestRepositoryDefinitionsProtoPreservesProviderMetadata(t *testing.T) {
	t.Parallel()
	schema := repositoryDefinitionsProto(nil, realqagithub.RepositoryDefinitions{
		Forms: []realqagithub.IssueForm{{
			IssueType: "Bug",
			Fields: []realqagithub.FormField{{
				ID: "browsers", Kind: realqagithub.FormFieldDropdown,
				Label: "Browsers", Multiple: true,
			}, {
				ID: "summary", Kind: realqagithub.FormFieldInput,
				Label: "Summary", DefaultValue: "A bug happened",
			}, {
				ID: "logs", Kind: realqagithub.FormFieldTextarea,
				Label: "Logs", Render: "shell",
			}},
		}},
	})
	if len(schema.IssueForms) != 1 ||
		schema.IssueForms[0].IssueType != "Bug" ||
		len(schema.IssueForms[0].Fields) != 3 ||
		!schema.IssueForms[0].Fields[0].Multiple ||
		schema.IssueForms[0].Fields[1].DefaultValue != "A bug happened" ||
		schema.IssueForms[0].Fields[2].RenderLanguage != "shell" {
		t.Fatalf("provider metadata was not preserved: %#v", schema.IssueForms)
	}
}
