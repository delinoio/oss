package github

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOptInPublicGitHubResponseProjection(t *testing.T) {
	prefix := os.Getenv("DELIDEV_GITHUB_SCHEMA_FIXTURE_PREFIX")
	if prefix == "" {
		t.Skip("requires explicitly captured public GitHub schema fixtures")
	}
	// These are read-only public response captures, not production adapter/PAT
	// acceptance. No ambient gh token is read or forwarded by this test.
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(prefix + name + "-shape.json")
		if err != nil || len(raw) > 1<<20 {
			t.Fatal("invalid public fixture", name, err)
		}
		return raw
	}
	fields, ok := jsonObject(read("repository"))
	if !ok {
		t.Fatal("invalid repository")
	}
	owner, _ := jsonObject(fields["owner"])
	repository, ok := parseRepository(read("repository"), stringField(owner, "login"), stringField(fields, "name"))
	if !ok {
		t.Fatal("repository projection failed")
	}
	var pulls []json.RawMessage
	if domain.Decode(read("pr"), &pulls) != nil || len(pulls) != 1 {
		t.Fatal("capture one PR")
	}
	q := listQuery(domain.RepositoryPullRequest)
	list, skip, err := parseItem(pulls[0], *repository, q)
	if err != nil || skip {
		t.Fatal("list projection failed", err)
	}
	detailQuery := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: list.Number}
	detail, skip, err := parseItem(read("detail"), *repository, detailQuery)
	if err != nil || skip || detail.NodeID != list.NodeID || detail.ID != list.ID {
		t.Fatal("detail projection changed identity", err)
	}
	searchFields, ok := jsonObject(read("search"))
	var matches []json.RawMessage
	if !ok || domain.Decode(searchFields["items"], &matches) != nil || len(matches) != 1 {
		t.Fatal("capture matching PR search")
	}
	q.Operation = domain.RepositorySearch
	q.Search = list.Number
	search, skip, err := parseItem(matches[0], *repository, q)
	if err != nil || skip || search.NodeID != list.NodeID || search.IdentitySource != domain.RepositoryIssueIdentity {
		t.Fatal("search projection failed", err)
	}
	issue, skip, err := parseItem(read("issue"), *repository, q)
	if err != nil || skip || issue.ID != search.ID {
		t.Fatal("issue/search identity disagrees", err)
	}
	// PR node IDs agree, but the issue API numeric ID is a different namespace.
	if search.ID == list.ID {
		t.Fatal("fixture must demonstrate distinct numeric namespaces")
	}
}
