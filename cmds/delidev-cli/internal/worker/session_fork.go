// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/executionenv"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type forkRuntimePhase string

const (
	forkRuntimeUnused            forkRuntimePhase = "unused"
	forkSourceInspectionUnproved forkRuntimePhase = "source-inspection-unproved"
	forkChildNativePossible      forkRuntimePhase = "child-native-possible"
)

type ForkCheckpoint struct {
	DirectoryReload     *codex.DirectoryReloadEvidence `json:"directory_reload,omitempty"`
	SidechatPolicy      domain.SidechatPolicy          `json:"sidechat_policy,omitempty"`
	Version             uint32                         `json:"version"`
	JobID               domain.ID                      `json:"job_id"`
	JobInputDigest      string                         `json:"job_input_digest"`
	SessionID           domain.ID                      `json:"session_id"`
	MachineID           domain.ID                      `json:"machine_id"`
	RuntimeID           domain.ID                      `json:"runtime_id"`
	ConfigurationDigest string                         `json:"configuration_digest"`
	AccountID           domain.ID                      `json:"account_id"`
	ConnectionID        domain.ID                      `json:"connection_id"`
	ManifestDigest      string                         `json:"manifest_digest"`
	Native              codex.ContinuationCheckpoint   `json:"native"`
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
	if input.Retry != nil && (config.execution.Instance != input.Retry.WorkerInstanceID || config.execution.Credential.DeviceID != input.Retry.WorkerDeviceID) {
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
	manager := &workspace.Manager{NamedDirectories: config.namedWorkspaceDirectories, Root: config.Root, Logger: logger}
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
	checkpoint, err := ReadCodexExecutionCheckpoint(manager.Root, ExecutionCheckpointRef{ForkDirectory: assignment.ForkDirectory, Directory: assignment.Directory, ContextRevision: assignment.ContextRevision, ApprovalsReviewer: assignment.Configuration.Options.ApprovalsReviewer, Subscription: assignment.Configuration.Subscription, JobID: input.SourceJobID, SessionID: input.SourceSessionID, MachineID: job.MachineID, HistoryExecutionID: historyID, AssignmentInputDigest: executionInputDigest(mustForkJSON(assignment)), ConfigurationDigest: assignment.ConfigurationDigest, AccountID: assignment.AccountID, ConnectionID: assignment.ConnectionID, Completion: input.Completion, InputMode: assignment.Input.Mode, PromptDigest: assignment.Input.InputDigest(), AcceptedInputs: input.Progress.AcceptedInputs, WorkspaceRoots: nativeCheckpointWorkspaceRoots(manifest, assignment.Directory, assignment.ForkDirectory != nil)})
	if err != nil {
		return nil, err
	}
	if input.Directory != nil {
		projected := assignment
		projected.Directory = input.Directory
		projected.Continuation = &domain.ExecutionContinuation{PreviousDirectory: assignment.Directory, Previous: input.Progress, Completion: input.Completion, HistoryExecutionID: historyID, AssignmentInputDigest: executionInputDigest(mustForkJSON(assignment)), InputMode: assignment.Input.Mode}
		checkpoint, _, err = directoryContinuationProjection(ctx, config.Root, config.execution.Credential, projected, checkpoint)
		if err != nil {
			return nil, err
		}
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
	var ordinaryTools executionenv.Ordinary
	if input.Purpose != domain.SidechatFork {
		ordinaryTools = ordinaryExecutionTools(config.Logger)
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, executionCheckpointUncertain()
	}
	if err := writeForkStartupExecutable(config.Root, input.RuntimeID, installation); err != nil {
		return nil, publicationUncertain()
	}
	phase := forkRuntimeUnused
	var childProcessOwner domain.ID
	var childProcessIdentity os.FileInfo
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
			returned = finishForkChildProcessFailure(config.Root, childProcessOwner, childProcessIdentity, phase, returned)
			returned = finishForkPreNativeFailure(home, phase, returned)
			logger.InfoContext(ctx, "session_fork_failure_ownership", "job_id", owner, "runtime_phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	sourceHome := filepath.Join(root, string(historyID), "codex")
	sourceEnv, err := replaceCodexHome(append([]string(nil), env...), sourceHome)
	if err != nil {
		return nil, err
	}
	processConfig := process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: checkpoint.Native.Effective.Cwd, Env: sourceEnv, Logger: logger}
	phase = forkSourceInspectionUnproved
	sourceConfig := codex.Config{WebSearch: assignment.Configuration.Options.WebSearch, RevertHistory: checkpoint.Native.PaginatedHistory, ManagedForkHistory: assignment.Configuration.Subscription && input.Purpose == domain.IndependentFork, ManagedAuthentication: assignment.Configuration.Subscription, ImageRoot: config.Root, ImageMachineID: job.MachineID, Mode: codex.ThreadProtocol, Version: installation.Version, Home: sourceHome, Process: processConfig}
	if input.Purpose == domain.SidechatFork {
		sourceConfig.Sidechat = codex.ReadOnlySidechatV1
		sourceConfig.WebSearch = ""
		sourceConfig.ManagedAuthentication = assignment.Configuration.Subscription
		if sourceConfig.ManagedAuthentication {
			if err := validateManagedAuthenticationHome(sourceHome, manifest.WorkspaceRoots()); err != nil {
				return nil, err
			}
		}
	}
	if sourceConfig.ManagedAuthentication {
		if err := validateManagedAuthenticationHome(sourceHome, manifest.WorkspaceRoots()); err != nil {
			return nil, err
		}
		if _, err := os.Lstat(filepath.Join(sourceHome, "auth.json")); !errors.Is(err, os.ErrNotExist) {
			return nil, executionCheckpointUncertain()
		}
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
	source, err = source.RehomeSkills(ctx, filepath.Join(home, "codex"))
	if err != nil {
		return nil, err
	}
	checkpoint.Native = source.Checkpoint()
	var childPreparation workspace.PrepareRequest
	var childManifest workspace.Manifest
	var workspaceSnapshot *workspace.ForkSnapshot
	if input.Retry != nil {
		if domain.Decode(input.Retry.ChildPreparation, &childPreparation) != nil || domain.Decode(input.Retry.ChildManifest, &childManifest) != nil || childPreparation.SessionID != input.ChildSessionID || childPreparation.MachineID != job.MachineID || childPreparation.ForkSourceID != input.SourceSessionID {
			return nil, executionCheckpointUncertain()
		}
		err = manager.VerifySidechatReference(ctx, childPreparation, childManifest)
	} else if input.Purpose == domain.SidechatFork {
		childPreparation, childManifest, err = manager.PrepareSidechatReference(ctx, owner, input.ChildSessionID, preparation, manifest)
		if err == nil {
			unpublishedSidechatInput = childPreparation
			unpublishedSidechat = &childManifest
		}
	} else {
		childPreparation, err = manager.ForkPreparation(ctx, manifest, input.ChildSessionID, input.Workspace)
		if err == nil {
			childProcessOwner = input.ChildSessionID
			childProcessIdentity = childPreparation.ForkProcessIdentity()
			if childProcessIdentity == nil && len(childPreparation.Repositories) != 0 {
				err = executionCheckpointUncertain()
			}
		}
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
	instructions, err := childSnapshot.Configuration.NativeInstructions(assignment.Input.Mode)
	if err != nil {
		return nil, err
	}
	selectedLocation := forkSelectedLocation(input)
	childCwd := childManifest.PrimaryPath
	if selectedLocation != nil {
		childCwd, err = mappedForkDirectory(manifest, childManifest, selectedLocation, checkpoint.Native.Effective.Cwd)
		if err != nil {
			return nil, err
		}
	}
	settings := codex.ThreadSettings{Model: assignment.Configuration.NativeModel, Provider: codexExecutionProvider(assignment.Configuration.Subscription), Effort: assignment.Configuration.Effort, Cwd: childCwd, WorkspaceRoots: nativeCheckpointWorkspaceRoots(childManifest, nil, selectedLocation != nil), Instructions: instructions, Options: assignment.Configuration.Options}
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
		settings.Options.ApprovalsReviewer = ""
		settings.Options.Permission, settings.Options.ApprovalPolicy, settings.Options.ApprovalReviewModel = domain.PermissionReadOnly, "never", ""
		settings.Options.SubagentModel, settings.Options.SubagentEffort, settings.Options.MaxConcurrency = "", "", 0
		if settings.Options.WebSearch != "" {
			settings.Options.WebSearch = domain.WebSearchDisabled
		}
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
	managedSkills, err := (skills.Manager{Root: config.Root}).ManagedRuntime(ctx, config.execution.Credential.ServerID, config.execution.Credential.DeviceID, assignment.Configuration.ManagedSkills, filepath.Join(home, "codex"))
	if err != nil {
		return nil, err
	}
	nativeConfig := codex.Config{WebSearch: settings.Options.WebSearch, ManagedSkills: managedSkills, RevertHistory: checkpoint.Native.PaginatedHistory, ManagedForkHistory: assignment.Configuration.Subscription && input.Purpose == domain.IndependentFork, ImageRoot: config.Root, ImageMachineID: job.MachineID, Mode: codex.ThreadProtocol, Version: installation.Version, Home: filepath.Join(home, "codex"), API: &codex.APIConfig{ServerOrigin: config.execution.Credential.Endpoint, Token: apiproxy.TokenPrefix + rawToken}, Process: processConfig}
	if input.Purpose != domain.SidechatFork {
		nativeConfig.OrdinaryTools = ordinaryTools
	}
	if input.Purpose == domain.SidechatFork {
		nativeConfig.Sidechat = codex.ReadOnlySidechatV1
	}

	var managed *managedSubscriptionLease
	var latest []byte
	var closeRPC func()
	cleanup, captured, unused, preNative, finished := false, false, false, false, false
	defer func() {
		if closeRPC != nil {
			defer closeRPC()
		}
		if managed == nil {
			return
		}
		defer clear(managed.response.Bundle)
		if finished {
			return
		}
		if preNative {
			cleanup = cleanupUnusedExecutionAuthentication(nativeConfig.Home, managed.response.Bundle) == nil
			if cleanup {
				latest = bytes.Clone(managed.response.Bundle)
				unused = true
			}
		}
		conclusive := cleanup && (captured || unused)
		if err := managed.finish(latest, cleanup, false, captured); err != nil {
			output, returned = nil, &managedExecutionUncertain{err}
		} else if !conclusive {
			output, returned = nil, &managedExecutionUncertain{subscription.Invalid()}
		}
	}()
	if assignment.Configuration.Subscription {
		if err := validateManagedAuthenticationHome(nativeConfig.Home, manifest.WorkspaceRoots()); err != nil {
			return nil, err
		}
		rpcClient, close, err := subscriptionRPC(ctx, config, config.execution.Credential)
		if err != nil {
			return nil, err
		}
		closeRPC = close
		logger.InfoContext(ctx, "managed_fork_authentication", "job_id", owner, "phase", "take")
		managed, err = takeManagedSubscription(ctx, config, rpcClient, config.execution.Credential, config.execution.Instance, assignment.AccountID, owner, config.execution.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
		if err != nil {
			return nil, err
		}
		if managed.response.GenerationId != string(input.SubscriptionGeneration) {
			return nil, &managedExecutionUncertain{subscription.Invalid()}
		}
		if _, _, err := subscription.Parse(managed.response.Bundle); err != nil {
			return nil, err
		}
		if err := security.PrivateDir(nativeConfig.Home); err != nil {
			return nil, err
		}
		preNative = true
		if err := security.WriteAtomic(filepath.Join(nativeConfig.Home, "auth.json"), managed.response.Bundle); err != nil {
			return nil, subscription.Invalid()
		}
		nativeConfig.API, nativeConfig.ManagedAuthentication = nil, true
	}
	preNative = false
	client, err := codex.Open(ctx, nativeConfig)
	if err != nil {
		preNative = domain.SafeError(err).Code != domain.RecoveryRequired
		return nil, executionCheckpointUncertain()
	}
	var bound codex.ThreadResult
	var forkErr error
	if selectedLocation != nil {
		bound, forkErr = client.ForkDirectoryThread(ctx, input.NativeRequestID, source, settings)
	} else {
		bound, forkErr = client.ForkThread(ctx, input.NativeRequestID, source, settings)
	}
	var reload *codex.DirectoryReloadEvidence
	if forkErr == nil && selectedLocation != nil {
		proof, e := client.ReadDirectoryReloadEvidence(ctx, domain.NewID(), bound)
		if e != nil {
			forkErr = e
		} else {
			reload = &proof
		}
	}

	if managed != nil {
		bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
		var captureErr error
		logger.InfoContext(ctx, "managed_fork_authentication", "job_id", owner, "phase", "capture")
		latest, captureErr = client.ManagedBundle(bounded, false)
		captured = captureErr == nil
		stop()
	}
	nativeErr, helperErr := client.CloseWithExecutionHelpers()
	closeErr = errors.Join(nativeErr, helperErr)
	if managed != nil && nativeErr == nil {
		logger.InfoContext(ctx, "managed_fork_authentication", "job_id", owner, "phase", "cleanup")
		authErr := cleanupExecutionAuthentication(nativeConfig.Home, latest, managed.response.Bundle)
		cleanup = helperErr == nil && authErr == nil
		logExecutionAuthenticationCleanup(config.Logger, owner, authErr)
		if !captured || authErr != nil {
			return nil, &managedExecutionUncertain{subscription.Invalid()}
		}
	}
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
	native.ContextRevision = 0
	native.PaginatedHistory = bound.Thread.History == codex.PaginatedHistory
	native.ThreadID, native.SessionID, native.Effective = bound.Thread.ID, bound.Thread.SessionID, *bound.Effective
	file := ForkCheckpoint{DirectoryReload: reload, Version: input.Version, SidechatPolicy: childSnapshot.Configuration.SidechatPolicy, JobID: owner, JobInputDigest: executionInputDigest(job.Input), SessionID: input.ChildSessionID, MachineID: job.MachineID, RuntimeID: input.RuntimeID, ConfigurationDigest: childSnapshot.ConfigurationDigest, AccountID: assignment.AccountID, ConnectionID: assignment.ConnectionID, ManifestDigest: executionInputDigest(mustForkJSON(childManifest)), Native: native}
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

	var managedFinish domain.ID
	if managed != nil {
		logger.InfoContext(ctx, "managed_fork_authentication", "job_id", owner, "phase", "finish")
		if err := managed.finish(latest, cleanup, false, captured); err != nil {
			finished = true
			return nil, &managedExecutionUncertain{err}
		}
		finished = true
		managedFinish = managed.finishID
	}
	return json.Marshal(domain.ForkJobResult{ManagedFinish: managedFinish, Version: input.Version, ChildSessionID: input.ChildSessionID, RuntimeID: input.RuntimeID, NativeThreadID: domain.NativeIdentity(bound.Thread.ID), NativeTurnID: input.Completion.NativeTurnID, CheckpointDigest: executionInputDigest(raw), Preparation: mustForkJSON(childPreparation), Manifest: mustForkJSON(childManifest), CleanupVerified: true})
}

// Preparation reads use the unpublished child owner. Only a definite rejection
// before native creation may retire it; cleanup runs independently of its caller.
func finishForkChildProcessFailure(root string, child domain.ID, original os.FileInfo, phase forkRuntimePhase, returned error) error {
	if returned == nil || child == "" || phase != forkRuntimeUnused || domain.SafeError(returned).Code == domain.RecoveryRequired {
		return returned
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := process.RetireCompletedOwnerContext(ctx, filepath.Join(root, "processes"), child, original); err != nil {
		return executionCheckpointUncertain()
	}
	return returned
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
	if input.Configuration.Subscription && input.Configuration.SidechatPolicy == "" && (result.Native.ForkHistory == nil || result.Native.ForkHistory.TurnsCount == 0 || result.Native.ForkHistory.TurnsCount > 128 || len(result.Native.ForkHistory.HistoryDigest) != 64) {
		return codex.ContinuationCheckpoint{}, executionCheckpointUncertain()
	}
	return result.Native, nil
}

func forkSelectedLocation(input domain.ForkJobInput) *domain.SessionDirectoryLocation {
	if input.Directory != nil {
		return &domain.SessionDirectoryLocation{RepositoryID: input.Directory.RepositoryID, RelativePath: input.Directory.RelativePath}
	}
	return input.SourceAssignment.ForkDirectory
}
func mappedForkDirectory(source, child workspace.Manifest, location *domain.SessionDirectoryLocation, sourceCwd string) (string, error) {
	if location == nil || location.Validate() != nil {
		return "", domain.DirectoryUncertain()
	}
	original, destination := source.PrimaryPath, child.PrimaryPath
	if location.RepositoryID != "" {
		original, destination = "", ""
		for _, r := range source.Repositories {
			if r.ID == location.RepositoryID {
				original = r.Path
			}
		}
		for _, r := range child.Repositories {
			if r.ID == location.RepositoryID {
				destination = r.Path
			}
		}
	}
	if original == "" || destination == "" || filepath.Join(original, filepath.FromSlash(location.RelativePath)) != sourceCwd {
		return "", domain.DirectoryUncertain()
	}
	selected := filepath.Join(destination, filepath.FromSlash(location.RelativePath))
	canonical, e := filepath.EvalSymlinks(selected)
	info, statErr := os.Stat(selected)
	if e != nil || statErr != nil || canonical != selected || !info.IsDir() {
		return "", domain.DirectoryUncertain()
	}
	return selected, nil
}
func readForkDirectoryReload(root string, input domain.ExecutionJobInput) (*codex.DirectoryReloadEvidence, error) {
	if _, err := readForkCheckpoint(root, input); err != nil {
		return nil, err
	}
	var p ForkCheckpoint
	raw, err := security.ReadPrivate(filepath.Join(root, "runtimes", string(input.Fork.RuntimeID), "fork-completion.json"), maxExecutionCheckpointBytes)
	if err != nil || domain.Decode(raw, &p) != nil || p.DirectoryReload == nil {
		return nil, domain.DirectoryUncertain()
	}
	return p.DirectoryReload, nil
}
func verifyInheritedDirectoryReload(ctx context.Context, native *codex.Client, bound codex.ThreadResult, expected *codex.DirectoryReloadEvidence, selection *workspace.DirectorySelection) (*codex.DirectoryReloadEvidence, error) {
	observed, err := native.ReadDirectoryReloadEvidence(ctx, domain.NewID(), bound)
	if err != nil || expected == nil || !sameDirectoryValue(observed, *expected) || selection == nil || selection.Verify() != nil {
		return nil, domain.DirectoryUncertain()
	}
	return &observed, nil
}
