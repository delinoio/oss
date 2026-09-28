package cli

import (
	"context"
	"encoding/json"
	"net/http"
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

func TestCLIRemediationHistoryAndResumePreserveOriginalIdentity(t *testing.T) {
	for _, action := range []string{"list", "resume", "foreign-chain", "duplicate", "foreign-pr"} {
		t.Run(action, func(t *testing.T) {
			at := time.Now().UTC()
			setID, chainID, requestID := domain.NewID(), domain.NewID(), domain.NewID()
			target := domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: domain.NewID(), RemoteRepositoryID: "37", RepositoryNodeID: "R_37", Owner: "fixture-owner", Name: "repo", PullRequestID: "9007199254740993", PullRequestNodeID: "PR_17", Number: "17", Title: "Original", ObservedAt: at}
			set := domain.PRProblemSet{Version: 1, Type: domain.PRProblemSetRecord, Target: target, Feedback: &domain.PRProblemObservation{BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), ObservedAt: at}, Remediation: &domain.PRRemediationChain{ID: chainID, Sequence: 1}}
			attempt := domain.PRRemediationAttempt{Version: 1, Type: domain.PRRemediationAttemptRecord, SetID: setID, ChainID: chainID, Sequence: 1, Mode: domain.PRRemediationManual, State: domain.PRRemediationCanceled, Policy: domain.DefaultRemediationPolicy(), Problems: []domain.PRRemediationProblemRef{{ID: domain.NewID(), ContentVersion: strings.Repeat("c", 64)}}, Reserved: domain.PRProblemDismissal{RequestID: domain.NewID(), ActorType: domain.OwnerDevice, At: at}, FinishedAt: &at}
			if action == "foreign-chain" {
				attempt.ChainID = domain.NewID()
			}
			if action == "foreign-pr" {
				set.Target.PullRequestID = "99"
			}
			resource := func(id domain.ID, value any) *pb.Resource {
				raw, _ := json.Marshal(value)
				return &pb.Resource{Id: string(id), Kind: pb.EntityKind_ENTITY_KIND_PROBLEM, SchemaVersion: 1, Revision: 9007199254740994, DocumentJson: raw}
			}
			setRow, row := resource(setID, set), resource(domain.NewID(), attempt)
			calls := 0
			mux := http.NewServeMux()
			mux.Handle(delidevv1connect.IntegrationServiceListPullRequestRemediationAttemptsProcedure, connect.NewUnaryHandler(delidevv1connect.IntegrationServiceListPullRequestRemediationAttemptsProcedure, func(_ context.Context, req *connect.Request[pb.ListPullRequestRemediationAttemptsRequest]) (*connect.Response[pb.ListPullRequestRemediationAttemptsResponse], error) {
				calls++
				if req.Msg.RemoteRepositoryId != "37" || req.Msg.PullRequestId != "9007199254740993" || req.Msg.PageSize != 20 {
					t.Error("CLI changed original numeric identity")
				}
				rows := []*pb.Resource{row}
				if action == "duplicate" {
					rows = append(rows, row)
				}
				return connect.NewResponse(&pb.ListPullRequestRemediationAttemptsResponse{ProblemSet: setRow, Attempts: rows}), nil
			}))
			mux.Handle(delidevv1connect.IntegrationServiceResumePullRequestRemediationProcedure, connect.NewUnaryHandler(delidevv1connect.IntegrationServiceResumePullRequestRemediationProcedure, func(_ context.Context, req *connect.Request[pb.ResumePullRequestRemediationRequest]) (*connect.Response[pb.ResumePullRequestRemediationResponse], error) {
				calls++
				if req.Msg.Mutation == nil || req.Msg.Mutation.Id != string(setID) || req.Msg.Mutation.ExpectedRevision != 9007199254740993 || req.Msg.Mutation.RequestId != string(requestID) {
					t.Error("CLI changed original resumption identity")
				}
				return connect.NewResponse(&pb.ResumePullRequestRemediationResponse{ProblemSet: setRow, RequestId: string(requestID), Replayed: true}), nil
			}))
			peer := httptest.NewServer(mux)
			defer peer.Close()
			args := []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "--request-id", string(requestID), "github", "pr", "remediation"}
			if action == "resume" {
				args = append(args, "resume", "--id", string(setID), "--revision", "9007199254740993")
			} else {
				args = append(args, "list", "--remote-repository-id", "37", "--pull-request-id", "9007199254740993")
			}
			var output, diagnostic strings.Builder
			code := Run(context.Background(), args, IO{In: strings.NewReader("private-remediation-fixture-token"), Out: &output, Err: &diagnostic})
			if (code == 0) != (action == "list" || action == "resume") || calls != 1 {
				t.Fatalf("CLI result=%d calls=%d diagnostics=%s", code, calls, diagnostic.String())
			}
			if strings.Contains(output.String(), "private-remediation-fixture-token") {
				t.Fatal("fixture token escaped into output")
			}
		})
	}
}
