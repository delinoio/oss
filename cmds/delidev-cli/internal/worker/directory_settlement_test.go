// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectorySettlementLostReplyReplaysExactOriginalBytes(t *testing.T) {
	for _, name := range []string{"before commit", "after commit"} {
		t.Run(name, func(t *testing.T) {
			root, f, credential := managedLaneFixture(t, connect.NewError(connect.CodeUnavailable, nil))
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
			f.finishError = nil
			// Reconstruct only receipt ownership after restart. No bundle or native call
			// is recoverable from this object; the protected original bytes own replay.
			lease.response.Bundle = nil
			if err := lease.finishDirectory(nil, false, false); err != nil {
				t.Fatal(err)
			}
			second := <-f.finished
			if !proto.Equal(first, second) {
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
