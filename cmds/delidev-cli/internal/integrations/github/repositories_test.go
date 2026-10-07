// SPDX-License-Identifier: Apache-2.0
package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const listRepositoryFixture = `{"id":9007199254740993,"node_id":"R_fixture","name":"repo","full_name":"fixture-owner/repo","owner":{"login":"fixture-owner"},"private":true,"archived":true,"default_branch":"main","clone_url":"ext::untrusted-helper","temp_clone_token":"unprojected-fixture-secret"}`

func repositoryListFixture(t *testing.T, status int, body, link string) (*Client, *int) {
	t.Helper()
	c := New()
	calls := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "api.github.com" || r.URL.RequestURI() != repositoryInventoryPath(1, 30) || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Error("unscoped listing request")
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Link": {link}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return c, &calls
}
func TestGitHubRepositoryListProjectsMetadataAndConstructsURLs(t *testing.T) {
	link := "<" + apiOrigin + repositoryInventoryPath(2, 30) + ">; rel=\"next\""
	c, calls := repositoryListFixture(t, 200, "["+listRepositoryFixture+"]", link)
	got, err := c.ListRepositories(context.Background(), []byte("private-fixture-pat"), 1, 30)
	if err != nil || *calls != 1 || len(got.Repositories) != 1 || got.NextPage != 2 {
		t.Fatal(got, err, calls)
	}
	r := got.Repositories[0]
	if r.Repository.ID != "9007199254740993" || !r.Repository.Private || !r.Archived || r.HTTPSURL != "https://github.com/fixture-owner/repo.git" || r.SSHURL != "git@github.com:fixture-owner/repo.git" {
		t.Fatal(r)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "untrusted") || strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "pat") {
		t.Fatal("unselected credential/native fields projected")
	}
}
func TestGitHubRepositoryListRejectsWholeMalformedPages(t *testing.T) {
	for _, body := range []string{"null", "{}", "[null]", "[" + strings.Replace(listRepositoryFixture, `"archived":true`, `"archived":null`, 1) + "]", "[" + strings.Replace(listRepositoryFixture, `"private":true`, `"private":"yes"`, 1) + "]", "[" + strings.Replace(listRepositoryFixture, `"full_name":"fixture-owner/repo"`, `"full_name":"foreign/repo"`, 1) + "]", "[" + listRepositoryFixture + "," + listRepositoryFixture + "]", "[" + strings.Replace(listRepositoryFixture, `"id":9007199254740993`, `"id":1,"id":2`, 1) + "]", "[" + strings.TrimSuffix(strings.Repeat(listRepositoryFixture+",", 31), ",") + "]"} {
		c, _ := repositoryListFixture(t, 200, body, "")
		if got, err := c.ListRepositories(context.Background(), []byte("private-fixture-pat"), 1, 30); err == nil || len(got.Repositories) != 0 {
			t.Fatal("malformed page accepted", got, err)
		}
	}
}
func TestGitHubRepositoryListEmptyAuthenticationAndRestrictedAreDistinct(t *testing.T) {
	c, _ := repositoryListFixture(t, 200, "[]", "")
	if got, err := c.ListRepositories(context.Background(), []byte("private-fixture-pat"), 1, 30); err != nil || got.Repositories == nil || len(got.Repositories) != 0 {
		t.Fatal(got, err)
	}
	for _, status := range []int{401, 403, 429, 500} {
		c, _ := repositoryListFixture(t, status, "private-native-error", "")
		if got, err := c.ListRepositories(context.Background(), []byte("private-fixture-pat"), 1, 30); err == nil || strings.Contains(err.Error(), "private-native-error") || got.NextPage != 0 {
			t.Fatal(got, err)
		}
	}
}
func TestGitHubRepositoryListRejectsForeignPagingAndBoundViolations(t *testing.T) {
	for _, link := range []string{"<https://foreign.invalid/user/repos?page=2>; rel=\"next\"", "<" + apiOrigin + repositoryInventoryPath(3, 30) + ">; rel=\"next\"", "<" + apiOrigin + "/user/repos?page=2&per_page=100>; rel=\"next\""} {
		c, calls := repositoryListFixture(t, 200, "[]", link)
		if _, err := c.ListRepositories(context.Background(), []byte("private-fixture-pat"), 1, 30); err == nil || *calls != 1 {
			t.Fatal(err, calls)
		}
	}
	c, calls := repositoryListFixture(t, 200, "[]", "")
	for _, input := range [][2]uint32{{0, 30}, {1, 0}, {1, 101}, {1000001, 30}} {
		if _, err := c.ListRepositories(context.Background(), []byte("private-fixture-pat"), input[0], input[1]); domain.SafeError(err).Code != domain.InvalidArgument {
			t.Fatal(err)
		}
	}
	if *calls != 0 {
		t.Fatal("invalid input reached GitHub")
	}
	c, _ = repositoryListFixture(t, 200, strings.Repeat(" ", (1<<20)+1), "")
	if _, err := c.ListRepositories(context.Background(), []byte("private-fixture-pat"), 1, 30); err == nil {
		t.Fatal("oversized response accepted")
	}
}
