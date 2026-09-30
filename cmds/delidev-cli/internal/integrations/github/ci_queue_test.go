package github

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func ciQueueNodeFixture(node map[string]any) map[string]any {
	sha := strings.Repeat("f", 40)
	entry := map[string]any{"id": "ENTRY_E", "position": 1, "state": "UNMERGEABLE", "pullRequest": map[string]any{"id": "ITEM_stable", "number": 17}, "baseCommit": map[string]any{"oid": accessSHA}, "headCommit": map[string]any{"oid": sha}}
	rollup := node["statusCheckRollup"].(map[string]any)
	rollup["commit"] = map[string]any{"oid": sha}
	run := rollup["contexts"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
	run["id"] = "CHECK_G"
	suite := run["checkSuite"].(map[string]any)
	suite["commit"] = map[string]any{"oid": sha}
	workflow := suite["workflowRun"].(map[string]any)
	workflow["event"] = "merge_group"
	workflow["checkSuite"] = map[string]any{"id": "SUITE_1", "commit": map[string]any{"oid": sha}}
	node["statusCheckRollup"] = nil
	node["isInMergeQueue"] = true
	node["mergeQueueEntry"] = map[string]any{"id": entry["id"], "position": entry["position"], "state": entry["state"], "pullRequest": entry["pullRequest"], "baseCommit": entry["baseCommit"], "headCommit": map[string]any{"oid": sha, "repository": map[string]any{"id": "R_37"}, "statusCheckRollup": rollup}, "mergeQueue": map[string]any{"id": "QUEUE_Q", "repository": map[string]any{"id": "R_37"}, "configuration": map[string]any{"mergingStrategy": "ALLGREEN"}, "entries": map[string]any{"totalCount": 1, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": "entry-one"}, "nodes": []any{entry}}}}
	return map[string]any{"data": map[string]any{"node": node}}
}

func TestQueueCICompletesIndependentEntryAndCheckPages(t *testing.T) {
	c, reads := ciClientFixture(t, func(read int, node map[string]any) map[string]any {
		envelope := ciQueueNodeFixture(node)
		entry := node["mergeQueueEntry"].(map[string]any)
		entry["position"] = 2
		queue := entry["mergeQueue"].(map[string]any)
		connection := queue["entries"].(map[string]any)
		connection["totalCount"] = 2
		checks := entry["headCommit"].(map[string]any)["statusCheckRollup"].(map[string]any)["contexts"].(map[string]any)
		checks["totalCount"] = 2
		connection["pageInfo"] = map[string]any{"hasNextPage": read%2 == 1, "endCursor": "queue-next"}
		checks["pageInfo"] = map[string]any{"hasNextPage": read%2 == 1, "endCursor": "check-next"}
		target := connection["nodes"].([]any)[0].(map[string]any)
		target["position"] = 2
		if read%2 == 1 {
			connection["nodes"] = []any{map[string]any{"id": "ENTRY_FIRST", "position": 1, "state": "QUEUED", "baseCommit": map[string]any{"oid": accessSHA}, "headCommit": nil, "pullRequest": map[string]any{"id": "PR_FIRST", "number": 16}}}
		} else {
			run := checks["nodes"].([]any)[0].(map[string]any)
			run["id"], run["name"], run["isRequired"] = "OPTIONAL_G", "optional", false
		}
		return envelope
	})
	original := c.http.Transport
	queries := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/graphql" {
			queries++
			raw, _ := io.ReadAll(r.Body)
			fields, _ := jsonObject(raw)
			vars, _ := jsonObject(fields["variables"])
			if queries%2 == 0 && (stringField(vars, "queueAfter") != "queue-next" || stringField(vars, "queueChecksAfter") != "check-next") {
				t.Fatal("independent queue cursors lost")
			}
			r.Body = io.NopCloser(strings.NewReader(string(raw)))
		}
		return original.RoundTrip(r)
	})
	value, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryCI, Number: "17"})
	if err != nil || *reads != 6 || value.CI == nil || len(value.CI.MergeQueue.Entries) != 2 || len(value.CI.MergeQueue.Rollup.Contexts) != 2 || value.CI.Result.State != domain.CITerminalFailure {
		t.Fatal(value.CI, err, *reads)
	}
}

func TestQueueCIRejectsChangedEarlierMembershipWithUnchangedTargetPosition(t *testing.T) {
	c, _ := ciClientFixture(t, func(read int, node map[string]any) map[string]any {
		envelope := ciQueueNodeFixture(node)
		entry := node["mergeQueueEntry"].(map[string]any)
		entry["position"] = 3
		connection := entry["mergeQueue"].(map[string]any)["entries"].(map[string]any)
		target := connection["nodes"].([]any)[0].(map[string]any)
		target["position"] = 3
		first, second := "A", "B"
		if read == 2 {
			first, second = second, first
		}
		makeEntry := func(id string, position int) map[string]any {
			return map[string]any{"id": "E_" + id, "position": position, "state": "QUEUED", "pullRequest": map[string]any{"id": "PR_" + id, "number": position}, "baseCommit": nil, "headCommit": nil}
		}
		connection["totalCount"], connection["nodes"] = 3, []any{makeEntry(first, 1), makeEntry(second, 2), target}
		return envelope
	})
	value, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryCI, Number: "17"})
	if domain.SafeError(err).Code != domain.Conflict || value.CI != nil {
		t.Fatal("reordered prior queue entries authorized target", err)
	}
}

func TestALLGREENQueueQueryBindsEntryRulesChecksAndOriginalWorkflow(t *testing.T) {
	for _, mode := range []string{"failure", "HEADGREEN", "no-config", "removed", "replaced", "reordered", "changed-base", "changed-head", "changed-queue", "changed-strategy", "changed-after-rules", "foreign-app", "foreign-event", "old-check", "PR-head-check", "foreign-workflow", "missing-page", "missing-permission", "success", "pending", "unmergeable-only"} {
		t.Run(mode, func(t *testing.T) {
			c, reads := ciClientFixture(t, func(read int, node map[string]any) map[string]any {
				envelope := ciQueueNodeFixture(node)
				entry := node["mergeQueueEntry"].(map[string]any)
				queue := entry["mergeQueue"].(map[string]any)
				connection := queue["entries"].(map[string]any)
				run := entry["headCommit"].(map[string]any)["statusCheckRollup"].(map[string]any)["contexts"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
				suite := run["checkSuite"].(map[string]any)
				workflow := suite["workflowRun"].(map[string]any)
				switch mode {
				case "HEADGREEN":
					queue["configuration"] = map[string]any{"mergingStrategy": "HEADGREEN"}
				case "no-config":
					queue["configuration"] = nil
				case "removed":
					if read == 2 {
						node["isInMergeQueue"], node["mergeQueueEntry"] = false, nil
					}
				case "replaced":
					if read == 2 {
						entry["id"] = "E_NEW"
					}
				case "reordered":
					if read == 2 {
						entry["position"] = 2
					}
				case "changed-base":
					if read == 2 {
						entry["baseCommit"] = map[string]any{"oid": strings.Repeat("d", 40)}
					}
				case "changed-head":
					if read == 2 {
						entry["headCommit"].(map[string]any)["oid"] = strings.Repeat("d", 40)
					}
				case "changed-queue":
					if read == 2 {
						queue["id"] = "Q_NEW"
					}
				case "changed-strategy":
					if read == 2 {
						queue["configuration"] = map[string]any{"mergingStrategy": "HEADGREEN"}
					}
				case "changed-after-rules":
					if read == 3 {
						node["isInMergeQueue"], node["mergeQueueEntry"] = false, nil
					}
				case "foreign-app":
					suite["app"] = map[string]any{"databaseId": 99, "id": "APP_99", "slug": "fixture"}
				case "foreign-event":
					workflow["event"] = "pull_request"
				case "old-check":
					suite["commit"] = map[string]any{"oid": strings.Repeat("d", 40)}
				case "PR-head-check":
					suite["commit"] = map[string]any{"oid": strings.Repeat("b", 40)}
				case "foreign-workflow":
					workflow["checkSuite"] = map[string]any{"id": "FOREIGN", "commit": map[string]any{"oid": strings.Repeat("f", 40)}}
				case "missing-page":
					connection["totalCount"] = 2
				case "missing-permission":
					envelope["errors"] = []any{map[string]any{"type": "FORBIDDEN", "message": "private diagnostic"}}
				case "success":
					run["conclusion"] = "SUCCESS"
				case "pending":
					run["status"], run["conclusion"] = "IN_PROGRESS", nil
				case "unmergeable-only":
					entry["headCommit"].(map[string]any)["statusCheckRollup"] = nil
				}
				return envelope
			})
			q := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryCI, Number: "17"}
			value, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
			switch mode {
			case "failure", "success", "pending", "HEADGREEN", "no-config", "foreign-app", "foreign-event", "unmergeable-only":
				want := domain.CIUnknown
				if mode == "failure" {
					want = domain.CITerminalFailure
				}
				if mode == "success" {
					want = domain.CINonFailing
				}
				if mode == "pending" {
					want = domain.CIPending
				}
				if err != nil || value.CI == nil || value.CI.Validate(value.Items[0]) != nil || value.CI.Result.State != want || *reads != 3 {
					t.Fatal(value.CI, err, *reads)
				}
				if mode == "failure" && (value.CI.Result.Source != domain.CIMergeQueueCommit || value.CI.Result.EvaluatedSHA != strings.Repeat("f", 40)) {
					t.Fatal("wrong queue commit")
				}
			default:
				if err == nil || value.CI != nil {
					t.Fatal("partial/changed queue evidence published", value.CI, err)
				}
			}
		})
	}
}
