package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const accessSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func accessFixture(t *testing.T, changes map[string]struct {
	status int
	body   string
}) (*Client, *atomic.Int32) {
	t.Helper()
	client := New()
	calls := &atomic.Int32{}
	responses := map[string]string{
		"/user":                     `{"id":17,"node_id":"U_17","login":"fixture-user","type":"User"}`,
		"/repos/fixture-owner/repo": `{"id":37,"node_id":"R_37","name":"repo","full_name":"fixture-owner/repo","owner":{"login":"fixture-owner"},"private":true,"default_branch":"release/main"}`,
		"/repos/fixture-owner/repo/branches/release%2Fmain":                         `{"name":"release/main","commit":{"sha":"` + accessSHA + `"}}`,
		"/repos/fixture-owner/repo/pulls?state=all&per_page=1":                      `[]`,
		"/repos/fixture-owner/repo/issues?state=all&per_page=1":                     `[]`,
		"/repos/fixture-owner/repo/collaborators/fixture-user/permission":           `{"permission":"read","role_name":"triage","user":{"id":17,"node_id":"U_17","login":"fixture-user"}}`,
		"/repos/fixture-owner/repo/rules/branches/release%2Fmain?per_page=1":        `[]`,
		"/repos/fixture-owner/repo/commits/" + accessSHA + "/check-runs?per_page=1": `{"total_count":0,"check_runs":[]}`,
		"/repos/fixture-owner/repo/commits/" + accessSHA + "/status?per_page=1":     `{"state":"failure","sha":"` + accessSHA + `","total_count":0,"statuses":[]}`,
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
func TestRepositoryAccessIndependentFeaturesAndNoPassingCIFiction(t *testing.T) {
	checkPath := "/repos/fixture-owner/repo/commits/" + accessSHA + "/check-runs?per_page=1"
	client, calls := accessFixture(t, map[string]struct {
		status int
		body   string
	}{checkPath: {403, `private-response-marker`}})
	observed, err := client.InspectRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo")
	if err != nil || calls.Load() != 9 || observed.Repository == nil || observed.Repository.HeadCommit != accessSHA || observed.Identity.ID != "17" {
		t.Fatalf("inspection: %+v %v calls=%d", observed, err, calls.Load())
	}
	for _, access := range observed.Features {
		if access.Feature == domain.ChecksFeature {
			if access.State != domain.IntegrationAccessDenied {
				t.Fatal("denied checks made available")
			}
		} else if access.State != domain.IntegrationAccessAvailable {
			t.Fatalf("independent feature blocked: %+v", access)
		}
	}
	raw, _ := json.Marshal(observed)
	for _, forbidden := range []string{"private-fixture-pat", "private-response-marker", `"state":"success"`, `"state":"failure"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("raw secret, diagnostic or CI result escaped")
		}
	}
}
func TestRepositoryAccessRefusesMissingForeignAndMalformedRoot(t *testing.T) {
	for name, change := range map[string]struct {
		status int
		body   string
	}{"missing": {404, `private`}, "redirect": {301, `private`}, "foreign": {200, `{"id":37,"node_id":"R_37","name":"foreign","full_name":"other/foreign","owner":{"login":"other"},"private":true,"default_branch":"main"}`}, "no-privacy": {200, `{"id":37,"node_id":"R_37","name":"repo","full_name":"fixture-owner/repo","owner":{"login":"fixture-owner"}}`}} {
		t.Run(name, func(t *testing.T) {
			client, calls := accessFixture(t, map[string]struct {
				status int
				body   string
			}{"/repos/fixture-owner/repo": change})
			result, err := client.InspectRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo")
			if err != nil || result.Repository != nil || calls.Load() != 2 {
				t.Fatalf("root accepted: %+v %v", result, err)
			}
			for _, feature := range result.Features {
				if feature.State == domain.IntegrationAccessAvailable {
					t.Fatal("access inferred after failed root")
				}
			}
		})
	}
}
func TestRepositoryAccessMissingBranchDoesNotHideIndependentReads(t *testing.T) {
	client, calls := accessFixture(t, map[string]struct {
		status int
		body   string
	}{"/repos/fixture-owner/repo/branches/release%2Fmain": {404, `private`}})
	observed, err := client.InspectRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo")
	if err != nil || calls.Load() != 7 || observed.Repository.HeadCommit != "" {
		t.Fatal("branch absence invented a head", err)
	}
	for _, feature := range observed.Features {
		switch feature.Feature {
		case domain.RepositoryContentsFeature:
			if feature.State != domain.IntegrationAccessNotFound {
				t.Fatal(feature)
			}
		case domain.ChecksFeature, domain.CommitStatusesFeature:
			if feature.State != domain.IntegrationAccessNotEvaluated {
				t.Fatal(feature)
			}
		default:
			if feature.State != domain.IntegrationAccessAvailable {
				t.Fatal(feature)
			}
		}
	}
}
func TestRepositoryAccessCancelJoinsBoundedProbeWorkers(t *testing.T) {
	client, _ := accessFixture(t, nil)
	original := client.http.Transport
	entered := make(chan struct{}, 2)
	var active atomic.Int32
	var once sync.Once
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/pulls") || strings.Contains(r.URL.Path, "/issues") {
			active.Add(1)
			defer active.Add(-1)
			entered <- struct{}{}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return original.RoundTrip(r)
	})
	done := make(chan error, 1)
	go func() {
		_, err := client.InspectRepository(ctx, []byte("private-fixture-pat"), "fixture-owner", "repo")
		done <- err
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			once.Do(cancel)
			t.Fatal("probe workers not active")
		}
	}
	cancel()
	if err := <-done; err == nil || active.Load() != 0 {
		t.Fatal("canceled workers survived", err)
	}
}

func TestRepositoryAccessRejectsNullFeatureRows(t *testing.T) {
	for _, path := range []string{"/repos/fixture-owner/repo/pulls?state=all&per_page=1", "/repos/fixture-owner/repo/rules/branches/release%2Fmain?per_page=1"} {
		client, _ := accessFixture(t, map[string]struct {
			status int
			body   string
		}{path: {200, `[null]`}})
		value, err := client.InspectRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo")
		if err != nil {
			t.Fatal(err)
		}
		feature := domain.PullRequestsFeature
		if strings.Contains(path, "/rules/") {
			feature = domain.RulesetsFeature
		}
		for _, result := range value.Features {
			if result.Feature == feature && result.State != domain.IntegrationAccessUnavailable {
				t.Fatal("malformed feature granted access")
			}
		}
	}
}
