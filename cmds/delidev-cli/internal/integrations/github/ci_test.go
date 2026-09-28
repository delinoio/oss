package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func ciNodeFixture() map[string]any {
	head := strings.Repeat("b", 40)
	return map[string]any{"id": "ITEM_stable", "number": 17, "state": "OPEN", "merged": false, "baseRefName": "main", "baseRefOid": accessSHA, "headRefName": "feature", "headRefOid": head, "mergeable": "MERGEABLE", "isInMergeQueue": false, "repository": map[string]any{"id": "R_37"},
		"potentialMergeCommit": map[string]any{"oid": strings.Repeat("c", 40), "parents": map[string]any{"totalCount": 2, "nodes": []any{map[string]any{"oid": accessSHA}, map[string]any{"oid": head}}}, "statusCheckRollup": nil},
		"statusCheckRollup":    map[string]any{"commit": map[string]any{"oid": head}, "contexts": map[string]any{"totalCount": 1, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": "one"}, "nodes": []any{map[string]any{"__typename": "CheckRun", "id": "CHECK_53", "name": "CI Result", "status": "COMPLETED", "conclusion": "FAILURE", "isRequired": true, "repository": map[string]any{"id": "R_37"}, "checkSuite": map[string]any{"commit": map[string]any{"oid": head}, "app": map[string]any{"databaseId": 15368, "id": "APP_15368", "slug": "github-actions"}, "workflowRun": map[string]any{"event": "pull_request"}}}}}},
	}
}
func ciClientFixture(t *testing.T, mutate func(int, map[string]any) map[string]any) (*Client, *int) {
	t.Helper()
	c, _ := rulesFixtureClient(t, nil)
	original := c.http.Transport
	reads := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/graphql" {
			return original.RoundTrip(r)
		}
		if r.Method != "POST" || r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Fatal("unscoped CI read")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var request struct {
			Query         string `json:"query"`
			OperationName string `json:"operationName"`
			Variables     struct {
				ID         string  `json:"id"`
				HeadAfter  *string `json:"headAfter"`
				MergeAfter *string `json:"mergeAfter"`
			} `json:"variables"`
		}
		if domain.Decode(raw, &request) != nil || request.Query != ciGraphQL || request.OperationName != "DeliDevRequiredCI" || request.Variables.ID != "ITEM_stable" {
			t.Fatal("arbitrary CI document or wrong PR identity")
		}
		reads++
		node := ciNodeFixture()
		envelope := map[string]any{"data": map[string]any{"node": node}}
		if mutate != nil {
			envelope = mutate(reads, node)
		}
		raw, _ = json.Marshal(envelope)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	return c, &reads
}
func ciFixtureItem(t *testing.T) (domain.RemoteRepository, domain.RepositoryItem) {
	t.Helper()
	repo := domain.RemoteRepository{Provider: domain.GitHubCom, ID: "37", NodeID: "R_37", Owner: "fixture-owner", Name: "repo"}
	raw, _ := json.Marshal(queryFixtureItem(domain.RepositoryPullRequest, 17, true, false))
	item, _, err := parseItem(raw, repo, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: "17"})
	if err != nil {
		t.Fatal(err)
	}
	return repo, item
}
func TestCIInventoryPinsDocumentCommitAppAndWorkflow(t *testing.T) {
	c, reads := ciClientFixture(t, nil)
	repo, item := ciFixtureItem(t)
	v, err := c.readCIInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
	if err != nil || *reads != 1 || v.Head.Validate(item.HeadSHA) != nil || v.TestMerge == nil || v.TestMerge.TotalCount != "0" {
		t.Fatal("CI inventory not bound", err)
	}
	check := v.Head.Contexts[0]
	if check.Application.ID != "15368" || *check.WorkflowEvent != "pull_request" || !check.Required || *check.NativeConclusion != "FAILURE" {
		t.Fatal("CI provenance lost")
	}
}
func TestCIInventoryRejectsPartialErrorsForeignCommitsAndMissingAuthority(t *testing.T) {
	for _, mode := range []string{"partial-error", "foreign-head", "foreign-repo", "missing-required", "wrong-merge-parents", "missing-node", "truncated-page"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := ciClientFixture(t, func(_ int, node map[string]any) map[string]any {
				envelope := map[string]any{"data": map[string]any{"node": node}}
				contexts := node["statusCheckRollup"].(map[string]any)["contexts"].(map[string]any)
				switch mode {
				case "partial-error":
					envelope["errors"] = []any{map[string]any{"message": "private diagnostic"}}
				case "foreign-head":
					node["headRefOid"] = accessSHA
				case "foreign-repo":
					node["repository"] = map[string]any{"id": "FOREIGN"}
				case "missing-required":
					delete(contexts["nodes"].([]any)[0].(map[string]any), "isRequired")
				case "wrong-merge-parents":
					node["potentialMergeCommit"].(map[string]any)["parents"].(map[string]any)["totalCount"] = 1
				case "missing-node":
					contexts["nodes"] = []any{nil}
				case "truncated-page":
					contexts["totalCount"] = 2
				}
				return envelope
			})
			repo, item := ciFixtureItem(t)
			_, err := c.readCIInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
			if err == nil || strings.Contains(err.Error(), "private diagnostic") {
				t.Fatal("partial CI accepted or private error leaked", err)
			}
		})
	}
}
func TestCIAccumulatorRejectsDuplicateOrRepeatedCursor(t *testing.T) {
	sha := strings.Repeat("b", 40)
	cursor := "same"
	page := ciPage{total: 3, next: &cursor, rollup: domain.CIRollup{CommitSHA: sha, TotalCount: "3", Contexts: []domain.CIContext{{NodeID: "first"}}}}
	a := ciAccumulator{}
	if a.append(page) != nil {
		t.Fatal("first page rejected")
	}
	if a.append(page) == nil {
		t.Fatal("duplicate context accepted")
	}
	page.rollup.Contexts[0].NodeID = "second"
	if a.append(page) == nil {
		t.Fatal("repeated cursor accepted")
	}
}

func TestCIQueryBindsActiveRulesAndRejectsChangedResults(t *testing.T) {
	for _, changed := range []bool{false, true} {
		c, reads := ciClientFixture(t, func(read int, node map[string]any) map[string]any {
			if changed && read == 2 {
				node["statusCheckRollup"].(map[string]any)["contexts"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["conclusion"] = "SUCCESS"
			}
			return map[string]any{"data": map[string]any{"node": node}}
		})
		query := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryCI, Number: "17"}
		value, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", query)
		if *reads != 2 {
			t.Fatal("CI inventory was not rechecked", *reads)
		}
		if changed {
			if domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("changed CI result published", err)
			}
		} else if err != nil || value.CI == nil || value.CI.Validate(value.Items[0]) != nil || value.CI.Result.State != domain.CITerminalFailure || value.CI.Result.Requirements[0].RulesetID != "9007199254740993" {
			t.Fatal("required CI evaluation missing", err)
		}
	}
}

func TestCIInventoryCompletesBothPagesAndJoinsCancellation(t *testing.T) {
	c, _ := ciClientFixture(t, func(read int, node map[string]any) map[string]any {
		contexts := node["statusCheckRollup"].(map[string]any)["contexts"].(map[string]any)
		contexts["totalCount"] = 2
		contexts["pageInfo"] = map[string]any{"hasNextPage": read == 1, "endCursor": "next"}
		if read == 2 {
			row := contexts["nodes"].([]any)[0].(map[string]any)
			row["id"], row["name"], row["isRequired"] = "OPTIONAL_2", "Optional", false
		}
		return map[string]any{"data": map[string]any{"node": node}}
	})
	original := c.http.Transport
	queries := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/graphql" {
			queries++
			raw, _ := io.ReadAll(r.Body)
			f, _ := jsonObject(raw)
			variables, _ := jsonObject(f["variables"])
			if queries == 2 && stringField(variables, "headAfter") != "next" {
				t.Fatal("second page lost original cursor")
			}
			r.Body = io.NopCloser(strings.NewReader(string(raw)))
		}
		return original.RoundTrip(r)
	})
	repo, item := ciFixtureItem(t)
	v, err := c.readCIInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
	if err != nil || queries != 2 || len(v.Head.Contexts) != 2 || v.Head.Validate(item.HeadSHA) != nil {
		t.Fatal("paginated CI inventory failed", err)
	}
	c, _ = ciClientFixture(t, nil)
	started, stopped := make(chan struct{}), make(chan struct{})
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(stopped)
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.readCIInventory(ctx, []byte("private-fixture-pat"), repo, item); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("CI read not started")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled CI observation published")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("CI transport outlived its owner")
	}
}

func TestCIGraphQLErrorsNeverAuthorizePartialDataOrLeakMessages(t *testing.T) {
	for native, code := range map[string]domain.Code{"FORBIDDEN": domain.PermissionDenied, "RATE_LIMITED": domain.ResourceExhausted, "NOT_FOUND": domain.NotFound, "FUTURE_ERROR": domain.Unavailable} {
		c, _ := ciClientFixture(t, func(_ int, node map[string]any) map[string]any {
			return map[string]any{"data": map[string]any{"node": node}, "errors": []any{map[string]any{"type": native, "message": "private diagnostic"}}}
		})
		repo, item := ciFixtureItem(t)
		_, err := c.readCIInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
		if domain.SafeError(err).Code != code || strings.Contains(err.Error(), "private diagnostic") {
			t.Fatal("partial error classification changed", native, err)
		}
	}
}
func TestCIPublicGraphQLProjection(t *testing.T) {
	prefix := os.Getenv("DELIDEV_GITHUB_CI_FIXTURE_PREFIX")
	if prefix == "" {
		t.Skip("explicit public schema capture only")
	}
	detail, err := os.ReadFile(prefix + "detail.json")
	if err != nil {
		t.Fatal(err)
	}
	query, err := os.ReadFile(prefix + "query.json")
	if err != nil {
		t.Fatal(err)
	}
	repo := domain.RemoteRepository{Provider: domain.GitHubCom, ID: "1158679171", NodeID: "R_kgDORRAKgw", Owner: "delinoio", Name: "oss"}
	item, _, err := parseItem(detail, repo, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: "935"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := jsonObject(query)
	data, _ := jsonObject(envelope["data"])
	node, _ := jsonObject(data["node"])
	_, head, merge, err := parseCINode(node, repo, item)
	if err != nil || head.total != 40 || merge == nil || merge.total != 0 {
		t.Fatal("public CI evidence not retained", err)
	}
	found := false
	for _, v := range head.rollup.Contexts {
		if v.Name == "CI Result" && v.Required && v.Application != nil && v.Application.ID == "15368" && v.WorkflowEvent != nil && *v.WorkflowEvent == "pull_request" {
			found = true
		}
	}
	if !found {
		t.Fatal("real required check provenance missing")
	}
}
