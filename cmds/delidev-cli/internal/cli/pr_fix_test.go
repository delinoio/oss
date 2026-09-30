// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIPRFixKeepsOriginalSelectionAndRejectsForeignAcknowledgment(t *testing.T) {
	for _, scenario := range []string{"valid", "foreign-audit", "foreign-chain", "foreign-repository", "shared-stdin", "capabilities"} {
		t.Run(scenario, func(t *testing.T) {
			at := time.Now().UTC()
			setID, repo, project, problem, requestID, attemptID, chainID, sessionID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			input := domain.PRFixRequest{SetID: setID, SetRevision: 9007199254740993, RepositoryID: repo, ProjectID: project, Problems: []domain.PRFixProblem{{ID: problem, Revision: 9007199254740995, ContentVersion: strings.Repeat("c", 64)}}}
			raw, _ := json.Marshal(input)
			path := filepath.Join(t.TempDir(), "selection.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			target := domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: repo, RemoteRepositoryID: "37", RepositoryNodeID: "R_37", Owner: "fixture-owner", Name: "repo", PullRequestID: "53", PullRequestNodeID: "PR_53", Number: "17", Title: "Original", ObservedAt: at}
			git := domain.PRGitTarget{Version: 1, Target: target, HeadRepository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "79", NodeID: "R_fork", Owner: "fixture-author", Name: "fork", Private: true}, BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "feature", HeadSHA: strings.Repeat("b", 40)}
			attempt := domain.PRRemediationAttempt{Version: 1, Type: domain.PRRemediationAttemptRecord, SetID: setID, ChainID: chainID, Sequence: 1, Mode: domain.PRRemediationManual, State: domain.PRRemediationBound, Policy: domain.DefaultRemediationPolicy(), Problems: []domain.PRRemediationProblemRef{{ID: problem, ContentVersion: input.Problems[0].ContentVersion}}, Reserved: domain.PRProblemDismissal{RequestID: requestID, ActorType: domain.OwnerDevice, At: at}, SessionID: sessionID, InputID: domain.NewID(), InputDigest: strings.Repeat("d", 64), GitTarget: &git, ProjectID: project}
			set := domain.PRProblemSet{Version: 1, Type: domain.PRProblemSetRecord, Target: target, Feedback: &domain.PRProblemObservation{BaseSHA: git.BaseSHA, HeadSHA: git.HeadSHA, ObservedAt: at}, Remediation: &domain.PRRemediationChain{ID: chainID, Sequence: 1, ActiveAttemptID: attemptID}}
			switch scenario {
			case "foreign-audit":
				attempt.Reserved.RequestID = domain.NewID()
			case "foreign-chain":
				set.Remediation.ID = domain.NewID()
			case "foreign-repository":
				attempt.GitTarget.Target.RepositoryID = domain.NewID()
			}
			resource := func(id domain.ID, value any) *pb.Resource {
				raw, _ := json.Marshal(value)
				return &pb.Resource{Id: string(id), Kind: pb.EntityKind_ENTITY_KIND_PROBLEM, SchemaVersion: 1, Revision: 3, DocumentJson: raw}
			}
			sessionRaw, _ := json.Marshal(domain.Session{ProjectID: project})
			calls := 0
			mux := http.NewServeMux()
			mux.Handle(delidevv1connect.PullRequestFixServiceRequestPullRequestFixProcedure, connect.NewUnaryHandler(delidevv1connect.PullRequestFixServiceRequestPullRequestFixProcedure, func(_ context.Context, req *connect.Request[pb.RequestPullRequestFixRequest]) (*connect.Response[pb.RequestPullRequestFixResponse], error) {
				calls++
				var got domain.PRFixRequest
				if req.Msg.RequestId != string(requestID) || req.Msg.SchemaVersion != 1 || domain.Decode(req.Msg.DocumentJson, &got) != nil || got.SetRevision != input.SetRevision || got.Problems[0] != input.Problems[0] {
					t.Error("CLI changed original selection")
				}
				return connect.NewResponse(&pb.RequestPullRequestFixResponse{Attempt: resource(attemptID, attempt), ProblemSet: resource(setID, set), Session: &pb.Resource{Id: string(sessionID), SessionId: string(sessionID), ProjectId: string(project), Kind: pb.EntityKind_ENTITY_KIND_SESSION, SchemaVersion: 1, Revision: 1, DocumentJson: sessionRaw}, RequestId: string(requestID), Replayed: true}), nil
			}))
			mux.Handle(delidevv1connect.PullRequestFixServiceGetPullRequestFixCapabilitiesProcedure, connect.NewUnaryHandler(delidevv1connect.PullRequestFixServiceGetPullRequestFixCapabilitiesProcedure, func(context.Context, *connect.Request[pb.GetPullRequestFixCapabilitiesRequest]) (*connect.Response[pb.GetPullRequestFixCapabilitiesResponse], error) {
				calls++
				return connect.NewResponse(&pb.GetPullRequestFixCapabilitiesResponse{Profiles: []pb.PullRequestFixProfile{pb.PullRequestFixProfile_PULL_REQUEST_FIX_PROFILE_CODEX_GIT_V1}}), nil
			}))
			peer := httptest.NewServer(mux)
			defer peer.Close()
			args := []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "--request-id", string(requestID), "github", "pr", "remediation"}
			if scenario == "capabilities" {
				args = append(args, "capabilities")
			} else {
				args = append(args, "fix", "--input", path)
				if scenario == "shared-stdin" {
					args[len(args)-1] = "-"
				}
			}
			var output, diagnostic strings.Builder
			code := Run(context.Background(), args, IO{In: strings.NewReader("private-server-fixture-token"), Out: &output, Err: &diagnostic})
			if (code == 0) != (scenario == "valid" || scenario == "capabilities") || calls != map[bool]int{true: 0, false: 1}[scenario == "shared-stdin"] {
				t.Fatalf("invalid CLI acknowledgment result %s code=%d calls=%d diagnostic=%s", scenario, code, calls, diagnostic.String())
			}
			if strings.Contains(output.String(), "private-server-fixture-token") {
				t.Fatal("credential entered CLI output")
			}
		})
	}
}
