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

func TestCLIPRProblemCommandsPreserveExactIdentityAndRejectForeignHistory(t *testing.T) {
	for _, mode := range []string{"refresh", "refresh-ci", "refresh-conflict", "list", "dismiss", "foreign-history", "generated-refresh", "generated-dismiss"} {
		t.Run(mode, func(t *testing.T) {
			generated := strings.HasPrefix(mode, "generated-")
			mode = strings.TrimPrefix(mode, "generated-")
			repository, id, setID, requestID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			at := time.Now().UTC()
			target := domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: repository, RemoteRepositoryID: "37", RepositoryNodeID: "R_37", Owner: "fixture-owner", Name: "repo", PullRequestID: "9007199254740993", PullRequestNodeID: "PR_17", Number: "17", Title: "Original", ObservedAt: at}
			observation := domain.PRProblemObservation{BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), ObservedAt: at}
			entry := domain.PRFeedback{Kind: domain.PRConversationComment, ID: "71", NodeID: "COMMENT_71", Body: "Original retained feedback", PublishedAt: at, URL: "https://github.com/fixture-owner/repo/pull/17#issuecomment-71"}
			entry.ContentVersion = entry.Version()
			provider, _ := domain.FeedbackProviderState(entry, nil)
			value := domain.PRProblem{Version: 1, Type: domain.PRProblemEvidenceRecord, SetID: setID, Kind: domain.PRFeedbackProblem, Target: target, Observation: observation, ContentVersion: entry.ContentVersion, Feedback: &entry, OriginalProvider: &provider, LatestProvider: &provider, State: domain.PRProblemDismissed, Dismissal: &domain.PRProblemDismissal{ActorType: domain.OwnerDevice, RequestID: requestID, At: at}}
			set := domain.PRProblemSet{Version: 1, Type: domain.PRProblemSetRecord, Target: target, Feedback: &observation}
			expectedKind := pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_FEEDBACK
			if mode == "refresh-ci" {
				expectedKind = pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CI
				set.CI = &domain.PRCIObservationSummary{Observation: observation, State: domain.CIUnknown, Reason: domain.CICommitUnverified, Source: domain.CIUnknownCommit, RulesDigest: strings.Repeat("a", 64)}
			} else if mode == "refresh-conflict" {
				expectedKind = pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CONFLICT
				set.Conflict = &domain.PRConflictObservation{Observation: observation, BaseRef: "main", HeadRef: "feature", PullRequestState: domain.RepositoryItemOpen, State: domain.PRConflictUnknown}
			}
			if mode == "foreign-history" {
				set.Target.PullRequestID = "99"
			}
			raw, _ := json.Marshal(value)
			setRaw, _ := json.Marshal(set)
			problem := &pb.Resource{Id: string(id), Kind: pb.EntityKind_ENTITY_KIND_PROBLEM, SchemaVersion: 1, Revision: 9007199254740994, DocumentJson: raw}
			setRow := &pb.Resource{Id: string(setID), Kind: pb.EntityKind_ENTITY_KIND_PROBLEM, SchemaVersion: 1, Revision: 2, DocumentJson: setRaw}
			calls := 0
			mux := http.NewServeMux()
			mux.Handle(delidevv1connect.IntegrationServiceRefreshPullRequestProblemsProcedure, connect.NewUnaryHandler(delidevv1connect.IntegrationServiceRefreshPullRequestProblemsProcedure, func(_ context.Context, r *connect.Request[pb.RefreshPullRequestProblemsRequest]) (*connect.Response[pb.RefreshPullRequestProblemsResponse], error) {
				calls++
				if generated {
					requestID = domain.ID(r.Msg.RequestId)
				}
				if r.Msg.RepositoryId != string(repository) || r.Msg.Number != "17" || r.Msg.RequestId != string(requestID) || r.Msg.Kind != expectedKind {
					t.Error("collection identity changed")
				}
				return connect.NewResponse(&pb.RefreshPullRequestProblemsResponse{ProblemSet: setRow, RequestId: string(requestID)}), nil
			}))
			mux.Handle(delidevv1connect.IntegrationServiceListPullRequestProblemsProcedure, connect.NewUnaryHandler(delidevv1connect.IntegrationServiceListPullRequestProblemsProcedure, func(_ context.Context, r *connect.Request[pb.ListPullRequestProblemsRequest]) (*connect.Response[pb.ListPullRequestProblemsResponse], error) {
				calls++
				if r.Msg.RemoteRepositoryId != "37" || r.Msg.PullRequestId != "9007199254740993" || r.Msg.PageSize != 20 {
					t.Error("history identity changed")
				}
				return connect.NewResponse(&pb.ListPullRequestProblemsResponse{ProblemSet: setRow, Problems: []*pb.Resource{problem}}), nil
			}))
			mux.Handle(delidevv1connect.IntegrationServiceDismissPullRequestProblemProcedure, connect.NewUnaryHandler(delidevv1connect.IntegrationServiceDismissPullRequestProblemProcedure, func(_ context.Context, r *connect.Request[pb.DismissPullRequestProblemRequest]) (*connect.Response[pb.DismissPullRequestProblemResponse], error) {
				calls++
				if generated {
					requestID = domain.ID(r.Msg.Mutation.GetRequestId())
					value.Dismissal.RequestID = requestID
					problem.DocumentJson, _ = json.Marshal(value)
				}
				if r.Msg.Mutation == nil || r.Msg.Mutation.Id != string(id) || r.Msg.Mutation.ExpectedRevision != 9007199254740993 || r.Msg.Mutation.RequestId != string(requestID) || r.Msg.ContentVersion != entry.ContentVersion {
					t.Error("dismissal identity changed")
				}
				return connect.NewResponse(&pb.DismissPullRequestProblemResponse{Problem: problem, RequestId: string(requestID)}), nil
			}))
			peer := httptest.NewServer(mux)
			defer peer.Close()
			args := []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "--request-id", string(requestID), "github", "pr", "problems"}
			switch mode {
			case "refresh", "refresh-ci", "refresh-conflict":
				args = append(args, "refresh", "--repository-id", string(repository), "--number", "17")
				if mode == "refresh-ci" {
					args = append(args, "--kind", "ci")
				} else if mode == "refresh-conflict" {
					args = append(args, "--kind", "conflict")
				}
			case "dismiss":
				args = append(args, "dismiss", "--id", string(id), "--revision", "9007199254740993", "--content-version", entry.ContentVersion)
			default:
				args = append(args, "list", "--remote-repository-id", "37", "--pull-request-id", "9007199254740993")
			}
			if generated {
				args = append(args[:5], args[7:]...)
			}
			var output, diagnostic strings.Builder
			code := Run(context.Background(), args, IO{In: strings.NewReader("private-problem-fixture-token"), Out: &output, Err: &diagnostic})
			if (code == 0) != (mode != "foreign-history") || calls != 1 {
				t.Fatalf("CLI result=%d calls=%d output=%s diagnostics=%s", code, calls, output.String(), diagnostic.String())
			}
			if generated {
				var envelope map[string]any
				if json.Unmarshal([]byte(output.String()), &envelope) != nil || domain.ID(requestID).Validate() != nil || envelope["request_id"] != string(requestID) {
					t.Fatal("generated mutation identity was not retained", output.String())
				}
			}
			if strings.Contains(output.String(), "private-problem-fixture-token") {
				t.Fatal("credential exposed")
			}
		})
	}
}
