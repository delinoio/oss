package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestPRStartupRecoveryAcrossWorkerReplacementRetainsOriginalInput(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(map[bool]string{false: "paused", true: "archive-pending"}[archive], func(t *testing.T) {
			f, rejected := newStartupReportFixture(t)
			originalInstance := f.instance
			replaceRecoveryFixtureWorker(t, f)
			client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
			if archive {
				sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = client.ControlSession(context.Background(), ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
				if err != nil {
					t.Fatal(err)
				}
			}
			request, change := acceptRecovery(t, f)
			var job domain.Job
			var expected domain.ExecutionRecoveryRequest
			if domain.Decode(change.ExecutionRecoveryJob.DocumentJson, &job) != nil || domain.Decode(job.Input, &expected) != nil || expected.Validate() != nil || expected.Startup == nil || expected.InstanceID != originalInstance || expected.DeviceID != f.device || bytes.Contains(job.Input, []byte(f.input.Input.Prompt)) {
				t.Fatal("recovery changed original Worker or exposed native input")
			}
			replay, err := client.RecoverSessionExecution(context.Background(), ownerRequest(f.service.Identity, request))
			if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.ExecutionRecoveryJob.Id != change.ExecutionRecoveryJob.Id {
				t.Fatal("inspection request replay", err)
			}
			claimed, _ := claimRecovery(t, f, change.ExecutionRecoveryJob)
			evidence := domain.PRStartupRecoveryEvidence{Version: 1, JobID: f.job, ReportID: domain.NewID(), Rejection: rejected}
			if err := evidence.Validate(expected); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(evidence)
			report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(claimed.ID), ExpectedRevision: claimed.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}
			for attempt := 0; attempt < 2; attempt++ {
				response, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, report))
				if err != nil || response.Msg.Replayed != (attempt == 1) {
					t.Fatal("startup recovery report/replay", err)
				}
				var recovered domain.Job
				if domain.Decode(response.Msg.Job.DocumentJson, &recovered) != nil || recovered.State != domain.JobSucceeded {
					t.Fatal("startup inspection did not succeed", recovered.Problem)
				}
			}
			err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				_, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return err
				}
				wanted := domain.NotArchived
				if archive {
					wanted = domain.Archived
				}
				if session.Archive != wanted || session.Recovery != domain.NoRecovery || session.Outcome != domain.ExecutionNotStarted || session.Dispatch != domain.DispatchPaused || session.ActiveExecutionID != "" || session.Execution != nil || session.PendingInputs != 0 || session.PendingInputBytes != 0 || session.StartupRejection == nil || session.StartupRejection.InstanceID != originalInstance || session.ExecutionRecoveryJobID != claimed.ID {
					t.Fatal("recovery lost original paused startup state")
				}
				original, proof, verified, err := tx.VerifiedStartupRejection(f.input.SessionID)
				if err != nil || !verified || original.ID != f.job || proof != rejected {
					t.Fatal("recovered rejection did not retain original proof", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			replay, err = client.RecoverSessionExecution(context.Background(), ownerRequest(f.service.Identity, request))
			if err != nil || !replay.Msg.Change.Replayed {
				t.Fatal("settled recovery request replay", err)
			}
		})
	}
}

func TestPRStartupRecoveryRejectsForeignOrChangedEvidence(t *testing.T) {
	for _, scenario := range []string{"original-instance", "device", "job", "input", "assignment", "phase", "report", "registered", "mixed-native"} {
		t.Run(scenario, func(t *testing.T) {
			f, rejected := newStartupReportFixture(t)
			if scenario == "registered" {
				f.registerGrant(t)
			}
			replaceRecoveryFixtureWorker(t, f)
			if scenario == "registered" {
				client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
				if _, err := client.RecoverSessionExecution(context.Background(), ownerRequest(f.service.Identity, recoveryRequest(t, f))); err == nil {
					t.Fatal("native grant was treated as pre-native rejection")
				}
				return
			}
			_, change := acceptRecovery(t, f)
			claimed, _ := claimRecovery(t, f, change.ExecutionRecoveryJob)
			evidence := domain.PRStartupRecoveryEvidence{Version: 1, JobID: f.job, ReportID: domain.NewID(), Rejection: rejected}
			switch scenario {
			case "original-instance":
				evidence.Rejection.InstanceID = f.instance
			case "device":
				evidence.Rejection.DeviceID = domain.NewID()
			case "job":
				evidence.JobID = domain.NewID()
			case "input":
				evidence.Rejection.InputID = domain.NewID()
			case "assignment":
				evidence.Rejection.AssignmentDigest = strings.Repeat("a", 64)
			case "phase":
				evidence.Rejection.Workspace.JournalDigest = strings.Repeat("b", 64)
			case "report":
				evidence.ReportID = ""
			}
			raw, _ := json.Marshal(evidence)
			if scenario == "mixed-native" {
				raw = append(raw[:len(raw)-1], []byte(`,"completion":{}}`)...)
			}
			response, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(claimed.ID), ExpectedRevision: claimed.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}))
			if err != nil {
				t.Fatal(err)
			}
			var result domain.Job
			if scenario == "original-instance" || scenario == "device" {
				if domain.Decode(response.Msg.Job.DocumentJson, &result) != nil || result.State != domain.JobSucceeded {
					t.Fatal("ownership metadata blocked accepted startup recovery")
				}
				return
			}
			if domain.Decode(response.Msg.Job.DocumentJson, &result) != nil || result.State != domain.JobUncertain {
				t.Fatal("invalid proof cleared recovery")
			}
			sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil || session.Recovery != domain.NeedsRecovery || session.StartupRejection != nil || session.PendingInputs != 1 || session.ActiveExecutionID != f.input.ExecutionID {
				t.Fatal("invalid evidence changed original input", err)
			}
		})
	}
}
