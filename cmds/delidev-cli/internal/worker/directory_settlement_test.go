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
