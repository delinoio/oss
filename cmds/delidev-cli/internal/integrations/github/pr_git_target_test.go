package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func prHeadRepositoryFixture() map[string]any {
	return map[string]any{"id": uint64(9007199254740993), "node_id": "FORK_1", "name": "fork-repo", "full_name": "fixture-author/fork-repo", "owner": map[string]any{"login": "fixture-author"}, "private": true, "default_branch": "main", "clone_url": "https://github.com/fixture-author/fork-repo.git", "ssh_url": "git@github.com:fixture-author/fork-repo.git"}
}

func TestPRDetailRetainsOriginalForkAndRejectsForeignTransportMetadata(t *testing.T) {
	for _, scenario := range []string{"fork", "null", "missing", "id", "node", "full-name", "https-userinfo", "ssh-host", "null-owner", "url-suffix"} {
		t.Run(scenario, func(t *testing.T) {
			q := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: "17"}
			body := queryFixtureItem(q.Kind, 17, true, false)
			head := body["head"].(map[string]any)
			repo := prHeadRepositoryFixture()
			head["repo"] = repo
			switch scenario {
			case "null":
				head["repo"] = nil
			case "missing":
				delete(head, "repo")
			case "id":
				repo["id"] = 37
			case "node":
				repo["node_id"] = "R_37"
			case "full-name":
				repo["full_name"] = "other/repo"
			case "https-userinfo":
				repo["clone_url"] = "https://other@github.com/fixture-author/fork-repo.git"
			case "ssh-host":
				repo["ssh_url"] = "git@other.invalid:fixture-author/fork-repo.git"
			case "null-owner":
				repo["owner"] = nil
			case "url-suffix":
				repo["clone_url"] = "https://github.com/fixture-author/fork-repo.git?token=fixture"
			}
			client, _ := queryFixture(t, q, body, "")
			result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
			if scenario != "fork" && scenario != "null" {
				if err == nil {
					t.Fatal("foreign head repository accepted")
				}
				return
			}
			if err != nil || len(result.Items) != 1 || result.Items[0].HeadRepository == nil {
				t.Fatal("missing original source", err)
			}
			observed := result.Items[0].HeadRepository
			if scenario == "null" {
				if observed.State != domain.PRHeadRepositoryUnavailable || observed.Repository != nil {
					t.Fatal("deleted fork inferred")
				}
			} else if observed.State != domain.PRHeadRepositoryAvailable || observed.Repository.ID != "9007199254740993" || observed.Repository.Owner != "fixture-author" {
				t.Fatal("fork overwritten")
			}
		})
	}
}

func TestPRRepeatedReadBindsOriginalHeadRepository(t *testing.T) {
	for _, scenario := range []string{"unchanged", "replaced", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			q := observationQuery(domain.RepositoryDiff)
			client, _ := observationFixture(t, q, "diff --git a/file b/file\n", false)
			base := client.http.Transport
			details := 0
			client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(r)
				if err != nil || r.URL.Path != "/repos/fixture-owner/repo/pulls/17" {
					return response, err
				}
				defer response.Body.Close()
				var body map[string]any
				if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				details++
				repo := prHeadRepositoryFixture()
				head := body["head"].(map[string]any)
				head["repo"] = repo
				if details == 2 {
					if scenario == "replaced" {
						repo["id"] = 99
						repo["node_id"] = "FORK_99"
					}
					if scenario == "deleted" {
						head["repo"] = nil
					}
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				response.Body = io.NopCloser(strings.NewReader(string(raw)))
				return response, nil
			})
			result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
			if details != 2 {
				t.Fatal("missing final original-PR read", details)
			}
			if scenario != "unchanged" {
				if domain.SafeError(err).Code != domain.Conflict {
					t.Fatal("changed head repository published", err)
				}
			} else if err != nil || result.Diff == nil || result.Items[0].HeadRepository.Repository.ID != "9007199254740993" {
				t.Fatal("original fork not published", err)
			}
		})
	}
}

func TestOptInPublicPRHeadRepositoryProjection(t *testing.T) {
	path := os.Getenv("DELIDEV_GITHUB_PR_HEAD_FIXTURE")
	if path == "" {
		t.Skip("explicit public response capture required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fields, ok := jsonObject(raw)
	if !ok {
		t.Fatal("capture")
	}
	base, ok := jsonObject(fields["base"])
	if !ok {
		t.Fatal("base")
	}
	head, ok := jsonObject(fields["head"])
	if !ok {
		t.Fatal("head")
	}
	baseRepo, ok := parseRepository(base["repo"], "delinoio", "oss")
	if !ok {
		t.Fatal("base identity")
	}
	observed, ok := parsePRHeadRepository(head["repo"], *baseRepo)
	if !ok || observed.State != domain.PRHeadRepositoryAvailable || observed.Repository == nil {
		t.Fatal("public head repository projection")
	}
	encoded, _ := json.Marshal(observed)
	if len(encoded) == 0 {
		t.Fatal("empty projection")
	}
}
