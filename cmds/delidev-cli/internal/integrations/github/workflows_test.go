// SPDX-License-Identifier: Apache-2.0
package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func workflowRuleFixture() map[string]any {
	rule := activeRuleFixture()
	rule["type"] = "workflows"
	rule["parameters"] = map[string]any{"workflows": []any{map[string]any{"repository_id": 11, "path": ".github/workflows/required.yml", "sha": strings.Repeat("d", 40)}}}
	return rule
}

func workflowClientFixture(t *testing.T, mode string) *Client {
	t.Helper()
	c, _ := ciClientFixture(t, func(_ int, node map[string]any) map[string]any {
		contexts := node["statusCheckRollup"].(map[string]any)["contexts"].(map[string]any)
		row := contexts["nodes"].([]any)[0].(map[string]any)
		row["checkSuite"].(map[string]any)["commit"].(map[string]any)["oid"] = strings.Repeat("c", 40)
		row["id"] = "CHECK_merge"
		if mode == "success" || mode == "rerun-success" {
			row["conclusion"] = "SUCCESS"
		}
		if mode == "newer-attempt" || mode == "rerun-success" {
			row["checkSuite"].(map[string]any)["workflowRun"].(map[string]any)["runAttempt"] = 2
		}
		if mode == "rerun-success" {
			old := map[string]any{}
			for k, v := range row {
				old[k] = v
			}
			old["id"], old["conclusion"] = "CHECK_old", "FAILURE"
			contexts["nodes"] = []any{row, old}
			contexts["totalCount"] = 2
		}
		if mode == "pending" {
			row["status"] = "IN_PROGRESS"
			row["conclusion"] = nil
		}
		if mode == "optional" {
			row["isRequired"] = false
		}
		if mode == "target" {
			row["checkSuite"].(map[string]any)["workflowRun"].(map[string]any)["event"] = "pull_request_target"
		}
		node["potentialMergeCommit"].(map[string]any)["statusCheckRollup"] = map[string]any{"commit": map[string]any{"oid": strings.Repeat("c", 40)}, "contexts": contexts}
		node["statusCheckRollup"] = nil
		return map[string]any{"data": map[string]any{"node": node}}
	})
	original := c.http.Transport
	suiteReads, repoReads, runReads, jobReads, contentReads := 0, 0, 0, 0, 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		var body any
		h := http.Header{}
		status := 200
		switch r.URL.Path {
		case "/graphql":
			raw, _ := io.ReadAll(r.Body)
			f, _ := jsonObject(raw)
			r.Body = io.NopCloser(strings.NewReader(string(raw)))
			if stringField(f, "operationName") != "DeliDevWorkflowSuites" {
				return original.RoundTrip(r)
			}
			if stringField(f, "query") != workflowSuitesGraphQL {
				t.Fatal("arbitrary suite document")
			}
			suiteReads++
			node := ciNodeFixture()
			sha := strings.Repeat("d", 40)
			file := map[string]any{"id": "FILE_1", "path": ".github/workflows/required.yml", "repositoryName": "renamed/source", "repositoryFileUrl": "https://github.com/renamed/source/blob/" + sha + "/.github/workflows/required.yml", "viewerCanReadRepository": true, "run": map[string]any{"id": "RUN_1"}}
			event := "pull_request"
			attempt := 1
			if mode == "target" {
				event = "pull_request_target"
			}
			if mode == "newer-attempt" || mode == "rerun-success" {
				attempt = 2
			}
			if mode == "reusable" {
				file["path"] = ".github/workflows/reusable.yml"
				file["repositoryFileUrl"] = "https://github.com/renamed/source/blob/" + sha + "/.github/workflows/reusable.yml"
			}
			if mode == "wrong-sha" || mode == "source-drift" && suiteReads > 1 {
				file["repositoryFileUrl"] = "https://github.com/renamed/source/blob/" + strings.Repeat("e", 40) + "/.github/workflows/required.yml"
			}
			if mode == "wrong-source" {
				file["repositoryName"] = "other/source"
				file["repositoryFileUrl"] = "https://github.com/other/source/blob/" + sha + "/.github/workflows/required.yml"
			}
			if mode == "unreadable" {
				file["viewerCanReadRepository"] = false
			}
			if mode == "foreign-file" {
				file["run"] = map[string]any{"id": "OTHER_RUN"}
			}
			if mode == "name-only" {
				file = nil
			}
			w := map[string]any{"id": "RUN_1", "databaseId": 91, "event": event, "runAttempt": attempt, "file": file}
			suite := map[string]any{"id": "SUITE_1", "commit": map[string]any{"oid": strings.Repeat("c", 40)}, "app": map[string]any{"databaseId": 15368}, "workflowRun": w}
			rows := []any{suite}
			total := 1
			if mode == "competing" {
				otherFile := map[string]any{}
				for k, v := range file {
					otherFile[k] = v
				}
				otherFile["run"] = map[string]any{"id": "RUN_2"}
				other := map[string]any{"id": "SUITE_2", "commit": suite["commit"], "app": suite["app"], "workflowRun": map[string]any{"id": "RUN_2", "databaseId": 92, "event": event, "runAttempt": 1, "file": otherFile}}
				rows = append(rows, other)
				total = 2
			}
			if mode == "suite-cap" {
				total = 501
			}
			info := map[string]any{"hasNextPage": false, "endCursor": nil}
			if mode == "suite-page-limit" {
				total = 500
				info["hasNextPage"] = true
				info["endCursor"] = "same"
			}
			node["potentialMergeCommit"].(map[string]any)["checkSuites"] = map[string]any{"totalCount": total, "pageInfo": info, "nodes": rows}
			if mode == "old-merge" {
				node["potentialMergeCommit"].(map[string]any)["oid"] = strings.Repeat("e", 40)
			}
			body = map[string]any{"data": map[string]any{"node": node}}
		case "/repos/fixture-owner/repo/rules/branches/main":
			rule := workflowRuleFixture()
			if mode == "missing-sha" {
				delete(rule["parameters"].(map[string]any)["workflows"].([]any)[0].(map[string]any), "sha")
			}
			body = []any{rule}
		case "/repositories/11", "/repos/other/source", "/repos/renamed/source":
			repoReads++
			name, id := "renamed/source", 11
			if r.URL.Path == "/repos/other/source" {
				name, id = "other/source", 12
			}
			if mode == "rename-drift" && repoReads > 1 {
				name = "changed/source"
			}
			parts := strings.Split(name, "/")
			body = map[string]any{"id": id, "node_id": "SOURCE_11", "name": parts[1], "full_name": name, "owner": map[string]any{"login": parts[0]}, "private": true, "default_branch": "main"}
			if mode == "403" {
				status = 403
			}
			if mode == "404" {
				status = 404
			}
		case "/repos/renamed/source/contents/.github/workflows/required.yml", "/repos/renamed/source/contents/.github/workflows/reusable.yml", "/repos/other/source/contents/.github/workflows/required.yml":
			contentReads++
			if r.URL.Query().Get("ref") != strings.Repeat("d", 40) && mode != "wrong-sha" && mode != "source-drift" {
				t.Fatal("mutable source ref")
			}
			body = map[string]any{"type": "file", "path": strings.TrimPrefix(r.URL.Path, "/repos/"+strings.Split(r.URL.Path, "/")[2]+"/source/contents/"), "sha": strings.Repeat("f", 40)}
			if mode == "blob-drift" && contentReads > 1 {
				body.(map[string]any)["sha"] = strings.Repeat("e", 40)
			}
			if mode == "content-403" {
				status = 403
			}
		case "/repos/fixture-owner/repo/actions/runs/91", "/repos/fixture-owner/repo/actions/runs/92":
			runReads++
			id, suite, node, attempt := 91, "SUITE_1", "RUN_1", 1
			if strings.HasSuffix(r.URL.Path, "/92") {
				id, suite, node = 92, "SUITE_2", "RUN_2"
			}
			if mode == "newer-attempt" || mode == "rerun-success" || mode == "attempt-drift" && runReads > 1 {
				attempt = 2
			}
			event := "pull_request"
			if mode == "target" {
				event = "pull_request_target"
			}
			state, conclusion := "completed", any("failure")
			if mode == "success" || mode == "rerun-success" {
				conclusion = "success"
			}
			if mode == "pending" || mode == "newer-attempt" {
				state, conclusion = "in_progress", nil
			}
			body = map[string]any{"id": id, "node_id": node, "event": event, "run_attempt": attempt, "check_suite_node_id": suite, "head_sha": strings.Repeat("b", 40), "status": state, "conclusion": conclusion, "pull_requests": []any{map[string]any{"number": 17, "base": map[string]any{"repo": map[string]any{"id": 37}}}}}
		case "/repos/fixture-owner/repo/actions/runs/91/attempts/1/jobs", "/repos/fixture-owner/repo/actions/runs/91/attempts/2/jobs", "/repos/fixture-owner/repo/actions/runs/92/attempts/1/jobs":
			jobReads++
			id, node := 91, "CHECK_merge"
			if strings.Contains(r.URL.Path, "/92/") {
				id, node = 92, "CHECK_competing"
			}
			state, conclusion := "completed", any("failure")
			if mode == "success" || mode == "rerun-success" {
				conclusion = "success"
			}
			if mode == "pending" {
				state, conclusion = "in_progress", nil
			}
			job := map[string]any{"node_id": node, "run_id": id, "run_attempt": 1, "status": state, "conclusion": conclusion}
			if mode == "rerun-success" {
				job["run_attempt"] = 2
			}
			if mode == "newer-attempt" {
				job["run_attempt"], job["node_id"], job["status"], job["conclusion"] = 2, "CHECK_new", "in_progress", nil
			}
			if mode == "job-drift" && jobReads > 1 {
				job["conclusion"] = "success"
			}
			if mode == "wrong-job-attempt" {
				job["run_attempt"] = 2
			}
			body = map[string]any{"total_count": 1, "jobs": []any{job}}
			if mode == "job-page-limit" {
				body.(map[string]any)["total_count"] = 2
				h.Set("Link", `<https://api.github.com/repos/fixture-owner/repo/actions/runs/91/attempts/1/jobs?page=2&per_page=100>; rel="next"`)
			}
		default:
			return original.RoundTrip(r)
		}
		if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Fatal("unscoped workflow read")
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	return c
}

func TestPinnedWorkflowProviderProvesRenamedNumericSourceAndCurrentOutcomes(t *testing.T) {
	for mode, want := range map[string]domain.CIState{"failure": domain.CITerminalFailure, "success": domain.CINonFailing, "rerun-success": domain.CINonFailing, "pending": domain.CIPending} {
		t.Run(mode, func(t *testing.T) {
			c := workflowClientFixture(t, mode)
			v, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryCI, Number: "17"})
			if err != nil || v.CI == nil || v.CI.Result.State != want || v.CI.Validate(v.Items[0]) != nil {
				t.Fatal("original workflow not evaluated", err, v.CI)
			}
			if v.CI.WorkflowRuns[0].Source.RepositoryID != "11" || v.CI.Result.EvaluatedSHA != strings.Repeat("c", 40) {
				t.Fatal("immutable identity lost")
			}
		})
	}
}

func TestPinnedWorkflowProviderNeverPublishesUnprovedFailure(t *testing.T) {
	for _, mode := range []string{"missing-sha", "wrong-sha", "wrong-source", "reusable", "target", "name-only", "optional", "unreadable", "foreign-file", "old-merge", "competing", "newer-attempt", "wrong-job-attempt", "403", "404", "content-403", "suite-cap", "suite-page-limit", "job-page-limit", "rename-drift", "attempt-drift", "source-drift", "job-drift", "blob-drift"} {
		t.Run(mode, func(t *testing.T) {
			c := workflowClientFixture(t, mode)
			v, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryCI, Number: "17"})
			if err == nil && (v.CI == nil || v.CI.Result.State != domain.CIUnknown) {
				t.Fatal("unproved failure published", mode, v.CI)
			}
		})
	}
}

func TestWorkflowRuleParserRetainsExplicitPinsAndRejectsMalformedReferences(t *testing.T) {
	for _, mode := range []string{"valid", "missing-sha", "unknown", "duplicate", "bad-id", "bad-sha", "traversal"} {
		rule := workflowRuleFixture()
		params := rule["parameters"].(map[string]any)
		ref := params["workflows"].([]any)[0].(map[string]any)
		switch mode {
		case "missing-sha":
			delete(ref, "sha")
		case "unknown":
			params["future"] = true
		case "duplicate":
			params["workflows"] = []any{ref, ref}
		case "bad-id":
			ref["repository_id"] = json.Number("11.0")
		case "bad-sha":
			ref["sha"] = "main"
		case "traversal":
			ref["path"] = ".github/workflows/../secret"
		}
		raw, _ := json.Marshal(rule)
		v, err := parseActiveRule(raw)
		if mode == "valid" || mode == "missing-sha" || mode == "unknown" {
			if err != nil || v.RequiredWorkflows == nil {
				t.Fatal("valid profile lost", mode, err)
			}
		} else if err == nil {
			t.Fatal("malformed source accepted", mode)
		}
	}
}

func TestRequiredWorkflowSourceReadJoinsCancellation(t *testing.T) {
	c := workflowClientFixture(t, "failure")
	original := c.http.Transport
	started, stopped := make(chan struct{}), make(chan struct{})
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/repositories/11" {
			return original.RoundTrip(r)
		}
		close(started)
		<-r.Context().Done()
		close(stopped)
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.QueryRepository(ctx, []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryCI, Number: "17"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("source read not started")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled workflow proof published")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("source read outlived observation")
	}
}

func workflowAndOrdinaryClientFixture(t *testing.T) *Client {
	t.Helper()
	c := workflowClientFixture(t, "failure")
	original := c.http.Transport
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		response, err := original.RoundTrip(r)
		if err != nil || r.URL.Path != "/repos/fixture-owner/repo/rules/branches/main" {
			return response, err
		}
		_ = response.Body.Close()
		ordinary := activeRuleFixture()
		ordinary["ruleset_id"] = 19
		raw, _ := json.Marshal([]any{workflowRuleFixture(), ordinary})
		response.Body = io.NopCloser(strings.NewReader(string(raw)))
		return response, nil
	})
	return c
}

func TestOversizedOptionalWorkflowProofPreservesOrdinaryCI(t *testing.T) {
	c := workflowAndOrdinaryClientFixture(t)
	repo, item := ciFixtureItem(t)
	ci, err := c.readCIInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := c.readActiveRules(context.Background(), []byte("private-fixture-pat"), repo, item)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := c.requiredWorkflowInventory(context.Background(), []byte("private-fixture-pat"), repo, item, ci, *rules)
	if err != nil || !retainRequiredWorkflowEvidence(&ci, *rules, item, runs) {
		t.Fatal("bounded optional proof rejected", err)
	}
	ci.WorkflowRuns = nil
	for n := 0; n < 4; n++ {
		run := runs[0]
		run.NodeID, run.SuiteNodeID, run.RunID = "RUN_large_"+strconv.Itoa(n), "SUITE_large_"+strconv.Itoa(n), strconv.Itoa(100+n)
		run.Jobs = []domain.CIWorkflowJob{}
		for job := 0; job < domain.MaxCIContexts; job++ {
			run.Jobs = append(run.Jobs, domain.CIWorkflowJob{NodeID: strings.Repeat("J", 240) + strconv.Itoa(job), NativeStatus: "COMPLETED", NativeConclusion: runs[0].NativeConclusion})
		}
		runs = append(runs, run)
	}
	if retainRequiredWorkflowEvidence(&ci, *rules, item, runs) || ci.WorkflowRuns != nil {
		t.Fatal("oversized proof admitted or partially retained")
	}
	ci.Rules = *rules
	ci.Result = ci.Evaluate(item)
	if ci.Validate(item) != nil || ci.Result.State != domain.CIUnknown || len(ci.TestMerge.Contexts) != 1 || len(ci.Result.Requirements) != 2 {
		t.Fatal("ordinary observation erased by optional proof", ci.Result)
	}
	for _, requirement := range ci.Result.Requirements {
		if requirement.Workflow == nil && requirement.State != domain.CITerminalFailure {
			t.Fatal("ordinary required check assessment lost")
		}
	}
}

func TestWorkflowSuiteInventoryCompletesPaginationAndCannotIgnoreUnboundActions(t *testing.T) {
	for _, mode := range []string{"complete", "missing-run", "missing-app", "malformed-app", "foreign-parent", "changed-total"} {
		t.Run(mode, func(t *testing.T) {
			c := workflowClientFixture(t, "failure")
			original := c.http.Transport
			reads := 0
			c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/graphql" {
					return original.RoundTrip(r)
				}
				raw, _ := io.ReadAll(r.Body)
				f, _ := jsonObject(raw)
				r.Body = io.NopCloser(strings.NewReader(string(raw)))
				if stringField(f, "operationName") != "DeliDevWorkflowSuites" {
					return original.RoundTrip(r)
				}
				reads++
				v, _ := jsonObject(f["variables"])
				if reads == 2 && stringField(v, "after") != "page1" {
					t.Fatal("suite cursor lost")
				}
				response, err := original.RoundTrip(r)
				if err != nil {
					return response, err
				}
				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				var data map[string]any
				_ = json.Unmarshal(body, &data)
				merge := data["data"].(map[string]any)["node"].(map[string]any)["potentialMergeCommit"].(map[string]any)
				connection := merge["checkSuites"].(map[string]any)
				connection["totalCount"] = 2
				connection["pageInfo"] = map[string]any{"hasNextPage": reads == 1, "endCursor": "page1"}
				suite := connection["nodes"].([]any)[0].(map[string]any)
				if reads == 2 {
					suite["id"] = "EXTERNAL_SUITE"
					suite["app"] = map[string]any{"databaseId": 47}
					suite["workflowRun"] = nil
				}
				if mode == "missing-run" && reads == 2 {
					suite["app"] = map[string]any{"databaseId": 15368}
				}
				if mode == "missing-app" && reads == 2 {
					suite["app"] = nil
				}
				if mode == "malformed-app" && reads == 2 {
					suite["app"] = map[string]any{"databaseId": "unknown"}
				}
				if mode == "foreign-parent" {
					merge["parents"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["oid"] = strings.Repeat("f", 40)
				}
				if mode == "changed-total" && reads == 2 {
					connection["totalCount"] = 3
				}
				body, _ = json.Marshal(data)
				response.Body = io.NopCloser(strings.NewReader(string(body)))
				return response, nil
			})
			repo, item := ciFixtureItem(t)
			ci, err := c.readCIInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
			if err != nil {
				t.Fatal(err)
			}
			suites, err := c.workflowSuites(context.Background(), []byte("private-fixture-pat"), repo, item, ci)
			if mode == "complete" {
				if err != nil || reads != 2 || len(suites) != 1 {
					t.Fatal("complete suite inventory lost", err)
				}
			} else if err == nil {
				t.Fatal("incomplete suite authority accepted", mode)
			}
		})
	}
}
