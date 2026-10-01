// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
)

func TestRepositoryMetadataNegotiationAndReportValidation(t *testing.T) {
	endpoint, owner, stop, done := runTestServer(t, filepath.Join(t.TempDir(), "state"))
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	credential, device := pairedWorker(t, ctx, endpoint, owner)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, endpoint.URL)
	instance := string(domain.NewID())
	metadata := pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_INSPECTION_METADATA_V1
	attach := func(capabilities []pb.WorkerCapability) (*connect.Response[pb.AttachWorkerResponse], error) {
		return client.AttachWorker(ctx, ownerRequest(credential, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, InstanceId: instance, Version: rpc.Version, Capabilities: capabilities}))
	}
	initial, err := attach([]pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1})
	if err != nil || !slices.Contains(initial.Msg.SupportedWorkerCapabilities, metadata) {
		t.Fatal("server did not advertise metadata support", err)
	}
	var machine domain.Machine
	if domain.Decode(initial.Msg.Machine.DocumentJson, &machine) != nil || slices.Contains(machine.WorkerCapabilities, domain.RepositoryInspectionMetadataV1) {
		t.Fatal("initial attachment fabricated acceptance")
	}
	for _, capabilities := range [][]pb.WorkerCapability{{metadata, metadata}, {metadata, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1, metadata}, {pb.WorkerCapability(99)}} {
		if _, err := attach(capabilities); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("invalid/duplicate capability accepted", err)
		}
	}
	negotiated, err := attach([]pb.WorkerCapability{metadata, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1, pb.WorkerCapability_WORKER_CAPABILITY_AUTOMATIC_TITLES_CODEX_V1})
	if err != nil || domain.Decode(negotiated.Msg.Machine.DocumentJson, &machine) != nil || !slices.Contains(machine.WorkerCapabilities, domain.RepositoryInspectionMetadataV1) {
		t.Fatal("three independent capabilities rejected", err)
	}

	accepted, err := client.InspectRepository(ctx, ownerRequest(owner, &pb.InspectRepositoryRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, Path: "/test/checkout"}))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.WatchWork(ctx, ownerRequest(credential, &pb.WatchWorkRequest{MachineId: device.Machine.Id, InstanceId: instance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var claimed *pb.Resource
	for stream.Receive() {
		if stream.Msg().Job != nil {
			claimed = stream.Msg().Job
			break
		}
	}
	if claimed == nil || claimed.Id != accepted.Msg.Job.Id {
		t.Fatal("inspection claim unavailable", stream.Err())
	}
	good := workspace.Inspection{Root: "/test/checkout", Name: "checkout", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}, GitHubRepositories: map[string]workspace.GitHubRepository{"origin": {Owner: "owner", Name: "repo"}}}
	report := func(output []byte) error {
		_, err := client.ReportWork(ctx, ownerRequest(credential, &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: claimed.Id, ExpectedRevision: claimed.Revision, RequestId: string(domain.NewID())}, MachineId: device.Machine.Id, InstanceId: instance, OutputJson: output}))
		return err
	}
	for _, raw := range []string{`{"root":"/test/checkout","name":"checkout","remotes":["origin"],"default_refs":{},"github_repositories":null}`, `{"root":"/test/checkout","name":"checkout","remotes":["origin"],"default_refs":{},"github_repositories":{"foreign":{"owner":"owner","name":"repo"}}}`, `{"root":"/test/checkout","name":"checkout","remotes":["origin"],"default_refs":{},"github_repositories":{"origin":{"owner":"owner","name":"repo","url":"private"}}}`} {
		if err := report([]byte(raw)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("foreign/raw metadata accepted", err)
		}
	}
	// Current support must still be accepted at publication. Removing it is not
	// authority to return an enriched result from an earlier negotiated process.
	if _, err := attach(nil); err != nil {
		t.Fatal(err)
	}
	if err := report([]byte(`{"root":"/test/checkout","name":"checkout","remotes":["origin"],"default_refs":{},"github_repositories":null}`)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unnegotiated null metadata accepted", err)
	}
	raw, _ := json.Marshal(good)
	if err := report(raw); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unnegotiated result accepted", err)
	}
	good.GitHubRepositories = nil
	raw, _ = json.Marshal(good)
	if err := report(raw); err != nil {
		t.Fatal("legacy output rejected", err)
	}
}
