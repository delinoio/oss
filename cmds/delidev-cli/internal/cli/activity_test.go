package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestCLIActivityPRMetadataUsesOriginalTypedRPCWithoutMutation(t *testing.T) {
	id, source, set, problem := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	mux := http.NewServeMux()
	calls := 0
	mux.Handle(delidevv1connect.ActivityServiceListActivityProcedure, connect.NewUnaryHandler(delidevv1connect.ActivityServiceListActivityProcedure, func(_ context.Context, r *connect.Request[pb.ListActivityRequest]) (*connect.Response[pb.ListActivityResponse], error) {
		calls++
		if r.Msg.PageSize != 1 || r.Msg.PageToken != "original-page" {
			t.Error("CLI changed activity pagination")
		}
		return connect.NewResponse(&pb.ListActivityResponse{Entries: []*pb.ActivityEntry{{Id: string(id), SourceKind: pb.EntityKind_ENTITY_KIND_PROBLEM, SourceRevision: 9007199254740993, ObservedAtUnixMs: 1000, Kind: pb.ActivityKind_ACTIVITY_KIND_PR_REMEDIATION_ATTEMPT, PullRequest: &pb.ActivityPRMetadata{SourceId: string(source), ProblemSetId: string(set), RemoteRepositoryId: "37", PullRequestId: "9007199254740993", Number: "17", Owner: "fixture-owner", Name: "repo", ActorType: pb.ActivityPRActorType_ACTIVITY_PR_ACTOR_TYPE_OWNER, RequestId: string(domain.NewID()), AttemptState: pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_FAILED, Mode: pb.ActivityPRMode_ACTIVITY_PR_MODE_AUTOMATIC, Problems: []*pb.ActivityPRProblemReference{{Id: string(problem), ContentVersion: strings.Repeat("a", 64)}}}}}, Capabilities: []pb.ActivityCapability{pb.ActivityCapability_ACTIVITY_CAPABILITY_PR_HANDLING_V1}}), nil
	}))
	peer := httptest.NewServer(mux)
	defer peer.Close()
	var output, diagnostic strings.Builder
	args := []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "activity", "list", "--limit", "1", "--page-token", "original-page"}
	code := Run(context.Background(), args, IO{In: strings.NewReader("activity-secret-sentinel"), Out: &output, Err: &diagnostic})
	if code != 0 || calls != 1 {
		t.Fatal("CLI activity read", code, calls, diagnostic.String())
	}
	for _, want := range []string{"pr-remediation-attempt", "ACTIVITY_PR_ATTEMPT_STATE_FAILED", "ACTIVITY_PR_MODE_AUTOMATIC", "ACTIVITY_CAPABILITY_PR_HANDLING_V1", `"pull_request_id":"9007199254740993"`, `"source_revision":9007199254740993`, string(source), string(problem)} {
		if !strings.Contains(output.String(), want) {
			t.Fatal("CLI lost original typed metadata", want, output.String())
		}
	}
	if strings.Contains(output.String()+diagnostic.String(), "activity-secret-sentinel") {
		t.Fatal("CLI exposed credential")
	}
}
