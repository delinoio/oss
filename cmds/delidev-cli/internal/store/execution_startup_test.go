package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func rejectedStartupTestDigest(raw []byte) string {
	d := sha256.Sum256(raw)
	return hex.EncodeToString(d[:])
}

func TestRemediationReleasesOnlyOriginalAcceptedStartupRejection(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "changed-job-output"}[corrupt], func(t *testing.T) {
			s, _ := openTest(t)
			f := newRemediationStoreFixture(t, s)
			reserved, err := f.reserve(t, domain.PRRemediationAutomatic)
			if err != nil {
				t.Fatal(err)
			}
			bound := f.bind(t, reserved)
			started, _, err := f.start(t, bound, domain.NewID(), f.observation)
			if err != nil {
				t.Fatal(err)
			}
			var jobID domain.ID
			// Model only the server's accepted report transaction here. Actual
			// workspace phase proof and authenticated acceptance have separate
			// workspace/Worker/HTTP tests; absence alone never creates this state.
			_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.accept-startup-rejection", nil, func(tx *Tx) (any, error) {
				_, v, err := tx.GetPRRemediationAttempt(started.ID)
				if err != nil {
					return nil, err
				}
				sr, err := tx.Get(domain.SessionKind, v.SessionID)
				if err != nil {
					return nil, err
				}
				session, _ := Decode[domain.Session](sr)
				ir, err := tx.Get(domain.QueueKind, v.InputID)
				if err != nil {
					return nil, err
				}
				queued, _ := Decode[domain.QueuedInput](ir)
				claim := session.InitialExecution
				input := domain.ExecutionJobInput{Version: 1, SessionID: sr.ID, MachineID: session.MachineID, ExecutionID: v.ExecutionID, InputID: v.InputID, ThreadRequestID: domain.NewID(), TurnRequestID: queued.NativeRequestID, Configuration: claim.Configuration, ConfigurationDigest: claim.ConfigurationDigest, AccountID: claim.InitialAccountID, ConnectionID: claim.ConnectionID, Input: domain.SessionInput{Prompt: queued.Prompt, Mode: queued.Mode}, Installation: domain.Installation{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified}}, Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`)}
				inputRaw, _ := json.Marshal(input)
				job := domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: input.MachineID, InstanceID: domain.NewID(), Input: inputRaw, AcceptedAt: tx.now}
				jobID = domain.NewID()
				jr, err := tx.PutJob(jobID, 0, sr.ID, sr.ProjectID, job)
				if err != nil {
					return nil, err
				}
				proof := domain.PRStartupRejectionProof{JobID: jobID, ExecutionID: input.ExecutionID, SessionID: sr.ID, PreparationDigest: strings.Repeat("a", 64), ManifestDigest: strings.Repeat("b", 64), TargetDigest: strings.Repeat("c", 64), JournalDigest: strings.Repeat("d", 64), Reason: domain.Conflict, StartedAt: tx.now, FinishedAt: tx.now}
				rejected := domain.ExecutionStartupRejection{Version: 1, Type: domain.PRStartupRejectedResult, ServerID: domain.NewID(), DeviceID: domain.NewID(), InstanceID: job.InstanceID, MachineID: input.MachineID, InputID: input.InputID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, AssignmentRevision: jr.Revision, AssignmentDigest: rejectedStartupTestDigest(jr.Data), AssignmentInputDigest: rejectedStartupTestDigest(inputRaw), ConfigurationDigest: input.ConfigurationDigest, Workspace: proof}
				if err := rejected.ValidateAssignment(jr.ID, jr.Revision, jr.Data); err != nil {
					return nil, err
				}
				job.State, job.FinishedAt, job.Problem = domain.JobFailed, &tx.now, domain.Fail(domain.Conflict, "Fixture rejection", "Preserve original input.")
				job.Output, _ = json.Marshal(rejected)
				if _, err := tx.PutJob(jr.ID, jr.Revision, sr.ID, sr.ProjectID, job); err != nil {
					return nil, err
				}
				session.StartupRejection = &rejected
				session.ActiveExecutionID, session.Dispatch = "", domain.DispatchPaused
				session.Outcome = domain.ExecutionNotStarted
				session.PendingInputs--
				session.PendingInputBytes -= uint64(len(queued.Prompt))
				queued.Delivery = domain.InputRejected
				if _, err := tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, queued); err != nil {
					return nil, err
				}
				return tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.corrupt-rejection", nil, func(tx *Tx) (any, error) {
					r, err := tx.Get(domain.JobKind, jobID)
					if err != nil {
						return nil, err
					}
					job, _ := Decode[domain.Job](r)
					var rejected domain.ExecutionStartupRejection
					if err := domain.Decode(job.Output, &rejected); err != nil {
						return nil, err
					}
					rejected.Workspace.Reason = domain.Canceled
					job.Output, _ = json.Marshal(rejected)
					return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.finish-rejected-attempt", nil, func(tx *Tx) (any, error) { return tx.FinishPRRemediation(started.ID, started.Revision) })
			if corrupt != (err != nil) {
				t.Fatal("unexpected attempt settlement", err)
			}
			err = s.Read(notificationOwner(), func(tx *Tx) error {
				_, attempt, err := tx.GetPRRemediationAttempt(started.ID)
				if err != nil {
					return err
				}
				_, set, err := tx.GetPRProblemSet(f.set.ID)
				if err != nil {
					return err
				}
				if set.Remediation.AutomaticAttempts != 1 {
					t.Fatal("startup rejection reset the automatic attempt budget")
				}
				if corrupt {
					if !attempt.State.Active() || set.Remediation.ActiveAttemptID != started.ID {
						t.Fatal("corrupt evidence released the original attempt")
					}
				} else {
					if attempt.State != domain.PRRemediationFinished || attempt.Outcome != domain.ExecutionNotStarted || attempt.StartupRejectionJobID != jobID || set.Remediation.ActiveAttemptID != "" || attempt.Validate() != nil {
						t.Fatal("pre-native rejection became a native outcome or lost its original reference")
					}
					attempt.StartupRejectionJobID = ""
					if attempt.Validate() == nil {
						t.Fatal("missing rejection reference granted finished authority")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
