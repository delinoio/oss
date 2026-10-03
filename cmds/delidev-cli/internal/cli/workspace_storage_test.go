// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestStorageCLIUsesExactConnectMutationAndConfirmation(t *testing.T) {
	var observed []*pb.RequestWorkspaceStorageRequest
	handler := connect.NewUnaryHandler(delidevv1connect.WorkspaceStorageServiceRequestWorkspaceStorageProcedure, func(ctx context.Context, req *connect.Request[pb.RequestWorkspaceStorageRequest]) (*connect.Response[pb.RequestWorkspaceStorageResponse], error) {
		observed = append(observed, req.Msg)
		return connect.NewResponse(&pb.RequestWorkspaceStorageResponse{Job: &pb.Resource{Id: string(domain.NewID()), Revision: 9007199254740993}, RequestId: req.Msg.Mutation.RequestId}), nil
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	c := client{storage: delidevv1connect.NewWorkspaceStorageServiceClient(http.DefaultClient, server.URL), token: "fixture-only"}
	session, preview, snapshot, recovery := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	o := options{requestID: domain.NewID()}
	for _, action := range []string{"preview", "create", "cleanup", "inspect", "restore", "delete", "recover"} {
		args := []string{action, "--session-id", string(session), "--expected-revision", "9007199254740993", "--snapshot-id", string(snapshot), "--preview-job-id", string(preview), "--recovery-job-id", string(recovery)}
		if action == "cleanup" || action == "delete" {
			before := len(observed)
			if _, err := workspaceStorageCommand(context.Background(), c, o, args); domain.SafeError(err).Code != domain.MissingInput || len(observed) != before {
				t.Fatal("unconfirmed removal called RPC", err)
			}
			args = append(args, "--confirm")
		}
		result, err := workspaceStorageCommand(context.Background(), c, o, args)
		if err != nil {
			t.Fatal(action, err)
		}
		got := observed[len(observed)-1]
		if got.Mutation.RequestId != string(o.requestID) || got.Mutation.Id != string(session) || got.Mutation.ExpectedRevision != 9007199254740993 || got.SnapshotId != string(snapshot) || got.PreviewJobId != string(preview) || got.RecoveryJobId != string(recovery) {
			t.Fatal("CLI changed selected immutable request", got)
		}
		encoded, ok := result.(json.RawMessage)
		if !ok || !strings.Contains(string(encoded), `"revision":"9007199254740993"`) {
			t.Fatal("CLI lost exact decimal revision", result)
		}
	}
	if len(observed) != 7 {
		t.Fatal("CLI duplicated acceptance", len(observed))
	}
}
