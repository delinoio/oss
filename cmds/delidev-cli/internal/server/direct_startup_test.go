// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func directStartupFixture(t *testing.T, configure ...func(*domain.ExecutionJobInput)) *authorityFixture {
	t.Helper()
	f := newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", func(i *domain.ExecutionJobInput) {
		i.Version = 4
		i.Startup = &domain.ExecutionStartupSelection{Harness: domain.Codex}
		i.Installation = domain.Installation{}
		if len(configure) > 0 {
			configure[0](i)
		}
	}, false)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.direct-startup", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = []domain.WorkerCapability{domain.InlineModelExecutionV1, domain.ExecutionStartupV1, domain.BranchPrefixInstructionsV1}
		// No installation inspection exists on this machine.
		if _, err = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine); err != nil {
			return nil, err
		}
		ir, err := tx.Get(domain.QueueKind, f.input.InputID)
		if err != nil {
			return nil, err
		}
		q, err := store.Decode[domain.QueuedInput](ir)
		if err != nil {
			return nil, err
		}
		q.Delivery = domain.InputClaimed
		q.Skills = f.input.Input.Skills
		q.Attachments = f.input.Input.Attachments
		if _, err = tx.Put(domain.QueueKind, ir.ID, ir.Revision, ir.SessionID, "", q); err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.PendingInputs, session.PendingInputBytes = 1, uint64(len(q.Prompt))
		_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, "", session)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func startupRequest(f *authorityFixture, o domain.ExecutionStartupObservation) *connect.Request[pb.ReportExecutionStartupRequest] {
	r := connect.NewRequest(&pb.ReportExecutionStartupRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Observation: rpc.StartupMessage(o)})
	r.Header().Set("Authorization", "Bearer "+f.workerToken)
	return r
}

func TestDirectStartupGatesRelayAndRetainsExactReplay(t *testing.T) {
	f := directStartupFixture(t)
	legacy := domain.SessionStartPreparation{Phase: domain.StartWaitingDispatch, DiscoveryJobID: domain.NewID()}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.legacy-preparation", nil, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		// A retained waiting observation cannot bypass actual process startup.
		session.StartPreparation = &legacy
		return tx.Put(domain.SessionKind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.registerGrant(t)
	if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		lease.Release()
		t.Fatal("initializing assignment acquired inference authority")
	}
	o := domain.ExecutionStartupObservation{State: domain.StartupReady, Phase: domain.StartupSettings, Harness: domain.Codex, NativeVersion: "0.150.9", ExecutableSHA256: strings.Repeat("a", 64), Protocol: domain.CodexAppServer, CorrelationID: f.job, InputDelivery: domain.StartupNotSent}
	req := startupRequest(f, o)
	first, err := f.client.ReportExecutionStartup(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Msg.Replayed {
		t.Fatal("first report was replayed")
	}
	second, err := f.client.ReportExecutionStartup(context.Background(), req)
	if err != nil || !second.Msg.Replayed {
		t.Fatalf("exact replay: %v", err)
	}
	record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](record)
	if err != nil {
		t.Fatal(err)
	}
	if session.Execution != nil && session.Execution.TurnTiming != nil {
		t.Fatal("acknowledged READY fabricated accepted-turn timing")
	}
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	o.State, o.ProblemCode, o.Cleanup = domain.StartupFailed, domain.Unsupported, domain.StartupCleanupConfirmed
	o.Phase = domain.StartupInitialize
	if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, o)); err != nil {
		t.Fatal(err)
	}
	if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		lease.Release()
		t.Fatal("failed startup retained inference authority")
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		r, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return err
		}
		if r.Revision != 1 {
			t.Fatal("startup report rewrote immutable assignment revision")
		}
		_, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		if s.Startup.Ready.NativeVersion != "0.150.9" || s.Startup.Failure.ProblemCode != domain.Unsupported {
			t.Fatal("ready evidence or first failure lost")
		}
		if s.StartPreparation == nil || *s.StartPreparation != legacy {
			t.Fatal("startup publication changed historical observation metadata")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDirectStartupRejectsForeignReportsAndSettlesNoInputFailure(t *testing.T) {
	f := directStartupFixture(t)
	o := domain.ExecutionStartupObservation{State: domain.StartupFailed, Phase: domain.StartupResolve, Harness: domain.Codex, ProblemCode: domain.NotFound, CorrelationID: f.job, InputDelivery: domain.StartupNotSent, Cleanup: domain.StartupCleanupConfirmed}
	for _, change := range []string{"revision", "instance", "job", "secret"} {
		r := startupRequest(f, o)
		switch change {
		case "revision":
			r.Msg.Mutation.ExpectedRevision++
		case "instance":
			r.Msg.InstanceId = string(domain.NewID())
		case "job":
			r.Msg.Observation.CorrelationId = string(domain.NewID())
		case "secret":
			r.Msg.Observation.NativeVersion = "secret/native/path"
		}
		if _, err := f.client.ReportExecutionStartup(context.Background(), r); err == nil {
			t.Fatalf("accepted foreign %s", change)
		}
	}
	if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, o)); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.finish-startup", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		j, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		return finishNativeExecution(tx, r, j, r.Revision, nil, domain.Fail(domain.NotFound, "Missing executable.", "Install it."))
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		if s.Execution != nil && s.Execution.TurnTiming != nil {
			t.Fatal("confirmed pre-send rejection fabricated accepted-turn timing")
		}
		if s.ActiveExecutionID != "" || s.PendingInputs != 0 || s.Recovery != domain.NoRecovery || s.Dispatch != domain.DispatchPaused {
			t.Fatal("positive no-send proof was not settled")
		}
		r, err := tx.Get(domain.QueueKind, f.input.InputID)
		if err != nil {
			return err
		}
		q, err := store.Decode[domain.QueuedInput](r)
		if err != nil {
			return err
		}
		if q.Delivery != domain.InputRejected {
			t.Fatal("failed original input was not retained as rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDirectStartupExplicitRetryRetainsOriginalSelectionAndReceipt(t *testing.T) {
	for _, total := range []int{4094, 4095} {
		name := "last execution plus recovery slots"
		if total == 4095 {
			name = "insufficient recovery slot"
		}
		t.Run(name, func(t *testing.T) {
			f := newFirstDispatchFixture(t)
			ctx := context.Background()
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.no-inspection", nil, func(tx *store.Tx) (any, error) {
				mr, machine, err := activeMachine(tx, domain.ID(f.machine.Id))
				if err != nil {
					return nil, err
				}
				machine.Installations = nil
				return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
			})
			if err != nil {
				t.Fatal(err)
			}
			sr, err := f.service.Store.Get(ctx, domain.SessionKind, domain.ID(f.change.Session.Id))
			if err != nil {
				t.Fatal(err)
			}
			if err = f.service.dispatchExecution(ctx, sr); err != nil {
				t.Fatal(err)
			}
			if !f.workerStream.Receive() || f.workerStream.Msg().Job == nil {
				t.Fatal("missing direct assignment", f.workerStream.Err())
			}
			original := f.workerStream.Msg().Job
			var job domain.Job
			var input domain.ExecutionJobInput
			if domain.Decode(original.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil || input.Version != 4 || input.Installation.ResolvedPath != "" {
				t.Fatal("inspection evidence was required")
			}
			o := domain.ExecutionStartupObservation{State: domain.StartupFailed, Phase: domain.StartupResolve, Harness: domain.Codex, ProblemCode: domain.NotFound, CorrelationID: domain.ID(original.Id), InputDelivery: domain.StartupNotSent, Cleanup: domain.StartupCleanupConfirmed}
			if _, err = f.workerClient.ReportExecutionStartup(ctx, ownerRequest(f.workerIdentity, &pb.ReportExecutionStartupRequest{Mutation: acctMutation(original, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Observation: rpc.StartupMessage(o)})); err != nil {
				t.Fatal(err)
			}
			if _, err = f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(original, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Problem: &pb.ErrorDetail{Code: string(domain.NotFound)}})); err != nil {
				t.Fatal(err)
			}
			sr, err = f.service.Store.Get(ctx, domain.SessionKind, domain.ID(f.change.Session.Id))
			if err != nil {
				t.Fatal(err)
			}
			padExecutionHistory(t, f.service.Store, sr.ID, domain.ID(f.machine.Id), total)
			request := &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}
			if total == 4095 {
				before, err := f.service.Store.List(ctx, store.Filter{Kind: domain.QueueKind, SessionID: sr.ID, Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, request)); err == nil || !strings.Contains(err.Error(), string(domain.ResourceExhausted)) {
					t.Fatal("retry without recovery headroom", err)
				}
				after, err := f.service.Store.List(ctx, store.Filter{Kind: domain.QueueKind, SessionID: sr.ID, Limit: 100})
				if err != nil || len(after) != len(before) || executionHistoryCount(t, f.service.Store, sr.ID) != total {
					t.Fatal("rejected startup wrote input/job", err)
				}
				return
			}
			for range 2 {
				if _, err = sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, request)); err != nil {
					t.Fatal(err)
				}
			}
			if !f.workerStream.Receive() || f.workerStream.Msg().Job == nil {
				t.Fatal("missing explicit retry", f.workerStream.Err())
			}
			retry := f.workerStream.Msg().Job
			var retryJob domain.Job
			var next domain.ExecutionJobInput
			if domain.Decode(retry.DocumentJson, &retryJob) != nil || domain.Decode(retryJob.Input, &next) != nil || next.Validate() != nil {
				t.Fatal("invalid retry assignment")
			}
			if retry.Id == original.Id || next.ExecutionID == input.ExecutionID || next.InputID == input.InputID || next.Retry == nil || next.Retry.JobID != domain.ID(original.Id) || !next.Input.Equal(input.Input) || next.ConfigurationDigest != input.ConfigurationDigest || next.AccountID != input.AccountID || next.ConnectionID != input.ConnectionID || next.MachineID != input.MachineID || *next.Startup != *input.Startup {
				t.Fatal("retry replaced original selection or input")
			}
			sr, err = f.service.Store.Get(ctx, domain.SessionKind, domain.ID(f.change.Session.Id))
			session, decodeErr := store.Decode[domain.Session](sr)
			if err != nil || decodeErr != nil || session.PendingInputs != 1 || session.ActiveExecutionID != next.ExecutionID || !session.OwnsExecution(next) || session.InitialExecution.ID != input.ExecutionID {
				t.Fatal("receipt replay duplicated input or rewrote history")
			}
			retained, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(original.Id))
			old, decodeErr := store.Decode[domain.Job](retained)
			if err != nil || decodeErr != nil || old.State != domain.JobFailed || string(old.Input) != string(job.Input) || old.Startup == nil || old.Startup.Failure == nil {
				t.Fatal("failed attempt was rewritten")
			}

			if got := executionHistoryCount(t, f.service.Store, sr.ID); got != total+1 {
				t.Fatal("startup replay consumed jobs", got)
			}
		})
	}
}

func TestImageRejectionReportBindsOriginalClaimAndReadyProcess(t *testing.T) {
	for _, scenario := range []string{"valid", "text", "no-thread", "changed-input", "changed-mode", "changed-skills", "changed-process", "acknowledged"} {
		t.Run(scenario, func(t *testing.T) {
			f := directStartupFixture(t, func(i *domain.ExecutionJobInput) {
				if scenario != "text" {
					i.Configuration.ImageInputDeclared = true
					i.ConfigurationDigest, _ = i.Configuration.Digest()
					i.Input.Attachments = []domain.ImageAttachment{{ID: domain.NewID(), MachineID: i.MachineID, MediaType: domain.ImagePNG, ByteLength: 1, SHA256: strings.Repeat("a", 64)}}
				}
			})
			f.registerGrant(t)
			ready := domain.ExecutionStartupObservation{State: domain.StartupReady, Phase: domain.StartupSettings, Harness: domain.Codex, NativeVersion: "0.150.9", ExecutableSHA256: strings.Repeat("a", 64), Protocol: domain.CodexAppServer, CorrelationID: f.job, InputDelivery: domain.StartupNotSent}
			if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, ready)); err != nil {
				t.Fatal(err)
			}
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.image-prewire", nil, func(tx *store.Tx) (any, error) {
				sr, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				if scenario != "no-thread" {
					session.Execution = &domain.ExecutionProgress{JobID: f.job, InputID: f.input.InputID, ExecutionID: f.input.ExecutionID, NativeThreadID: string(domain.NewID())}
				}
				if scenario == "acknowledged" {
					session.Execution.NativeTurnID = string(domain.NewID())
				}
				if scenario == "changed-input" || scenario == "changed-mode" || scenario == "changed-skills" {
					qr, err := tx.Get(domain.QueueKind, f.input.InputID)
					if err != nil {
						return nil, err
					}
					q, err := store.Decode[domain.QueuedInput](qr)
					if err != nil {
						return nil, err
					}
					switch scenario {
					case "changed-input":
						q.Prompt += " changed"
					case "changed-mode":
						// Mode is outside InputDigest, so only full input equality catches it.
						if q.Mode == domain.PlanMode {
							q.Mode = domain.ExecuteMode
						} else {
							q.Mode = domain.PlanMode
						}
					case "changed-skills":
						// Skills are outside InputDigest, so only full input equality catches them.
						q.Skills = []domain.SkillBinding{{WorkerDeviceID: domain.NewID(), InventoryID: domain.NewID(), SkillID: domain.NewID(), ContentRevision: "revision", SnapshotID: domain.NewID()}}
					}
					if _, err = tx.Put(domain.QueueKind, qr.ID, qr.Revision, qr.SessionID, "", q); err != nil {
						return nil, err
					}
				}
				return tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, "", session)
			})
			if err != nil {
				t.Fatal(err)
			}
			rejected := ready
			rejected.State = domain.StartupFailed
			rejected.Phase = domain.StartupInput
			rejected.ProblemCode = domain.Unsupported
			rejected.Cleanup = domain.StartupCleanupConfirmed
			rejected.FailureKind = domain.StartupImageInputRejected
			if scenario == "changed-process" {
				rejected.ExecutableSHA256 = strings.Repeat("b", 64)
			}
			request := startupRequest(f, rejected)
			first, err := f.client.ReportExecutionStartup(context.Background(), request)
			if scenario != "valid" {
				if err == nil {
					t.Fatal("foreign or mixed image report accepted")
				}
				if readErr := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
					_, session, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return err
					}
					if session.Startup.Failure != nil {
						t.Fatal("rejected image report stored a startup failure")
					}
					if session.Problem != nil && session.Problem.Message == domain.UnsupportedImageInput().Message {
						t.Fatal("rejected image report changed the session problem")
					}
					return nil
				}); readErr != nil {
					t.Fatal(readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			second, err := f.client.ReportExecutionStartup(context.Background(), request)
			if err != nil || !second.Msg.Replayed || first.Msg.Observation.FailureKind != second.Msg.Observation.FailureKind {
				t.Fatal("original image receipt changed", err)
			}
			err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				jr, err := tx.Get(domain.JobKind, f.job)
				if err != nil {
					return err
				}
				if jr.Revision != 1 {
					t.Fatal("report changed original assignment")
				}
				_, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return err
				}
				if session.Problem == nil || session.Problem.Message != domain.UnsupportedImageInput().Message {
					t.Fatal("typed image guidance lost")
				}
				qr, err := tx.Get(domain.QueueKind, f.input.InputID)
				if err != nil {
					return err
				}
				q, err := store.Decode[domain.QueuedInput](qr)
				if err != nil {
					return err
				}
				if !queuedSessionInput(q).Equal(f.input.Input) {
					t.Fatal("original draft or images changed")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.settle-image", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.JobKind, f.job)
				if err != nil {
					return nil, err
				}
				j, err := store.Decode[domain.Job](r)
				if err != nil {
					return nil, err
				}
				return finishNativeExecution(tx, r, j, r.Revision, nil, domain.UnsupportedImageInput())
			})
			if err != nil {
				t.Fatal(err)
			}
			err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				_, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return err
				}
				if session.ActiveExecutionID != "" || session.PendingInputs != 0 || session.PendingInputBytes != 0 || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchPaused || session.Execution != nil {
					t.Fatal("positive image rejection did not settle without recovery")
				}
				r, err := tx.Get(domain.QueueKind, f.input.InputID)
				if err != nil {
					return err
				}
				q, err := store.Decode[domain.QueuedInput](r)
				if err != nil {
					return err
				}
				if q.Delivery != domain.InputRejected || !queuedSessionInput(q).Equal(f.input.Input) {
					t.Fatal("original image draft lost in settlement")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

		})
	}
}

func startupSessionOf(t *testing.T, f *authorityFixture) domain.Session {
	t.Helper()
	record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](record)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func failedStartupObservation(f *authorityFixture) domain.ExecutionStartupObservation {
	return domain.ExecutionStartupObservation{State: domain.StartupFailed, Phase: domain.StartupResolve, Harness: domain.Codex, ProblemCode: domain.NotFound, CorrelationID: f.job, InputDelivery: domain.StartupNotSent, Cleanup: domain.StartupCleanupConfirmed}
}

func uncertainStartupObservation(f *authorityFixture) domain.ExecutionStartupObservation {
	return domain.ExecutionStartupObservation{State: domain.StartupUncertain, Phase: domain.StartupExecution, Harness: domain.Codex, ProblemCode: domain.Unsupported, CorrelationID: f.job, InputDelivery: domain.StartupDeliveryUncertain, Cleanup: domain.StartupCleanupUncertain}
}

func TestDirectStartupFailureRejectsPreviousServerEpoch(t *testing.T) {
	for name, observation := range map[string]func(*authorityFixture) domain.ExecutionStartupObservation{"failed": failedStartupObservation, "uncertain": uncertainStartupObservation} {
		t.Run(name, func(t *testing.T) {
			f := directStartupFixture(t)
			f.registerGrant(t)
			original := f.service.executionAuthority.epoch
			// A restarted server creates a fresh authority epoch for the same claimed job.
			f.service.executionAuthority.epoch = domain.NewID()
			request := startupRequest(f, observation(f))
			if _, err := f.client.ReportExecutionStartup(context.Background(), request); err == nil {
				t.Fatal("previous-epoch startup observation was accepted")
			} else if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("previous-epoch denial code = %v", connect.CodeOf(err))
			}
			if session := startupSessionOf(t, f); session.Startup != nil && session.Startup.Failure != nil || session.Problem != nil {
				t.Fatal("denied previous-epoch observation mutated the session")
			}
			// The same observation under the original epoch remains accepted and replays exactly.
			f.service.executionAuthority.epoch = original
			request = startupRequest(f, observation(f))
			first, err := f.client.ReportExecutionStartup(context.Background(), request)
			if err != nil || first.Msg.Replayed {
				t.Fatalf("same-epoch original observation: %v", err)
			}
			second, err := f.client.ReportExecutionStartup(context.Background(), request)
			if err != nil || !second.Msg.Replayed {
				t.Fatalf("same-epoch exact replay: %v", err)
			}
			if session := startupSessionOf(t, f); session.Startup == nil || session.Startup.Failure == nil {
				t.Fatal("same-epoch observation was not recorded")
			}
		})
	}
}

func TestDirectStartupFailureRejectsChangedGrantOwnership(t *testing.T) {
	for _, change := range []string{"device", "instance", "execution"} {
		t.Run(change, func(t *testing.T) {
			f := directStartupFixture(t)
			// A grant owned by another Worker identity must not authenticate this claim.
			grant := store.ExecutionGrant{JobID: f.job, Digest: bytes32(), ExecutionID: f.input.ExecutionID, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.device, ServerEpoch: f.service.executionAuthority.epoch}
			switch change {
			case "device":
				grant.DeviceID = domain.NewID()
			case "instance":
				grant.InstanceID = domain.NewID()
			case "execution":
				grant.ExecutionID = domain.NewID()
			}
			if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.foreign-grant", nil, func(tx *store.Tx) (any, error) { return nil, tx.PutExecutionGrant(grant) }); err != nil {
				t.Fatal(err)
			}
			if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, failedStartupObservation(f))); err == nil {
				t.Fatal("changed grant ownership was accepted")
			}
			if session := startupSessionOf(t, f); session.Startup != nil && session.Startup.Failure != nil {
				t.Fatal("denied observation mutated the session")
			}
		})
	}
}

func bytes32() []byte {
	digest := make([]byte, 32)
	digest[0] = 1
	return digest
}

func TestDirectStartupPreGrantFailureBindsProgressEpoch(t *testing.T) {
	setProgress := func(t *testing.T, f *authorityFixture, mutate func(*domain.StartupProgressAttempt)) {
		t.Helper()
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.startup-progress", nil, func(tx *store.Tx) (any, error) {
			r, session, err := sessionRecord(tx, f.input.SessionID)
			if err != nil {
				return nil, err
			}
			attempt := &domain.StartupProgressAttempt{JobID: f.job, ExecutionID: f.input.ExecutionID, AssignmentRevision: 1, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.device, ServerEpoch: f.service.executionAuthority.epoch}
			mutate(attempt)
			session.StartupProgress = &domain.SessionStartupProgress{Native: attempt}
			return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, session)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Run("no grant and no progress is accepted", func(t *testing.T) {
		f := directStartupFixture(t)
		f.service.executionAuthority.epoch = domain.NewID()
		if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, failedStartupObservation(f))); err != nil {
			t.Fatalf("pre-registration failure without epoch evidence regressed: %v", err)
		}
	})
	t.Run("same epoch progress is accepted", func(t *testing.T) {
		f := directStartupFixture(t)
		setProgress(t, f, func(*domain.StartupProgressAttempt) {})
		if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, failedStartupObservation(f))); err != nil {
			t.Fatalf("pre-registration failure with matching progress: %v", err)
		}
	})
	t.Run("previous epoch progress is denied", func(t *testing.T) {
		f := directStartupFixture(t)
		setProgress(t, f, func(a *domain.StartupProgressAttempt) { a.ServerEpoch = domain.NewID() })
		if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, failedStartupObservation(f))); err == nil {
			t.Fatal("previous-epoch pre-grant failure was accepted")
		}
		if session := startupSessionOf(t, f); session.Startup != nil && session.Startup.Failure != nil {
			t.Fatal("denied observation mutated the session")
		}
	})
	t.Run("changed progress instance is denied", func(t *testing.T) {
		f := directStartupFixture(t)
		setProgress(t, f, func(a *domain.StartupProgressAttempt) { a.InstanceID = domain.NewID() })
		if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, failedStartupObservation(f))); err == nil {
			t.Fatal("changed-instance pre-grant failure was accepted")
		}
	})
}
