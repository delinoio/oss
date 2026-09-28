package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof(t *testing.T) {
	for _, scenario := range []string{"matches", "different", "pause", "unlink"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFirstDispatchFixtureWorkspaceProfile(t, domain.Codex, domain.ExecuteMode, "/fixture/codex", "", "fixture-model", domain.Worktree)
			ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 15*time.Second)
			defer cancel()
			selected := f.refresh(t)
			var prepared workspace.PrepareRequest
			var manifest workspace.Manifest
			if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
				var err error
				prepared, manifest, err = workspaceReadScope(tx, selected.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			target := domain.PRGitTarget{Version: 1, Target: domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: prepared.PrimaryRepository, RemoteRepositoryID: "37", RepositoryNodeID: "R_original", Owner: "fixture-owner", Name: "repo", PullRequestID: "53", PullRequestNodeID: "PR_original", Number: "17", Title: "Original PR", ObservedAt: time.Now().UTC()}, HeadRepository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "37", NodeID: "R_original", Owner: "fixture-owner", Name: "repo"}, BaseRef: "main", BaseSHA: manifest.Repositories[0].BaseCommit, HeadRef: "feature", HeadSHA: manifest.Repositories[0].StartingCommit}
			link := domain.NewID()
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.match-link", nil, func(tx *store.Tx) (any, error) {
				return tx.Put(domain.PullRequestKind, link, 0, selected.ID, selected.ProjectID, target.Target)
			})
			if err != nil {
				t.Fatal(err)
			}
			stream, err := f.workerClient.WatchWorkspaceReads(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkspaceReadsRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if !stream.Receive() || !stream.Msg().Heartbeat {
				t.Fatal("reader unavailable", stream.Err())
			}
			type reply struct {
				value workspace.PRWorkspaceMatch
				err   error
			}
			done := make(chan reply, 1)
			go func() {
				value, err := f.service.matchPRRemediationWorkspace(ctx, selected, target)
				done <- reply{value, err}
			}()
			if !stream.Receive() {
				t.Fatal(stream.Err())
			}
			var request workspace.ReadRequest
			if domain.Decode(stream.Msg().RequestJson, &request) != nil || request.PRCandidate == nil || request.PRCandidate.HeadSHA != target.HeadSHA || request.Query != (domain.WorkspaceReadQuery{}) {
				t.Fatal("private PR matching selection was altered")
			}
			// This is a scripted Worker protocol report, not native Git evidence.
			// Native matching is tested separately with real private repositories.
			raw, _ := json.Marshal(request)
			digest := sha256.Sum256(raw)
			proof := workspace.PRWorkspaceMatch{Version: 1, ReadID: request.ID, SessionID: selected.ID, RepositoryID: target.Target.RepositoryID, State: workspace.PRWorkspaceMatches, SelectionDigest: hex.EncodeToString(digest[:]), ObservedAt: time.Now().UTC()}
			if scenario == "different" {
				proof.State = workspace.PRWorkspaceDifferent
			}
			report := &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(request.ID), DocumentJson: []byte(`{"text":"foreign file content","size":"20","binary":false,"truncated":false}`)}
			if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, report)); err == nil {
				t.Fatal("public file content accepted as private matching proof")
			}
			invalid := proof
			invalid.SelectionDigest = strings.Repeat("0", 64)
			report.DocumentJson, _ = json.Marshal(invalid)
			if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, report)); err == nil {
				t.Fatal("foreign original-selection digest accepted")
			}
			if scenario == "pause" || scenario == "unlink" {
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.change-match-candidate", scenario, func(tx *store.Tx) (any, error) {
					if scenario == "unlink" {
						return nil, tx.Delete(domain.PullRequestKind, link, 1)
					}
					r, s, err := sessionRecord(tx, selected.ID)
					if err != nil {
						return nil, err
					}
					s.Dispatch = domain.DispatchPaused
					return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, s)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			report.DocumentJson, _ = json.Marshal(proof)
			if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, report)); err != nil {
				t.Fatal(err)
			}
			result := <-done
			if scenario == "pause" || scenario == "unlink" {
				if result.err == nil || domain.SafeError(result.err).Code != domain.Conflict || result.value.Version != 0 {
					t.Fatal("changed candidate kept a usable match", result.err)
				}
			} else if result.err != nil || result.value.State != proof.State || result.value.SelectionDigest != proof.SelectionDigest {
				t.Fatal("original match report lost", result.err)
			}
			if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, report)); err == nil {
				t.Fatal("original matching report replayed")
			}
			current := f.refresh(t)
			state, err := store.Decode[domain.Session](current)
			if err != nil || state.InitialExecution != nil || state.ActiveExecutionID != "" || state.PendingInputs != 1 || (scenario == "pause" && state.Dispatch != domain.DispatchPaused) {
				t.Fatal("matching changed dispatch or input ownership", err)
			}
			if scenario == "matches" {
				worker := domain.WithPrincipal(ctx, domain.Principal{Type: domain.WorkerDevice})
				if _, err := f.service.matchPRRemediationWorkspace(worker, current, target); domain.SafeError(err).Code != domain.PermissionDenied {
					t.Fatal("Worker selected its own remediation", err)
				}
			}
		})
	}
}
