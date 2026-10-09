// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
	"sync/atomic"
	"testing"
	"time"
)

type blockedProgressClient struct {
	delidevv1connect.WorkerServiceClient
	called chan struct{}
	calls  atomic.Int32
	first  *pb.ReportSessionStartupProgressRequest
	retry  bool
}

func (c *blockedProgressClient) ReportSessionStartupProgress(ctx context.Context, r *connect.Request[pb.ReportSessionStartupProgressRequest]) (*connect.Response[pb.ReportSessionStartupProgressResponse], error) {
	n := c.calls.Add(1)
	if n == 1 {
		c.first = proto.Clone(r.Msg).(*pb.ReportSessionStartupProgressRequest)
		close(c.called)
	}
	if c.retry {
		if n == 1 {
			return nil, errors.New("fixture lost acknowledgment")
		}
		if !proto.Equal(c.first, r.Msg) {
			return nil, errors.New("changed retry")
		}
		return connect.NewResponse(&pb.ReportSessionStartupProgressResponse{Replayed: true}), nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestSessionStartupProgressNeverBackpressuresAndJoins(t *testing.T) {
	c := &blockedProgressClient{called: make(chan struct{})}
	resource := &pb.Resource{Id: string(domain.NewID()), SessionId: string(domain.NewID()), Revision: 1}
	cfg := Config{startupProgress: true, execution: &PublicationConfig{Assignment: resource, Instance: domain.NewID(), Client: c}}
	r := newSessionStartupReporter(context.Background(), cfg, domain.Job{Type: domain.PrepareWorkspaceJob})
	defer r.close()
	r.observe(domain.StartupProgressStep{WorkspaceOperation: domain.StartupWorkspaceSetup, State: domain.StartupProgressRunning})
	select {
	case <-c.called:
	case <-time.After(time.Second):
		t.Fatal("report not started")
	}
	start := time.Now()
	for range 2000 {
		r.observe(domain.StartupProgressStep{WorkspaceOperation: domain.StartupWorkspaceVerify, State: domain.StartupProgressRunning})
	}
	if time.Since(start) > time.Second || r.sequence != 806 {
		t.Fatal("telemetry blocked or exceeded bound")
	}
	r.close()
	select {
	case <-r.done:
	default:
		t.Fatal("report owner not joined")
	}
	cfg.startupProgress = false
	if newSessionStartupReporter(context.Background(), cfg, domain.Job{Type: domain.PrepareWorkspaceJob}) != nil {
		t.Fatal("old peer gained telemetry")
	}
}
func TestSessionStartupProgressLostAcknowledgmentRetainsExactRequest(t *testing.T) {
	c := &blockedProgressClient{called: make(chan struct{}), retry: true}
	r := newSessionStartupReporter(context.Background(), Config{startupProgress: true, execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(domain.NewID()), SessionId: string(domain.NewID()), Revision: 7}, Instance: domain.NewID(), Client: c}}, domain.Job{Type: domain.PrepareWorkspaceJob})
	defer r.close()
	r.observe(domain.StartupProgressStep{WorkspaceOperation: domain.StartupWorkspaceSetup, State: domain.StartupProgressRunning})
	deadline := time.After(time.Second)
	for c.calls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("receipt retry absent")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if c.first.Mutation.ExpectedRevision != 7 || c.first.Sequence != 1 {
		t.Fatal("original assignment changed")
	}
}
