// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestCLIUsesDedicatedReviewRPCWithOriginalTarget(t *testing.T) {
	session, job, repository := domain.NewID(), domain.NewID(), domain.NewID()
	target := domain.NativeCodeReviewTarget{Kind: domain.ReviewCommit, RepositoryID: repository, DiffRevision: strings.Repeat("a", 64), Reference: strings.Repeat("b", 40)}
	calls := 0
	server := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.SessionServiceCreateNativeCodeReviewProcedure, func(ctx context.Context, req *connect.Request[pb.CreateNativeCodeReviewRequest]) (*connect.Response[pb.CreateNativeCodeReviewResponse], error) {
		calls++
		if req.Msg.Mutation == nil || req.Msg.Mutation.Id != string(session) || req.Msg.Mutation.ExpectedRevision != 7 || req.Msg.Target == nil || req.Msg.Target.Reference != target.Reference || req.Msg.Target.DiffRevision != target.DiffRevision || req.Msg.Target.RepositoryId != string(repository) {
			t.Fatal("CLI changed original target or session authority")
		}
		return connect.NewResponse(&pb.CreateNativeCodeReviewResponse{Review: &pb.NativeCodeReview{Job: &pb.Resource{Id: string(job)}, Target: req.Msg.Target, State: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_QUEUED}}), nil
	}))
	defer server.Close()
	c, err := connectClient(options{server: server.URL, tokenStdin: true, dataDir: filepath.Join(t.TempDir(), "client")}, strings.NewReader("isolated-fixture-token"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.transport.CloseIdleConnections()
	raw, _ := json.Marshal(target)
	result, err := sessionNativeReview(context.Background(), c, options{requestID: domain.NewID()}, []string{"create", "--id", string(session), "--revision", "7"}, IO{In: strings.NewReader(string(raw))})
	if err != nil || result == nil || calls != 1 {
		t.Fatal("dedicated review dispatch", err)
	}
	target.Instructions = "unselected foreign instructions"
	raw, _ = json.Marshal(target)
	if _, err = sessionNativeReview(context.Background(), c, options{requestID: domain.NewID()}, []string{"create", "--id", string(session), "--revision", "7"}, IO{In: strings.NewReader(string(raw))}); err == nil || calls != 1 {
		t.Fatal("invalid target reached native RPC")
	}
}
