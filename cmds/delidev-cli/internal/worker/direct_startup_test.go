// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type startupReportFixture struct {
	delidevv1connect.WorkerServiceClient
	observation *pb.ExecutionStartupObservation
}

func (f *startupReportFixture) ReportExecutionStartup(_ context.Context, r *connect.Request[pb.ReportExecutionStartupRequest]) (*connect.Response[pb.ReportExecutionStartupResponse], error) {
	f.observation = r.Msg.Observation
	return connect.NewResponse(&pb.ReportExecutionStartupResponse{Observation: r.Msg.Observation}), nil
}

func TestDirectStartupFailurePreservesFirstErrorAndIndependentCleanup(t *testing.T) {
	for _, scenario := range []string{"before-input", "claimed-input", "acknowledged-input", "cleanup-uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			client := &startupReportFixture{}
			job := domain.NewID()
			config := Config{Root: t.TempDir(), execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(job), Revision: 3}, Client: client, Instance: domain.NewID()}}
			// The recovery lock inherits the private process-root ACL on Windows.
			if err := security.PrivateDir(filepath.Join(config.Root, "processes")); err != nil {
				t.Fatal(err)
			}
			if err := security.CreatePrivateDirExclusive(filepath.Join(config.Root, "processes", string(job))); err != nil {
				t.Fatal(err)
			}
			a := newExecutionStartupAttempt(config, job, domain.ExecutionJobInput{MachineID: domain.NewID(), Configuration: domain.ExecutionConfiguration{Harness: domain.Codex}})
			a.claimedWorkspace()
			original := domain.Fail(domain.PermissionDenied, "Access denied.", "Review permissions.")
			returned := error(original)
			switch scenario {
			case "claimed-input":
				a.claimInput()
			case "acknowledged-input":
				a.claimInput()
				a.acknowledgeInput()
			case "cleanup-uncertain":
				returned = a.cleanupFailure(original, domain.Fail(domain.Unavailable, "Cleanup failed.", "Recover it."))
			}
			if a.finish(returned) == nil || client.observation == nil {
				t.Fatal("lost startup failure")
			}
			o := client.observation
			if o.ProblemCode != string(domain.PermissionDenied) || o.CorrelationId != string(job) {
				t.Fatal("cleanup replaced the first failure")
			}
			if (scenario == "before-input") != (o.State == pb.ExecutionStartupState_EXECUTION_STARTUP_STATE_FAILED) {
				t.Fatal("uncertainty granted retry")
			}
			if (scenario == "cleanup-uncertain") != (o.Cleanup == pb.ExecutionStartupCleanup_EXECUTION_STARTUP_CLEANUP_UNCERTAIN) {
				t.Fatal("delivery and cleanup were collapsed")
			}
		})
	}
}

func TestDirectStartupMissingExecutableReportsNoSendWithoutInspection(t *testing.T) {
	f := newCheckpointFixture(t)
	f.input.Version, f.input.Installation = 4, domain.Installation{}
	f.input.Startup = &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: filepath.Join(f.root, "not-installed")}
	raw, err := json.Marshal(f.input)
	if err != nil {
		t.Fatal(err)
	}
	f.job.Input = raw
	client := &startupReportFixture{}
	config := Config{Root: f.root, executionContext: context.Background(), execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(f.jobID), Revision: 2}, Client: client, Instance: domain.NewID()}}
	_, err = executeSession(context.Background(), config, f.jobID, f.job)
	if domain.SafeError(err).Code != domain.NotFound || client.observation == nil || client.observation.State != pb.ExecutionStartupState_EXECUTION_STARTUP_STATE_FAILED || client.observation.InputDelivery != pb.ExecutionStartupInputDelivery_EXECUTION_STARTUP_INPUT_DELIVERY_NOT_SENT || client.observation.Cleanup != pb.ExecutionStartupCleanup_EXECUTION_STARTUP_CLEANUP_CONFIRMED || client.observation.NativeVersion != "" {
		t.Fatalf("missing executable failure was not proven: %v %#v", err, client.observation)
	}
}

func TestForkStartupRetainsOriginalExecutableAfterCreationJournalDeletion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "original-codex.exe")
	if err := os.WriteFile(executable, []byte("private executable fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	installation, err := harness.ResolveExecution(context.Background(), domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: executable})
	if err != nil {
		t.Fatal(err)
	}
	creation, runtime, job := domain.NewID(), domain.NewID(), domain.NewID()
	if err := security.PrivateDir(filepath.Join(root, "runtimes")); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(root, "runtimes", string(runtime))); err != nil {
		t.Fatal(err)
	}
	if err := writeStartupExecutable(root, creation, installation); err != nil {
		t.Fatal(err)
	}
	if err := writeForkStartupExecutable(root, runtime, installation); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "jobs", string(creation))); err != nil {
		t.Fatal(err)
	}
	input := domain.ExecutionJobInput{Version: 4, Startup: &domain.ExecutionStartupSelection{Harness: domain.Codex, ExecutableSHA256: installation.ExecutableSHA256}, Fork: &domain.ForkExecution{JobID: creation, RuntimeID: runtime}}
	config := Config{Root: root, startup: &executionStartupAttempt{}}
	resolved, err := resolveExecutionStartup(context.Background(), config, job, input)
	if err != nil || resolved.ResolvedPath != installation.ResolvedPath || resolved.ExecutableSHA256 != installation.ExecutableSHA256 {
		t.Fatal("Fork lost its child-owned executable identity", err)
	}
	if err := os.Remove(filepath.Join(root, "runtimes", string(runtime), "startup-executable.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveExecutionStartup(context.Background(), config, domain.NewID(), input); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("missing original identity authorized fallback", err)
	}
}

func TestDirectStartupPrelaunchProofRejectsExistingOrUnsyncedScope(t *testing.T) {
	for _, scenario := range []string{"fresh", "empty", "nonempty", "malformed", "symlink", "create-failure", "sync-failure"} {
		t.Run(scenario, func(t *testing.T) {
			client := &startupReportFixture{}
			job := domain.NewID()
			config := Config{Root: t.TempDir(), execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(job), Revision: 3}, Client: client, Instance: domain.NewID()}}
			root := filepath.Join(config.Root, "processes")
			if err := security.PrivateDir(root); err != nil {
				t.Fatal(err)
			}
			owner := filepath.Join(root, string(job))
			switch scenario {
			case "empty", "nonempty":
				if err := security.CreatePrivateDirExclusive(owner); err != nil {
					t.Fatal(err)
				}
				if scenario == "nonempty" {
					if err := os.WriteFile(filepath.Join(owner, "keep"), []byte("retained"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "malformed":
				if err := os.WriteFile(owner, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink fixture requires explicit Windows link privileges")
				}
				if err := os.Symlink(t.TempDir(), owner); err != nil {
					t.Fatal(err)
				}
			}
			create, syncParent := security.CreatePrivateDirExclusive, security.SyncParent
			if scenario == "create-failure" {
				create = func(string) error { return errors.New("injected exclusive-create failure") }
			}
			if scenario == "sync-failure" {
				syncParent = func(string) error { return errors.New("injected parent-sync failure") }
			}
			a := newExecutionStartupAttempt(config, job, domain.ExecutionJobInput{MachineID: domain.NewID(), Configuration: domain.ExecutionConfiguration{Harness: domain.Codex}})
			original := domain.Fail(domain.NotFound, "Missing executable.", "Install it.")
			a.preparePrelaunchCleanup(original, create, syncParent)
			if a.finish(original) != original || client.observation == nil || client.observation.ProblemCode != string(domain.NotFound) {
				t.Fatal("lost original failure")
			}
			confirmed := client.observation.Cleanup == pb.ExecutionStartupCleanup_EXECUTION_STARTUP_CLEANUP_CONFIRMED
			if confirmed != (scenario == "fresh") {
				t.Fatal("foreign or unsynchronized scope granted cleanup proof")
			}
			if _, err := os.Lstat(filepath.Join(root, string(job)+".recovery.lock")); scenario != "fresh" && !os.IsNotExist(err) {
				t.Fatal("uncertain scope was reconciled", err)
			}
			if scenario == "nonempty" {
				if raw, err := os.ReadFile(filepath.Join(owner, "keep")); err != nil || string(raw) != "retained" {
					t.Fatal("original content changed", err)
				}
			}
		})
	}
}

func TestDirectStartupResolvedExecutableLeavesWorkspaceClaimFresh(t *testing.T) {
	for _, scenario := range []string{"fresh", "empty", "nonempty", "malformed", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			existing := scenario != "fresh"
			f := newCheckpointFixture(t)
			f.input.ExecutionID = domain.NewID()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			binary, err = filepath.EvalSymlinks(binary)
			if err != nil {
				t.Fatal(err)
			}
			f.input.Version, f.input.Installation = 4, domain.Installation{}
			f.input.Startup = &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: binary}
			m := &workspace.Manager{Root: f.root}
			preparation := workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			manifest, err := m.Prepare(context.Background(), preparation)
			if err != nil {
				t.Fatal(err)
			}
			f.input.Preparation, _ = json.Marshal(preparation)
			f.input.Manifest, _ = json.Marshal(manifest)
			// A preexisting runtime fails only after the workspace has been claimed.
			if err := security.PrivateDir(filepath.Join(f.root, "runtimes", string(f.input.ExecutionID))); err != nil {
				t.Fatal(err)
			}
			owner := filepath.Join(f.root, "processes", string(f.jobID))
			switch scenario {
			case "empty", "nonempty":
				if err := security.CreatePrivateDirExclusive(owner); err != nil {
					t.Fatal(err)
				}
				if scenario == "nonempty" {
					if err := os.WriteFile(filepath.Join(owner, "keep"), []byte("retained"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "malformed":
				if err := os.WriteFile(owner, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink fixture requires explicit Windows link privileges")
				}
				if err := os.Symlink(t.TempDir(), owner); err != nil {
					t.Fatal(err)
				}
			}
			f.job.Input, _ = json.Marshal(f.input)
			client := &startupReportFixture{}
			config := Config{Root: f.root, executionContext: context.Background(), execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(f.jobID), Revision: 3}, Client: client, Instance: domain.NewID()}}
			_, err = executeSession(context.Background(), config, f.jobID, f.job)
			if domain.SafeError(err).Code != domain.RecoveryRequired || client.observation == nil || client.observation.Phase != pb.ExecutionStartupPhase_EXECUTION_STARTUP_PHASE_LAUNCH {
				t.Fatal("post-resolution failure did not report Launch", err)
			}
			claim := filepath.Join(f.root, "execution-claims", string(f.input.SessionID)+".json")
			_, claimErr := os.Stat(claim)
			if existing != os.IsNotExist(claimErr) {
				t.Fatal("fresh workspace was rejected or existing owner adopted", claimErr)
			}
			if existing {
				if _, err := os.Stat(filepath.Join(f.root, "processes", string(f.jobID)+".recovery.lock")); !os.IsNotExist(err) {
					t.Fatal("rejected owner was reconciled", err)
				}
			}
		})
	}
}

func TestDirectStartupUncertainResolutionCannotCreateCleanupProof(t *testing.T) {
	for _, scenario := range []string{"identity-mismatch", "journal-publication"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCheckpointFixture(t)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			binary, err = filepath.EvalSymlinks(binary)
			if err != nil {
				t.Fatal(err)
			}
			f.input.Version, f.input.Installation = 4, domain.Installation{}
			f.input.Startup = &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: binary}
			if scenario == "identity-mismatch" {
				f.input.Startup.ExecutableSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			} else {
				if err := security.PrivateDir(filepath.Join(f.root, "jobs", string(f.jobID), "startup-executable.json")); err != nil {
					t.Fatal(err)
				}
			}
			f.job.Input, _ = json.Marshal(f.input)
			client := &startupReportFixture{}
			config := Config{Root: f.root, executionContext: context.Background(), execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(f.jobID), Revision: 3}, Client: client, Instance: domain.NewID()}}
			_, err = executeSession(context.Background(), config, f.jobID, f.job)
			if domain.SafeError(err).Code != domain.RecoveryRequired || client.observation == nil || client.observation.Phase != pb.ExecutionStartupPhase_EXECUTION_STARTUP_PHASE_RESOLVE || client.observation.Cleanup != pb.ExecutionStartupCleanup_EXECUTION_STARTUP_CLEANUP_UNCERTAIN {
				t.Fatal("resolution uncertainty granted proof", err)
			}
			if _, err := os.Lstat(filepath.Join(f.root, "processes", string(f.jobID))); !os.IsNotExist(err) {
				t.Fatal("uncertain resolution minted process index", err)
			}
		})
	}
}
