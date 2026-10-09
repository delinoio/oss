package github

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const repositorySHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func repositoryFixture(t *testing.T, changes map[string]struct {
	status int
	body   string
}) (*Client, *atomic.Int32) {
	t.Helper()
	client := New()
	calls := &atomic.Int32{}
	responses := map[string]string{
		"/user":                     `{"id":17,"node_id":"U_17","login":"fixture-user","type":"User"}`,
		"/repos/fixture-owner/repo": `{"id":37,"node_id":"R_37","name":"repo","full_name":"fixture-owner/repo","owner":{"login":"fixture-owner"},"private":true,"default_branch":"release/main"}`,
		"/repos/fixture-owner/repo/branches/release%2Fmain":                             `{"name":"release/main","commit":{"sha":"` + repositorySHA + `"}}`,
		"/repos/fixture-owner/repo/pulls?state=all&per_page=1":                          `[]`,
		"/repos/fixture-owner/repo/issues?state=all&per_page=1":                         `[]`,
		"/repos/fixture-owner/repo/collaborators/fixture-user/permission":               `{"permission":"read","role_name":"triage","user":{"id":17,"node_id":"U_17","login":"fixture-user"}}`,
		"/repos/fixture-owner/repo/rules/branches/release%2Fmain?per_page=1":            `[]`,
		"/repos/fixture-owner/repo/commits/" + repositorySHA + "/check-runs?per_page=1": `{"total_count":0,"check_runs":[]}`,
		"/repos/fixture-owner/repo/commits/" + repositorySHA + "/status?per_page=1":     `{"state":"failure","sha":"` + repositorySHA + `","total_count":0,"statuses":[]}`,
	}
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Error("unscoped request")
		}
		path := r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		body, ok := responses[path]
		status := 200
		if changed, exists := changes[path]; exists {
			status, body = changed.status, changed.body
		} else if !ok {
			t.Errorf("unexpected path: %s", path)
			status = 500
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return client, calls
}
