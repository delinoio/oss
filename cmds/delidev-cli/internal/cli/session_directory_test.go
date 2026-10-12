// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http/httptest"
	"strings"
	"testing"
)

type directoryCLIFixture struct {
	delidevv1connect.UnimplementedSessionDirectoryServiceHandler
	t                *testing.T
	session, request domain.ID
	changes, reads   int
	lost             bool
}

func (f *directoryCLIFixture) operation() *pb.SessionDirectoryOperation {
	return &pb.SessionDirectoryOperation{SessionId: string(f.session), RequestId: string(f.request), Job: &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(f.session), SchemaVersion: 1, Revision: 1, DocumentJson: []byte(`{"type":"change-session-directory","state":"queued"}`)}}
}
func (f *directoryCLIFixture) ChangeSessionDirectory(_ context.Context, r *connect.Request[pb.ChangeSessionDirectoryRequest]) (*connect.Response[pb.ChangeSessionDirectoryResponse], error) {
	f.changes++
	if r.Header().Get("Authorization") != "Bearer fixture" || r.Msg.Mutation.Id != string(f.session) || r.Msg.Mutation.RequestId != string(f.request) || r.Msg.Mutation.ExpectedRevision != 7 || r.Msg.RelativePath != "src" {
		f.t.Error("changed original request authority")
	}
	if f.lost {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.ChangeSessionDirectoryResponse{Operation: f.operation()}), nil
}
func (f *directoryCLIFixture) GetSessionDirectoryOperation(_ context.Context, r *connect.Request[pb.GetSessionDirectoryOperationRequest]) (*connect.Response[pb.GetSessionDirectoryOperationResponse], error) {
	f.reads++
	if r.Header().Get("Authorization") != "Bearer fixture" || r.Msg.SessionId != string(f.session) || r.Msg.RequestId != string(f.request) {
		f.t.Error("lost original receipt authority")
	}
	return connect.NewResponse(&pb.GetSessionDirectoryOperationResponse{Operation: f.operation()}), nil
}
func TestSessionDirectoryCLIOriginalReceipt(t *testing.T) {
	f := &directoryCLIFixture{t: t, session: domain.NewID(), request: domain.NewID()}
	_, h := delidevv1connect.NewSessionDirectoryServiceHandler(f)
	s := httptest.NewServer(h)
	defer s.Close()
	c := client{token: "fixture", directories: delidevv1connect.NewSessionDirectoryServiceClient(s.Client(), s.URL)}
	o := options{requestID: f.request, requestIDExplicit: true}
	for _, args := range [][]string{{"change", "--id", string(f.session), "--path", "src"}, {"change", "--id", string(f.session), "--revision", "7", "--path", "../src"}} {
		if _, e := sessionDirectoryCommand(context.Background(), c, o, args); e == nil {
			t.Fatal("invalid change admitted")
		}
	}
	if _, e := sessionDirectoryCommand(context.Background(), c, options{requestID: f.request}, []string{"operation", "--id", string(f.session)}); e == nil {
		t.Fatal("minted request accepted")
	}
	if f.changes+f.reads != 0 {
		t.Fatal("invalid request reached server")
	}
	f.lost = true
	if _, e := sessionDirectoryCommand(context.Background(), c, o, []string{"change", "--id", string(f.session), "--revision", "7", "--path", "src"}); e == nil {
		t.Fatal("lost response passed")
	}
	v, e := sessionDirectoryCommand(context.Background(), c, o, []string{"operation", "--id", string(f.session)})
	if e != nil || f.changes != 1 || f.reads != 1 {
		t.Fatal("observation replayed mutation", e)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), string(f.session)) || strings.Contains(string(raw), string(f.request)) {
		t.Fatal("private identities displayed")
	}
	op := f.operation()
	op.RequestId = string(domain.NewID())
	if _, e := directoryOperationView(op, string(f.session), string(f.request), "", "", nil); e == nil {
		t.Fatal("wrong receipt accepted")
	}
}
func TestSessionDirectoryCLIVerifiedGeneration(t *testing.T) {
	session, requestID, repo := string(domain.NewID()), string(domain.NewID()), domain.NewID()
	job := string(domain.NewID())
	op := &pb.SessionDirectoryOperation{SessionId: session, RequestId: requestID, Job: &pb.Resource{Id: job, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: session, Revision: 1, SchemaVersion: 1, DocumentJson: []byte(`{"type":"change-session-directory","state":"succeeded"}`)}, Generation: &pb.SessionDirectoryGeneration{GenerationId: string(domain.NewID()), JobId: job, RequestId: requestID, SourceExecutionId: string(domain.NewID()), RepositoryId: string(repo), RelativePath: "src"}}
	roots := []domain.WorkspaceRoot{{RepositoryID: repo, Name: "Example"}}
	v, e := directoryOperationView(op, session, requestID, string(repo), "src", roots)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(v)
	if !strings.Contains(string(raw), "Example") || strings.Contains(string(raw), job) || strings.Contains(string(raw), session) {
		t.Fatal("unsafe result")
	}
	op.Generation.JobId = string(domain.NewID())
	if _, e := directoryOperationView(op, session, requestID, string(repo), "src", roots); e == nil {
		t.Fatal("wrong job generation accepted")
	}
}
