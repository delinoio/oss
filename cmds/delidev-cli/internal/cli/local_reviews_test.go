package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestCLIReviewWaitsForBoundedServerAcceptance(t *testing.T) {
	// A real delayed HTTP response exercises the CLI's header deadline as well
	// as its overall command context. Ordinary immediate mocks missed this gap.
	requestID := domain.NewID()
	server := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.SessionServiceSubmitLocalReviewProcedure, func(ctx context.Context, request *connect.Request[pb.SubmitLocalReviewRequest]) (*connect.Response[pb.SubmitLocalReviewResponse], error) {
		if request.Msg.RequestId != string(requestID) {
			t.Error("lost original request identity")
		}
		select {
		case <-time.After(16 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return connect.NewResponse(&pb.SubmitLocalReviewResponse{RequestId: string(requestID), Submission: &pb.Resource{Id: string(requestID)}, Change: &pb.SessionChange{Input: &pb.Resource{Id: string(domain.NewID())}}}), nil
	}))
	defer server.Close()
	dir := t.TempDir()
	input := filepath.Join(dir, "submission.json")
	raw, _ := json.Marshal(domain.SubmitReviewComments{Mode: domain.PlanMode, Comments: []domain.ReviewCommentRef{{ID: domain.NewID(), Revision: 1}}})
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostic strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	code := Run(ctx, []string{"--data-dir", filepath.Join(dir, "client"), "--server", server.URL, "--token-stdin", "--request-id", string(requestID), "session", "review", "submit", "--id", string(domain.NewID()), "--input", input}, IO{In: strings.NewReader("isolated-fixture-token"), Out: &output, Err: &diagnostic})
	if code != 0 {
		t.Fatalf("bounded review response was abandoned: %d %s", code, output.String())
	}
}

func TestCLIReviewMissingAcknowledgmentRetainsRecovery(t *testing.T) {
	server := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.SessionServiceSubmitLocalReviewProcedure, func(ctx context.Context, r *connect.Request[pb.SubmitLocalReviewRequest]) (*connect.Response[pb.SubmitLocalReviewResponse], error) {
		return connect.NewResponse(&pb.SubmitLocalReviewResponse{RequestId: r.Msg.RequestId}), nil
	}))
	defer server.Close()
	c, err := connectClient(options{server: server.URL, tokenStdin: true, dataDir: filepath.Join(t.TempDir(), "client")}, strings.NewReader("isolated-fixture-token"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.transport.CloseIdleConnections()
	_, err = sessionReview(context.Background(), c, options{requestID: domain.NewID()}, []string{"submit", "--id", string(domain.NewID())}, IO{In: strings.NewReader("{}")})
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("missing acknowledgment was accepted or retried", err)
	}
}
