// SPDX-License-Identifier: Apache-2.0
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func workspaceFixture(t *testing.T, refs map[int][2]string, change func(int, map[string]any)) *Client {
	t.Helper()
	c := New()
	counts := map[int]int{}
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		var body any
		status := 200
		if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Error("unscoped provider read")
		}
		switch r.URL.Path {
		case "/user":
			body = map[string]any{"id": 17, "node_id": "U_17", "login": "fixture-user", "type": "User"}
		case "/repos/fixture-owner/repo":
			body = map[string]any{"id": 37, "node_id": "R_37", "name": "repo", "full_name": "fixture-owner/repo", "owner": map[string]any{"login": "fixture-owner"}, "private": true, "default_branch": "main"}
		case "/repos/fixture-owner/repo/pulls":
			rows := []any{}
			for n, ref := range refs {
				if r.URL.Query().Get("state") != "all" {
					t.Error("related read excludes closed context")
				}
				head := r.URL.Query().Get("head")
				base := r.URL.Query().Get("base")
				if head == "fixture-owner:"+ref[1] || base == ref[0] {
					rows = append(rows, map[string]any{"number": n})
				}
			}
			body = rows
		default:
			n, e := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/repos/fixture-owner/repo/pulls/"))
			if e != nil {
				t.Errorf("unexpected path %s", r.URL.Path)
				status = 500
				body = map[string]any{}
				break
			}
			ref, ok := refs[n]
			if !ok {
				status = 404
				body = map[string]any{}
				break
			}
			item := queryFixtureItem(domain.RepositoryPullRequest, n, true, false)
			item["id"] = n + 1000
			item["node_id"] = "PR_" + strconv.Itoa(n)
			item["base"].(map[string]any)["ref"] = ref[0]
			item["head"].(map[string]any)["ref"] = ref[1]
			item["head"].(map[string]any)["repo"] = map[string]any{"id": 37, "node_id": "R_37", "name": "repo", "full_name": "fixture-owner/repo", "owner": map[string]any{"login": "fixture-owner"}, "private": true, "default_branch": "main", "clone_url": "https://github.com/fixture-owner/repo.git", "ssh_url": "git@github.com:fixture-owner/repo.git"}
			item["additions"] = 24
			item["deletions"] = 8
			counts[n]++
			if change != nil {
				change(counts[n], item)
			}
			body = item
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})
	return c
}
func TestWorkspaceExactBranchesContextAndOwnCounts(t *testing.T) {
	c := workspaceFixture(t, map[int][2]string{101: {"main", "s1"}, 102: {"s1", "s2"}, 103: {"s2", "s3"}, 104: {"main", "independent"}}, nil)
	value, e := c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"102", "104"})
	if e != nil {
		t.Fatal(e)
	}
	if value.State != domain.PRWorkspaceComplete || len(value.Nodes) != 4 || len(value.Edges) != 2 {
		t.Fatalf("incomplete graph: %#v", value)
	}
	for _, node := range value.Nodes {
		if node.Counts == nil || node.Counts.Additions != "24" || node.Counts.Deletions != "8" {
			t.Fatal("provider counts replaced with a sum")
		}
		if node.Seed != (node.Item.Number == "102" || node.Item.Number == "104") {
			t.Fatal("context inflated matching seeds")
		}
	}
}
func TestWorkspaceAmbiguityAndDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		refs   map[int][2]string
		reason string
	}{{"self-cycle", map[int][2]string{1: {"same", "same"}}, "cycle"}, {"cycle", map[int][2]string{1: {"a", "b"}, 2: {"b", "a"}}, "cycle"}, {"parents", map[int][2]string{1: {"main", "b"}, 2: {"other", "b"}, 3: {"b", "c"}}, "competing_parents"}} {
		t.Run(tc.name, func(t *testing.T) {
			v, e := workspaceFixture(t, tc.refs, nil).Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"})
			if e != nil {
				t.Fatal(e)
			}
			if v.State != domain.PRWorkspaceAmbiguous {
				t.Fatal("ambiguous graph claimed tree", v.State)
			}
		})
	}
	c := workspaceFixture(t, map[int][2]string{1: {"main", "feature"}}, func(n int, item map[string]any) {
		if n >= 3 {
			item["head"].(map[string]any)["sha"] = strings.Repeat("c", 40)
		}
	})
	v, e := c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"})
	if e != nil || v.State != domain.PRWorkspaceIncomplete {
		t.Fatal("drift claimed complete", e, v.State)
	}
}
func TestWorkspaceCountsRejectMalformedProviderIntegers(t *testing.T) {
	for _, raw := range []string{`{"additions":-1,"deletions":0}`, `{"additions":1.5,"deletions":0}`, `{"additions":"1","deletions":0}`, `{"additions":18446744073709551616,"deletions":0}`} {
		f, _ := jsonObject([]byte(raw))
		if parseCounts(f) != nil {
			t.Fatal("invalid counts admitted", raw)
		}
	}
	f, _ := jsonObject([]byte(`{"additions":0,"deletions":0}`))
	if parseCounts(f).Additions != "0" {
		t.Fatal("zero missing")
	}
}
func TestAvatarOriginCredentialIsolationAndSanitizing(t *testing.T) {
	c := New()
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 128, 128)))
	calls := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "avatars.githubusercontent.com" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("avatar received credentials or foreign authority")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw.Bytes()))}, nil
	})
	bytes, e := c.Avatar(context.Background(), "https://avatars.githubusercontent.com/u/19?v=4", "19")
	if e != nil {
		t.Fatal(e)
	}
	config, format, e := image.DecodeConfig(strings.NewReader(string(bytes)))
	if e != nil || format != "png" || config.Width > 64 || config.Height > 64 {
		t.Fatal("unsanitized avatar", e)
	}
	for _, url := range []string{"http://avatars.githubusercontent.com/u/19", "https://evil.invalid/u/19", "https://avatars.githubusercontent.com/u/20", "https://avatars.githubusercontent.com:443/u/19", "https://user@avatars.githubusercontent.com/u/19", "https://avatars.githubusercontent.com/u/19?redirect=https://evil.invalid"} {
		if _, e = c.Avatar(context.Background(), url, "19"); e == nil {
			t.Fatal("forbidden avatar allowed", url)
		}
	}
	if calls != 1 {
		t.Fatal("forbidden locator fetched")
	}
}
func TestAvatarRedirectOversizeAndMalformed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
	}{{"redirect", 302, []byte("redirect")}, {"oversize", 200, make([]byte, 128<<10+1)}, {"malformed", 200, []byte("<script>not a raster</script>")}} {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			calls := 0
			c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://evil.invalid"}}, Body: io.NopCloser(bytes.NewReader(tc.body))}, nil
			})
			if _, e := c.Avatar(context.Background(), "https://avatars.githubusercontent.com/u/19", "19"); e == nil {
				t.Fatal("unsafe image admitted")
			}
			if calls != 1 {
				t.Fatal("redirect followed")
			}
		})
	}
}

func TestPRCommitsOwnCountsFullMessageNullActorAndHeadFence(t *testing.T) {
	c := workspaceFixture(t, map[int][2]string{1: {"main", "feature"}}, nil)
	base := c.http.Transport
	graphCalls := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/graphql" {
			return base.RoundTrip(r)
		}
		graphCalls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Error("unscoped GraphQL commit read")
		}
		person := map[string]any{"name": "Original author", "date": "2026-10-11T00:00:00Z", "user": nil, "email": "private-fixture-email@example.invalid"}
		commit := map[string]any{
			"oid": strings.Repeat("c", 40), "message": "Original Unicode 한글 headline\n\nComplete message body", "additions": 8, "deletions": 3, "author": person, "committer": person,
			"parents": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{map[string]any{"oid": strings.Repeat("b", 40)}}},
		}
		node := map[string]any{
			"id": "PR_1", "databaseId": 1001, "number": 1, "baseRefOid": repositorySHA, "headRefOid": strings.Repeat("b", 40),
			"repository": map[string]any{"id": "R_37", "databaseId": 37},
			"commits":    map[string]any{"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}, "nodes": []any{map[string]any{"commit": commit}}},
		}
		raw, _ := json.Marshal(map[string]any{"data": map[string]any{"node": node}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})
	value, e := c.Commits(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", "1", "1001", "37", repositorySHA, strings.Repeat("b", 40), "")
	if e != nil {
		t.Fatal(e)
	}
	if len(value.Value.Commits) != 1 || value.Value.Commits[0].Counts.Additions != "8" || value.Value.Commits[0].Author.Actor != nil || !strings.Contains(value.Value.Commits[0].Message, "Complete message body") {
		t.Fatal("commit observations replaced or truncated")
	}
	raw, _ := json.Marshal(value.Value)
	if bytes.Contains(raw, []byte("private-fixture-email")) {
		t.Fatal("email leaked")
	}
	if _, e = c.Commits(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", "1", "1001", "37", repositorySHA, strings.Repeat("d", 40), ""); e == nil || graphCalls != 1 {
		t.Fatal("changed head reached commit connection", e, graphCalls)
	}
}
func TestWorkspaceProviderPageBudgetAndForkFence(t *testing.T) {
	refs := map[int][2]string{}
	for n := 1; n <= 30; n++ {
		refs[n] = [2]string{"s" + strconv.Itoa(n-1), "s" + strconv.Itoa(n)}
	}
	c := workspaceFixture(t, refs, nil)
	base := c.http.Transport
	calls := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { calls++; return base.RoundTrip(r) })
	v, e := c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"})
	if e != nil || v.State == domain.PRWorkspaceComplete || calls > 40 {
		t.Fatal("capacity claimed complete or exceeded page budget", calls, e, v.State)
	}
	c = workspaceFixture(t, map[int][2]string{1: {"main", "feature"}, 2: {"feature", "child"}}, func(_ int, item map[string]any) {
		if item["number"].(int) == 2 {
			item["head"].(map[string]any)["repo"] = nil
		}
	})
	v, e = c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1", "2"})
	if e != nil || v.State != domain.PRWorkspaceIncomplete || len(v.Edges) != 0 {
		t.Fatal("missing fork identity granted graph edge", e, v.State)
	}
}

func TestWorkspaceForeignForkStaysCompleteFlatNode(t *testing.T) {
	foreign := func(_ int, item map[string]any) {
		item["head"].(map[string]any)["repo"] = map[string]any{"id": 99, "node_id": "R_99", "name": "repo", "full_name": "fork-owner/repo", "owner": map[string]any{"login": "fork-owner"}, "private": false, "default_branch": "main", "clone_url": "https://github.com/fork-owner/repo.git", "ssh_url": "git@github.com:fork-owner/repo.git"}
	}
	c := workspaceFixture(t, map[int][2]string{1: {"main", "feature"}}, foreign)
	v, e := c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"})
	if e != nil || v.State != domain.PRWorkspaceComplete || len(v.Nodes) != 1 || len(v.Edges) != 0 || len(v.Reasons) != 0 {
		t.Fatal("foreign fork was not a complete flat node", e, v.State, len(v.Nodes), len(v.Edges), v.Reasons)
	}

	// A foreign child never acquires an edge to a same-repository parent.
	c = workspaceFixture(t, map[int][2]string{1: {"main", "feature"}, 2: {"feature", "child"}}, func(n int, item map[string]any) {
		if item["number"].(int) == 2 {
			foreign(n, item)
		}
	})
	v, e = c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1", "2"})
	if e != nil || v.State != domain.PRWorkspaceComplete || len(v.Nodes) != 2 || len(v.Edges) != 0 || len(v.Reasons) != 0 {
		t.Fatal("foreign fork gained an edge or incomplete reason", e, v.State, len(v.Edges), v.Reasons)
	}
}

func TestWorkspaceMissingHeadRepositoryStaysIncomplete(t *testing.T) {
	c := workspaceFixture(t, map[int][2]string{1: {"main", "feature"}}, func(_ int, item map[string]any) {
		item["head"].(map[string]any)["repo"] = nil
	})
	v, e := c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"})
	found := false
	for _, reason := range v.Reasons {
		found = found || reason == "head_repository_unavailable"
	}
	if e != nil || v.State != domain.PRWorkspaceIncomplete || !found || len(v.Edges) != 0 {
		t.Fatal("missing head repository identity did not stay incomplete", e, v.State, v.Reasons)
	}
}

func TestWorkspaceSameRepositoryStackKeepsEdges(t *testing.T) {
	c := workspaceFixture(t, map[int][2]string{1: {"main", "feature"}, 2: {"feature", "child"}}, nil)
	v, e := c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"})
	if e != nil || v.State != domain.PRWorkspaceComplete || len(v.Nodes) != 2 || len(v.Edges) != 1 || v.Edges[0].Parent != "1" || v.Edges[0].Child != "2" || len(v.Reasons) != 0 {
		t.Fatal("same-repository stack lost its edge", e, v.State, v.Edges, v.Reasons)
	}
}

func TestWorkspaceRechecksCompleteRelationInventory(t *testing.T) {
	c := workspaceFixture(t, map[int][2]string{101: {"main", "s1"}, 102: {"s1", "s2"}}, nil)
	original := c.http.Transport
	pages := map[string]int{}
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		reply, err := original.RoundTrip(r)
		if err != nil {
			return reply, err
		}
		if r.URL.Path == "/repos/fixture-owner/repo/pulls" {
			pages[r.URL.String()]++
			if pages[r.URL.String()] > 1 {
				reply.Body.Close()
				reply.Body = io.NopCloser(strings.NewReader(`[{"number":999}]`))
			}
		}
		return reply, nil
	})
	result, err := c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"102"})
	if err != nil {
		t.Fatal(err)
	}
	if result.State == domain.PRWorkspaceComplete {
		t.Fatal("a new competing PR appeared between relationship reads")
	}
	found := false
	for _, reason := range result.Reasons {
		found = found || reason == "relationship_drift"
	}
	if !found {
		t.Fatalf("missing drift classification: %v", result.Reasons)
	}
}

func TestWorkspaceRejectsRepositoryIdentityDrift(t *testing.T) {
	c := workspaceFixture(t, map[int][2]string{101: {"main", "s1"}}, nil)
	original := c.http.Transport
	reads := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		reply, err := original.RoundTrip(r)
		if err != nil {
			return reply, err
		}
		if r.URL.Path == "/repos/fixture-owner/repo" {
			reads++
			if reads > 1 {
				raw, _ := io.ReadAll(reply.Body)
				reply.Body.Close()
				var value map[string]any
				json.Unmarshal(raw, &value)
				value["id"] = 999
				value["node_id"] = "R_replacement"
				raw, _ = json.Marshal(value)
				reply.Body = io.NopCloser(bytes.NewReader(raw))
			}
		}
		return reply, nil
	})
	result, err := c.Workspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"101"})
	if err != nil {
		t.Fatal(err)
	}
	if result.State == domain.PRWorkspaceComplete {
		t.Fatal("replaced numeric repository acquired complete context")
	}
}

func chainRefs(count int) map[int][2]string {
	refs := map[int][2]string{}
	for n := 1; n <= count; n++ {
		refs[n] = [2]string{"s" + strconv.Itoa(n-1), "s" + strconv.Itoa(n)}
	}
	return refs
}

// The provider read budget is cumulative: reads issued by an interrupted attempt
// stay charged, and an exhausted budget admits no further attempt.
func TestWorkspaceBudgetIsCumulativeAcrossInterruptedAttempts(t *testing.T) {
	c := workspaceFixture(t, chainRefs(30), nil)
	base := c.http.Transport
	var mu sync.Mutex
	calls := 0
	interrupt := func() {}
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		n := calls
		cancel := interrupt
		mu.Unlock()
		if n == 20 {
			cancel()
		}
		return base.RoundTrip(r)
	})
	budget := NewWorkspaceBudget()
	first, cancel := context.WithCancel(context.Background())
	mu.Lock()
	interrupt = cancel
	mu.Unlock()
	_, e := c.WorkspaceWithBudget(first, []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"}, budget)
	if e == nil {
		t.Fatal("interrupted attempt returned an observation")
	}
	afterFirst := budget.Reads()
	if afterFirst < 19 || afterFirst > 22 {
		t.Fatal("interrupted attempt reads were not retained", afterFirst)
	}
	value, e := c.WorkspaceWithBudget(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"}, budget)
	mu.Lock()
	total := calls
	mu.Unlock()
	if e != nil || value.State == domain.PRWorkspaceComplete || total > 40 || budget.Reads() > 40 {
		t.Fatal("resumed attempt exceeded the cumulative page budget", total, budget.Reads(), e, value.State)
	}
	if !contains(value.Reasons, "read_limit") {
		t.Fatal("exhaustion was not reported", value.Reasons)
	}
	before := total
	_, e = c.WorkspaceWithBudget(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"}, budget)
	mu.Lock()
	after := calls
	mu.Unlock()
	if domain.SafeError(e).Code != domain.ResourceExhausted || after != before {
		t.Fatal("exhausted budget admitted another attempt", e, before, after)
	}
	if budget.Attempts() != 2 {
		t.Fatal("exhausted attempt was counted", budget.Attempts())
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// The adapter deadline is frozen when the first attempt begins; later attempts
// share it instead of receiving a new 30-second window.
func TestWorkspaceDeadlineIsFrozenAtFirstAttempt(t *testing.T) {
	c := workspaceFixture(t, map[int][2]string{1: {"main", "feature"}}, nil)
	base := c.http.Transport
	var mu sync.Mutex
	var deadlines []time.Time
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if deadline, ok := r.Context().Deadline(); ok {
			mu.Lock()
			deadlines = append(deadlines, deadline)
			mu.Unlock()
		}
		return base.RoundTrip(r)
	})
	started := time.Now()
	budget := NewWorkspaceBudget()
	for range 2 {
		if _, e := c.WorkspaceWithBudget(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []string{"1"}, budget); e != nil {
			t.Fatal(e)
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) < 2 {
		t.Fatal("no deadline observed")
	}
	frozen := budget.deadline
	if frozen.Before(started.Add(workspaceDeadline)) || frozen.After(started.Add(workspaceDeadline+5*time.Second)) {
		t.Fatal("deadline is not 30 seconds from the first attempt", frozen.Sub(started))
	}
	// Later attempts start after the first one and must still end by the frozen
	// deadline; a fresh window would end later.
	for _, deadline := range deadlines {
		if deadline.After(frozen) {
			t.Fatal("an attempt outlived the frozen deadline", deadline.Sub(frozen))
		}
	}
	if budget.Attempts() != 2 {
		t.Fatal("attempts were not counted", budget.Attempts())
	}
}
