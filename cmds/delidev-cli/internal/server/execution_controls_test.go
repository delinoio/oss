package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func seedAccountExecution(tx *store.Tx, f *publicationFixture, state domain.JobState, account, connection domain.ID) (store.Record, error) {
	input := f.input
	input.SessionID, input.ExecutionID, input.InputID = domain.NewID(), domain.NewID(), domain.NewID()
	input.ThreadRequestID, input.TurnRequestID = domain.NewID(), domain.NewID()
	input.AccountID, input.ConnectionID = account, connection
	input.Configuration.Accounts = []domain.WeightedAccount{{ID: account, Weight: 1}}
	// The original account is a candidate only on this other account's job.
	if account != f.input.AccountID {
		input.Configuration.Accounts = append(input.Configuration.Accounts, domain.WeightedAccount{ID: f.input.AccountID, Weight: 1})
	}
	var err error
	input.ConfigurationDigest, err = input.Configuration.Digest()
	if err != nil {
		return store.Record{}, err
	}
	initial := &domain.InitialExecution{ID: input.ExecutionID, InputID: input.InputID, Configuration: input.Configuration, ConfigurationDigest: input.ConfigurationDigest, InitialAccountID: account, ConnectionID: connection, AcceptedAt: time.Now().UTC()}
	session := domain.Session{Name: "Fixture", AgentID: input.Configuration.AgentID, MachineID: input.MachineID, Workspace: domain.GeneralChat, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchClaimed, ActiveExecutionID: input.ExecutionID, InitialExecution: initial, PendingInputs: 1, PendingInputBytes: uint64(len(input.Input.Prompt))}
	queued := domain.QueuedInput{Sequence: 1, ContentRevision: 1, Prompt: input.Input.Prompt, Mode: input.Input.Mode, Delivery: domain.InputClaimed, ExecutionID: input.ExecutionID, NativeRequestID: input.TurnRequestID}
	instance := f.instance
	if state == domain.JobQueued {
		instance = ""
	} else if state == domain.JobUncertain {
		session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
		queued.Delivery = domain.InputUncertain
	} else if state.Terminal() {
		session.ActiveExecutionID, session.Outcome, session.Dispatch = "", domain.ExecutionSucceeded, domain.DispatchPaused
		if state == domain.JobFailed {
			session.Outcome = domain.ExecutionFailed
		} else if state == domain.JobCanceled {
			session.Outcome = domain.ExecutionStopped
		}
		session.PendingInputs, session.PendingInputBytes = 0, 0
		queued.Delivery = domain.InputAccepted
	}
	if _, err := tx.Put(domain.SessionKind, input.SessionID, 0, input.SessionID, "", session); err != nil {
		return store.Record{}, err
	}
	if _, err := tx.Put(domain.QueueKind, input.InputID, 0, input.SessionID, "", queued); err != nil {
		return store.Record{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(domain.NewID(), 0, input.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: state, MachineID: input.MachineID, InstanceID: instance, Input: raw, AcceptedAt: time.Now().UTC()})
}

func TestAccountDisconnectCancelsOnlySelectedUnfinishedExecutions(t *testing.T) {
	ctx := context.Background()
	f := newPublicationFixture(t)
	var targeted, untouched []store.Record
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.account-executions", nil, func(tx *store.Tx) (any, error) {
		original, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		targeted = append(targeted, original)
		states := []domain.JobState{domain.JobQueued, domain.JobClaimed, domain.JobUncertain}
		for i := 0; i < store.MaxPage+3; i++ {
			r, err := seedAccountExecution(tx, f, states[i%len(states)], f.input.AccountID, f.input.ConnectionID)
			if err != nil {
				return nil, err
			}
			targeted = append(targeted, r)
		}
		for _, state := range []domain.JobState{domain.JobSucceeded, domain.JobFailed, domain.JobCanceled} {
			r, err := seedAccountExecution(tx, f, state, f.input.AccountID, f.input.ConnectionID)
			if err != nil {
				return nil, err
			}
			untouched = append(untouched, r)
		}
		r, err := seedAccountExecution(tx, f, domain.JobClaimed, domain.NewID(), domain.NewID())
		if err != nil {
			return nil, err
		}
		untouched = append(untouched, r)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewAccountServiceClient(f.http.Client(), f.http.URL)
	request := &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: 1}}
	response, err := client.DisconnectAccount(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || len(response.Msg.CleanupProblemJson) != 0 {
		t.Fatalf("account disconnect failed: %v", err)
	}
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		for _, before := range targeted {
			requested, err := tx.JobCancellationRequested(before.ID)
			if err != nil || !requested {
				t.Fatal("account disconnect missed an unfinished execution page")
			}
			after, err := tx.Get(domain.JobKind, before.ID)
			if err != nil {
				return err
			}
			old, _ := store.Decode[domain.Job](before)
			job, _ := store.Decode[domain.Job](after)
			if old.State == domain.JobQueued {
				if job.State != domain.JobCanceled || job.InstanceID != "" {
					t.Fatal("undispatched execution remained runnable after disconnect")
				}
			} else if before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("cancellation changed the immutable claimed assignment")
			}
			_, session, err := sessionRecord(tx, before.SessionID)
			if err != nil {
				return err
			}
			if session.Dispatch != domain.DispatchPaused || session.ActiveExecutionID == "" || session.PendingInputs != 1 || session.Problem == nil {
				t.Fatal("disconnect freed a claim or silently resumed the session")
			}
		}
		for _, before := range untouched {
			requested, err := tx.JobCancellationRequested(before.ID)
			if err != nil || requested {
				t.Fatal("candidate account or completed history became a cancellation target")
			}
			after, err := tx.Get(domain.JobKind, before.ID)
			if err != nil {
				return err
			}
			if before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("disconnect rewrote unrelated native history")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	connectID := domain.NewID()
	connected, err := client.ConnectAccount(ctx, ownerRequest(f.service.Identity, &pb.ConnectAccountRequest{Mutation: &pb.Mutation{RequestId: string(connectID), Id: string(f.input.AccountID), ExpectedRevision: response.Msg.Account.Revision}, ApiKey: []byte("replacement-private-fixture-key")}))
	if err != nil {
		t.Fatal(err)
	}
	var later store.Record
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.later-connection-execution", nil, func(tx *store.Tx) (any, error) {
		var err error
		later, err = seedAccountExecution(tx, f, domain.JobQueued, f.input.AccountID, connectID)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := client.DisconnectAccount(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !replayed.Msg.Replayed || replayed.Msg.Account.Revision != connected.Msg.Account.Revision {
		t.Fatalf("old disconnect affected the replacement connection: %v", err)
	}
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		requested, err := tx.JobCancellationRequested(later.ID)
		if requested {
			t.Fatal("receipt retry canceled an execution on a replacement connection")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, retained := f.service.accountSecrets.(*accountTestSecrets).counts(); retained != 1 {
		t.Fatal("disconnect retry deleted a replacement credential")
	}
}
