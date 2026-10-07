// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestRepositoryCloneAcceptReplayAndServerRegistration(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	worker, paired := pairedWorker(t, ctx, Endpoint{URL: f.url}, f.service.Identity)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.url)
	instance := string(domain.NewID())
	_, err := client.AttachWorker(ctx, ownerRequest(worker, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: paired.Machine.Id, InstanceId: instance, Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_CLONE_V1}}))
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.CloneRepositoryRequest{RequestId: string(domain.NewID()), MachineId: paired.Machine.Id, ParentPath: "/alias/parent", Url: "https://example.com/repo.git", DirectoryName: "repo", LocalWorkerToken: worker.Token}
	accepted, err := client.CloneRepository(ctx, ownerRequest(f.service.Identity, request))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := client.CloneRepository(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatal("clone duplicated", err)
	}
	request.DirectoryName = "other"
	if _, err := client.CloneRepository(ctx, ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed retry accepted", err)
	}
	if bytesContain(accepted.Msg.Job.DocumentJson, worker.Token) {
		t.Fatal("proof entered durable job")
	}
	// Dispatch and reporting continue after the original HTTP request has ended.
	stream, err := client.WatchWork(ctx, ownerRequest(worker, &pb.WatchWorkRequest{MachineId: paired.Machine.Id, InstanceId: instance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var assignment *pb.Resource
	for stream.Receive() {
		if stream.Msg().Job != nil {
			assignment = stream.Msg().Job
			break
		}
	}
	if assignment == nil {
		t.Fatal("accepted clone not dispatched", stream.Err())
	}
	output, _ := json.Marshal(workspace.CloneResult{Inspection: &workspace.Inspection{Root: "/canonical/parent/repo", Name: "repo", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}}})
	report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: assignment.Id, ExpectedRevision: assignment.Revision}, MachineId: paired.Machine.Id, InstanceId: instance, OutputJson: output}
	finished, err := client.ReportWork(ctx, ownerRequest(worker, report))
	if err != nil {
		t.Fatal(err)
	}
	var job domain.Job
	if domain.Decode(finished.Msg.Job.DocumentJson, &job) != nil || job.State != domain.JobSucceeded {
		t.Fatal("server did not finish clone", job.Problem)
	}
	var result workspace.CloneResult
	if domain.Decode(job.Output, &result) != nil || result.RepositoryID == "" {
		t.Fatal("server did not register checkout")
	}
	actor := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	repository, err := f.service.Store.Get(actor, domain.RepositoryKind, result.RepositoryID)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := store.Decode[domain.Repository](repository)
	if value.Checkouts[0].Path != "/canonical/parent/repo" || value.PreferredRemote != "origin" {
		t.Fatal("wrong checkout registration")
	}
	retry, err := client.ReportWork(ctx, ownerRequest(worker, report))
	if err != nil || !retry.Msg.Replayed {
		t.Fatal("completion duplicated", err)
	}
	request.DirectoryName = "repo"
	otherClient, otherToken := domain.NewID(), randomCode()
	verifier := sha256.Sum256([]byte(otherToken))
	_, err = f.service.Store.Mutate(actor, domain.NewID(), "fixture.clone-client", nil, func(tx *store.Tx) (any, error) {
		_, err := tx.Put(domain.DeviceKind, otherClient, 0, "", "", domain.Device{Name: "other client", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		return nil, tx.PutCredential(otherClient, verifier[:])
	})
	if err != nil {
		t.Fatal(err)
	}
	otherRequest := connect.NewRequest(request)
	otherRequest.Header().Set("Authorization", "Bearer "+otherToken)
	if _, err := client.CloneRepository(ctx, otherRequest); err != nil {
		t.Fatal("another client adopted the original receipt", err)
	}
	_, err = f.service.Store.Mutate(actor, domain.NewID(), "fixture.retire-clone-worker", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.DeviceKind, domain.ID(paired.Device.Id))
		if err != nil {
			return nil, err
		}
		d, err := store.Decode[domain.Device](r)
		if err != nil {
			return nil, err
		}
		d.Revoked = true
		if _, err = tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d); err != nil {
			return nil, err
		}
		return nil, tx.RevokeCredential(r.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	retiredReplay, err := client.CloneRepository(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !retiredReplay.Msg.Replayed || retiredReplay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatal("secondary proof retirement lost the original acknowledgment", err)
	}
	request.RequestId = string(domain.NewID())
	if _, err := client.CloneRepository(ctx, ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("revoked selected Worker registration was not rejected", err)
	}
}
func bytesContain(raw []byte, value string) bool { return strings.Contains(string(raw), value) }

func TestRepositoryCloneRegistrationFailurePreservesPublishedMetadata(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	machine, device, repositoryID, jobID, instance := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	input := domain.RepositoryCloneInput{RepositoryID: repositoryID, MachineID: machine, LocalOrigin: domain.LocalOrigin{MachineID: machine, DeviceID: device}, ParentPath: "/alias/parent", URL: "https://example.com/repo.git", DirectoryName: "repo"}
	raw, _ := json.Marshal(input)
	job := domain.Job{Type: domain.CloneRepositoryJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, AssignedDeviceID: device, Input: raw, AcceptedAt: time.Now().UTC()}
	// Revoked original provenance remains metadata when an authenticated owner
	// registers the checkout that the Worker already published.
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.clone", nil, func(tx *store.Tx) (any, error) {
		_, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: rpc.Version, WorkerCapabilities: []domain.WorkerCapability{domain.RepositoryCloneV1}})
		if err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "fixture", Type: domain.WorkerDevice, MachineID: machine, Revoked: true, PairedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		return tx.PutJob(jobID, 0, "", "", job)
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.service.Store.Get(ctx, domain.JobKind, jobID)
	if err != nil {
		t.Fatal(err)
	}
	outcome, _ := json.Marshal(workspace.CloneResult{Inspection: &workspace.Inspection{Root: "/canonical/parent/repo", Name: "repo", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}}})
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.finish", nil, func(tx *store.Tx) (any, error) {
		return finishRepositoryClone(tx, record, job, record.Revision, outcome, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := f.service.Store.Get(ctx, domain.JobKind, jobID)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := store.Decode[domain.Job](failed)
	var result workspace.CloneResult
	_ = domain.Decode(value.Output, &result)
	if value.State != domain.JobSucceeded || result.Inspection == nil || result.Inspection.Root != "/canonical/parent/repo" || result.RepositoryID != repositoryID {
		t.Fatal("published checkout metadata lost")
	}
	if _, err := f.service.Store.Get(ctx, domain.RepositoryKind, repositoryID); err != nil {
		t.Fatal("historical actor blocked checkout registration", err)
	}
}
