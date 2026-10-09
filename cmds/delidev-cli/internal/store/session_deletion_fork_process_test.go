// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func failedForkDeletionInput(t *testing.T, session, machine domain.ID) domain.ForkJobInput {
	t.Helper()
	account, provider := domain.NewID(), domain.NewID()
	model := &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: provider, NativeID: "fixture-model"}, MetadataSource: domain.UserDeclared}
	configuration, err := domain.ResolveExecutionConfiguration(domain.NewID(), 1, domain.Agent{Name: "Fixture", Harness: domain.Codex, ModelID: model.ModelIdentity.Key(), Model: model, Accounts: []domain.WeightedAccount{{ID: account, Weight: 1}}, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}}, 4, domain.Model{Name: "Fixture", NativeID: "fixture-model", ProviderID: provider, Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared}, domain.Priority, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := configuration.Digest()
	assignment := domain.ExecutionJobInput{Version: 1, SessionID: session, MachineID: machine, ExecutionID: domain.NewID(), InputID: domain.NewID(), ThreadRequestID: domain.NewID(), TurnRequestID: domain.NewID(), Configuration: configuration, ConfigurationDigest: digest, AccountID: account, ConnectionID: domain.NewID(), Input: domain.SessionInput{Mode: domain.PlanMode, Prompt: "ordinary fixture"}, Installation: domain.Installation{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified}}, Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`)}
	completion := domain.ExecutionCompletion{Version: 2, ExecutionID: assignment.ExecutionID, InputID: assignment.InputID, NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()), LastSequence: 10, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
	sourceJob := domain.NewID()
	input := domain.ForkJobInput{Version: 1, Purpose: domain.IndependentFork, SourceSessionID: session, SourceRevision: 1, ChildSessionID: domain.NewID(), RuntimeID: domain.NewID(), NativeRequestID: domain.NewID(), Name: "Rejected fork", Workspace: domain.Worktree, Actor: domain.Principal{Type: domain.OwnerDevice}, SourceJobID: sourceJob, SourceAssignment: assignment, Completion: completion, Snapshot: domain.InitialExecution{Configuration: configuration, ConfigurationDigest: digest, InitialAccountID: account, ConnectionID: assignment.ConnectionID}, Progress: domain.ExecutionProgress{JobID: sourceJob, ExecutionID: assignment.ExecutionID, InputID: assignment.InputID, LastSequence: completion.LastSequence, NativeThreadID: string(completion.NativeThreadID), NativeTurnID: string(completion.NativeTurnID), Outcome: domain.ExecutionSucceeded, CleanupVerified: true}}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	return input
}

func TestLegacyFailedForkDeletionDerivesOnlyOriginalChildOwner(t *testing.T) {
	for _, scenario := range []string{"original", "digest", "instance", "missing", "succeeded"} {
		t.Run(scenario, func(t *testing.T) {
			s, ctx, _, _, v := sessionDeletionWorkerFixture(t)
			sr, _, _ := deletionSession(t, s, "fork-source")
			v.SessionID = sr.ID
			v.ID = domain.NewID()
			v.RequestID = domain.NewID()
			v.ExpectedRevision = sr.Revision
			v.Workers[0].Work.SessionID = sr.ID
			v.Workers[0].Work.DeletionID = v.ID
			work := &v.Workers[0].Work
			input := failedForkDeletionInput(t, v.SessionID, work.MachineID)
			rawInput, _ := json.Marshal(input)
			jobID := domain.NewID()
			var original Record
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.failed-fork", nil, func(tx *Tx) (any, error) {
				job := domain.Job{Type: domain.ForkSessionJob, State: domain.JobClaimed, MachineID: work.MachineID, InstanceID: work.Copies[0].InstanceID, AssignedDeviceID: work.DeviceID, Input: rawInput, AcceptedAt: time.Now().UTC()}
				var err error
				original, err = tx.PutJob(jobID, 0, v.SessionID, "", job)
				if err != nil {
					return nil, err
				}
				job.State = domain.JobFailed
				job.Problem = domain.Fail(domain.Unsupported, "Rejected.", "Preserve the source.")
				job.FinishedAt = &tx.now
				if scenario == "succeeded" {
					job.State = domain.JobSucceeded
					job.Problem = nil
				}
				return tx.PutJob(jobID, original.Revision, v.SessionID, "", job)
			})
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(original.Data)
			copy := domain.SessionDeletionCopy{JobID: jobID, Type: domain.ForkSessionJob, Revision: original.Revision, Digest: hex.EncodeToString(hash[:]), InstanceID: work.Copies[0].InstanceID, ExecutionID: input.RuntimeID}
			switch scenario {
			case "digest":
				copy.Digest = strings.Repeat("aa", 32)
			case "instance":
				copy.InstanceID = domain.NewID()
			case "missing":
				copy.JobID = domain.NewID()
			}
			work.Copies = append(work.Copies, copy)
			before := work.Digest()
			if err := s.writeSessionDeletion(v); err != nil {
				t.Fatal(err)
			}
			items, err := s.SessionDeletions(context.Background())
			if scenario != "original" {
				if err != nil {
					t.Fatal("unresolved legacy plan blocked independent inventory", err)
				}
				retainedCheck, _ := s.readSessionDeletion(v.SessionID)
				if retainedCheck.Workers[0].Work.Copies[1].UnpublishedChildProcessID != "" {
					t.Fatal("foreign proof accepted")
				}
				retained, e := s.readSessionDeletion(v.SessionID)
				if e != nil || retained.Workers[0].Work.Digest() != before {
					t.Fatal("failed derivation changed immutable plan", e)
				}
				return
			}
			current, e := s.readSessionDeletion(v.SessionID)
			if err != nil || e != nil || len(items) != 2 || current.Workers[0].Work.Copies[1].UnpublishedChildProcessID != input.ChildSessionID {
				t.Fatal("original child omitted", err)
			}
			_, err = s.SessionDeletions(context.Background())
			again, e := s.readSessionDeletion(v.SessionID)
			if err != nil || e != nil || again.Revision != current.Revision {
				t.Fatal("derivation was not idempotent", err)
			}
		})
	}
}
