package github

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
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

func TestOptInPublicGitHubPRObservationProjection(t *testing.T) {
	prefix := os.Getenv("DELIDEV_GITHUB_SCHEMA_FIXTURE_PREFIX")
	if prefix == "" {
		t.Skip("requires explicitly captured public PR observation fixtures")
	}
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(prefix + name)
		if err != nil || len(raw) > 1<<20 {
			t.Fatal("invalid public fixture", name, err)
		}
		return raw
	}
	fields, ok := jsonObject(read("repository-shape.json"))
	if !ok {
		t.Fatal("repository JSON")
	}
	owner, _ := jsonObject(fields["owner"])
	repository, ok := parseRepository(read("repository-shape.json"), stringField(owner, "login"), stringField(fields, "name"))
	if !ok {
		t.Fatal("repository projection")
	}
	detailFields, ok := jsonObject(read("detail-shape.json"))
	number, numberOK := exactUnsigned(detailFields["number"])
	if !ok || !numberOK {
		t.Fatal("detail shape")
	}
	item, skip, err := parseItem(read("detail-shape.json"), *repository, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: strconv.FormatUint(number, 10)})
	if err != nil || skip {
		t.Fatal("detail projection", err)
	}
	if _, err := parsePRChecks(read("checks-shape.json"), *repository, item, 1); err != nil {
		t.Fatal("check projection", err)
	}
	if _, err := parsePRStatuses(read("statuses-shape.json"), *repository, item, 1); err != nil {
		t.Fatal("status projection", err)
	}
	if !bytes.Equal(read("diff-shape.diff"), read("compare-shape.diff")) {
		t.Fatal("immutable comparison differs from original PR diff fixture")
	}
}

func TestOptInPublicGitHubPaginationHeaders(t *testing.T) {
	prefix := os.Getenv("DELIDEV_GITHUB_SCHEMA_FIXTURE_PREFIX")
	if prefix == "" {
		t.Skip("requires public pagination header captures")
	}
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(prefix + name)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	fields, ok := jsonObject(read("repository-shape.json"))
	owner, _ := jsonObject(fields["owner"])
	repository, valid := parseRepository(read("repository-shape.json"), stringField(owner, "login"), stringField(fields, "name"))
	if !ok || !valid {
		t.Fatal("repository fixture")
	}
	q := listQuery(domain.RepositoryPullRequest)
	q.State = domain.RepositoryItemsAll
	q.PageSize = 1
	path, _ := queryPath(*repository, q)
	header := strings.TrimPrefix(strings.TrimSpace(string(read("pr-page-link.txt"))), "Link: ")
	if next, err := queryNextPage(header, path, 1, *repository); err != nil || next != 2 {
		t.Fatal("actual PR pagination rejected", next, err)
	}
	detail, _ := jsonObject(read("detail-shape.json"))
	head, _ := jsonObject(detail["head"])
	path = repositoryPath(*repository) + "/commits/" + stringField(head, "sha") + "/check-runs?filter=latest&page=1&per_page=1"
	header = strings.TrimPrefix(strings.TrimSpace(string(read("check-page-link.txt"))), "Link: ")
	if next, err := queryNextPage(header, path, 1, *repository); err != nil || next != 2 {
		t.Fatal("actual Checks pagination rejected", next, err)
	}
}
