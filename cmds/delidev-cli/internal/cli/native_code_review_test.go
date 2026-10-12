// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
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

func TestRunAdmitsAndDispatchesNativeReviewCreateAndGet(t *testing.T) {
	session, job, repository := domain.NewID(), domain.NewID(), domain.NewID()
	target := domain.NativeCodeReviewTarget{Kind: domain.ReviewCommit, RepositoryID: repository, DiffRevision: strings.Repeat("a", 64), Reference: strings.Repeat("b", 40)}
	var createCalls, getCalls atomic.Int32
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.SessionServiceCreateNativeCodeReviewProcedure, connect.NewUnaryHandler(delidevv1connect.SessionServiceCreateNativeCodeReviewProcedure, func(_ context.Context, req *connect.Request[pb.CreateNativeCodeReviewRequest]) (*connect.Response[pb.CreateNativeCodeReviewResponse], error) {
		createCalls.Add(1)
		if req.Msg.Mutation == nil || req.Msg.Mutation.Id != string(session) || req.Msg.Mutation.ExpectedRevision != 7 || req.Msg.Target == nil || req.Msg.Target.Reference != target.Reference {
			t.Errorf("Run changed native-review create authority")
		}
		return connect.NewResponse(&pb.CreateNativeCodeReviewResponse{Review: &pb.NativeCodeReview{Job: &pb.Resource{Id: string(job)}, Target: req.Msg.Target, State: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_QUEUED}}), nil
	}))
	mux.Handle(delidevv1connect.SessionServiceGetNativeCodeReviewProcedure, connect.NewUnaryHandler(delidevv1connect.SessionServiceGetNativeCodeReviewProcedure, func(_ context.Context, req *connect.Request[pb.GetNativeCodeReviewRequest]) (*connect.Response[pb.GetNativeCodeReviewResponse], error) {
		getCalls.Add(1)
		if req.Msg.SessionId != string(session) || req.Msg.JobId != string(job) {
			t.Errorf("Run changed native-review get identity")
		}
		return connect.NewResponse(&pb.GetNativeCodeReviewResponse{Review: &pb.NativeCodeReview{Job: &pb.Resource{Id: string(job)}, Target: rpc.NativeReviewTargetWire(target), State: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_QUEUED}}), nil
	}))
	server := httptest.NewServer(mux)
	defer server.Close()

	targetJSON, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(targetPath, targetJSON, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, map[string]any) {
		t.Helper()
		var out, diagnostic bytes.Buffer
		code := Run(context.Background(), append([]string{"--server", server.URL, "--token-stdin", "--data-dir", filepath.Join(t.TempDir(), "client")}, args...), IO{In: strings.NewReader("fixture-token"), Out: &out, Err: &diagnostic})
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("Run did not return JSON: %q (%s): %v", out.String(), diagnostic.String(), err)
		}
		return code, result
	}

	code, result := run("session", "native-review", "create", "--id", string(session), "--revision", "7", "--input", targetPath)
	if code != 0 || createCalls.Load() != 1 || result["error"] != nil {
		t.Fatalf("Run did not admit and dispatch create: exit=%d calls=%d result=%+v", code, createCalls.Load(), result)
	}
	code, result = run("session", "native-review", "get", "--id", string(session), "--job-id", string(job))
	if code != 0 || getCalls.Load() != 1 || result["error"] != nil {
		t.Fatalf("Run did not admit and dispatch get: exit=%d calls=%d result=%+v", code, getCalls.Load(), result)
	}
}
