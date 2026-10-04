// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestSidechatWaitKeepsOriginalObservationPastOrdinaryCommandDeadline(t *testing.T) {
	id := domain.NewID()
	child := domain.NewID()
	started := time.Now()
	job := func() *pb.Resource {
		state := domain.JobQueued
		if time.Since(started) >= 31*time.Second {
			state = domain.JobSucceeded
		}
		raw, _ := json.Marshal(domain.Job{State: state})
		return &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_JOB, Id: string(id), SchemaVersion: 1, Revision: 1, DocumentJson: raw}
	}
	var forks atomic.Int32
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
		return connect.NewResponse(&pb.GetStatusResponse{Capabilities: []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_SIDECHAT_V1}}), nil
	}))
	mux.Handle(delidevv1connect.SessionServiceGetSessionForkProcedure, connect.NewUnaryHandler(delidevv1connect.SessionServiceGetSessionForkProcedure, func(context.Context, *connect.Request[pb.GetSessionForkRequest]) (*connect.Response[pb.GetSessionForkResponse], error) {
		forks.Add(1)
		return connect.NewResponse(&pb.GetSessionForkResponse{Job: job(), Session: &pb.Resource{Id: string(child), Kind: pb.EntityKind_ENTITY_KIND_SESSION, DocumentJson: []byte(`{}`)}}), nil
	}))
	mux.Handle(delidevv1connect.ResourceServiceGetResourceProcedure, connect.NewUnaryHandler(delidevv1connect.ResourceServiceGetResourceProcedure, func(context.Context, *connect.Request[pb.GetResourceRequest]) (*connect.Response[pb.GetResourceResponse], error) {
		return connect.NewResponse(&pb.GetResourceResponse{Resource: job()}), nil
	}))
	server := httptest.NewServer(mux)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "private-state")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	var out, errors bytes.Buffer
	code := Run(context.Background(), []string{"--data-dir", root, "--server", server.URL, "--token-stdin", "session", "sidechat", "--job-id", string(id), "--wait"}, IO{In: bytes.NewBufferString("deadline-fixture-token"), Out: &out, Err: &errors})
	if code != 0 || forks.Load() != 2 {
		t.Fatal("ordinary deadline abandoned retained Sidechat wait", code, out.String(), errors.String(), forks.Load())
	}
	var result struct {
		Result struct {
			Job struct {
				ID string `json:"id"`
			} `json:"job"`
		} `json:"result"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Result.Job.ID != string(id) {
		t.Fatal("wait lost original accepted identity")
	}
}
