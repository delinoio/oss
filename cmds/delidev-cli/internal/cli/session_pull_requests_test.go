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

func TestCLISessionPRLinkValidatesOriginalAcknowledgment(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(strconvBool(foreign), func(t *testing.T) {
			session, repository, requestID := domain.NewID(), domain.NewID(), domain.NewID()
			peer := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.SessionServiceLinkSessionPullRequestProcedure, func(_ context.Context, req *connect.Request[pb.LinkSessionPullRequestRequest]) (*connect.Response[pb.LinkSessionPullRequestResponse], error) {
				if req.Msg.SessionId != string(session) || req.Msg.RepositoryId != string(repository) || req.Msg.RequestId != string(requestID) || req.Msg.Number != "9007199254740993" {
					t.Error("CLI changed original link selection")
				}
				v := domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: repository, RemoteRepositoryID: "37", RepositoryNodeID: "R_37", Owner: "fixture-owner", Name: "repo", PullRequestID: "9007199254740995", PullRequestNodeID: "PR_17", Number: req.Msg.Number, Title: "Original", ObservedAt: time.Now().UTC()}
				if foreign {
					v.RepositoryID = domain.NewID()
				}
				raw, _ := json.Marshal(v)
				return connect.NewResponse(&pb.LinkSessionPullRequestResponse{RequestId: string(requestID), Association: &pb.Resource{Id: string(requestID), Kind: pb.EntityKind_ENTITY_KIND_PULL_REQUEST, SessionId: string(session), ProjectId: string(domain.NewID()), SchemaVersion: 1, Revision: 1, DocumentJson: raw}}), nil
			}))
			defer peer.Close()
			var output, diagnostic strings.Builder
			code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "session", "pr", "link", "--id", string(session), "--repository-id", string(repository), "--number", "9007199254740993", "--request-id", string(requestID)}, IO{In: strings.NewReader("private-fixture-token"), Out: &output, Err: &diagnostic})
			if (code == 0) == foreign {
				t.Fatal("unexpected link result", code, output.String(), diagnostic.String())
			}
			if !foreign && !strings.Contains(output.String(), "9007199254740995") {
				t.Fatal("CLI rounded PR identity")
			}
		})
	}
}

func TestCLISessionPRUnlinkKeepsOriginalRevision(t *testing.T) {
	session, association, requestID := domain.NewID(), domain.NewID(), domain.NewID()
	peer := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.SessionServiceUnlinkSessionPullRequestProcedure, func(_ context.Context, req *connect.Request[pb.UnlinkSessionPullRequestRequest]) (*connect.Response[pb.UnlinkSessionPullRequestResponse], error) {
		if req.Msg.SessionId != string(session) || req.Msg.Mutation.Id != string(association) || req.Msg.Mutation.ExpectedRevision != 9007199254740993 {
			t.Error("CLI changed unlink ownership/revision")
		}
		return connect.NewResponse(&pb.UnlinkSessionPullRequestResponse{Id: string(association), RequestId: req.Msg.Mutation.RequestId, Replayed: true}), nil
	}))
	defer peer.Close()
	var output, diagnostic strings.Builder
	code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "session", "pr", "unlink", "--id", string(session), "--association-id", string(association), "--revision", "9007199254740993", "--request-id", string(requestID)}, IO{In: strings.NewReader("private-fixture-token"), Out: &output, Err: &diagnostic})
	if code != 0 || !strings.Contains(output.String(), `"replayed":true`) {
		t.Fatal("unlink failed", code, output.String(), diagnostic.String())
	}
}

func strconvBool(v bool) string {
	if v {
		return "foreign"
	}
	return "valid"
}
