// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestTextQueueRetainsAdmissionWithoutImageWorkerAuthority(t *testing.T) {
	for _, change := range []string{"disabled-machine", "revoked-worker", "missing-worker"} {
		t.Run(change, func(t *testing.T) {
			f, _, raw := imageRPCFixture(t)
			_, session := createSessionFixture(t, f.accountFixture, f.selection)
			client := sessionClient(f.accountFixture)
			ctx := context.Background()
			actorCtx := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
			operation := domain.NewID()
			staged := domain.ImageUpload{
				Version:    1,
				Attachment: domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.ID(f.machine.Id), MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: imageinput.Digest(raw)},
				Actor:      domain.Principal{Type: domain.OwnerDevice}, WorkerDeviceID: f.workerDevice,
				DraftID: domain.NewID(), OperationID: operation, MachineRevision: f.machine.Revision,
				SessionID: domain.ID(session.Session.Id), State: domain.ImageReady, UploadedBytes: uint64(len(raw)),
			}
			_, err := f.service.Store.Mutate(actorCtx, domain.NewID(), "fixture.text-queue-authority", nil, func(tx *store.Tx) (any, error) {
				if _, err := putImageUpload(tx, store.Record{}, staged); err != nil {
					return nil, err
				}
				kind, id := domain.DeviceKind, f.workerDevice
				if change == "disabled-machine" {
					kind, id = domain.MachineKind, domain.ID(f.machine.Id)
				}
				row, err := tx.Get(kind, id)
				if err != nil {
					return nil, err
				}
				if change == "missing-worker" {
					return nil, tx.Delete(kind, id, row.Revision)
				}
				var value any
				if change == "disabled-machine" {
					machine, err := store.Decode[domain.Machine](row)
					if err != nil {
						return nil, err
					}
					machine.Disabled = true
					value = machine
				} else {
					device, err := store.Decode[domain.Device](row)
					if err != nil {
						return nil, err
					}
					device.Revoked = true
					value = device
				}
				return tx.Put(kind, id, row.Revision, row.SessionID, row.ProjectID, value)
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, explicitEmpty := range []bool{false, true} {
				document, _ := json.Marshal(domain.SessionInput{Prompt: "retained text queue", Mode: domain.PlanMode})
				request := &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: session.Session.Id, DocumentJson: document}
				if explicitEmpty {
					request.Attachments = []*pb.ImageAttachment{}
				}
				accepted, err := client.EnqueueInput(ctx, ownerRequest(f.identity, request))
				if err != nil {
					t.Fatalf("text-only input rejected (explicit empty=%v): %v", explicitEmpty, err)
				}
				retry, err := client.EnqueueInput(ctx, ownerRequest(f.identity, request))
				if err != nil {
					t.Fatal(err)
				}
				if !retry.Msg.Change.Replayed || retry.Msg.Change.Input.Id != accepted.Msg.Change.Input.Id || !reflect.DeepEqual(inputBody(t, retry.Msg.Change.Input), inputBody(t, accepted.Msg.Change.Input)) {
					t.Fatal("exact text retry changed its accepted input")
				}
				input := inputBody(t, accepted.Msg.Change.Input)
				if input.Prompt != "retained text queue" || input.Mode != domain.PlanMode || len(input.Attachments) != 0 {
					t.Fatal("text acceptance changed", input)
				}
			}
			document, _ := json.Marshal(domain.SessionInput{Prompt: "image requires original Worker", Mode: domain.PlanMode})
			if _, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(operation), SessionId: session.Session.Id, DocumentJson: document, Attachments: []*pb.ImageAttachment{imageinput.ToProto(staged.Attachment)}})); err == nil {
				t.Fatal("nonempty image accepted without original Worker authority")
			}
			if err := f.service.Store.Read(actorCtx, func(tx *store.Tx) error {
				_, current, err := imageUploadRecord(tx, staged.Attachment.ID)
				if err == nil && !reflect.DeepEqual(current, staged) {
					t.Fatal("rejected image changed staging ownership", current)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			queue, err := client.ListQueue(ctx, ownerRequest(f.identity, &pb.ListQueueRequest{SessionId: session.Session.Id}))
			if err != nil || len(queue.Msg.Inputs) != 3 {
				t.Fatalf("rejected images or retries appended input: %v", err)
			}
		})
	}
}
