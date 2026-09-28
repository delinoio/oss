package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestCLIGitHubQueriesUseVersionedScopedRead(t *testing.T) {
	for _, bad := range []string{"", "query", "repository", "version"} {
		t.Run(bad, func(t *testing.T) {
			id := domain.NewID()
			calls := 0
			peer := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.IntegrationServiceQueryRepositoryIntegrationProcedure, func(_ context.Context, r *connect.Request[pb.QueryRepositoryIntegrationRequest]) (*connect.Response[pb.QueryRepositoryIntegrationResponse], error) {
				calls++
				var q domain.RepositoryQuery
				if r.Msg.RepositoryId != string(id) || r.Msg.SchemaVersion != 1 || domain.Decode(r.Msg.QueryJson, &q) != nil || q.Search != "fix OR 오류" || q.Kind != domain.RepositoryIssue || q.Operation != domain.RepositorySearch {
					t.Error("changed query")
				}
				total := "0"
				incomplete := false
				result := domain.RepositoryQueryResult{RepositoryID: id, RepositoryRevision: "9007199254740993", ProfileID: domain.NewID(), GenerationID: domain.NewID(), ObservedAt: time.Now().UTC(), Identity: domain.GitHubIdentity{ID: "17", NodeID: "U_17", Login: "fixture-user"}, Repository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "37", NodeID: "R_37", Owner: "fixture-owner", Name: "repo", Private: true}, Query: q, Items: []domain.RepositoryItem{}, TotalCount: &total, Incomplete: &incomplete}
				version := uint32(1)
				switch bad {
				case "query":
					result.Query.Search = "changed"
				case "repository":
					result.RepositoryID = domain.NewID()
				case "version":
					version = 2
				}
				raw, _ := json.Marshal(result)
				return connect.NewResponse(&pb.QueryRepositoryIntegrationResponse{SchemaVersion: version, DocumentJson: raw}), nil
			}))
			defer peer.Close()
			var output, diagnostic strings.Builder
			code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "github", "issue", "search", "--repository-id", string(id), "--text", "fix OR 오류"}, IO{In: strings.NewReader("private-fixture-token"), Out: &output, Err: &diagnostic})
			if calls != 1 || (code == 0) != (bad == "") {
				t.Fatalf("unexpected CLI result: %d %s", code, output.String())
			}
			if strings.Contains(output.String(), "private-fixture-token") {
				t.Fatal("secret escaped")
			}
		})
	}
}

func TestCLIGitHubPRObservationCommandsUseExactNumberAndPage(t *testing.T) {
	for _, command := range []string{"diff", "checks", "statuses", "rules", "ci"} {
		t.Run(command, func(t *testing.T) {
			id := domain.NewID()
			calls := 0
			peer := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.IntegrationServiceQueryRepositoryIntegrationProcedure, func(_ context.Context, r *connect.Request[pb.QueryRepositoryIntegrationRequest]) (*connect.Response[pb.QueryRepositoryIntegrationResponse], error) {
				calls++
				var q domain.RepositoryQuery
				if domain.Decode(r.Msg.QueryJson, &q) != nil || q.Number != "9007199254740993" || string(q.Operation) != command || q.Kind != domain.RepositoryPullRequest || q.Validate() != nil {
					t.Error("changed PR query")
				}
				now := time.Now().UTC()
				body := ""
				no := false
				item := domain.RepositoryItem{Provider: domain.GitHubCom, Kind: domain.RepositoryPullRequest, IdentitySource: domain.RepositoryPullRequestIdentity, ID: "17", NodeID: "PR_17", Number: q.Number, Title: "Fixture", State: domain.RepositoryItemOpen, CreatedAt: now, UpdatedAt: now, URL: domain.RepositoryItemURL("fixture-owner", "repo", q.Kind, q.Number), Body: &body, Draft: &no, Merged: &no, BaseRef: "main", HeadRef: "feature", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
				value := domain.RepositoryQueryResult{RepositoryID: id, RepositoryRevision: "1", ProfileID: domain.NewID(), GenerationID: domain.NewID(), ObservedAt: now, Identity: domain.GitHubIdentity{ID: "17", NodeID: "U_17", Login: "fixture-user"}, Repository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "37", NodeID: "R_37", Owner: "fixture-owner", Name: "repo"}, Query: q, Items: []domain.RepositoryItem{item}}
				switch q.Operation {
				case domain.RepositoryDiff:
					sum := sha256.Sum256(nil)
					value.Diff = &domain.PullRequestDiff{Digest: hex.EncodeToString(sum[:]), BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA}
				case domain.RepositoryChecks:
					value.Checks = &domain.PullRequestChecks{HeadSHA: item.HeadSHA, Filter: domain.LatestCheckRuns, TotalCount: "0", Runs: []domain.PullRequestCheck{}}
				case domain.RepositoryCI:
					value.CI = &domain.PullRequestCI{Rules: domain.PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: []domain.ActiveRepositoryRule{}, Digest: domain.ActiveRulesDigest(nil)}, Head: domain.CIRollup{CommitSHA: item.HeadSHA, TotalCount: "0", Contexts: []domain.CIContext{}}, NativeMergeability: "CONFLICTING"}
					value.CI.Result = value.CI.Evaluate(item)
				case domain.RepositoryRules:
					value.Rules = &domain.PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: []domain.ActiveRepositoryRule{}, Digest: domain.ActiveRulesDigest(nil)}
				case domain.RepositoryStatuses:
					value.Statuses = &domain.PullRequestCommitStatuses{HeadSHA: item.HeadSHA, State: domain.CommitStatusPending, NativeState: "pending", TotalCount: "0", Contexts: []domain.PullRequestCommitStatus{}}
				}
				if err := value.Validate(); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(value)
				return connect.NewResponse(&pb.QueryRepositoryIntegrationResponse{SchemaVersion: 1, DocumentJson: raw}), nil
			}))
			defer peer.Close()
			var output, diagnostic strings.Builder
			args := []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "github", "pr", command, "--repository-id", string(id), "--number", "9007199254740993"}
			if command != "diff" && command != "rules" && command != "ci" {
				args = append(args, "--page", "2", "--page-size", "1")
			}
			code := Run(context.Background(), args, IO{In: strings.NewReader("private-fixture-token"), Out: &output, Err: &diagnostic})
			if code != 0 || calls != 1 {
				t.Fatalf("PR query: %d %s calls=%d", code, output.String(), calls)
			}
		})
	}
}
