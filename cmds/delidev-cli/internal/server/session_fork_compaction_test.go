// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func enableForkCompaction(t *testing.T, f *continuationFixture) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.fork-compaction-capabilities", nil, func(tx *store.Tx) (any, error) {
		row, m, err := activeMachine(tx, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		capability := domain.CodexSessionCompactionV1
		if f.input.Configuration.Harness == domain.OpenCode {
			capability = domain.OpenCodeSessionCompactionV1
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeSessionCompactionV1, capability)
		return tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, m)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFirstForkTurnCompactionUsesIndependentHistory(t *testing.T) {
	for _, scenario := range []string{"independent", "independent-parent-deleted", "independent-later-turn", "sidechat"} {
		t.Run(scenario, func(t *testing.T) {
			sidechat := scenario == "sidechat"
			var f *continuationFixture
			if !sidechat {
				if scenario == "independent-parent-deleted" {
					var child *pb.Resource
					f, child, _ = publishedForkFixture(t)
					deleteForkCompactionParent(t, f)
					f.change.Session = child
					f.enqueue(t, "Child after parent deletion.", domain.ExecuteMode)
					f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
					f.claim(t)
					f.thread = domain.ID(f.input.Fork.NativeThreadID)
					f.grant(t)
				} else {
					f = claimedForkExecutionFixture(t)
				}
			} else {
				var child *pb.Resource
				f, child = publishedSidechatFixture(t)
				f.change.Session = child
				f.enqueue(t, "Read-only child input.", domain.ExecuteMode)
				f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
				f.claim(t)
				f.thread = domain.ID(f.input.Fork.NativeThreadID)
				f.grant(t)
			}
			history := f.input.Fork.RuntimeID
			if sidechat {
				event := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(f.thread), Observed: &domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "never"}}
				raw, _ := json.Marshal(event)
				if _, err := f.workerClient.PublishExecution(context.Background(), ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw})); err != nil {
					t.Fatal(err)
				}
			} else {
				f.publish(t, domain.ExecutionThreadBound, 1, "")
			}
			f.publish(t, domain.ExecutionInputAccepted, 2, "")
			f.finish(t, domain.ExecutionSucceeded)
			if scenario == "independent-later-turn" {
				f.enqueue(t, "Later child input.", domain.ExecuteMode)
				if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
					t.Fatal(err)
				}
				f.claim(t)
				f.grant(t)
				f.publish(t, domain.ExecutionThreadBound, 1, "")
				f.publish(t, domain.ExecutionInputAccepted, 2, "")
				f.finish(t, domain.ExecutionSucceeded)
				if f.input.Fork != nil || f.input.Continuation == nil || f.input.Continuation.HistoryExecutionID != history {
					t.Fatal("later child lost independent history")
				}
			}
			enableForkCompaction(t, f)
			before := f.refresh(t)
			var source domain.Job
			jobBefore, err := f.service.Store.Get(context.Background(), domain.JobKind, domain.ID(f.job.Id))
			if err != nil {
				t.Fatal(err)
			}
			_ = domain.Decode(jobBefore.Data, &source)
			contextReply, err := sessionClient(f.accountFixture).GetSessionContext(context.Background(), ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: string(before.ID)}))
			if err != nil {
				t.Fatal(err)
			}
			var view sessionContextView
			if domain.Decode(contextReply.Msg.DocumentJson, &view) != nil || !slices.Contains(contextReply.Msg.Capabilities, pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_CODEX_MANUAL_COMPACTION_V1) {
				t.Fatal("eligible first child omitted compaction")
			}
			request := &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}
			response, err := sessionClient(f.accountFixture).CompactSession(context.Background(), ownerRequest(f.identity, request))
			if err != nil || response.Msg.Job == nil {
				t.Fatal("eligible child compaction", err)
			}
			var job domain.Job
			var input domain.SessionCompactionInput
			_ = domain.Decode(response.Msg.Job.DocumentJson, &job)
			_ = domain.Decode(job.Input, &input)
			if input.Validate() != nil || input.Restore.Version != 4 || input.Restore.Fork != nil || input.Restore.Continuation.HistoryExecutionID != history || input.Restore.Continuation.HistoryExecutionID == f.input.ExecutionID {
				t.Fatal("compaction lost independent history or kept one-shot Fork")
			}
			if sidechat && (input.Restore.Configuration.SidechatPolicy != domain.CodexReadOnlySidechatV1 || input.Restore.Configuration.Options.ApprovalPolicy != "never" || !bytes.Equal(input.Restore.Manifest, f.input.Manifest)) {
				t.Fatal("Sidechat compaction loosened original restriction")
			}
			replay, err := sessionClient(f.accountFixture).CompactSession(context.Background(), ownerRequest(f.identity, request))
			if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != response.Msg.Job.Id {
				t.Fatal("compaction retry replaced job", err)
			}
			after, err := f.service.Store.Get(context.Background(), domain.JobKind, jobBefore.ID)
			if err != nil || after.Revision != jobBefore.Revision || !bytes.Equal(after.Data, jobBefore.Data) {
				t.Fatal("compaction rewrote original assignment", err)
			}
		})
	}
}

func TestSharedForkCompactionValidatorPreservesLegacyAndStartupProfiles(t *testing.T) {
	for _, harness := range []domain.Harness{domain.Codex, domain.OpenCode} {
		f := newContinuationFixtureProfile(t, domain.ExecutionSucceeded, harness)
		enableForkCompaction(t, f)
		row := f.refresh(t)
		session, err := store.Decode[domain.Session](row)
		if err != nil {
			t.Fatal(err)
		}
		var original domain.SessionCompactionInput
		if err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
			var err error
			original, err = compactionSource(tx, row, session, domain.NewID())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for _, version := range []uint32{3, 4} {
			t.Run(string(harness)+map[uint32]string{3: "-legacy", 4: "-startup"}[version], func(t *testing.T) {
				input := original
				a := input.Assignment
				a.Version = version
				if version == 3 {
					a.Startup = nil
					nativeVersion := domain.CodexProtocolVersion
					if harness == domain.OpenCode {
						nativeVersion = domain.OpenCodeProtocolVersion
					}
					a.Installation = domain.Installation{Harness: harness, State: domain.InstallationDetected, ResolvedPath: "/fixture/original-native", Version: nativeVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(harness), State: domain.ProtocolVerified}}
				}
				a.Fork = &domain.ForkExecution{JobID: domain.NewID(), RuntimeID: domain.NewID(), NativeThreadID: input.Completion.NativeThreadID, NativeTurnID: input.Completion.NativeTurnID, CheckpointDigest: strings.Repeat("cd", 32), HistoryRequestID: domain.NewID()}
				if a.Validate() != nil {
					t.Fatal("invalid original fork fixture", a.Validate())
				}
				input.Assignment = a
				input.Restore = a
				input.Restore.ExecutionID, input.Restore.InputID = input.ActionID, original.Restore.InputID
				input.Restore.ThreadRequestID, input.Restore.TurnRequestID = original.Restore.ThreadRequestID, original.Restore.TurnRequestID
				input.Restore.Version = 2
				if version == 4 {
					input.Restore.Version = 4
				}
				input.Restore.Fork = nil
				continuation := *original.Restore.Continuation
				continuation.HistoryExecutionID = a.Fork.RuntimeID
				raw, _ := json.Marshal(a)
				continuation.AssignmentInputDigest = continuationDigest(raw)
				input.Restore.Continuation = &continuation
				if err = input.Validate(); err != nil {
					t.Fatal("valid first fork compaction refused", err)
				}
				rawBefore, _ := json.Marshal(input.Assignment)
				for _, badHistory := range []domain.ID{a.ExecutionID, domain.NewID()} {
					invalid := input
					copy := *input.Restore.Continuation
					copy.HistoryExecutionID = badHistory
					invalid.Restore.Continuation = &copy
					if invalid.Validate() == nil {
						t.Fatal("foreign history root accepted")
					}
				}
				for _, field := range []string{"runtime", "checkpoint", "source"} {
					invalid := input
					fork := *a.Fork
					invalid.Assignment.Fork = &fork
					switch field {
					case "runtime":
						fork.RuntimeID = domain.NewID()
					case "checkpoint":
						fork.CheckpointDigest = ""
					case "source":
						invalid.Assignment.ExecutionID = domain.NewID()
					}
					if invalid.Validate() == nil {
						t.Fatal("changed original " + field + " accepted")
					}
				}
				invalid := input
				invalid.Restore.Fork = a.Fork
				if invalid.Validate() == nil {
					t.Fatal("one-shot Fork import reused")
				}
				rawAfter, _ := json.Marshal(input.Assignment)
				if !bytes.Equal(rawBefore, rawAfter) {
					t.Fatal("validator changed immutable source")
				}
			})
		}
	}
}

func deleteForkCompactionParent(t *testing.T, f *continuationFixture) {
	t.Helper()
	ctx := context.Background()
	source := f.refresh(t)
	deletion, err := sessionClient(f.accountFixture).DeleteSession(ctx, ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: acctMutation(resourceForTest(source), domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.workerClient.ListSessionDeletionWork(ctx, ownerRequest(f.workerIdentity, &pb.ListSessionDeletionWorkRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil || len(work.Msg.WorkJson) != 1 {
		t.Fatal(err)
	}
	var w domain.SessionDeletionWork
	_ = domain.Decode(work.Msg.WorkJson[0], &w)
	if _, err = f.workerClient.ReportSessionDeletion(ctx, ownerRequest(f.workerIdentity, &pb.ReportSessionDeletionRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, SessionId: string(source.ID), DeletionId: deletion.Msg.Job.Id, WorkDigest: w.Digest()})); err != nil {
		t.Fatal(err)
	}
	owner := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	if _, err = f.service.Store.PurgeDeletedSession(owner, source.ID); err != nil {
		t.Fatal(err)
	}
}
