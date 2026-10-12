// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type nativeShellCLIService struct {
	delidevv1connect.UnimplementedSessionServiceHandler
	t                        *testing.T
	session, action, request domain.ID
	command                  string
	run, cancel, status      int
}

func (s *nativeShellCLIService) RunNativeShell(_ context.Context, r *connect.Request[pb.RunNativeShellRequest]) (*connect.Response[pb.RunNativeShellResponse], error) {
	if r.Header().Get("Authorization") != "Bearer private-cli-fixture" || r.Msg.Mutation.Id != string(s.session) || r.Msg.Mutation.RequestId != string(s.request) || r.Msg.Mutation.ExpectedRevision != 7 || r.Msg.Access != pb.NativeShellAccess_NATIVE_SHELL_ACCESS_FULL_ACCESS || r.Msg.Command != s.command {
		s.t.Error("native action lost exact authenticated request")
	}
	s.run++
	return connect.NewResponse(&pb.RunNativeShellResponse{Action: &pb.Resource{Id: string(s.action), Revision: 1, DocumentJson: []byte(`{"state":"queued"}`)}, RequestId: string(s.request)}), nil
}
func (s *nativeShellCLIService) GetNativeShell(_ context.Context, r *connect.Request[pb.GetNativeShellRequest]) (*connect.Response[pb.GetNativeShellResponse], error) {
	if r.Msg.SessionId != string(s.session) || r.Msg.ActionId != string(s.action) {
		s.t.Error("status replaced original action")
	}
	s.status++
	return connect.NewResponse(&pb.GetNativeShellResponse{Action: &pb.Resource{Id: string(s.action), Revision: 2, DocumentJson: []byte(`{"state":"uncertain"}`)}}), nil
}
func (s *nativeShellCLIService) CancelNativeShell(_ context.Context, r *connect.Request[pb.CancelNativeShellRequest]) (*connect.Response[pb.CancelNativeShellResponse], error) {
	if r.Msg.SessionId != string(s.session) || r.Msg.Mutation.Id != string(s.action) || r.Msg.Mutation.ExpectedRevision != 2 {
		s.t.Error("cancellation replaced original owner")
	}
	s.cancel++
	return connect.NewResponse(&pb.CancelNativeShellResponse{Action: &pb.Resource{Id: string(s.action), Revision: 3, DocumentJson: []byte(`{"state":"running"}`)}, RequestId: string(s.request)}), nil
}
func TestNativeShellCLIDisclosureConfirmationAndReadOnlyStatus(t *testing.T) {
	s := &nativeShellCLIService{t: t, session: domain.NewID(), action: domain.NewID(), request: domain.NewID(), command: "  printf 'exact script'  "}
	_, handler := delidevv1connect.NewSessionServiceHandler(s)
	server := httptest.NewServer(handler)
	defer server.Close()
	c := client{token: "private-cli-fixture", sessions: delidevv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)}
	o := options{requestID: s.request}
	ctx := context.Background()
	output := &bytes.Buffer{}
	args := []string{"run", "--id", string(s.session), "--revision", "7", "--command", s.command}
	if _, err := sessionNativeShellCommand(ctx, c, o, args, IO{Err: output}); err == nil || s.run != 0 || !strings.Contains(output.String(), domain.NativeShellDisclosure) || !strings.Contains(output.String(), s.command) {
		t.Fatal("implicit action skipped full-access disclosure", err)
	}
	if _, err := sessionNativeShellCommand(ctx, c, o, append(args, "--confirm-full-access"), IO{}); err == nil || s.run != 0 {
		t.Fatal("unshown disclosure authorized native send")
	}
	if _, err := sessionNativeShellCommand(ctx, c, o, append(args, "--confirm-full-access"), IO{Err: output}); err != nil || s.run != 1 {
		t.Fatal("confirmed native action lost Connect parity", err)
	}
	if _, err := sessionNativeShellCommand(ctx, c, o, []string{"status", "--id", string(s.session), "--action", string(s.action)}, IO{}); err != nil || s.run != 1 || s.status != 1 {
		t.Fatal("status resent native execution", err)
	}
	if _, err := sessionNativeShellCommand(ctx, c, o, []string{"cancel", "--id", string(s.session), "--action", string(s.action), "--revision", "2"}, IO{}); err != nil || s.cancel != 1 || s.run != 1 {
		t.Fatal("cancel adopted another execution", err)
	}
}
