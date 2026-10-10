// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"strings"
	"testing"
)

func directoryFixture(t *testing.T) *continuationFixture {
	t.Helper()
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	f.workerStream.Close()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.directory-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.SessionDirectoryV1)
		return tx.Put(r.Kind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestSessionDirectoryOriginalReceiptProjectionAndPromotion(t *testing.T) {
	f := directoryFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	before := f.refresh(t)
	var manifest struct {
		Repositories []struct {
			ID domain.ID `json:"id"`
		} `json:"repositories"`
	}
	json.Unmarshal(f.input.Manifest, &manifest)
	repository := ""
	if len(manifest.Repositories) > 0 {
		repository = string(manifest.Repositories[0].ID)
	}
	request := &pb.ChangeSessionDirectoryRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID()), RepositoryId: repository, RelativePath: "nested"}
	accepted, err := f.service.ChangeSessionDirectory(ctx, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.ChangeSessionDirectory(ctx, connect.NewRequest(request))
	if err != nil || replay.Msg.Operation.Job.Id != accepted.Msg.Operation.Job.Id {
		t.Fatal("original receipt changed", err)
	}
	changed := *request
	changed.RelativePath = "different"
	if _, err := f.service.ChangeSessionDirectory(ctx, connect.NewRequest(&changed)); err == nil {
		t.Fatal("mismatched replay accepted")
	}
	if strings.Contains(string(accepted.Msg.Operation.Job.DocumentJson), f.input.Input.Prompt) || strings.Contains(string(accepted.Msg.Operation.Job.DocumentJson), "preparation") {
		t.Fatal("private assignment leaked")
	}
	pending := f.refresh(t)
	session, _ := store.Decode[domain.Session](pending)
	if directoryFence(session) == nil {
		t.Fatal("pending writer not fenced")
	}
	jobID := domain.ID(accepted.Msg.Operation.Job.Id)
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.directory-promotion", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.JobKind, jobID)
		if e != nil {
			return nil, e
		}
		j, e := store.Decode[domain.Job](r)
		if e != nil {
			return nil, e
		}
		var input domain.SessionDirectoryInput
		if domain.DecodeWithLimit(j.Input, &input, domain.MaxCompactionInputBytes) != nil {
			return nil, domain.DirectoryUncertain()
		}
		result := domain.SessionDirectoryResult{Version: 1, RequestID: input.RequestID, GenerationID: input.GenerationID, ExecutionID: input.Assignment.ExecutionID, CleanupVerified: true, Checkpoint: domain.SessionDirectoryRef{GenerationID: input.GenerationID, JobID: jobID, RequestID: input.RequestID, ExecutionID: input.Assignment.ExecutionID, RepositoryID: input.RepositoryID, RelativePath: input.RelativePath, CheckpointDigest: strings.Repeat("a", 64)}}
		raw, _ := json.Marshal(result)
		return finishSessionDirectory(tx, r, j, r.Revision, raw, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	current := f.refresh(t)
	promoted, _ := store.Decode[domain.Session](current)
	if promoted.Directory == nil || promoted.DirectoryJobID != "" || promoted.Dispatch != domain.DispatchReady || promoted.Execution.ExecutionID != f.input.ExecutionID {
		t.Fatal("directory changed execution history")
	}
	observed, err := f.service.GetSessionDirectoryOperation(ctx, connect.NewRequest(&pb.GetSessionDirectoryOperationRequest{SessionId: string(f.input.SessionID), RequestId: request.Mutation.RequestId}))
	if err != nil || observed.Msg.Operation.Generation == nil {
		t.Fatal("original operation unavailable", err)
	}
}

func TestSessionDirectoryAdmissionKeepsOriginalSettledBoundary(t *testing.T) {
	for name, change := range map[string]func(*domain.Session){
		"active":         func(s *domain.Session) { s.ActiveExecutionID = domain.NewID() },
		"input":          func(s *domain.Session) { s.PendingInputs = 1 },
		"steer":          func(s *domain.Session) { s.PendingSteerID = domain.NewID() },
		"context job":    func(s *domain.Session) { s.CompactionJobID = domain.NewID() },
		"directory job":  func(s *domain.Session) { s.DirectoryJobID = domain.NewID() },
		"archive":        func(s *domain.Session) { s.Archive = domain.Archived },
		"recovery":       func(s *domain.Session) { s.Recovery = domain.NeedsRecovery },
		"cleanup":        func(s *domain.Session) { s.Execution.CleanupVerified = false },
		"native waiting": func(s *domain.Session) { s.Execution.UnconfirmedResponses = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			f := directoryFixture(t)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			before := f.refresh(t)
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.directory-admission", nil, func(tx *store.Tx) (any, error) {
				s, e := store.Decode[domain.Session](before)
				if e != nil {
					return nil, e
				}
				change(&s)
				return tx.Put(domain.SessionKind, before.ID, before.Revision, before.ID, before.ProjectID, s)
			})
			if err != nil {
				t.Fatal(err)
			}
			current := f.refresh(t)
			request := &pb.ChangeSessionDirectoryRequest{Mutation: acctMutation(resourceForTest(current), domain.NewID()), RelativePath: "."}
			if _, err := f.service.ChangeSessionDirectory(ctx, connect.NewRequest(request)); err == nil {
				t.Fatal("unsafe directory admitted")
			}
		})
	}
}

func TestSessionDirectoryMetadataCannotAcquireInferenceAndLossRetainsFence(t *testing.T) {
	f := directoryFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	before := f.refresh(t)
	accepted, err := f.service.ChangeSessionDirectory(ctx, connect.NewRequest(&pb.ChangeSessionDirectoryRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID()), RelativePath: "."}))
	if err != nil {
		t.Fatal(err)
	}
	job := domain.ID(accepted.Msg.Operation.Job.Id)
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		_, e := f.service.executionAuthority.inferenceScope(tx, store.ExecutionGrant{JobID: job})
		if e == nil {
			t.Fatal("directory granted upstream inference")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.directory-lost", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.JobKind, job)
		if e != nil {
			return nil, e
		}
		j, e := store.Decode[domain.Job](r)
		if e != nil {
			return nil, e
		}
		j.State = domain.JobUncertain
		j.InstanceID = domain.ID(f.workerInstance)
		if _, e := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j); e != nil {
			return nil, e
		}
		return nil, loseSessionDirectory(tx, r, j)
	})
	if err != nil {
		t.Fatal(err)
	}
	current := f.refresh(t)
	s, _ := store.Decode[domain.Session](current)
	if s.DirectoryJobID != job || s.Directory != nil || s.Recovery != domain.NeedsRecovery || s.Dispatch != domain.DispatchPaused {
		t.Fatal("lost directory operation cleared safety fence")
	}
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		_, e := executionRecoveryRequest(tx, f.service.Identity.ServerID, current, s)
		if e == nil {
			t.Fatal("old execution recovery cleared directory uncertainty")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionDirectoryRegistersOnlyOriginalMetadataAuthority(t *testing.T) {
	f := directoryFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	before := f.refresh(t)
	accepted, err := f.service.ChangeSessionDirectory(ctx, connect.NewRequest(&pb.ChangeSessionDirectoryRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID()), RelativePath: "."}))
	if err != nil {
		t.Fatal(err)
	}
	var claimed store.Record
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.directory-claim", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.JobKind, domain.ID(accepted.Msg.Operation.Job.Id))
		if e != nil {
			return nil, e
		}
		j, e := store.Decode[domain.Job](r)
		if e != nil {
			return nil, e
		}
		j.State = domain.JobClaimed
		j.InstanceID = domain.ID(f.workerInstance)
		j.AssignedDeviceID = f.workerDevice
		claimed, e = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
		return claimed, e
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "directory fixture metadata only"
	digest := sha256.Sum256([]byte(token))
	_, err = f.workerClient.RegisterExecution(context.Background(), ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(resourceForTest(claimed), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]}))
	if err != nil {
		t.Fatal("original metadata config registration failed", err)
	}
	if lease, err := f.service.executionAuthority.Acquire(context.Background(), token); err == nil {
		lease.Release()
		t.Fatal("metadata registration granted inference")
	}
	var generation domain.ID
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		g, e := tx.ExecutionGrantForJob(claimed.ID)
		if e != nil {
			return e
		}
		scope, e := f.service.executionAuthority.scope(tx, g)
		if e != nil {
			return e
		}
		generation = scope.ExecutionID
		if scope.AccountID != f.input.AccountID || scope.ConnectionID != f.input.ConnectionID || scope.SessionID != f.input.SessionID {
			t.Fatal("directory substituted original credential authority")
		}
		return nil
	})
	if err != nil || generation == f.input.ExecutionID {
		t.Fatal("metadata scope changed or reused original inference", err)
	}
}

func TestSessionDirectoryRejectsUnverifiedReportsWithoutClearingRequest(t *testing.T) {
	for name, edit := range map[string]func(*domain.SessionDirectoryResult){
		"cleanup":             func(r *domain.SessionDirectoryResult) { r.CleanupVerified = false },
		"generation":          func(r *domain.SessionDirectoryResult) { r.GenerationID = domain.NewID() },
		"directory":           func(r *domain.SessionDirectoryResult) { r.Checkpoint.RelativePath = "different" },
		"previous generation": func(r *domain.SessionDirectoryResult) { r.Checkpoint.PreviousGenerationID = domain.NewID() },
	} {
		t.Run(name, func(t *testing.T) {
			f := directoryFixture(t)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			before := f.refresh(t)
			request := domain.NewID()
			accepted, err := f.service.ChangeSessionDirectory(ctx, connect.NewRequest(&pb.ChangeSessionDirectoryRequest{Mutation: acctMutation(resourceForTest(before), request), RelativePath: "."}))
			if err != nil {
				t.Fatal(err)
			}
			jobID := domain.ID(accepted.Msg.Operation.Job.Id)
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.directory-invalid-result", nil, func(tx *store.Tx) (any, error) {
				r, e := tx.Get(domain.JobKind, jobID)
				if e != nil {
					return nil, e
				}
				j, e := store.Decode[domain.Job](r)
				if e != nil {
					return nil, e
				}
				var input domain.SessionDirectoryInput
				if domain.DecodeWithLimit(j.Input, &input, domain.MaxCompactionInputBytes) != nil {
					return nil, domain.DirectoryUncertain()
				}
				result := domain.SessionDirectoryResult{Version: 1, RequestID: request, GenerationID: input.GenerationID, ExecutionID: input.Assignment.ExecutionID, CleanupVerified: true, Checkpoint: domain.SessionDirectoryRef{GenerationID: input.GenerationID, JobID: jobID, RequestID: request, ExecutionID: input.Assignment.ExecutionID, RelativePath: ".", CheckpointDigest: strings.Repeat("a", 64)}}
				edit(&result)
				raw, _ := json.Marshal(result)
				return finishSessionDirectory(tx, r, j, r.Revision, raw, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
			current := f.refresh(t)
			s, _ := store.Decode[domain.Session](current)
			if s.Directory != nil || s.DirectoryJobID != jobID || s.Recovery != domain.NeedsRecovery || s.Execution.ExecutionID != f.input.ExecutionID {
				t.Fatal("invalid report promoted a directory or cleared original ownership")
			}
		})
	}
}

func TestSessionDirectoryContinuationFreezesPredecessorSeparately(t *testing.T) {
	f := directoryFixture(t)
	r := f.refresh(t)
	s, _ := store.Decode[domain.Session](r)
	current := domain.SessionDirectoryRef{GenerationID: domain.NewID(), JobID: domain.NewID(), RequestID: domain.NewID(), ExecutionID: f.input.ExecutionID, RelativePath: ".", CheckpointDigest: strings.Repeat("a", 64)}
	s.Directory = &current
	var completion domain.ExecutionCompletion
	job, _ := f.service.Store.Get(context.Background(), domain.JobKind, s.Execution.JobID)
	j, _ := store.Decode[domain.Job](job)
	json.Unmarshal(j.Output, &completion)
	candidate := continuationAssignment(s, f.input, completion, continuationDigest(j.Input), domain.ContinueAutomatically, f.input.AccountID, f.input.ConnectionID)
	candidate.Directory = s.Directory
	if candidate.Continuation.PreviousDirectory != nil || candidate.Directory != s.Directory {
		t.Fatal("promoted current generation rewrote a nil predecessor generation")
	}
	predecessor := current
	predecessor.GenerationID = domain.NewID()
	f.input.Directory = &predecessor
	candidate = continuationAssignment(s, f.input, completion, continuationDigest(j.Input), domain.ContinueAutomatically, f.input.AccountID, f.input.ConnectionID)
	candidate.Directory = s.Directory
	if !sameDirectoryRef(candidate.Continuation.PreviousDirectory, &predecessor) || sameDirectoryRef(candidate.Continuation.PreviousDirectory, candidate.Directory) {
		t.Fatal("different current and predecessor generations were conflated")
	}
}
