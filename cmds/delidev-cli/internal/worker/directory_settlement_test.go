// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectorySettlementLostReplyReplaysExactOriginalBytes(t *testing.T) {
	for _, name := range []string{"before commit", "after commit"} {
		t.Run(name, func(t *testing.T) {
			root, f, credential := directoryReceiptFixture(t, name == "before commit")
			client, closeRPC, err := subscriptionRPC(context.Background(), Config{Root: root}, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer closeRPC()
			if err := security.PrivateDir(filepath.Join(root, "managed-auth")); err != nil {
				t.Fatal(err)
			}
			id, account, instance, generation, operation := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			j := managedSubscriptionJournal{Version: 1, Lease: id, Account: account, Operation: operation, Instance: instance, Finish: domain.NewID(), Generation: generation, LeaseRevision: 3, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, State: managedClaimed}
			path := filepath.Join(root, "managed-auth", string(id)+".json")
			if err := writeJSON(path, j); err != nil {
				t.Fatal(err)
			}
			lease := &managedSubscriptionLease{client: client, credential: credential, instance: instance, account: account, finishID: j.Finish, journalPath: path, journal: j, response: &pb.TakeSubscriptionResponse{LeaseId: string(id), GenerationId: string(generation), LeaseRevision: 3}}
			bundle := []byte("fixture-dirty-authentication")
			if lease.finishDirectory(bundle, true, true) == nil {
				t.Fatal("lost reply reported success")
			}
			first := <-f.finished
			if !bytes.Equal(first.Bundle, []byte("fixture-dirty-authentication")) || !bytes.Equal(bundle, make([]byte, len(bundle))) {
				t.Fatal("original settlement or cleanup changed")
			}
			raw, err := security.ReadPrivate(path+".directory-finish.pb", 128<<10)
			if err != nil {
				t.Fatal(err)
			}
			// Reconstruct only receipt ownership after restart. No bundle or native call
			// is recoverable from this object; the protected original bytes own replay.
			lease.response.Bundle = nil
			if err := lease.finishDirectory(nil, false, false); err != nil {
				t.Fatal(err)
			}
			second := <-f.finished
			if !proto.Equal(first, second) || f.commits != 1 {
				t.Fatal("lost response changed exact receipt arguments")
			}
			after, err := os.ReadFile(path + ".directory-finish.pb")
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("protected receipt rewritten")
			}
			lease.instance = domain.NewID()
			if lease.finishDirectory(nil, false, false) == nil {
				t.Fatal("foreign owner replayed receipt")
			}
			select {
			case <-f.finished:
				t.Fatal("foreign receipt reached protected lane")
			default:
			}
		})
	}
}

func TestDirectoryDeletionIncludesOnlyOriginalSettledLeaseReceipts(t *testing.T) {
	root := t.TempDir()
	if err := security.PrivateDir(filepath.Join(root, "managed-auth")); err != nil {
		t.Fatal(err)
	}
	instance, operation := domain.NewID(), domain.NewID()
	for _, own := range []bool{true, false} {
		id := domain.NewID()
		op := operation
		if !own {
			op = domain.NewID()
		}
		j := managedSubscriptionJournal{Version: 1, Lease: id, Account: domain.NewID(), Operation: op, Instance: instance, Finish: domain.NewID(), Generation: domain.NewID(), LeaseRevision: 2, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, State: managedReported}
		if err := writeJSON(filepath.Join(root, "managed-auth", string(id)+".json"), j); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := directoryLeaseJournals(root, operation, instance)
	if err != nil || len(paths) != 1 {
		t.Fatal("bounded exact lease inventory", err, len(paths))
	}
	if _, err := directoryLeaseJournals(root, operation, domain.NewID()); err == nil {
		t.Fatal("foreign instance adopted")
	}
}

func TestDirectorySettlementRecoveryPromotesOnlyOriginalReadyGeneration(t *testing.T) {
	root, credential, p, expected := directoryCheckpointFixture(t)
	_, f, network := managedLaneFixture(t, connect.NewError(connect.CodeUnavailable, nil))
	credential.Endpoint, credential.MachineID = network.Endpoint, p.Input.Assignment.MachineID
	client, closeRPC, err := subscriptionRPC(context.Background(), Config{Root: root}, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRPC()
	if err := security.PrivateDir(filepath.Join(root, "managed-auth")); err != nil {
		t.Fatal(err)
	}
	leaseID, generation := domain.NewID(), domain.NewID()
	j := managedSubscriptionJournal{Version: 1, Lease: leaseID, Account: p.Input.Assignment.AccountID, Operation: p.Ownership.JobID, Instance: p.Ownership.InstanceID, Finish: domain.NewID(), Generation: generation, LeaseRevision: 3, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, State: managedClaimed}
	path := filepath.Join(root, "managed-auth", string(leaseID)+".json")
	if err := writeJSON(path, j); err != nil {
		t.Fatal(err)
	}
	ready := mustDirectoryJSON(p)
	if err := security.WriteAtomic(filepath.Join(root, "jobs", string(p.Ownership.JobID), "directory-ready.json"), ready); err != nil {
		t.Fatal(err)
	}
	lease := &managedSubscriptionLease{client: client, credential: credential, instance: j.Instance, account: j.Account, finishID: j.Finish, journalPath: path, journal: j, response: &pb.TakeSubscriptionResponse{LeaseId: string(j.Lease), GenerationId: string(j.Generation), LeaseRevision: j.LeaseRevision}}
	if lease.finishDirectory([]byte("fixture-bundle"), true, true) == nil {
		t.Fatal("lost settlement reply expected")
	}
	first := <-f.finished
	f.finishError = nil
	job := domain.Job{Type: domain.ChangeSessionDirectoryJob, Input: mustDirectoryJSON(p.Input)}
	resource := &pb.Resource{Id: string(p.Ownership.JobID), Revision: p.Ownership.Revision, DocumentJson: mustDirectoryJSON(job)}
	config := Config{Root: root, execution: &PublicationConfig{Credential: credential}}
	output, recovered, err := recoverDirectorySettlement(context.Background(), config, resource, p.Ownership.InstanceID, job)
	if err != nil || !recovered {
		t.Fatal("exact protected recovery", err, recovered)
	}
	second := <-f.finished
	if !proto.Equal(first, second) {
		t.Fatal("recovery reconstructed original settlement")
	}
	var result domain.SessionDirectoryResult
	if domain.Decode(output, &result) != nil || result.Validate() != nil || result.Checkpoint != expected {
		t.Fatal("recovery replaced generation identity")
	}
	after, err := os.ReadFile(filepath.Join(root, "jobs", string(p.Ownership.JobID), "directory-ready.json"))
	if err != nil || !bytes.Equal(ready, after) {
		t.Fatal("ready record changed")
	}
	resource.Revision++
	if _, _, err := recoverDirectorySettlement(context.Background(), config, resource, p.Ownership.InstanceID, job); err == nil {
		t.Fatal("foreign revision promoted generation")
	}
	select {
	case <-f.finished:
		t.Fatal("foreign revision replayed settlement")
	default:
	}
}

type directoryReceiptServer struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	finished          chan *pb.FinishSubscriptionRequest
	beforeCommit      bool
	attempts, commits int
	receipt           *pb.FinishSubscriptionRequest
}

func (f *directoryReceiptServer) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	request := proto.Clone(req.Msg).(*pb.FinishSubscriptionRequest)
	f.finished <- request
	first := f.attempts == 0
	f.attempts++
	if first && f.beforeCommit {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	if f.receipt == nil {
		f.receipt = proto.Clone(request).(*pb.FinishSubscriptionRequest)
		f.commits++
	} else if !proto.Equal(f.receipt, request) {
		return nil, connect.NewError(connect.CodeAborted, nil)
	}
	if first {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}
func directoryReceiptFixture(t *testing.T, before bool) (string, *directoryReceiptServer, Credential) {
	t.Helper()
	f := &directoryReceiptServer{finished: make(chan *pb.FinishSubscriptionRequest, 2), beforeCommit: before}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(f)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return t.TempDir(), f, Credential{Endpoint: server.URL, MachineID: domain.NewID()}
}

func TestDirectoryDeletionRemovesGenerationAndOriginalReceiptWithoutForeignCopies(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	job, generation, instance := domain.NewID(), domain.NewID(), work.Copies[0].InstanceID
	digest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	work.Copies = append(work.Copies, domain.SessionDeletionCopy{JobID: job, Type: domain.ChangeSessionDirectoryJob, Revision: 2, Digest: digest, InstanceID: instance, ActionID: generation})
	if err := work.Validate(); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(config.Root, "jobs", string(job))
	if err := security.PrivateDir(folder); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(config.Root, "jobs", string(job)+".json"), journal{Version: 1, JobID: job, InstanceID: instance, Revision: 2, Digest: digest, State: journalReported, ReportID: domain.NewID()}); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(config.Root, "runtimes", string(generation))
	for _, path := range []string{runtime, filepath.Join(config.Root, "directory-checkpoints"), filepath.Join(config.Root, "managed-auth")} {
		if err := security.PrivateDir(path); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{runtime, filepath.Join(config.Root, "directory-checkpoints", string(generation)+".json"), folder, filepath.Join(config.Root, "jobs", string(job)+".json")}
	if err := security.WriteAtomic(paths[1], []byte("fixture-generation")); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(folder, "directory-ready.json"), []byte("fixture-ready")); err != nil {
		t.Fatal(err)
	}
	var foreign string
	for _, own := range []bool{true, false} {
		lease := domain.NewID()
		operation := job
		if !own {
			operation = domain.NewID()
		}
		journal := managedSubscriptionJournal{Version: 1, Lease: lease, Account: domain.NewID(), Operation: operation, Instance: instance, Finish: domain.NewID(), Generation: domain.NewID(), LeaseRevision: 3, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, State: managedReported}
		path := filepath.Join(config.Root, "managed-auth", string(lease)+".json")
		if err := writeJSON(path, journal); err != nil {
			t.Fatal(err)
		}
		if err := security.WriteAtomic(path+".directory-finish.pb", []byte("fixture-protected-receipt")); err != nil {
			t.Fatal(err)
		}
		if own {
			paths = append(paths, path, path+".directory-finish.pb")
		} else {
			foreign = path
		}
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err != nil {
		t.Fatal("complete directory deletion", err)
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("owned directory copy remains", path, err)
		}
	}
	for _, path := range []string{foreign, foreign + ".directory-finish.pb"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("foreign lease removed", err)
		}
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err != nil {
		t.Fatal("exact completed deletion replay", err)
	}
}
