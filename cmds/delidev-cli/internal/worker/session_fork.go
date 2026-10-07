// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type forkRuntimePhase string

const (
	forkRuntimeUnused            forkRuntimePhase = "unused"
	forkSourceInspectionUnproved forkRuntimePhase = "source-inspection-unproved"
	forkChildNativePossible      forkRuntimePhase = "child-native-possible"
)

type ForkCheckpoint struct {
	SidechatPolicy      domain.SidechatPolicy        `json:"sidechat_policy,omitempty"`
	Version             uint32                       `json:"version"`
	JobID               domain.ID                    `json:"job_id"`
	JobInputDigest      string                       `json:"job_input_digest"`
	SessionID           domain.ID                    `json:"session_id"`
	MachineID           domain.ID                    `json:"machine_id"`
	RuntimeID           domain.ID                    `json:"runtime_id"`
	ConfigurationDigest string                       `json:"configuration_digest"`
	AccountID           domain.ID                    `json:"account_id"`
	ConnectionID        domain.ID                    `json:"connection_id"`
	ManifestDigest      string                       `json:"manifest_digest"`
	Native              codex.ContinuationCheckpoint `json:"native"`
}

func replaceCodexHome(env []string, home string) ([]string, error) {
	count := 0
	for i, value := range env {
		if strings.HasPrefix(value, "CODEX_HOME=") {
			env[i] = "CODEX_HOME=" + home
			count++
		}
	}
	if count != 1 {
		return nil, executionCheckpointUncertain()
	}
	return env, nil
}

func forkSession(ctx context.Context, config Config, owner domain.ID, job domain.Job) (output json.RawMessage, returned error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var input domain.ForkJobInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || config.execution == nil || config.execution.Credential.MachineID != job.MachineID {
		return nil, executionCheckpointUncertain()
	}
	logger := config.Logger
	if logger == nil {
		return nil, executionCheckpointUncertain()
	}
	logger.InfoContext(ctx, "session_fork_started", "job_id", owner, "source_session_id", input.SourceSessionID, "child_session_id", input.ChildSessionID)
	defer func() {
		code := domain.Code("")
		if returned != nil {
			code = domain.SafeError(returned).Code
		}
		logger.InfoContext(ctx, "session_fork_finished", "job_id", owner, "code", code)
	}()
	assignment := input.SourceAssignment
	if assignment.Configuration.Harness == domain.OpenCode {
		return forkOpenCodeSession(ctx, config, owner, job, input)
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(assignment.Preparation, &preparation) != nil || domain.Decode(assignment.Manifest, &manifest) != nil || workspace.ValidateResult(preparation, manifest, runtime.GOOS) != nil {
		return nil, executionCheckpointUncertain()
	}
	if input.Workspace == domain.Local && input.Purpose != domain.SidechatFork {
		if err := workspace.ValidateLocalForkSource(manifest); err != nil {
			return nil, err
		}
	}
	manager := &workspace.Manager{Root: config.Root, Logger: logger}
	inspection, err := manager.InspectClosedExecution(ctx, workspace.ExecutionPredecessor{JobID: input.SourceJobID, ExecutionID: assignment.ExecutionID}, preparation, manifest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if inspection.Close() != nil {
			output, returned = nil, executionCheckpointUncertain()
		}
	}()
	historyID := assignment.ExecutionID
	if assignment.Continuation != nil {
		historyID = assignment.Continuation.HistoryExecutionID
	}
	if assignment.Fork != nil {
		historyID = assignment.Fork.RuntimeID
	}
	checkpoint, err := ReadCodexExecutionCheckpoint(manager.Root, ExecutionCheckpointRef{JobID: input.SourceJobID, SessionID: input.SourceSessionID, MachineID: job.MachineID, HistoryExecutionID: historyID, AssignmentInputDigest: executionInputDigest(mustForkJSON(assignment)), ConfigurationDigest: assignment.ConfigurationDigest, AccountID: assignment.AccountID, ConnectionID: assignment.ConnectionID, Completion: input.Completion, InputMode: assignment.Input.Mode, PromptDigest: sha256.Sum256([]byte(assignment.Input.Prompt)), AcceptedInputs: input.Progress.AcceptedInputs, WorkspaceRoots: nativeWorkspaceRoots(manifest)})
	if err != nil {
		return nil, err
	}
	if assignment.Version != 4 && !domain.CodexVersionAllowed(assignment.Installation.Version) {
		return nil, executionCheckpointUncertain()
	}
	installation, err := resolveOriginalStartup(ctx, config, input.SourceJobID, assignment)
	if err != nil {
		return nil, err
	}
	if input.Startup != nil && input.Startup.ExecutableSHA256 != installation.ExecutableSHA256 {
		return nil, executionCheckpointUncertain()
	}
	if err := writeStartupExecutable(config.Root, owner, installation); err != nil {
		return nil, publicationUncertain()
	}
	executable := installation.ResolvedPath
	canonical, err := filepath.EvalSymlinks(executable)
	if err != nil || canonical != executable || !filepath.IsAbs(executable) {
		return nil, executionCheckpointUncertain()
	}
	root := filepath.Join(manager.Root, "runtimes")
	if err := security.PrivateDir(root); err != nil {
		return nil, executionCheckpointUncertain()
	}
	// The original synchronized Worker operation journal precedes all sends.
	// Exclusive fresh runtime creation prevents a replacement job from adopting
	// an unconfirmed native child after an interrupted fork.
	home := filepath.Join(root, string(input.RuntimeID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, executionCheckpointUncertain()
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, executionCheckpointUncertain()
	}
	if err := writeForkStartupExecutable(config.Root, input.RuntimeID, installation); err != nil {
		return nil, publicationUncertain()
	}
	phase := forkRuntimeUnused
	var unpublishedSidechatInput workspace.PrepareRequest
	var unpublishedSidechat *workspace.Manifest
	defer func() {
		if returned != nil {
			output = nil
			if unpublishedSidechat != nil {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				cleanupErr := manager.DiscardUnpublishedSidechatReference(cleanupCtx, owner, unpublishedSidechatInput, *unpublishedSidechat)
				cancel()
				if cleanupErr != nil {
					returned = executionCheckpointUncertain()
				}
			}
			returned = finishForkPreNativeFailure(home, phase, returned)
			logger.InfoContext(ctx, "session_fork_failure_ownership", "job_id", owner, "runtime_phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	sourceHome := filepath.Join(root, string(historyID), "codex")
	sourceEnv, err := replaceCodexHome(append([]string(nil), env...), sourceHome)
	if err != nil {
		return nil, err
	}
	processConfig := process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: manifest.PrimaryPath, Env: sourceEnv, Logger: logger}
	phase = forkSourceInspectionUnproved
	sourceConfig := codex.Config{Mode: codex.ThreadProtocol, Version: installation.Version, Home: sourceHome, Process: processConfig}
	if input.Purpose == domain.SidechatFork {
		sourceConfig.Sidechat = codex.ReadOnlySidechatV1
	}
	sourceClient, err := codex.Open(ctx, sourceConfig)
	if err != nil {
		return nil, err
	}
	source, inspectErr := sourceClient.InspectForkSource(ctx, checkpoint.Native)
	closeErr := sourceClient.Close()
	if closeErr != nil {
		return nil, executionCheckpointUncertain()
	}
	phase = forkRuntimeUnused
	if inspectErr != nil {
		return nil, inspectErr
	}
	var childPreparation workspace.PrepareRequest
	var childManifest workspace.Manifest
	var workspaceSnapshot *workspace.ForkSnapshot
	if input.Purpose == domain.SidechatFork {
		childPreparation, childManifest, err = manager.PrepareSidechatReference(ctx, owner, input.ChildSessionID, preparation, manifest)
		if err == nil {
			unpublishedSidechatInput = childPreparation
			unpublishedSidechat = &childManifest
		}
	} else {
		childPreparation, err = manager.ForkPreparation(ctx, manifest, input.ChildSessionID, input.Workspace)
		if err == nil {
			workspaceSnapshot, err = manager.InspectForkSnapshot(ctx, manifest, childPreparation)
		}
		if err == nil {
			childManifest, err = manager.PrepareFork(ctx, childPreparation, workspaceSnapshot)
		}
	}
	if err != nil {
		return nil, err
	}
	childSnapshot, err := input.ChildSnapshot()
	if err != nil {
		return nil, err
	}
	settings := codex.ThreadSettings{Model: assignment.Configuration.NativeModel, Provider: codex.APIProvider, Effort: assignment.Configuration.Effort, Cwd: childManifest.PrimaryPath, WorkspaceRoots: nativeWorkspaceRoots(childManifest), Instructions: assignment.Configuration.Instructions, Options: assignment.Configuration.Options}
	// Pin native defaults to the original effective observations. Configuration
	// itself remains byte-identical; observed defaults cannot become new choices.
	settings.Effort = valueOrEmpty(checkpoint.Native.Effective.Effort)
	settings.Options.ServiceTier = valueOrEmpty(checkpoint.Native.Effective.ServiceTier)
	settings.Options.ApprovalPolicy = string(checkpoint.Native.Effective.ApprovalPolicy)
	switch checkpoint.Native.Effective.Sandbox.Type {
	case codex.ReadOnly:
		settings.Options.Permission = domain.PermissionReadOnly
	case codex.WorkspaceWrite:
		settings.Options.Permission = domain.PermissionWorkspaceWrite
	case codex.FullAccess:
		settings.Options.Permission = domain.PermissionFullAccess
	default:
		return nil, executionCheckpointUncertain()
	}
	if input.Purpose == domain.SidechatFork {
		settings.Options.Permission, settings.Options.ApprovalPolicy, settings.Options.ApprovalReviewModel = domain.PermissionReadOnly, "never", ""
		settings.Options.SubagentModel, settings.Options.SubagentEffort, settings.Options.MaxConcurrency = "", "", 0
	}
	processConfig.Env, processConfig.Cwd = env, settings.Cwd
	rawToken, err := security.RandomToken()
	if err != nil {
		return nil, executionCheckpointUncertain()
	}
	// This random credential is deliberately unregistered. Fork has no provider
	// inference authority; only the later ordinary execution can register a grant.
	// From this attempt onward the runtime may contain native child state. Even
	// an Open failure cannot justify deleting it through pre-native rollback.
	phase = forkChildNativePossible
	nativeConfig := codex.Config{Mode: codex.ThreadProtocol, Version: installation.Version, Home: filepath.Join(home, "codex"), API: &codex.APIConfig{ServerOrigin: config.execution.Credential.Endpoint, Token: apiproxy.TokenPrefix + rawToken}, Process: processConfig}
	if input.Purpose == domain.SidechatFork {
		nativeConfig.Sidechat = codex.ReadOnlySidechatV1
	}
	client, err := codex.Open(ctx, nativeConfig)
	if err != nil {
		return nil, executionCheckpointUncertain()
	}
	bound, forkErr := client.ForkThread(ctx, input.NativeRequestID, source, settings)
	closeErr = client.Close()
	if forkErr != nil || closeErr != nil || bound.Thread == nil || bound.Effective == nil {
		return nil, executionCheckpointUncertain()
	}
	if input.Purpose == domain.SidechatFork {
		err = manager.VerifySidechatReference(ctx, childPreparation, childManifest)
	} else {
		err = workspaceSnapshot.Verify(ctx, childManifest)
	}
	if source.Verify(ctx) != nil || err != nil {
		return nil, executionCheckpointUncertain()
	}
	native := checkpoint.Native
	native.ThreadID, native.SessionID, native.Effective = bound.Thread.ID, bound.Thread.SessionID, *bound.Effective
	file := ForkCheckpoint{Version: input.Version, SidechatPolicy: childSnapshot.Configuration.SidechatPolicy, JobID: owner, JobInputDigest: executionInputDigest(job.Input), SessionID: input.ChildSessionID, MachineID: job.MachineID, RuntimeID: input.RuntimeID, ConfigurationDigest: childSnapshot.ConfigurationDigest, AccountID: assignment.AccountID, ConnectionID: assignment.ConnectionID, ManifestDigest: executionInputDigest(mustForkJSON(childManifest)), Native: native}
	raw, err := json.Marshal(file)
	if err != nil || len(raw) > maxExecutionCheckpointBytes {
		return nil, executionCheckpointUncertain()
	}
	path := filepath.Join(home, "fork-completion.json")
	if err := security.WriteAtomic(path, raw); err != nil {
		return nil, executionCheckpointUncertain()
	}
	if err := security.SyncParent(path); err != nil {
		return nil, executionCheckpointUncertain()
	}
	if err := ctx.Err(); err != nil {
		return nil, executionCheckpointUncertain()
	}
	return json.Marshal(domain.ForkJobResult{Version: input.Version, ChildSessionID: input.ChildSessionID, RuntimeID: input.RuntimeID, NativeThreadID: domain.NativeIdentity(bound.Thread.ID), NativeTurnID: input.Completion.NativeTurnID, CheckpointDigest: executionInputDigest(raw), Preparation: mustForkJSON(childPreparation), Manifest: mustForkJSON(childManifest), CleanupVerified: true})
}

// Every pre-native validation/preparation return shares this guard. Workspace
// rollback remains independently owned by PrepareFork; possible native children
// and unjoined source inspection must never borrow unused-runtime removal proof.
// PrepareFork can retain owned copies on RecoveryRequired without attempting
// rollback, so even an unused native runtime must remain with that evidence.
func finishForkPreNativeFailure(home string, phase forkRuntimePhase, returned error) error {
	if returned == nil || phase == forkChildNativePossible || domain.SafeError(returned).Code == domain.RecoveryRequired {
		return returned
	}
	if phase != forkRuntimeUnused {
		return executionCheckpointUncertain()
	}
	return finishForkSourceInspection(home, returned, nil)
}

// No native child has been created at this boundary. A rejected
// source must not leave an unused runtime behind, but an unjoined inspection
// process or unconfirmed removal still prevents a fresh fork attempt.
func finishForkSourceInspection(home string, inspectErr, closeErr error) error {
	if closeErr != nil {
		return executionCheckpointUncertain()
	}
	if inspectErr == nil {
		return nil
	}
	if err := os.RemoveAll(home); err != nil {
		return executionCheckpointUncertain()
	}
	if err := security.SyncParent(home); err != nil {
		return executionCheckpointUncertain()
	}
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return executionCheckpointUncertain()
	}
	return inspectErr
}

func mustForkJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func readForkCheckpoint(root string, input domain.ExecutionJobInput) (codex.ContinuationCheckpoint, error) {
	var result ForkCheckpoint
	f := input.Fork
	if f == nil || f.Validate(input) != nil {
		return result.Native, executionCheckpointUncertain()
	}
	if _, err := executionCheckpointPath(root, f.RuntimeID); err != nil {
		return result.Native, err
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "runtimes", string(f.RuntimeID), "fork-completion.json"), maxExecutionCheckpointBytes)
	if err != nil || executionInputDigest(raw) != f.CheckpointDigest || domain.Decode(raw, &result) != nil || ((input.Configuration.SidechatPolicy == "" && result.Version != 1) || (input.Configuration.SidechatPolicy == domain.CodexReadOnlySidechatV1 && result.Version != 3)) || result.SidechatPolicy != input.Configuration.SidechatPolicy || result.JobID != f.JobID || result.SessionID != input.SessionID || result.MachineID != input.MachineID || result.RuntimeID != f.RuntimeID || result.ConfigurationDigest != input.ConfigurationDigest || result.AccountID != input.AccountID || result.ConnectionID != input.ConnectionID || result.ManifestDigest != executionInputDigest(input.Manifest) || string(result.Native.ThreadID) != string(f.NativeThreadID) || string(result.Native.TurnID) != string(f.NativeTurnID) || result.Native.Status != codex.TurnCompleted || string(mustForkJSON(result)) != string(raw) {
		return codex.ContinuationCheckpoint{}, executionCheckpointUncertain()
	}
	return result.Native, nil
}
