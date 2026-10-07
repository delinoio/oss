// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"bytes"
	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestRegisteredWorkerProductAccessPreservesAuthenticationAndRevocation(t *testing.T) {
	endpoint, owner, stop, done := runTestServer(t, filepath.Join(t.TempDir(), "state"))
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	worker, paired := pairedWorker(t, ctx, endpoint, owner)
	system := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)
	config := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, endpoint.URL)
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, endpoint.URL)
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, endpoint.URL)
	if _, err := system.GetOverview(ctx, ownerRequest(worker, &pb.GetOverviewRequest{})); err != nil {
		t.Fatal("authenticated Worker product read", err)
	}
	requestID := domain.NewID()
	saved := saveProvider(t, ctx, worker, config, requestID)
	if replay := saveProvider(t, ctx, worker, config, requestID); !replay.Replayed || replay.Resource.Id != saved.Resource.Id {
		t.Fatal("Worker product mutation lost request deduplication")
	}
	if _, err := resources.GetResource(ctx, ownerRequest(worker, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_DEVICE, Id: paired.Device.Id})); err != nil {
		t.Fatal("Worker product resource read", err)
	}
	// A second independently paired credential can revoke the first registration.
	other, _ := pairedWorker(t, ctx, endpoint, worker)
	if _, err := devices.RevokeDevice(ctx, ownerRequest(other, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: paired.Device.Id, ExpectedRevision: paired.Device.Revision}})); err != nil {
		t.Fatal("authenticated cross-device revocation", err)
	}
	for _, identity := range []security.Identity{worker, {Token: randomCode()}, {}} {
		if _, err := system.GetOverview(ctx, ownerRequest(identity, &pb.GetOverviewRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("revoked, invalid or absent token accepted", err)
		}
	}
	if _, err := system.GetOverview(ctx, ownerRequest(other, &pb.GetOverviewRequest{})); err != nil {
		t.Fatal("unrevoked registration lost access", err)
	}
}

func TestUnconfirmedSuccessorPreservesHistoryAndStopIntent(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	original, err := f.service.Store.Get(context.Background(), domain.JobKind, domain.ID(f.job.Id))
	if err != nil {
		t.Fatal(err)
	}
	queued := f.enqueue(t, "a new input after unconfirmed cleanup", domain.ExecuteMode)
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.cleanup.observation", nil, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, domain.ID(f.change.Session.Id))
		if err != nil {
			return nil, err
		}
		session.Execution.CleanupVerified = false
		session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
		_, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err == nil {
		t.Fatal("Stop intent was lost")
	}
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	current, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.service.Store.Get(context.Background(), domain.JobKind, func() domain.ID {
		var id domain.ID
		_ = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
			r, e := tx.SessionExecutionJob(domain.ID(f.change.Session.Id), current.ExecutionSelection().ID)
			id = r.ID
			return e
		})
		return id
	}())
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](next)
	if err != nil {
		t.Fatal(err)
	}
	var input domain.ExecutionJobInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.InputID != domain.ID(queued.Id) || input.ExecutionID == f.input.ExecutionID || input.Continuation != nil || input.Fork != nil {
		t.Fatal("fresh input did not receive a distinct attempt")
	}
	retained, err := f.service.Store.Get(context.Background(), domain.JobKind, original.ID)
	if err != nil || retained.Revision != original.Revision || !bytes.Equal(retained.Data, original.Data) {
		t.Fatal("historical execution was rewritten", err)
	}
	if current.InitialExecution.ID != f.input.ExecutionID || current.NativeExecutionRoot() != input.ExecutionID {
		t.Fatal("initial snapshot was replaced or new native attempt lacks its own reference")
	}
}
