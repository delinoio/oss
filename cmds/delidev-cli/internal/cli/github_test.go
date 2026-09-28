package cli

import (
	"context"
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
