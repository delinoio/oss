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
	"testing"

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
	}{{"cycle", map[int][2]string{1: {"a", "b"}, 2: {"b", "a"}}, "cycle"}, {"parents", map[int][2]string{1: {"main", "b"}, 2: {"other", "b"}, 3: {"b", "c"}}, "competing_parents"}} {
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
