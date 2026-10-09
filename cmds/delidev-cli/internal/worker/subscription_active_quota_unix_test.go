// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type originalQuotaRPC struct {
	delidevv1connect.SubscriptionServiceClient
	original     domain.SubscriptionObservationOperation
	claimed      bool
	publications int
}

func (f *originalQuotaRPC) ClaimSubscriptionObservation(_ context.Context, req *connect.Request[pb.ClaimSubscriptionObservationRequest]) (*connect.Response[pb.ClaimSubscriptionObservationResponse], error) {
	if f.claimed {
		return nil, connect.NewError(connect.CodeFailedPrecondition, nil)
	}
	if req.Msg.OperationId != string(f.original.ID) || req.Msg.GenerationId != string(f.original.Generation) || req.Msg.MachineId != string(f.original.MachineID) {
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	}
	f.claimed = true
	op := f.original
	op.Phase = domain.SubscriptionObservationSending
	raw, _ := json.Marshal(op)
	return connect.NewResponse(&pb.ClaimSubscriptionObservationResponse{OperationJson: raw}), nil
}
func (f *originalQuotaRPC) PublishSubscriptionObservation(_ context.Context, req *connect.Request[pb.PublishSubscriptionObservationRequest]) (*connect.Response[pb.PublishSubscriptionObservationResponse], error) {
	var result domain.SubscriptionObservationResult
	if domain.Decode(req.Msg.ObservationJson, &result) != nil || result.Quota == nil || result.Outcome != "" || result.ConsumeUncertain {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	f.publications++
	return connect.NewResponse(&pb.PublishSubscriptionObservationResponse{}), nil
}
func TestActiveQuotaRegistryReadsOnlyInitializedOriginalProcess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "fixture")
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexec "+quote(binary)+" --managed-subscription-fixture active-quota \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	env, err := harness.PrivateRuntimeEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	native, err := codex.Open(ctx, codex.Config{Version: domain.CodexProtocolVersion, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: home, Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: executable, Cwd: root, Env: env}})
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	op := domain.SubscriptionObservationOperation{ID: domain.NewID(), Action: domain.SubscriptionQuota, MachineID: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, ConnectionID: domain.NewID(), Generation: domain.NewID(), Phase: domain.SubscriptionObservationQueued, RequestedAt: time.Now().UTC()}
	rpc := &originalQuotaRPC{original: op}
	lease := &managedSubscriptionLease{client: rpc, account: domain.NewID(), credential: Credential{MachineID: op.MachineID}, instance: domain.NewID(), response: &pb.TakeSubscriptionResponse{LeaseId: string(domain.NewID()), LeaseRevision: 2, GenerationId: string(op.Generation)}}
	registry := &managedObservationRegistry{}
	if handled, err := registry.run(ctx, lease.account, op); handled || err != nil {
		t.Fatal("absent original owner granted a read", err)
	}
	unregister := registry.register(lease.account, native, lease)
	if handled, err := registry.run(ctx, lease.account, op); !handled || err != nil {
		t.Fatal("original read failed", err)
	}
	if handled, err := registry.run(ctx, lease.account, op); !handled || err == nil {
		t.Fatal("claim replay read native again", err)
	}
	unregister()
	if handled, err := registry.run(ctx, lease.account, op); handled || err != nil {
		t.Fatal("retired original owner read", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "quota-reads"))
	if err != nil || string(raw) != "read\n" || rpc.publications != 1 {
		t.Fatal("quota did not retain one original process read", err)
	}
}
