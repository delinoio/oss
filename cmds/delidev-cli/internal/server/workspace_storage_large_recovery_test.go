// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestWorkspaceStorageLargeOriginalRequestRemainsRecoverable(t *testing.T) {
	f := newStorageFixture(t)
	// Replace fixture seed metadata with a valid large remote-Linux repository
	// preparation. These paths never grant local file operations or native
	// acceptance; this test exercises actual SQLite/Connect ownership transport.
	prefix := "/remote/" + strings.Repeat("segment/", 150)
	ref := domain.Reference{Type: domain.LocalBranch, Name: strings.Repeat("branch/", 90) + "main"}
	prep := workspace.PrepareRequest{SessionID: f.session, MachineID: f.machine, Type: domain.Worktree}
	manifest := workspace.Manifest{Version: 1, SessionID: f.session, MachineID: f.machine, Type: prep.Type, State: workspace.Ready, CreatedAt: time.Now().UTC()}
	for i := 0; i < 100; i++ {
		id := domain.NewID()
		source := path.Join(prefix, "source", string(id))
		destination := path.Join(prefix, "workspaces", string(f.session), string(id))
		prep.Repositories = append(prep.Repositories, workspace.RepositorySpec{ID: id, Checkout: source, Base: ref, Starting: ref})
		manifest.Repositories = append(manifest.Repositories, workspace.PreparedRepository{ID: id, Source: source, Path: destination, Base: ref, Starting: ref, BaseCommit: strings.Repeat("a", 40), StartingCommit: strings.Repeat("a", 40), Owned: true})
		if i == 0 {
			prep.PrimaryRepository, manifest.PrimaryPath = id, destination
		}
	}
	input, _ := json.Marshal(prep)
	digest := sha256.Sum256(input)
	manifest.InputDigest = hex.EncodeToString(digest[:])
	if workspace.ValidateResult(prep, manifest, "linux") != nil {
		t.Fatal("invalid remote metadata fixture")
	}
	output, _ := json.Marshal(manifest)
	_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.large-preparation", nil, func(tx *store.Tx) (any, error) {
		sr, err := tx.Get(domain.SessionKind, f.session)
		if err != nil {
			return nil, err
		}
		s, err := store.Decode[domain.Session](sr)
		if err != nil {
			return nil, err
		}
		r, err := tx.Get(domain.JobKind, s.Preparation.JobID)
		if err != nil {
			return nil, err
		}
		j, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		j.Input, j.Output = input, output
		if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j); err != nil {
			return nil, err
		}
		s.Workspace = domain.Worktree
		return tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
	if err != nil {
		t.Fatal("valid original request rejected", err)
	}
	claimed := watchWorkAssignment(t, f.worker, f.workerIdentity, f.machine, f.instance)
	if claimed.Id != accepted.Msg.Job.Id {
		t.Fatal("primary lane assigned another original job")
	}
	var original domain.Job
	if domain.Decode(claimed.DocumentJson, &original) != nil || len(claimed.DocumentJson) <= 512<<10 || len(claimed.DocumentJson) > 1<<20 {
		t.Fatal("fixture did not exercise the valid large original job", len(claimed.DocumentJson))
	}
	_, err = f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: claimed.Id, ExpectedRevision: claimed.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: []byte(`{"cleanup_verified":true}`)}))
	if err != nil {
		t.Fatal(err)
	}
	req := f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", claimed.Id)
	recovery, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, req))
	if err != nil {
		t.Fatal("accepted original was stranded by recovery document size", err)
	}
	var j domain.Job
	var r workspace.StorageRequest
	var prior workspace.StorageRequest
	if len(recovery.Msg.Job.DocumentJson) <= 1<<20 || workspace.DecodeStorageJob(recovery.Msg.Job.DocumentJson, &j) != nil || workspace.DecodeStorageRequest(j.Input, &r) != nil || domain.Decode(original.Input, &prior) != nil || !reflect.DeepEqual(r.Recovery.Original, prior) {
		t.Fatal("recovery changed or truncated original evidence")
	}
	acceptedInput := append([]byte(nil), j.Input...)
	replay, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, req))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != recovery.Msg.Job.Id {
		t.Fatal("large recovery original request did not replay", err)
	}
	assigned := watchWorkAssignment(t, f.worker, f.workerIdentity, f.machine, f.instance)
	if assigned.Id != recovery.Msg.Job.Id || workspace.DecodeStorageJob(assigned.DocumentJson, &j) != nil || !bytes.Equal(j.Input, acceptedInput) {
		t.Fatal("primary lane changed original recovery input")
	}
	assertWorkerClaimExceptionBounds(t, assigned, domain.MaxStorageRecoveryInputBytes, workspace.MaxStorageRecoveryJobBytes)
	for _, malformed := range []func(*workspace.StorageRequest){
		func(input *workspace.StorageRequest) { input.Action = workspace.StoragePreview },
		func(input *workspace.StorageRequest) { input.Recovery = nil },
		func(input *workspace.StorageRequest) { input.Recovery.Claims = nil },
		func(input *workspace.StorageRequest) { input.Recovery.Original.Action = workspace.StorageRecover },
	} {
		var input workspace.StorageRequest
		if err := workspace.DecodeStorageRequest(acceptedInput, &input); err != nil {
			t.Fatal(err)
		}
		malformed(&input)
		j.Input, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		document, err := json.Marshal(j)
		if err != nil {
			t.Fatal(err)
		}
		record := store.Record{ID: domain.ID(assigned.Id), Kind: domain.JobKind, Revision: assigned.Revision}
		if _, _, err := decodeWorkerClaim(workerClaimJSON(t, record, padClaimDocument(t, document, workspace.MaxStorageRecoveryJobBytes))); err == nil {
			t.Fatal("malformed enlarged recovery claim was accepted")
		}
	}
	reconnected := watchWorkAssignment(t, f.worker, f.workerIdentity, f.machine, f.instance)
	if reconnected.Id != assigned.Id || reconnected.Revision != assigned.Revision || !bytes.Equal(reconnected.DocumentJson, assigned.DocumentJson) {
		t.Fatal("reconnect changed the original recovery claim")
	}
	cancel, err := f.client.CancelWorkspaceStorageOperation(context.Background(), ownerRequest(f.service.Identity, &pb.CancelWorkspaceStorageOperationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: assigned.Id, ExpectedRevision: assigned.Revision}}))
	if err != nil || cancel.Msg.Job.Id != assigned.Id {
		t.Fatal("large recovery claim could not be observed/canceled", err)
	}
}
