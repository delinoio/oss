package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPRWorkspaceCountsRejectInvalidOperands(t *testing.T) {
	for _, raw := range []string{`{"additions":0,"deletions":0}`, `{}`, `{"additions":9007199254740991,"deletions":1}`} {
		fields, _ := jsonObject([]byte(raw))
		if _, err := parseCounts(fields); err != nil {
			t.Fatalf("valid counts: %s %v", raw, err)
		}
	}
	for _, raw := range []string{`{"additions":-1,"deletions":0}`, `{"additions":1.5,"deletions":0}`, `{"additions":"1","deletions":0}`, `{"additions":9007199254740992,"deletions":0}`, `{"additions":0}`, `{"additions":null,"deletions":0}`} {
		fields, _ := jsonObject([]byte(raw))
		if _, err := parseCounts(fields); err == nil {
			t.Fatalf("invalid counts accepted: %s", raw)
		}
	}
}
func TestPRWorkspaceEdgesRequireSameRepositoryAndExposeAmbiguity(t *testing.T) {
	repo := domain.RemoteRepository{ID: "37"}
	row := func(number, base, head, remote string) domain.PRWorkspaceRow {
		return domain.PRWorkspaceRow{Item: domain.RepositoryItem{Number: number, BaseRef: base, HeadRef: head, HeadRepository: &domain.PRHeadRepositoryObservation{State: domain.PRHeadRepositoryAvailable, Repository: &domain.RemoteRepository{ID: remote}}}}
	}
	rows := []domain.PRWorkspaceRow{row("1", "main", "first", "37"), row("2", "first", "second", "37"), row("3", "second", "third", "38")}
	edges, ambiguous := workspaceEdges(rows, repo)
	if ambiguous || len(edges) != 2 || edges[0].Parent != "1" || edges[0].Child != "2" {
		t.Fatalf("exact graph: %+v %v", edges, ambiguous)
	}
	rows = append(rows, row("4", "main", "first", "37"))
	if _, ambiguous = workspaceEdges(rows, repo); !ambiguous {
		t.Fatal("competing parents concealed")
	}
	rows = []domain.PRWorkspaceRow{row("1", "second", "first", "37"), row("2", "first", "second", "37")}
	if _, ambiguous = workspaceEdges(rows, repo); !ambiguous {
		t.Fatal("cycle concealed")
	}
}
func TestPRWorkspaceAvatarIsSanitizedAndCredentialFree(t *testing.T) {
	var source bytes.Buffer
	png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 100, 80)))
	client, _ := repositoryFixture(t, nil)
	calls := 0
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.Host != "avatars.githubusercontent.com" {
			t.Fatal("avatar credential or origin violation")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(source.Bytes()))}, nil
	})
	raw, err := client.ReadAvatar(context.Background(), "https://avatars.githubusercontent.com/u/19?v=4", "19")
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != "png" || config.Width != 64 || config.Height != 64 || calls != 1 {
		t.Fatalf("sanitized raster: %+v %s %v", config, format, err)
	}
	for _, locator := range []string{"https://evil.example/u/19", "https://avatars.githubusercontent.com/u/20", "https://x@avatars.githubusercontent.com/u/19", "https://avatars.githubusercontent.com:443/u/19", "https://avatars.githubusercontent.com/u/19?url=secret"} {
		if _, err = client.ReadAvatar(context.Background(), locator, "19"); err == nil {
			t.Fatalf("invalid locator: %s", locator)
		}
	}
	if calls != 1 {
		t.Fatal("invalid locator reached transport")
	}
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.example"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if _, err = client.ReadAvatar(context.Background(), "https://avatars.githubusercontent.com/u/19", "19"); err == nil {
		t.Fatal("redirect accepted")
	}
}

func TestPRWorkspaceDiscoversAllStateContextAndRechecksRelationships(t *testing.T) {
	for _, scenario := range []string{"stable", "branch-page", "sha"} {
		drift := scenario != "stable"
		client, _ := repositoryFixture(t, nil)
		original := client.http.Transport
		branchReads := map[string]int{}
		client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/user" || request.URL.Path == "/repos/fixture-owner/repo" {
				return original.RoundTrip(request)
			}
			if request.Header.Get("Authorization") != "Bearer private-fixture-pat" {
				t.Fatal("unscoped workspace request")
			}
			makeItem := func(number int) map[string]any {
				item := queryFixtureItem(domain.RepositoryPullRequest, number, true, false)
				item["id"] = number + 1000
				item["node_id"] = fmt.Sprintf("PR_%d", number)
				item["additions"] = number - 17
				item["deletions"] = 0
				repo := prHeadRepositoryFixture()
				repo["id"] = 37
				repo["node_id"] = "R_37"
				repo["name"] = "repo"
				repo["full_name"] = "fixture-owner/repo"
				repo["owner"] = map[string]any{"login": "fixture-owner"}
				repo["clone_url"] = "https://github.com/fixture-owner/repo.git"
				repo["ssh_url"] = "git@github.com:fixture-owner/repo.git"
				head := item["head"].(map[string]any)
				head["repo"] = repo
				head["ref"] = "first"
				if number == 18 {
					item["base"].(map[string]any)["ref"] = "first"
					item["base"].(map[string]any)["sha"] = strings.Repeat("b", 40)
					if scenario == "sha" {
						item["base"].(map[string]any)["sha"] = strings.Repeat("c", 40)
					}
					head["ref"] = "second"
					item["state"] = "closed"
				}
				return item
			}
			var body any
			if strings.HasSuffix(request.URL.Path, "/17") {
				body = makeItem(17)
			} else if strings.HasSuffix(request.URL.Path, "/18") {
				body = makeItem(18)
			} else {
				if request.URL.Query().Get("state") != "all" || request.URL.Query().Get("per_page") != "20" {
					t.Fatal("non-bounded discovery")
				}
				key := request.URL.RawQuery
				branchReads[key]++
				body = []any{}
				if request.URL.Query().Get("base") == "first" && (scenario != "branch-page" || branchReads[key] == 1) {
					body = []any{makeItem(18)}
				}
				if request.URL.Query().Get("head") == "fixture-owner:first" {
					body = []any{makeItem(17)}
				}
			}
			raw, _ := json.Marshal(body)
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
		})
		workspace, err := client.PullRequestWorkspace(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", []uint32{17})
		if err != nil || len(workspace.Rows) != 2 || len(workspace.Edges) != 1 || workspace.Edges[0].Parent != "17" || workspace.Edges[0].Child != "18" || !workspace.Rows[0].Seed || workspace.Rows[1].Seed {
			t.Fatalf("workspace graph: %+v %v", workspace, err)
		}
		expected := domain.PRWorkspaceComplete
		if drift {
			expected = domain.PRWorkspaceIncomplete
		}
		if workspace.State != expected {
			t.Fatalf("recheck classification %s, expected %s", workspace.State, expected)
		}
	}
}

func TestPRWorkspaceDepthUsesEntireComponentRatherThanSeedDistance(t *testing.T) {
	for _, length := range []int{20, 21, 40} {
		edges := []domain.PRWorkspaceEdge{}
		for i := 0; i < length; i++ {
			edges = append(edges, domain.PRWorkspaceEdge{Parent: fmt.Sprint(i), Child: fmt.Sprint(i + 1)})
		}
		if workspaceDepthExceeds(edges, 20) != (length > 20) {
			t.Fatalf("incorrect complete-component depth for %d edges", length)
		}
	}
}

func TestPRWorkspaceAvatarRejectsMalformedOversizedAndLargeDimensions(t *testing.T) {
	var large bytes.Buffer
	if err := png.Encode(&large, image.NewRGBA(image.Rect(0, 0, 1025, 1))); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{[]byte("not a raster"), bytes.Repeat([]byte{0}, (128<<10)+1), large.Bytes()} {
		client, _ := repositoryFixture(t, nil)
		client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
		})
		if _, err := client.ReadAvatar(context.Background(), "https://avatars.githubusercontent.com/u/19?v=4", "19"); err == nil {
			t.Fatal("invalid raster admitted")
		}
	}
}
