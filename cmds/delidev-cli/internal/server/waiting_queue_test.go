// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestWaitingQueueRPCRevisionScopeNoopAndOriginalReplay(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
	first := f.enqueue(t, "First waiting", domain.ExecuteMode)
	second := f.enqueue(t, "Second waiting", domain.ExecuteMode)
	session := f.refresh(t)
	client := sessionClient(f.accountFixture)
	ctx := context.Background()
	list, err := client.ListWaitingQueue(ctx, ownerRequest(f.identity, &pb.ListWaitingQueueRequest{SessionId: string(session.ID), PageSize: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if list.Msg.WaitingCount != 2 || len(list.Msg.Inputs) != 1 || list.Msg.Inputs[0].Id != first.Id || list.Msg.NextPageToken == "" {
		t.Fatal(list.Msg)
	}
	generation := list.Msg.CurrentQueueGeneration
	request := &pb.MoveQueuedInputRequest{Mutation: &pb.Mutation{Id: second.Id, ExpectedRevision: second.Revision, RequestId: string(domain.NewID())}, SessionId: string(session.ID), ExpectedQueueGeneration: &generation, BeforeInputId: first.Id, BeforeInputRevision: first.Revision}
	moved, err := client.MoveQueuedInput(ctx, ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	if moved.Msg.CurrentQueueGeneration != generation+1 || moved.Msg.Change.Session.Revision != session.Revision+1 {
		t.Fatal(moved.Msg)
	}
	if _, err := client.ListWaitingQueue(ctx, ownerRequest(f.identity, &pb.ListWaitingQueueRequest{SessionId: string(session.ID), PageToken: list.Msg.NextPageToken})); err == nil {
		t.Fatal("movement retained old cursor")
	}
	listed, err := client.ListWaitingQueue(ctx, ownerRequest(f.identity, &pb.ListWaitingQueueRequest{SessionId: string(session.ID)}))
	if err != nil {
		t.Fatal(err)
	}
	if listed.Msg.Inputs[0].Id != second.Id || listed.Msg.Inputs[0].Revision != second.Revision {
		t.Fatal(listed.Msg)
	}
	current := listed.Msg.CurrentQueueGeneration
	noop := &pb.MoveQueuedInputRequest{Mutation: &pb.Mutation{Id: second.Id, ExpectedRevision: second.Revision, RequestId: string(domain.NewID())}, SessionId: string(session.ID), ExpectedQueueGeneration: &current, BeforeInputId: first.Id, BeforeInputRevision: first.Revision}
	result, err := client.MoveQueuedInput(ctx, ownerRequest(f.identity, noop))
	if err != nil {
		t.Fatal(err)
	}
	if result.Msg.CurrentQueueGeneration != current || result.Msg.Change.Session.Revision != moved.Msg.Change.Session.Revision {
		t.Fatal("noop changed state", result.Msg)
	}
	if _, err := client.RemoveQueuedInput(ctx, ownerRequest(f.identity, &pb.RemoveQueuedInputRequest{Mutation: &pb.Mutation{Id: second.Id, ExpectedRevision: second.Revision, RequestId: string(domain.NewID())}, SessionId: string(session.ID)})); err != nil {
		t.Fatal(err)
	}
	replay, err := client.MoveQueuedInput(ctx, ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Change.Replayed {
		t.Fatal(replay, err)
	}
	listed, err = client.ListWaitingQueue(ctx, ownerRequest(f.identity, &pb.ListWaitingQueueRequest{SessionId: string(session.ID)}))
	if err != nil || listed.Msg.WaitingCount != 1 || listed.Msg.Inputs[0].Id != first.Id {
		t.Fatal(listed, err)
	}
	malformed := &pb.MoveQueuedInputRequest{Mutation: &pb.Mutation{Id: first.Id, ExpectedRevision: first.Revision, RequestId: string(domain.NewID())}, SessionId: string(session.ID)}
	if _, err := client.MoveQueuedInput(ctx, ownerRequest(f.identity, malformed)); err == nil {
		t.Fatal("absent generation admitted")
	}
}

func TestWaitingQueueForkExplicitEmptySnapshotNeverAdoptsLaterImages(t *testing.T) {
	f, _, accepted := acceptedForkFixture(t)
	ctx := context.Background()
	row, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(accepted.Job.Id))
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](row)
	if err != nil {
		t.Fatal(err)
	}
	var input domain.ForkJobInput
	if err := domain.Decode(job.Input, &input); err != nil {
		t.Fatal(err)
	}
	upload := domain.ImageUpload{Version: 1, WorkerDeviceID: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, Attachment: domain.ImageAttachment{ID: domain.NewID(), MachineID: job.MachineID, MediaType: domain.ImagePNG, ByteLength: 1, SHA256: strings.Repeat("a", 64)}, DraftID: domain.NewID(), OperationID: domain.NewID(), MachineRevision: 1, SessionID: input.SourceSessionID, InputID: input.Completion.InputID, State: domain.ImageClaimed, UploadedBytes: 1, Owners: []domain.ID{input.SourceSessionID}, GeneratedExecutionID: input.Completion.ExecutionID}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.late-fork-image", nil, func(tx *store.Tx) (any, error) {
		raw, _ := json.Marshal(upload)
		if _, err := tx.PutJob(upload.Attachment.ID, 0, "", "", domain.Job{Type: domain.ImageAttachmentJob, State: domain.JobSucceeded, MachineID: job.MachineID, Input: raw, AcceptedAt: time.Now()}); err != nil {
			return nil, err
		}
		return nil, tx.InheritForkImagesForJob(row.ID, input)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		_, value, err := tx.ImageUploadRecord(upload.Attachment.ID)
		if err == nil && !slices.Equal(value.Owners, []domain.ID{input.SourceSessionID}) {
			t.Fatal("explicit empty snapshot adopted late image", value.Owners)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
