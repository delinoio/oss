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
	request := &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}
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
}

func TestImageRejectionReportBindsOriginalClaimAndReadyProcess(t *testing.T) {
	for _, scenario := range []string{"valid", "text", "no-thread", "changed-input", "changed-process", "acknowledged"} {
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
				if scenario == "changed-input" {
					qr, err := tx.Get(domain.QueueKind, f.input.InputID)
					if err != nil {
						return nil, err
					}
					q, err := store.Decode[domain.QueuedInput](qr)
					if err != nil {
						return nil, err
					}
					q.Prompt += " changed"
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
