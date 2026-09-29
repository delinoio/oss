package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestTitleAuthorityRemainsBoundToFrozenInitialExecutionAfterFollowUp(t *testing.T) {
	machineID, agentID := domain.NewID(), domain.NewID()
	executionID, inputID := domain.NewID(), domain.NewID()
	accountID, connectionID := domain.NewID(), domain.NewID()
	followUpID, followUpInputID := domain.NewID(), domain.NewID()
	digest := strings.Repeat("a", 64)
	original := domain.ExecutionJobInput{
		MachineID: machineID, ExecutionID: executionID, InputID: inputID,
		Configuration: domain.ExecutionConfiguration{AgentID: agentID}, ConfigurationDigest: digest,
		AccountID: accountID, ConnectionID: connectionID,
	}
	initial := &domain.InitialExecution{
		ID: executionID, InputID: inputID, InitialAccountID: accountID, ConnectionID: connectionID,
		Configuration: domain.ExecutionConfiguration{AgentID: agentID}, ConfigurationDigest: digest,
	}
	session := domain.Session{
		MachineID: machineID, AgentID: agentID, InitialExecution: initial,
		ActiveExecutionID: followUpID,
		CurrentExecution:  &domain.ExecutionSelection{ID: followUpID, InputID: followUpInputID, AccountID: domain.NewID(), ConnectionID: domain.NewID()},
	}
	if !matchesInitialTitleExecution(session, original) {
		t.Fatal("follow-up execution replaced the immutable initial title authority")
	}

	original.AccountID = domain.NewID()
	if matchesInitialTitleExecution(session, original) {
		t.Fatal("title authority accepted an original account that differs from the frozen snapshot")
	}
}

func TestTitleRegistrationRejectsSecondSendClaimButReplaysExactReceipt(t *testing.T) {
	f := recoveredAutomaticTitleFixture(t)
	_, recovery := acceptRecovery(t, f)
	completeRecovery(t, f, recovery.ExecutionRecoveryJob)
	sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var session domain.Session
	if domain.Decode(sr.Data, &session) != nil || session.TitleJobID == "" {
		t.Fatalf("recovery did not queue the title: %+v", session)
	}
	claimed, err := claimTitleJob(context.Background(), f.service, f.input.MachineID, f.instance, f.device, session.TitleJobID)
	if err != nil {
		t.Fatal(err)
	}
	titleToken, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(titleToken))
	request := &pb.RegisterExecutionRequest{
		Mutation:  &pb.Mutation{RequestId: string(domain.NewID()), Id: string(claimed.ID), ExpectedRevision: claimed.Revision},
		MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CredentialDigest: digest[:],
	}
	worker := security.Identity{Token: f.workerToken}
	registered, err := f.client.RegisterExecution(context.Background(), ownerRequest(worker, request))
	if err != nil || registered.Msg.Replayed {
		t.Fatalf("first title send registration failed: %v", err)
	}
	replay, err := f.client.RegisterExecution(context.Background(), ownerRequest(worker, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("exact registration receipt did not replay: %v", err)
	}
	request.Mutation.RequestId = string(domain.NewID())
	if _, err := f.client.RegisterExecution(context.Background(), ownerRequest(worker, request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a fresh receipt sent the title a second time: %v", err)
	}
}

func TestSuccessfulTitleReportsRequireBothDurableRelayClaims(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, test := range []struct {
		name              string
		send, http        bool
		wantAuthorization bool
	}{
		{name: "no claims"},
		{name: "send only", send: true},
		{name: "HTTP only", http: true},
		{name: "both claims", send: true, http: true, wantAuthorization: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			jobID := domain.NewID()
			job := domain.Job{
				Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed,
				MachineID: domain.NewID(), InstanceID: domain.NewID(), AssignedDeviceID: domain.NewID(),
				Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC(),
			}
			if _, err := db.Mutate(ctx, domain.NewID(), "test.title-job", jobID, func(tx *store.Tx) (any, error) {
				return tx.PutJob(jobID, 0, "", "", job)
			}); err != nil {
				t.Fatal(err)
			}
			if test.send || test.http {
				if _, err := db.Mutate(ctx, domain.NewID(), "test.title-claims", struct {
					JobID domain.ID
					Send  bool
					HTTP  bool
				}{jobID, test.send, test.http}, func(tx *store.Tx) (any, error) {
					if test.send {
						if _, err := tx.ClaimTitleInference(jobID); err != nil {
							return nil, err
						}
					}
					if test.http {
						if _, err := tx.ClaimTitleHTTPRequest(jobID); err != nil {
							return nil, err
						}
					}
					return nil, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			err := db.Read(ctx, func(tx *store.Tx) error { return requireSessionTitleRelayProof(tx, jobID) })
			if test.wantAuthorization && err != nil {
				t.Fatalf("both relay claims were rejected: %v", err)
			}
			if !test.wantAuthorization && domain.SafeError(err).Code != domain.PermissionDenied {
				t.Fatalf("missing relay proof error = %v, want permission denied", err)
			}
		})
	}
}
