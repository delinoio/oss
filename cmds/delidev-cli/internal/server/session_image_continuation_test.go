// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"testing"
	"time"
)

// Transfer actual private bytes, then publish controlled native observations.
// This fixture never launches an installed harness or requests inference.
func newImageContinuationFixture(t *testing.T, images bool) (*continuationFixture, delidevv1connect.AttachmentServiceClient, []byte) {
	t.Helper()
	base, client, raw := imageRPCFixture(t)
	imageWorkerFixture(t, base, client)
	operation := domain.NewID()
	var attachments []*pb.ImageAttachment
	if images {
		upload := finishImage(t, base, client, beginImage(t, base, client, raw, domain.NewID(), operation, ""), raw)
		attachments = []*pb.ImageAttachment{upload.Attachment}
	}
	doc, _ := json.Marshal(base.selection)
	response, err := sessionClient(base.accountFixture).CreateSession(context.Background(), ownerRequest(base.identity, &pb.CreateSessionRequest{RequestId: string(operation), DocumentJson: doc, Attachments: attachments}))
	if err != nil {
		t.Fatal(err)
	}
	base.change = response.Msg.Change
	if !base.workerStream.Receive() || base.workerStream.Msg().Job == nil {
		t.Fatal("missing image session preparation")
	}
	preparation := base.workerStream.Msg().Job
	var job domain.Job
	var request workspace.PrepareRequest
	if domain.Decode(preparation.DocumentJson, &job) != nil || domain.Decode(job.Input, &request) != nil {
		t.Fatal("invalid preparation")
	}
	manager := workspace.Manager{Root: base.workerRoot}
	manifest, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(manifest)
	if _, err := base.workerClient.ReportWork(context.Background(), ownerRequest(base.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(preparation, domain.NewID()), MachineId: base.machine.Id, InstanceId: base.workerInstance, OutputJson: document})); err != nil {
		t.Fatal(err)
	}

	f := &continuationFixture{firstDispatchFixture: base, thread: domain.NewID()}
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	return f, client, raw
}
func enqueueImageContinuation(t *testing.T, f *continuationFixture, client delidevv1connect.AttachmentServiceClient, raw []byte, prompt string) domain.SessionInput {
	t.Helper()
	operation, draft := domain.NewID(), domain.NewID()
	first := finishImage(t, f.firstDispatchFixture, client, beginImage(t, f.firstDispatchFixture, client, raw, draft, operation, f.change.Session.Id), raw)
	second := finishImage(t, f.firstDispatchFixture, client, beginImage(t, f.firstDispatchFixture, client, raw, draft, operation, f.change.Session.Id), raw)
	doc, _ := json.Marshal(domain.SessionInput{Prompt: prompt, Mode: domain.ExecuteMode})
	reply, err := sessionClient(f.accountFixture).EnqueueInput(context.Background(), ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(operation), SessionId: f.change.Session.Id, DocumentJson: doc, Attachments: []*pb.ImageAttachment{second.Attachment, first.Attachment}}))
	if err != nil {
		t.Fatal(err)
	}
	var queued domain.QueuedInput
	if domain.Decode(reply.Msg.Change.Input.DocumentJson, &queued) != nil {
		t.Fatal("invalid image queue")
	}
	return queuedSessionInput(queued)
}
func TestImageContinuationPreservesCompleteInputs(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		primary bool
		next    string
		images  bool
	}{{"image-to-text", true, "next", false}, {"image-to-image", true, "next", true}, {"text-to-image", false, "next", true}, {"text-to-image-only", false, "", true}} {
		t.Run(scenario.name, func(t *testing.T) {
			f, client, raw := newImageContinuationFixture(t, scenario.primary)
			f.complete(t, domain.ExecutionSucceeded)
			original := f.input
			expected := domain.SessionInput{Prompt: scenario.next, Mode: domain.ExecuteMode}
			if scenario.images {
				expected = enqueueImageContinuation(t, f, client, raw, scenario.next)
			} else {
				f.enqueue(t, scenario.next, domain.ExecuteMode)
			}
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			f.claim(t)
			if !f.input.Input.Equal(expected) || f.input.Continuation.PromptDigest != domain.BindSessionInput(original.InputID, original.Input).PromptDigest {
				t.Fatal("lost original or ordered successor identity")
			}
			f.complete(t, domain.ExecutionSucceeded)
			f.enqueue(t, "final text", domain.ExecuteMode)
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
				t.Fatal("image successor cannot continue", err)
			}
		})
	}
}
func TestImageCompactionRetainsOriginalDigest(t *testing.T) {
	f, _, _ := newImageContinuationFixture(t, true)
	f.complete(t, domain.ExecutionSucceeded)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.image-compaction-capability", nil, func(tx *store.Tx) (any, error) {
		row, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.NativeSessionCompactionV1, domain.CodexSessionCompactionV1)
		return tx.Put(row.Kind, row.ID, row.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		row, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		input, err := compactionSource(tx, row, session, domain.NewID())
		if err != nil {
			return err
		}
		if !input.Assignment.Input.Equal(f.input.Input) || input.Restore.Continuation.PromptDigest != domain.BindSessionInput(f.input.InputID, f.input.Input).PromptDigest {
			t.Fatal("compaction omitted original images")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestImageOriginalRecoveryRetainsDigest(t *testing.T) {
	f, _, _ := newImageContinuationFixture(t, true)
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	f.publish(t, domain.ExecutionTurnFinished, 3, domain.ExecutionSucceeded)
	f.workerStream.Close()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.image-worker-lost", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(domain.ID(f.machine.Id), domain.ID(f.workerInstance), time.Now().UTC().Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	f.workerInstance = string(domain.NewID())
	if _, err := f.workerClient.AttachWorker(context.Background(), ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Version: rpc.Version})); err != nil {
		t.Fatal(err)
	}
	row := f.refresh(t)
	response, err := sessionClient(f.accountFixture).RecoverSessionExecution(context.Background(), ownerRequest(f.identity, &pb.RecoverSessionExecutionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), ExpectedExecutionId: string(f.input.ExecutionID)}))
	if err != nil {
		t.Fatal(err)
	}
	var job domain.Job
	var input domain.ExecutionRecoveryRequest
	if domain.Decode(response.Msg.Change.ExecutionRecoveryJob.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil || input.PromptDigest != domain.BindSessionInput(f.input.InputID, f.input.Input).PromptDigest {
		t.Fatal("recovery replaced original image identity")
	}
}
func TestImageContinuationRejectsChangedReferences(t *testing.T) {
	for _, scenario := range []string{"reference", "digest", "order"} {
		t.Run(scenario, func(t *testing.T) {
			f, client, raw := newImageContinuationFixture(t, false)
			f.complete(t, domain.ExecutionSucceeded)
			enqueueImageContinuation(t, f, client, raw, "two images")
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			f.claim(t)
			f.complete(t, domain.ExecutionSucceeded)
			f.enqueue(t, "retained next", domain.ExecuteMode)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.image-tamper", scenario, func(tx *store.Tx) (any, error) {
				row, err := tx.Get(domain.QueueKind, f.input.InputID)
				if err != nil {
					return nil, err
				}
				queued, err := store.Decode[domain.QueuedInput](row)
				if err != nil {
					return nil, err
				}
				switch scenario {
				case "reference":
					queued.Attachments[0].ID = domain.NewID()
				case "digest":
					queued.Attachments[0].SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				case "order":
					queued.Attachments[0], queued.Attachments[1] = queued.Attachments[1], queued.Attachments[0]
				}
				return tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, queued)
			})
			if err != nil {
				t.Fatal(err)
			}
			before := f.refresh(t)
			if err := f.service.dispatchExecution(context.Background(), before); err == nil {
				t.Fatal("changed images granted continuation")
			}
			after, _ := store.Decode[domain.Session](f.refresh(t))
			prior, _ := store.Decode[domain.Session](before)
			if after.ExecutionSelection() != prior.ExecutionSelection() || after.ActiveExecutionID != prior.ActiveExecutionID || after.PendingInputs != prior.PendingInputs || after.Execution.ExecutionID != prior.Execution.ExecutionID {
				t.Fatal("rejection consumed ownership")
			}
		})
	}
}

func TestImageForkChildDispatchRetainsOrderedAttachments(t *testing.T) {
	f, client, raw := newImageContinuationFixture(t, false)
	f.complete(t, domain.ExecutionSucceeded)
	f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
	row := f.refresh(t)
	accepted, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, &pb.ForkSessionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), ExpectedTurnId: string(f.turn), Name: "Image child"}))
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	output, _ := json.Marshal(forkResultFixture(t, input))
	if _, err := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: output})); err != nil {
		t.Fatal(err)
	}
	child, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: job.Id}))
	if err != nil {
		t.Fatal(err)
	}
	f.change.Session = child.Msg.Session
	expected := enqueueImageContinuation(t, f, client, raw, "")
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	if !f.input.Input.Equal(expected) || f.input.Fork == nil {
		t.Fatal("child lost ordered image-only input")
	}
	f.thread = domain.ID(f.input.Fork.NativeThreadID)
	f.complete(t, domain.ExecutionSucceeded)
}

func TestImageExecutionAuthorityRejectsChangedCompleteInput(t *testing.T) {
	for _, scenario := range []string{"attachment", "skill"} {
		t.Run(scenario, func(t *testing.T) {
			f, _, _ := newImageContinuationFixture(t, true)
			var original domain.QueuedInput
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.image-claimed-tamper", scenario, func(tx *store.Tx) (any, error) {
				row, err := tx.Get(domain.QueueKind, f.input.InputID)
				if err != nil {
					return nil, err
				}
				queued, err := store.Decode[domain.QueuedInput](row)
				if err != nil {
					return nil, err
				}
				original = queued
				if scenario == "attachment" {
					queued.Attachments = append([]domain.ImageAttachment(nil), queued.Attachments...)
					queued.Attachments[0].ID = domain.NewID()
				} else {
					queued.Skills = []domain.SkillBinding{{WorkerDeviceID: domain.NewID(), InventoryID: domain.NewID(), SkillID: domain.NewID(), SnapshotID: domain.NewID(), ContentRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
				}
				return tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, queued)
			})
			if err != nil {
				t.Fatal(err)
			}
			before := f.refresh(t)
			err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				grant, err := tx.ExecutionGrantForJob(domain.ID(f.job.Id))
				if err != nil {
					return err
				}
				_, err = f.service.executionAuthority.scope(tx, grant)
				return err
			})
			if err == nil {
				t.Fatal("changed complete input granted native API authority")
			}
			if f.refresh(t).Revision != before.Revision {
				t.Fatal("authority check changed session")
			}
			event := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(f.thread), Observed: &domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
			raw, _ := json.Marshal(event)
			if _, err := f.workerClient.PublishExecution(context.Background(), ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw})); err == nil {
				t.Fatal("changed input granted native publication authority")
			}
			if f.refresh(t).Revision != before.Revision {
				t.Fatal("rejected publication changed session")
			}

			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.restore-image-claim", nil, func(tx *store.Tx) (any, error) {
				row, err := tx.Get(domain.QueueKind, f.input.InputID)
				if err != nil {
					return nil, err
				}
				return tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, original)
			})
			if err != nil {
				t.Fatal(err)
			}
			f.complete(t, domain.ExecutionSucceeded)
		})
	}
}
