package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func startupTestDigest(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// This is a synthetic authenticated Worker result fixture. The workspace tests
// independently obtain the same proof from real private Git preparation and
// rejection; these server tests never inspect their inert remote path strings.
func newStartupReportFixture(t *testing.T) (*publicationFixture, domain.ExecutionStartupRejection) {
	t.Helper()
	var prep workspace.PrepareRequest
	var manifest workspace.Manifest
	var target domain.PRGitTarget
	f := publicationFixtureFromAuthority(t, newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", func(input *domain.ExecutionJobInput) {
		repo := domain.NewID()
		target = domain.PRGitTarget{Version: 1, Target: domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: repo, RemoteRepositoryID: "37", RepositoryNodeID: "R_37", Owner: "fixture-owner", Name: "repo", PullRequestID: "53", PullRequestNodeID: "PR_53", Number: "17", Title: "Private fixture title", ObservedAt: time.Now().UTC()}, HeadRepository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "39", NodeID: "R_39", Owner: "fixture-author", Name: "fork", DefaultBranch: "main"}, BaseRef: "main", HeadRef: "feature", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
		spec := workspace.RepositorySpec{ID: repo, PRTarget: &target, Checkout: "/fixture/source", PreferredRemote: "origin", Base: domain.Reference{Type: domain.CommitReference, Name: target.BaseSHA}, Starting: domain.Reference{Type: domain.CommitReference, Name: target.HeadSHA}, AutoFetch: true}
		prep = workspace.PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.Worktree, Repositories: []workspace.RepositorySpec{spec}, PrimaryRepository: repo}
		input.Preparation, _ = json.Marshal(prep)
		path := "/fixture/worker/workspaces/" + string(input.SessionID) + "/" + string(repo)
		manifest = workspace.Manifest{Version: 1, SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.Worktree, State: workspace.Ready, InputDigest: startupTestDigest(input.Preparation), PrimaryPath: path, CreatedAt: time.Now().UTC(), Repositories: []workspace.PreparedRepository{{ID: repo, PRTarget: &target, Source: spec.Checkout, Path: path, Base: spec.Base, Starting: spec.Starting, BaseCommit: target.BaseSHA, StartingCommit: target.HeadSHA, Owned: true}}}
		input.Manifest, _ = json.Marshal(manifest)
	}, false))
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.pr-workspace", nil, func(tx *store.Tx) (any, error) {
		r, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		s.Workspace = domain.Worktree
		return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.service.Store.Get(context.Background(), domain.JobKind, f.job)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rawTarget, _ := json.Marshal(target)
	proof := domain.PRStartupRejectionProof{JobID: f.job, ExecutionID: f.input.ExecutionID, SessionID: f.input.SessionID, PreparationDigest: startupTestDigest(f.input.Preparation), ManifestDigest: startupTestDigest(f.input.Manifest), TargetDigest: startupTestDigest(rawTarget), Reason: domain.Conflict, StartedAt: now, FinishedAt: now.Add(time.Millisecond)}
	phase := fmt.Sprintf(`{"version":1,"job_id":%q,"execution_id":%q,"session_id":%q,"preparation_digest":%q,"manifest_digest":%q,"target_digest":%q,"phase":"rejected","reason":%q,"started_at":%q,"finished_at":%q}`, proof.JobID, proof.ExecutionID, proof.SessionID, proof.PreparationDigest, proof.ManifestDigest, proof.TargetDigest, proof.Reason, proof.StartedAt.Format(time.RFC3339Nano), proof.FinishedAt.Format(time.RFC3339Nano))
	proof.JournalDigest = startupTestDigest([]byte(phase))
	result := domain.ExecutionStartupRejection{Version: 1, Type: domain.PRStartupRejectedResult, ServerID: f.service.Identity.ServerID, DeviceID: f.device, InstanceID: f.instance, MachineID: f.input.MachineID, InputID: f.input.InputID, AccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID, AssignmentRevision: assignment.Revision, AssignmentDigest: startupTestDigest(assignment.Data), AssignmentInputDigest: startupTestDigest(mustStartupInput(t, assignment)), ConfigurationDigest: f.input.ConfigurationDigest, Workspace: proof}
	if err := result.ValidateAssignment(f.job, assignment.Revision, assignment.Data); err != nil {
		t.Fatal(err)
	}
	if err := workspace.ValidatePRStartupRejection(prep, manifest, proof, "linux"); err != nil {
		t.Fatal(err)
	}
	return f, result
}

func mustStartupInput(t *testing.T, assignment store.Record) []byte {
	t.Helper()
	job, err := store.Decode[domain.Job](assignment)
	if err != nil {
		t.Fatal(err)
	}
	return job.Input
}

func TestPRStartupReportRetainsRejectedInputAndAllowsArchiveWithoutReplay(t *testing.T) {
	f, rejection := newStartupReportFixture(t)
	raw, _ := json.Marshal(rejection)
	req := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}
	for attempt := 0; attempt < 2; attempt++ {
		r, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, req))
		if err != nil || r.Msg.Replayed != (attempt == 1) {
			t.Fatal("rejection report/replay", err)
		}
		var job domain.Job
		if domain.Decode(r.Msg.Job.DocumentJson, &job) != nil || job.State != domain.JobFailed || job.Problem == nil || job.Problem.Code != domain.Conflict || job.FinishedAt == nil {
			t.Fatal("rejection was fabricated as completion or left uncertain")
		}
	}
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		r, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		if s.Outcome != domain.ExecutionNotStarted || s.Recovery != domain.NoRecovery || s.ActiveExecutionID != "" || s.Execution != nil || s.InitialExecution == nil || s.PendingInputs != 0 || s.PendingInputBytes != 0 || s.Dispatch != domain.DispatchPaused || s.StartupRejection == nil {
			t.Fatal("rejection lost the original snapshot or acquired native completion/replay authority")
		}
		ir, err := tx.Get(domain.QueueKind, f.input.InputID)
		if err != nil {
			return err
		}
		i, err := store.Decode[domain.QueuedInput](ir)
		if err != nil || i.Delivery != domain.InputRejected || i.ExecutionID != f.input.ExecutionID || i.Prompt != f.input.Input.Prompt {
			t.Fatal("rejected input was cleared or requeued", err)
		}
		_, _, verified, err := tx.VerifiedStartupRejection(r.ID)
		if err != nil || !verified {
			t.Fatal("retained rejection lost original proof", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	for _, action := range []pb.SessionAction{pb.SessionAction_SESSION_ACTION_RESUME, pb.SessionAction_SESSION_ACTION_ARCHIVE, pb.SessionAction_SESSION_ACTION_RESTORE} {
		r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.ControlSession(context.Background(), ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: action}))
		if (action == pb.SessionAction_SESSION_ACTION_RESUME) != (err != nil) {
			t.Fatal("rejected session control", action, err)
		}
		retained, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		session, err := store.Decode[domain.Session](retained)
		expectedArchive := domain.NotArchived
		if action == pb.SessionAction_SESSION_ACTION_ARCHIVE {
			expectedArchive = domain.Archived
		}
		if err != nil || session.Archive != expectedArchive || session.Dispatch != domain.DispatchPaused || session.ActiveExecutionID != "" || session.StartupRejection == nil || session.PendingInputs != 0 {
			t.Fatal("control did not retain paused rejection", action, err)
		}
	}
}

func TestPRStartupReportRejectsChangedOrContradictoryEvidence(t *testing.T) {
	for _, scenario := range []string{"server", "device", "assignment", "input", "configuration", "account", "target", "phase", "reason", "registered", "published", "recovery", "wrong-workspace"} {
		t.Run(scenario, func(t *testing.T) {
			f, rejection := newStartupReportFixture(t)
			switch scenario {
			case "server":
				rejection.ServerID = domain.NewID()
			case "device":
				rejection.DeviceID = domain.NewID()
			case "assignment":
				rejection.AssignmentDigest = strings.Repeat("a", 64)
			case "input":
				rejection.InputID = domain.NewID()
			case "configuration":
				rejection.ConfigurationDigest = strings.Repeat("a", 64)
			case "account":
				rejection.AccountID = domain.NewID()
			case "target":
				rejection.Workspace.TargetDigest = strings.Repeat("a", 64)
			case "phase":
				rejection.Workspace.JournalDigest = strings.Repeat("a", 64)
			case "reason":
				rejection.Workspace.Reason = domain.Canceled
			case "registered":
				f.registerGrant(t)
			case "published":
				f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			case "recovery", "wrong-workspace":
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.block-startup-report", nil, func(tx *store.Tx) (any, error) {
					r, s, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					if scenario == "recovery" {
						s.Recovery = domain.NeedsRecovery
					} else {
						s.Workspace = domain.GeneralChat
					}
					return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(rejection)
			response, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}))
			if err != nil {
				t.Fatal(err)
			}
			var job domain.Job
			if scenario == "server" {
				if domain.Decode(response.Msg.Job.DocumentJson, &job) != nil || job.State != domain.JobFailed {
					t.Fatal("server attribution blocked startup report")
				}
				return
			}
			if domain.Decode(response.Msg.Job.DocumentJson, &job) != nil || job.State != domain.JobUncertain {
				t.Fatal("invalid rejection released native ownership")
			}
			r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			s, err := store.Decode[domain.Session](r)
			if err != nil || s.Recovery != domain.NeedsRecovery || s.ActiveExecutionID != f.input.ExecutionID || s.StartupRejection != nil || s.PendingInputs != 1 {
				t.Fatal("uncertainty was discarded", err)
			}
		})
	}
}

func TestPRStartupReportCannotBorrowAnotherDeviceOrItsReceipt(t *testing.T) {
	f, rejected := newStartupReportFixture(t)
	foreign := domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: f.input.MachineID}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.other-device", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.DeviceKind, foreign.DeviceID, 0, "", "", domain.Device{Name: "Other fixture Worker", Type: domain.WorkerDevice, MachineID: foreign.MachineID, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rejected)
	request := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}
	for _, replay := range []bool{false, true} {
		if replay {
			if _, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, request)); err != nil {
				t.Fatal(err)
			}
		}
		_, err := f.service.ReportWork(domain.WithPrincipal(context.Background(), foreign), connect.NewRequest(request))
		if err != nil {
			t.Fatal("registered device could not report selected assignment", replay, err)
		}
	}
}
