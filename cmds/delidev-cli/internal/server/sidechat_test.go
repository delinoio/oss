// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func publishedSidechatFixture(t *testing.T) (*continuationFixture, *pb.Resource) {
	t.Helper()
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "test.sidechat-capability", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		m, err := store.Decode[domain.Machine](row)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexReadOnlySidechatWorkerV1)
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	parent := f.refresh(t)
	request := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(parent.ID), ExpectedRevision: parent.Revision}, ExpectedTurnId: string(f.turn), Name: "Read-only child", Purpose: pb.ForkPurpose_FORK_PURPOSE_SIDECHAT}
	accepted, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	var original workspace.PrepareRequest
	var source workspace.Manifest
	if domain.Decode(input.SourceAssignment.Preparation, &original) != nil || domain.Decode(input.SourceAssignment.Manifest, &source) != nil {
		t.Fatal("original workspace missing")
	}
	prep := original
	prep.SessionID = input.ChildSessionID
	prep.ForkSourceID = input.SourceSessionID
	prep.ForkProfile = workspace.CodexSidechatReferenceV1
	prep.SidechatSource = &workspace.SidechatSource{Preparation: original, Manifest: source}
	raw, _ := json.Marshal(prep)
	sourceRaw, _ := json.Marshal(source)
	manifest := source
	manifest.SessionID = input.ChildSessionID
	manifest.InputDigest = forkInputDigest(raw)
	manifest.CreatedAt = time.Now().UTC()
	manifest.Reference = &workspace.SidechatReference{SessionID: source.SessionID, PreparationDigest: source.InputDigest, ManifestDigest: forkInputDigest(sourceRaw), DirectoryIdentity: strings.Repeat("a", 64), MetadataIdentity: strings.Repeat("b", 64)}
	for i := range manifest.Repositories {
		manifest.Repositories[i].Owned = false
	}
	manifestRaw, _ := json.Marshal(manifest)
	result := forkResultFixture(t, input)
	result.Version = input.Version
	result.Preparation = raw
	result.Manifest = manifestRaw
	raw, _ = json.Marshal(result)
	_, err = f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	final, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: job.Id}))
	if err != nil || final.Msg.Session == nil {
		t.Fatal("child absent", err)
	}
	var child domain.Session
	if domain.Decode(final.Msg.Session.DocumentJson, &child) != nil || !child.IsSidechat() || child.Fork.Validate() != nil || child.Fork.Snapshot.Configuration.Options.Permission != domain.PermissionReadOnly || child.Fork.SidechatParentSnapshot.ConfigurationDigest != input.Snapshot.ConfigurationDigest {
		t.Fatal("snapshot enforcement missing")
	}
	replay, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Session.Id != final.Msg.Session.Id {
		t.Fatal("Sidechat creation replay", err)
	}
	return f, final.Msg.Session
}

func TestSidechatFindingsExactSelectionReplayAndParentRevision(t *testing.T) {
	f, child := publishedSidechatFixture(t)
	ctx := context.Background()
	put := func(owner domain.ID, role domain.MessageRole, state domain.MessageState, body string) store.Record {
		t.Helper()
		id := domain.NewID()
		result, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.findings", id, func(tx *store.Tx) (any, error) {
			return tx.Put(domain.MessageKind, id, 0, owner, "", domain.ExecutionMessage{ExecutionID: domain.NewID(), NativeThreadID: string(domain.NewID()), NativeTurnID: string(domain.NewID()), NativeID: "fixture", Role: role, State: state, Text: body, FirstSequence: 1, LastSequence: 2})
		})
		if err != nil {
			t.Fatal(err)
		}
		var r store.Record
		if domain.Decode(result.Data, &r) != nil {
			t.Fatal("message fixture")
		}
		return r
	}
	selected := put(domain.ID(child.Id), domain.AssistantMessage, domain.MessageComplete, "Entire selected reply")
	foreign := put(domain.ID(f.change.Session.Id), domain.AssistantMessage, domain.MessageComplete, "Parent reply")
	streaming := put(domain.ID(child.Id), domain.AssistantMessage, domain.MessageStreaming, "Unfinished reply")
	parent := f.refresh(t)
	request := &pb.SendSidechatFindingsRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: child.Id, ExpectedRevision: child.Revision}, ParentId: string(parent.ID), ExpectedParentRevision: parent.Revision, Messages: []*pb.SidechatFindingSelection{{MessageId: string(selected.ID), ExpectedRevision: selected.Revision}}}
	for _, row := range []store.Record{foreign, streaming} {
		bad := proto.Clone(request).(*pb.SendSidechatFindingsRequest)
		bad.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: child.Id, ExpectedRevision: child.Revision}
		bad.Messages = []*pb.SidechatFindingSelection{{MessageId: string(row.ID), ExpectedRevision: row.Revision}}
		if _, err := sessionClient(f.accountFixture).SendSidechatFindings(ctx, ownerRequest(f.identity, bad)); domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
			t.Fatal("foreign/incomplete findings accepted", err)
		}
	}
	result, err := sessionClient(f.accountFixture).SendSidechatFindings(ctx, ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	var queue domain.QueuedInput
	if domain.Decode(result.Msg.Change.Input.DocumentJson, &queue) != nil || queue.Prompt != "Selected Sidechat findings:\n\nEntire selected reply" {
		t.Fatal("selection changed")
	}
	replay, err := sessionClient(f.accountFixture).SendSidechatFindings(ctx, ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.Input.Id != result.Msg.Change.Input.Id {
		t.Fatal("findings receipt replay", err)
	}
	changed := proto.Clone(request).(*pb.SendSidechatFindingsRequest)
	changed.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: child.Id, ExpectedRevision: child.Revision}
	if _, err := sessionClient(f.accountFixture).SendSidechatFindings(ctx, ownerRequest(f.identity, changed)); domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatal("stale parent accepted", err)
	}
	var parentState domain.Session
	if domain.Decode(f.refresh(t).Data, &parentState) != nil || parentState.PendingInputs != 1 || parentState.Dispatch != domain.DispatchPaused {
		t.Fatal("findings inferred or duplicated")
	}
}

func TestSidechatCapacityRejectsBeforeNativeAdmissionAndSerializesLastSlot(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "last-slot", true: "full"}[full], func(t *testing.T) {
			f, _ := publishedSidechatFixture(t)
			before := f.refresh(t)
			capacity := 255
			if full {
				capacity = 256
			}
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "test.sidechat-capacity", nil, func(tx *store.Tx) (any, error) {
				ids, e := tx.SidechatDependents(before.ID)
				if e != nil {
					return nil, e
				}
				for i := len(ids); i < capacity; i++ {
					if e := tx.RegisterSidechat(before.ID, domain.NewID()); e != nil {
						return nil, e
					}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			filter := store.Filter{Kind: domain.JobKind, SessionID: before.ID, Limit: 100}
			jobs, err := f.service.Store.List(context.Background(), filter)
			if err != nil {
				t.Fatal(err)
			}
			results := make(chan error, 2)
			start := make(chan struct{})
			for i := 0; i < 2; i++ {
				go func() {
					<-start
					_, e := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, &pb.ForkSessionRequest{Mutation: &pb.Mutation{Id: string(before.ID), ExpectedRevision: before.Revision, RequestId: string(domain.NewID())}, ExpectedTurnId: string(f.turn), Name: "Capacity child", Purpose: pb.ForkPurpose_FORK_PURPOSE_SIDECHAT}))
					results <- e
				}()
			}
			close(start)
			accepted := 0
			for i := 0; i < 2; i++ {
				e := <-results
				if e == nil {
					accepted++
				} else {
					code := rpc.ClientError(e).Code
					if full && code != domain.ResourceExhausted || !full && code != domain.Conflict {
						t.Fatal("unexpected refusal", e)
					}
				}
			}
			expected := 1
			if full {
				expected = 0
			}
			afterJobs, err := f.service.Store.List(context.Background(), filter)
			after := f.refresh(t)
			if err != nil || accepted != expected || len(afterJobs) != len(jobs)+expected || after.Revision != before.Revision {
				t.Fatal("capacity admitted extra native work", accepted, len(afterJobs), err)
			}
		})
	}
}
