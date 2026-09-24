package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestDiscoveryRevisionReceiptsAuthorizationAndAtomicPublication(t *testing.T) {
	endpoint, owner, stop, done := runTestServer(t, filepath.Join(t.TempDir(), "server"))
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	worker, device := pairedWorker(t, ctx, endpoint, owner)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, endpoint.URL)
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, endpoint.URL)
	instance := string(domain.NewID())
	attach := &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, InstanceId: instance, Version: rpc.Version}
	attached, err := client.AttachWorker(ctx, ownerRequest(worker, attach))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.WatchWork(ctx, ownerRequest(worker, &pb.WatchWorkRequest{MachineId: device.Machine.Id, InstanceId: instance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("missing heartbeat")
	}
	selections, _ := json.Marshal(domain.ExecutableSelections{Executables: []domain.ExecutableSelection{{Harness: domain.Codex, Path: "/selected/codex"}}})
	request := &pb.DiscoverHarnessesRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: device.Machine.Id, ExpectedRevision: attached.Msg.Machine.Revision}, SelectionsJson: selections}
	if _, err := client.DiscoverHarnesses(ctx, ownerRequest(worker, request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("Worker changed owner selections: %v", err)
	}
	for _, invalid := range []string{`null`, `{}`, `{"executables":null}`, `{"executables":[{"harness":"codex"},{"harness":"codex"}]}`, `{"executables":[],"unexpected":true}`} {
		bad := &pb.DiscoverHarnessesRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: device.Machine.Id, ExpectedRevision: attached.Msg.Machine.Revision}, SelectionsJson: []byte(invalid)}
		if _, err := client.DiscoverHarnesses(ctx, ownerRequest(owner, bad)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("malformed selection accepted: %s: %v", invalid, err)
		}
	}
	accepted, err := client.DiscoverHarnesses(ctx, ownerRequest(owner, request))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := client.DiscoverHarnesses(ctx, ownerRequest(owner, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatalf("discovery duplicated: %v", err)
	}
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal("missing job", stream.Err())
	}
	first := stream.Msg().Job
	// A refresh with no document preserves explicit selections but creates a new
	// discovery generation. Old observations must not overwrite newer work.
	refresh := &pb.DiscoverHarnessesRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: device.Machine.Id, ExpectedRevision: accepted.Msg.Machine.Revision}}
	second, err := client.DiscoverHarnesses(ctx, ownerRequest(owner, refresh))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != second.Msg.Job.Id {
		t.Fatal("missing refreshed job")
	}
	claimed := stream.Msg().Job
	report := func(job *pb.Resource) *pb.ReportWorkRequest {
		var value domain.Job
		if err := domain.Decode(job.DocumentJson, &value); err != nil {
			t.Fatal(err)
		}
		var input domain.HarnessDiscoveryInput
		if err := domain.Decode(value.Input, &input); err != nil {
			t.Fatal(err)
		}
		output := domain.HarnessDiscoveryOutput{Installations: input.Selections.Installations()}
		for index := range output.Installations {
			output.Installations[index].State = domain.InstallationMissing
			output.Installations[index].Problem = domain.Fail(domain.NotFound, "secret-native-error", "secret-guidance")
		}
		raw, _ := json.Marshal(output)
		return &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: job.Id, ExpectedRevision: job.Revision}, MachineId: device.Machine.Id, InstanceId: instance, OutputJson: raw}
	}
	stale, err := client.ReportWork(ctx, ownerRequest(worker, report(first)))
	if err != nil {
		t.Fatal(err)
	}
	var staleJob domain.Job
	if err := domain.Decode(stale.Msg.Job.DocumentJson, &staleJob); err != nil {
		t.Fatal(err)
	}
	if staleJob.State != domain.JobFailed || staleJob.Problem.Code != domain.Conflict {
		t.Fatalf("stale result published: %+v", staleJob)
	}
	completion := report(claimed)
	// A general machine revision change from attachment is independent of the
	// executable discovery generation and must not invalidate fresh results.
	attach.RequestId = string(domain.NewID())
	if _, err := client.AttachWorker(ctx, ownerRequest(worker, attach)); err != nil {
		t.Fatal(err)
	}
	wrong := report(claimed)
	wrong.Mutation.ExpectedRevision++
	if _, err := client.ReportWork(ctx, ownerRequest(worker, wrong)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("wrong revision accepted: %v", err)
	}
	current, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: device.Machine.Id}))
	if err != nil {
		t.Fatal(err)
	}
	var machine domain.Machine
	if err := domain.Decode(current.Msg.Resource.DocumentJson, &machine); err != nil {
		t.Fatal(err)
	}
	if machine.Installations[0].State != domain.InstallationUnchecked {
		t.Fatal("failed job transaction partially published observations")
	}
	malformed := report(claimed)
	var invalid domain.HarnessDiscoveryOutput
	if err := domain.Decode(malformed.OutputJson, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid.Installations[0].Capabilities = []domain.Capability{domain.CapabilityExecute}
	malformed.OutputJson, _ = json.Marshal(invalid)
	if _, err := client.ReportWork(ctx, ownerRequest(worker, malformed)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("version probe claimed execution: %v", err)
	}
	finished, err := client.ReportWork(ctx, ownerRequest(worker, completion))
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := client.ReportWork(ctx, ownerRequest(worker, completion))
	if err != nil || !repeated.Msg.Replayed || repeated.Msg.Job.Revision != finished.Msg.Job.Revision {
		t.Fatalf("completion replay failed: %v", err)
	}
	current, err = resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: device.Machine.Id}))
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Decode(current.Msg.Resource.DocumentJson, &machine); err != nil {
		t.Fatal(err)
	}
	if machine.DiscoveryRevision != 2 || machine.Installations[0].ExplicitPath != "/selected/codex" || machine.Installations[0].ObservedAt == nil || machine.Installations[0].State != domain.InstallationMissing {
		t.Fatalf("incorrect observations: %+v", machine)
	}
	if strings.Contains(string(current.Msg.Resource.DocumentJson), "secret-") || strings.Contains(string(finished.Msg.Job.DocumentJson), "secret-") {
		t.Fatal("Worker diagnostics persisted raw")
	}
}
