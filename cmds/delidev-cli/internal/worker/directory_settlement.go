// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

// Persist the complete original protected settlement before delivery. This
// receipt has no native-send authority. Lost replies replay its exact bytes,
// even after the source bundle has been cleared by cleanup.
func (l *managedSubscriptionLease) finishDirectory(bundle []byte, cleanup, success bool) error {
	defer clear(bundle)
	path := l.journalPath + ".directory-finish.pb"
	raw, err := security.ReadPrivate(path, 128<<10)
	if errors.Is(err, os.ErrNotExist) {
		request := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(l.finishID), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, GenerationId: l.response.GenerationId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), Bundle: bytes.Clone(bundle), CleanupConfirmed: cleanup, Succeeded: success}
		raw, err = (proto.MarshalOptions{Deterministic: true}).Marshal(request)
		clear(request.Bundle)
		if err != nil || security.WriteAtomic(path, raw) != nil {
			clear(raw)
			return subscription.Invalid()
		}
	} else if err != nil {
		return subscription.Invalid()
	}
	defer clear(raw)
	var request pb.FinishSubscriptionRequest
	if proto.Unmarshal(raw, &request) != nil || request.Mutation == nil || request.Mutation.RequestId != string(l.finishID) || request.Mutation.Id != string(l.account) || request.Mutation.ExpectedRevision != l.response.LeaseRevision || request.LeaseId != l.response.LeaseId || request.GenerationId != l.response.GenerationId || request.MachineId != string(l.credential.MachineID) || request.InstanceId != string(l.instance) || request.RefreshConfirmed {
		return subscription.Invalid()
	}
	defer clear(request.Bundle)
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&request)
	if err != nil || !bytes.Equal(raw, canonical) {
		clear(canonical)
		return subscription.Invalid()
	}
	clear(canonical)
	l.journal.State, l.journal.Cleanup, l.journal.Refresh, l.journal.Success = managedClosed, request.CleanupConfirmed, request.RefreshConfirmed, request.Succeeded
	if writeJSON(l.journalPath, l.journal) != nil {
		return subscription.Invalid()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := l.client.FinishSubscription(ctx, authenticated(l.credential, &request)); err != nil {
		return rpc.ClientError(err)
	}
	l.journal.State = managedReported
	if writeJSON(l.journalPath, l.journal) != nil {
		return subscription.Invalid()
	}
	return nil
}

// Enumerate bounded private lease owners by original operation and instance.
// Foreign journals remain untouched. Missing receipts grant no retry authority.
func directoryLeaseJournals(root string, operation, instance domain.ID) ([]string, error) {
	folder := filepath.Join(root, "managed-auth")
	f, err := os.Open(folder)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || security.CheckPrivateDir(folder) != nil {
		if f != nil {
			f.Close()
		}
		return nil, subscription.Invalid()
	}
	defer f.Close()
	entries, err := f.ReadDir(4097)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 4096 {
		return nil, subscription.Invalid()
	}
	var paths []string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(folder, entry.Name())
		raw, err := security.ReadPrivate(path, 16<<10)
		var journal managedSubscriptionJournal
		if err != nil || domain.Decode(raw, &journal) != nil {
			return nil, subscription.Invalid()
		}
		if journal.Operation != operation {
			continue
		}
		if journal.Version != 1 || journal.Instance != instance || journal.Lease.Validate() != nil || entry.Name() != string(journal.Lease)+".json" || journal.Account.Validate() != nil || journal.Finish.Validate() != nil || journal.Generation.Validate() != nil || journal.LeaseRevision == 0 || journal.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
			return nil, subscription.Invalid()
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func recoverDirectorySettlement(ctx context.Context, config Config, resource *pb.Resource, instance domain.ID, job domain.Job) (json.RawMessage, bool, error) {
	c := config.execution
	if c == nil || resource == nil || domain.ID(resource.Id).Validate() != nil {
		return nil, false, domain.DirectoryUncertain()
	}
	paths, err := directoryLeaseJournals(config.Root, domain.ID(resource.Id), instance)
	if err != nil {
		return nil, false, err
	}
	if len(paths) == 0 {
		return nil, false, nil
	}
	if len(paths) != 1 {
		return nil, false, domain.DirectoryUncertain()
	}
	raw, err := security.ReadPrivate(paths[0], 16<<10)
	var j managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &j) != nil {
		return nil, false, domain.DirectoryUncertain()
	}
	receipt, err := security.ReadPrivate(paths[0]+".directory-finish.pb", 128<<10)
	var request pb.FinishSubscriptionRequest
	if err != nil || proto.Unmarshal(receipt, &request) != nil {
		clear(receipt)
		return nil, false, domain.DirectoryUncertain()
	}
	clear(receipt)
	defer clear(request.Bundle)
	var input domain.SessionDirectoryInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Assignment.AccountID != j.Account || request.MachineId != string(c.Credential.MachineID) {
		return nil, false, domain.DirectoryUncertain()
	}
	client, closeRPC, err := subscriptionRPC(ctx, config, c.Credential)
	if err != nil {
		return nil, false, err
	}
	defer closeRPC()
	lease := &managedSubscriptionLease{client: client, credential: c.Credential, instance: instance, account: j.Account, finishID: j.Finish, journalPath: paths[0], journal: j, response: &pb.TakeSubscriptionResponse{LeaseId: string(j.Lease), GenerationId: string(j.Generation), LeaseRevision: j.LeaseRevision}}
	if err := lease.finishDirectory(nil, false, false); err != nil {
		return nil, false, err
	}
	// A failed transition can still settle its exact dirty bundle. Only a ready
	// cleanup record and successful original receipt can promote a generation.
	if !request.CleanupConfirmed || !request.Succeeded {
		return nil, false, nil
	}
	ready, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", resource.Id, "directory-ready.json"), maxDirectoryCheckpointBytes)
	var p codexDirectoryCheckpoint
	if err != nil || domain.DecodeWithLimit(ready, &p, maxDirectoryCheckpointBytes) != nil || p.validateClaims(config.Root) != nil || !bytes.Equal(ready, mustDirectoryJSON(p)) || p.Ownership.ServerID != c.Credential.ServerID || p.Ownership.DeviceID != c.Credential.DeviceID || p.Ownership.InstanceID != instance || p.Ownership.JobID != domain.ID(resource.Id) || p.Ownership.Revision != resource.Revision || p.Ownership.AssignmentDigest != executionInputDigest(resource.DocumentJson) || !sameDirectoryValue(p.Input, input) {
		return nil, false, domain.DirectoryUncertain()
	}
	ref, err := retainDirectoryCheckpoint(config.Root, p)
	if err != nil {
		return nil, false, err
	}
	result := domain.SessionDirectoryResult{Version: 1, RequestID: input.RequestID, GenerationID: input.GenerationID, ExecutionID: input.Assignment.ExecutionID, Checkpoint: ref, CleanupVerified: true}
	if result.Validate() != nil {
		return nil, false, domain.DirectoryUncertain()
	}
	return mustDirectoryJSON(result), true, nil
}
