package service

import (
	"testing"

	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
)

func TestRepositoryDefinitionsProtoPreservesMultipleDropdown(t *testing.T) {
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
			}},
		}},
	})
	if len(schema.IssueForms) != 1 ||
		schema.IssueForms[0].IssueType != "Bug" ||
		len(schema.IssueForms[0].Fields) != 2 ||
		!schema.IssueForms[0].Fields[0].Multiple ||
		schema.IssueForms[0].Fields[1].DefaultValue != "A bug happened" {
		t.Fatalf("provider metadata was not preserved: %#v", schema.IssueForms)
	}
}
