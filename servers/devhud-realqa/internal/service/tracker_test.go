package service

import (
	"testing"

	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
)

func TestRepositoryDefinitionsProtoPreservesMultipleDropdown(t *testing.T) {
	t.Parallel()
	schema := repositoryDefinitionsProto(nil, realqagithub.RepositoryDefinitions{
		Forms: []realqagithub.IssueForm{{
			Fields: []realqagithub.FormField{{
				ID: "browsers", Kind: realqagithub.FormFieldDropdown,
				Label: "Browsers", Multiple: true,
			}},
		}},
	})
	if len(schema.IssueForms) != 1 ||
		len(schema.IssueForms[0].Fields) != 1 ||
		!schema.IssueForms[0].Fields[0].Multiple {
		t.Fatalf("multiple dropdown was not preserved: %#v", schema.IssueForms)
	}
}
