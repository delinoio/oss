package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type serviceFixture struct {
	mu      sync.Mutex
	present bool
	writes  int
}

func (f *serviceFixture) Inspect(context.Context, userservice.Spec) (userservice.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return userservice.Observation{Present: f.present}, nil
}
func (f *serviceFixture) Install(context.Context, userservice.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.present = true
	f.writes++
	return nil
}
func (f *serviceFixture) Enable(context.Context, userservice.Spec) error  { return nil }
func (f *serviceFixture) Start(context.Context, userservice.Spec) error   { return nil }
func (f *serviceFixture) Disable(context.Context, userservice.Spec) error { return nil }
func (f *serviceFixture) Remove(context.Context, userservice.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.present = false
	f.writes++
	return nil
}
func TestUserServiceConnectAuthorizationRevisionsReplayAndRedaction(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	backend := &serviceFixture{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan Endpoint, 1)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{disableKnownModelMaintenance: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), userServiceBackend: backend}, func(e Endpoint) { ready <- e })
	}()
	var endpoint Endpoint
	select {
	case endpoint = <-ready:
	case e := <-done:
		t.Fatal(e)
	case <-time.After(10 * time.Second):
		t.Fatal("readiness timeout")
	}
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	identity, e := security.LoadIdentity(root)
	if e != nil {
		t.Fatal(e)
	}
	c := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)
	if _, e = c.GetUserService(ctx, connect.NewRequest(&pb.GetUserServiceRequest{Kind: pb.UserServiceKind_USER_SERVICE_KIND_SERVER})); connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("anonymous service read accepted", e)
	}
	initial, e := c.GetUserService(ctx, ownerRequest(identity, &pb.GetUserServiceRequest{Kind: pb.UserServiceKind_USER_SERVICE_KIND_SERVER}))
	if e != nil || initial.Msg.Service.State != pb.UserServiceState_USER_SERVICE_STATE_ABSENT {
		t.Fatal(initial, e)
	}
	mutation := &pb.ControlUserServiceRequest{Kind: pb.UserServiceKind_USER_SERVICE_KIND_SERVER, Action: pb.UserServiceAction_USER_SERVICE_ACTION_INSTALL, RequestId: string(domain.NewID())}
	accepted, e := c.ControlUserService(ctx, ownerRequest(identity, mutation))
	if e != nil {
		t.Fatal(e)
	}
	if accepted.Msg.Service.Revision != 1 || accepted.Msg.Service.State != pb.UserServiceState_USER_SERVICE_STATE_STOPPED || accepted.Msg.Service.LoginEnabled {
		t.Fatal(accepted)
	}
	replay, e := c.ControlUserService(ctx, ownerRequest(identity, mutation))
	if e != nil || !replay.Msg.Replayed || backend.writes != 1 {
		t.Fatal("native install replayed", e)
	}
	raw, _ := json.Marshal(accepted.Msg)
	for _, value := range []string{root, identity.Token, "binary_identity", "birth", "root_identity"} {
		if strings.Contains(string(raw), value) {
			t.Fatal("private service ownership leaked")
		}
	}
	if accepted.Header().Get("x-delidev-correlation-id") == "" {
		t.Fatal("missing service correlation")
	}
	stale := &pb.ControlUserServiceRequest{Kind: mutation.Kind, Action: pb.UserServiceAction_USER_SERVICE_ACTION_REMOVE, RequestId: string(domain.NewID()), ExpectedRevision: 0}
	if _, e = c.ControlUserService(ctx, ownerRequest(identity, stale)); connect.CodeOf(e) != connect.CodeAlreadyExists && connect.CodeOf(e) != connect.CodeAborted {
		t.Fatal("stale revision accepted", e)
	}
	if _, e = c.GetUserService(ctx, ownerRequest(identity, &pb.GetUserServiceRequest{Kind: pb.UserServiceKind(999)})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("unknown service kind accepted", e)
	}
	removed, e := c.ControlUserService(ctx, ownerRequest(identity, &pb.ControlUserServiceRequest{Kind: mutation.Kind, Action: pb.UserServiceAction_USER_SERVICE_ACTION_REMOVE, RequestId: string(domain.NewID()), ExpectedRevision: 1}))
	if e != nil || removed.Msg.Service.State != pb.UserServiceState_USER_SERVICE_STATE_ABSENT || backend.writes != 2 {
		t.Fatal(removed, e)
	}
	// Registered roles share product access while token authentication remains required.
	workerIdentity, _ := pairedWorker(t, ctx, endpoint, identity)
	if _, e = c.GetUserService(ctx, ownerRequest(workerIdentity, &pb.GetUserServiceRequest{Kind: pb.UserServiceKind_USER_SERVICE_KIND_SERVER})); e != nil {
		t.Fatal("registered Worker could not inspect user service", e)
	}
}
