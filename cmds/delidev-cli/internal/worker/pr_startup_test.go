package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func TestWorkerPRStartupRejectionBindsJournalAndNeverStartsHarness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private executable fixture uses a POSIX shell; no Windows runtime claim")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "XDG_CONFIG_HOME": home, "SSH_AUTH_SOCK": "", "GIT_SSH": "", "GIT_SSH_COMMAND": ""} {
		t.Setenv(key, value)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal("private Git fixture", err, string(raw))
		}
		return strings.TrimSpace(string(raw))
	}
	source := filepath.Join(home, "source")
	git("init", "--quiet", "--initial-branch=main", source)
	git("-C", source, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "fixture")
	head := git("-C", source, "rev-parse", "HEAD")
	f := newCheckpointFixture(t)
	f.root = filepath.Join(home, "worker")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	manager := workspace.Manager{Root: f.root, Logger: logger}
	repo := domain.NewID()
	ref := domain.Reference{Type: domain.CommitReference, Name: head}
	preparation := workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.Worktree, PrimaryRepository: repo, Repositories: []workspace.RepositorySpec{{ID: repo, Checkout: source, Base: ref, Starting: ref}}}
	manifest, err := manager.Prepare(context.Background(), preparation)
	if err != nil {
		t.Fatal(err)
	}
	// The preparation's remote PR metadata is synthetic here. The execution
	// preflight sees a real dirty worktree and must reject before network access.
	// Full exact-head network preparation is covered by workspace integration.
	target := domain.PRGitTarget{Version: 1, Target: domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: repo, RemoteRepositoryID: "37", RepositoryNodeID: "R_37", Owner: "fixture-owner", Name: "repo", PullRequestID: "53", PullRequestNodeID: "PR_53", Number: "17", Title: "Private fixture title", ObservedAt: time.Now().UTC()}, HeadRepository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "39", NodeID: "R_39", Owner: "fixture-author", Name: "fork", DefaultBranch: "main"}, BaseRef: "main", HeadRef: "feature", BaseSHA: head, HeadSHA: head}
	preparation.Repositories[0].PRTarget, preparation.Repositories[0].AutoFetch = &target, true
	manifest.Repositories[0].PRTarget = &target
	f.input.Preparation, _ = json.Marshal(preparation)
	manifest.InputDigest = executionInputDigest(f.input.Preparation)
	f.input.Manifest, _ = json.Marshal(manifest)
	if err := security.WriteAtomic(filepath.Join(f.root, "workspaces", string(f.input.SessionID), "manifest.json"), f.input.Manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "unrelated"), []byte("preserve private change"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "native-started")
	native := filepath.Join(home, "native-fixture")
	if err := os.WriteFile(native, []byte("#!/bin/sh\nprintf started > '"+strings.ReplaceAll(marker, "'", "'\"'\"'")+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.input.Installation.ResolvedPath = native
	f.input.Input.Mode = domain.ExecuteMode
	f.job.InstanceID, f.job.AcceptedAt = domain.NewID(), time.Now().UTC()
	f.job.Input, _ = json.Marshal(f.input)
	assigned, _ := json.Marshal(f.job)
	resource := &pb.Resource{Id: string(f.jobID), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 2, SessionId: string(f.input.SessionID), DocumentJson: assigned}
	token, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: "http://127.0.0.1:1", ServerID: domain.NewID(), DeviceID: domain.NewID(), MachineID: f.input.MachineID, PairingID: domain.NewID(), Token: token}
	config := Config{Root: f.root, Logger: logger, executionContext: context.Background(), execution: &PublicationConfig{Root: f.root, Credential: credential, Instance: f.job.InstanceID, Assignment: resource, Client: delidevv1connect.NewWorkerServiceClient(http.DefaultClient, credential.Endpoint)}}
	if err := security.PrivateDir(filepath.Join(f.root, "jobs")); err != nil {
		t.Fatal(err)
	}
	result, err := runJob(context.Background(), config, f.job.InstanceID, resource, f.job)
	if err != nil || result.Problem != nil || result.State != journalFinished {
		t.Fatal("original rejection report", err, result.Problem)
	}
	var rejection domain.ExecutionStartupRejection
	if domain.Decode(result.Output, &rejection) != nil || rejection.ValidateAssignment(f.jobID, resource.Revision, assigned) != nil || rejection.Workspace.Reason != domain.Conflict {
		t.Fatal("missing original assignment-bound rejection")
	}
	for _, private := range []string{token, f.input.Input.Prompt, "Private fixture title", manifest.PrimaryPath, source} {
		if strings.Contains(string(result.Output), private) {
			t.Fatal("startup report exposed private content")
		}
	}
	for _, path := range []string{marker, filepath.Join(f.root, "runtimes", string(f.input.ExecutionID)), filepath.Join(f.root, "jobs", string(f.jobID)), filepath.Join(f.root, "execution-claims", string(f.input.SessionID)+".json")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("pre-native rejection acquired native eligibility", path, err)
		}
	}
	journalPath := filepath.Join(f.root, "jobs", string(f.jobID)+".json")
	originalJournal, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := runJob(context.Background(), config, f.job.InstanceID, resource, f.job)
	if err != nil || replay.ReportID != result.ReportID || string(replay.Output) != string(result.Output) {
		t.Fatal("rejection report replay", err)
	}
	after, err := os.ReadFile(journalPath)
	if err != nil || string(after) != string(originalJournal) {
		t.Fatal("report replay rewrote original evidence")
	}
	assertPRStartupRecovery(t, config, credential, resource, f.job, result)
	_, cause := manager.ClaimFirstExecution(context.Background(), f.jobID, f.input.ExecutionID, preparation, manifest)
	if !workspace.IsPRStartupRejection(cause) {
		t.Fatal(cause)
	}
	started := result
	started.State, started.Output, started.Problem = journalStarted, nil, nil
	if err := writeJSON(journalPath, started); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"reconstructed-error", "foreign-job", "changed-assignment", "wrong-instance", "missing-journal", "publisher"} {
		t.Run(scenario, func(t *testing.T) {
			copy := config
			publication := *config.execution
			publication.Assignment = proto.Clone(resource).(*pb.Resource)
			copy.execution = &publication
			owner, reportedCause := f.jobID, cause
			switch scenario {
			case "reconstructed-error":
				reportedCause = domain.SafeError(cause)
			case "foreign-job":
				owner = domain.NewID()
			case "changed-assignment":
				publication.Assignment.Revision++
			case "wrong-instance":
				publication.Instance = domain.NewID()
			case "missing-journal":
				if err := os.Remove(journalPath); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := writeJSON(journalPath, started); err != nil {
						t.Fatal(err)
					}
				}()
			case "publisher":
				path := filepath.Join(f.root, "jobs", string(f.jobID))
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
				defer os.Remove(path)
			}
			output, err := reportPRStartupRejection(copy, owner, f.job, reportedCause)
			if len(output) != 0 || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("unowned rejection gained reporting authority", err)
			}
		})
	}
	// A restart after the original phase but before durable result publication
	// remains uncertain. Only explicit read-only recovery may reconcile it.
	interrupted, err := runJob(context.Background(), config, f.job.InstanceID, resource, f.job)
	if err != nil || interrupted.Problem == nil || interrupted.Problem.Code != domain.RecoveryRequired || len(interrupted.Output) != 0 {
		t.Fatal("interrupted job reconstructed a report or replayed work", err)
	}
}
