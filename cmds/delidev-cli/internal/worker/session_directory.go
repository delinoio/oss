// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func mustDirectoryJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }

func executeSessionDirectory(ctx context.Context, config Config, owner domain.ID, job domain.Job) (output json.RawMessage, returned error) {
	c := config.execution
	var i domain.SessionDirectoryInput
	if c == nil || c.Assignment == nil || domain.ID(c.Assignment.Id) != owner || config.executionContext == nil || domain.DecodeWithLimit(job.Input, &i, domain.MaxCompactionInputBytes) != nil || i.Validate() != nil || c.Credential.MachineID != i.Assignment.MachineID || job.ParentID != i.SourceJobID || job.AssignedDeviceID != c.Credential.DeviceID || job.InstanceID != c.Instance || job.MachineID != c.Credential.MachineID || c.Assignment.Revision == 0 {
		return nil, domain.DirectoryUncertain()
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("job_id", owner, "session_id", i.Assignment.SessionID)
	phase := compactionPrepare
	nativeClosed, workspaceClosed, authenticationClosed, proxyClosed := false, false, !i.Assignment.Configuration.Subscription, true
	cleanupFailed, managedPreNative := false, false
	defer func() {
		if returned != nil {
			logger.WarnContext(ctx, "session_directory_uncertain", "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	var prep workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(i.Assignment.Preparation, &prep) != nil || domain.Decode(i.Assignment.Manifest, &manifest) != nil || workspace.ValidateResult(prep, manifest, runtime.GOOS) != nil {
		return nil, domain.DirectoryUncertain()
	}
	manager := &workspace.Manager{Root: config.Root, Logger: logger}
	predecessor := workspace.ExecutionPredecessor{JobID: i.SourceJobID, ExecutionID: i.Assignment.ExecutionID}
	if i.ContextAction != nil {
		predecessor = workspace.ExecutionPredecessor{JobID: i.ContextAction.JobID, ExecutionID: i.ContextAction.ActionID}
	}
	lease, err := manager.InspectClosedExecution(ctx, predecessor, prep, manifest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := lease.Close(); err != nil {
			output, returned = nil, err
		} else {
			workspaceClosed = true
		}
	}()
	selection, err := lease.SelectDirectory(i.RepositoryID, i.RelativePath)
	if err != nil {
		return nil, err
	}
	defer selection.Close()
	source, err := directorySourceCheckpoint(config.Root, i, manifest)
	if err != nil {
		return nil, err
	}
	contextProof, err := directoryContextCheckpoint(ctx, config.Root, c.Credential, i, source)
	if err != nil {
		return nil, err
	}
	before := source.Native
	if i.Previous != nil {
		previous, err := readDirectoryCheckpoint(config.Root, c.Credential, i.Assignment, *i.Previous)
		if err != nil {
			return nil, err
		}
		if source.Completion.ExecutionID == previous.Source.Completion.ExecutionID {
			if !sameDirectoryValue(source, previous.Source) {
				return nil, domain.DirectoryUncertain()
			}
			before = previous.Selected
		} else if !codex.DirectorySettingsEqual(previous.Selected.Effective, source.Native.Effective) || source.Native.Effective.Cwd != previous.Selected.Effective.Cwd {
			return nil, domain.DirectoryUncertain()
		}
	}
	if selection.Path() == before.Effective.Cwd || !sameDirectoryValue(directoryOriginalRoots(before.Effective), manifest.WorkspaceRoots()) {
		return nil, domain.DirectoryUncertain()
	}
	installation, err := resolveOriginalStartup(ctx, config, i.SourceJobID, i.Assignment)
	if err != nil {
		return nil, err
	}
	executable := installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.DirectoryUncertain()
	}
	runtimeRoot := filepath.Join(config.Root, "runtimes")
	if security.CheckPrivateDir(runtimeRoot) != nil {
		return nil, domain.DirectoryUncertain()
	}
	home := filepath.Join(runtimeRoot, string(i.GenerationID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, domain.DirectoryUncertain()
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, err
	}
	nativeHome := filepath.Join(runtimeRoot, string(i.HistoryExecutionID), "codex")
	env, err = replaceCodexHome(env, nativeHome)
	if err != nil {
		return nil, err
	}
	if contextProof != nil {
		if err := codex.VerifyCompactionRollout(ctx, nativeHome, *contextProof); err != nil {
			return nil, err
		}
	} else if err := codex.VerifyContinuationContextRollout(ctx, nativeHome, before); err != nil {
		return nil, err
	}
	instructions, err := i.Assignment.Configuration.NativeInstructions(i.Assignment.Input.Mode)
	if err != nil {
		return nil, err
	}
	settings := directoryThreadSettings(before.Effective, selection.Path(), manifest.WorkspaceRoots(), instructions, i.Assignment.Configuration.Options)
	ownership := directoryOwnership{ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, JobID: owner, InstanceID: c.Instance, Revision: c.Assignment.Revision, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), InputDigest: executionInputDigest(mustDirectoryJSON(i))}
	var managed *managedSubscriptionLease
	var managedLatest []byte
	var closeManagedRPC func()
	managedCleanup, managedSuccess, managedUnused := false, false, false
	defer func() {
		if closeManagedRPC != nil {
			defer closeManagedRPC()
		}
		if managed == nil {
			return
		}
		if managedPreNative {
			managedCleanup = cleanupUnusedExecutionAuthentication(nativeHome, managed.response.Bundle) == nil
			if managedCleanup {
				managedLatest = bytes.Clone(managed.response.Bundle)
				managedUnused = true
			}
		}
		conclusive := managedCleanup && (managedSuccess || managedUnused)
		if err := managed.finish(managedLatest, managedCleanup, false, managedSuccess); err != nil {
			cleanupFailed = true
			output, returned = nil, &managedExecutionUncertain{err}
		} else if conclusive {
			authenticationClosed = true
		} else if !conclusive {
			cleanupFailed = true
			output, returned = nil, &managedExecutionUncertain{subscription.Invalid()}
		}
		clear(managed.response.Bundle)
	}()
	if i.Assignment.Configuration.Subscription {
		if err := validateManagedAuthenticationHome(nativeHome, manifest.WorkspaceRoots()); err != nil {
			return nil, err
		}
		client, closeRPC, err := subscriptionRPC(ctx, config, c.Credential)
		if err != nil {
			return nil, err
		}
		closeManagedRPC = closeRPC
		managed, err = takeManagedSubscription(ctx, config, client, c.Credential, c.Instance, i.Assignment.AccountID, owner, c.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
		if err != nil {
			return nil, err
		}
		if _, _, err = subscription.Parse(managed.response.Bundle); err != nil {
			return nil, err
		}
		managedPreNative = true
		if err := security.WriteAtomic(filepath.Join(nativeHome, "auth.json"), managed.response.Bundle); err != nil {
			return nil, subscription.Invalid()
		}
	}
	token, err := security.RandomToken()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	token = apiproxy.TokenPrefix + token
	digest := sha256.Sum256([]byte(token))
	registration := domain.NewID()
	registrationClaim := directoryRegistration{Ownership: ownership, RequestID: registration, CredentialDigest: hex.EncodeToString(digest[:])}
	if err := writeDirectoryClaim(config.Root, owner, directoryRegistrationClaim, registrationClaim); err != nil {
		return nil, err
	}
	phase = compactionRegister
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	reply, err := c.Client.RegisterExecution(bounded, authenticated(c.Credential, &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(registration), Id: string(owner), ExpectedRevision: c.Assignment.Revision}, MachineId: string(i.Assignment.MachineID), InstanceId: string(c.Instance), CredentialDigest: digest[:]}))
	stop()
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	expectedProxy := apiproxy.Prefix
	if managed != nil {
		expectedProxy = ""
	}
	if reply == nil || reply.Msg == nil || reply.Msg.ProxyPath != expectedProxy {
		return nil, domain.DirectoryUncertain()
	}
	nativeCtx, cancel := context.WithCancel(config.executionContext)
	defer cancel()
	cancelAction := context.AfterFunc(ctx, cancel)
	defer cancelAction()
	nativeConfig := codex.Config{EnableImageGeneration: i.Assignment.NativeImageGeneration, SkillsRoot: config.Root, RevertHistory: before.PaginatedHistory || before.ContextRevision > 0 || contextProof != nil && contextProof.Revert != nil, ImageRoot: config.Root, ImageMachineID: i.Assignment.MachineID, Mode: codex.ThreadProtocol, Version: installation.Version, Home: nativeHome, API: &codex.APIConfig{ServerOrigin: c.Credential.Endpoint, Token: token}, Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: settings.Cwd, Env: env, Logger: logger}}
	if i.Assignment.Configuration.SidechatPolicy == domain.CodexReadOnlySidechatV1 {
		nativeConfig.Sidechat = codex.ReadOnlySidechatV1
	}
	var proxy *nativeproxy.Proxy
	if managed != nil {
		nativeConfig.API = nil
		nativeConfig.ManagedAuthentication = true
		nativeConfig.QuotaObserver = managed.publishRollingQuota
	} else {
		proxy, err = openCodexNativeProxy(nativeCtx, config, i.GenerationID)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			proxyClosed = false
			nativeConfig.API.LoopbackProxyURL = proxy.NativeURL()
			nativeConfig.Process.ProtectedValues = proxy.ProtectedValues()
			defer func() {
				if err := proxy.Close(); err != nil {
					cleanupFailed = true
					output, returned = nil, err
				} else {
					proxyClosed = true
				}
			}()
		}
	}
	phase = compactionLaunch
	managedPreNative = false
	native, err := codex.Open(nativeCtx, nativeConfig)
	if err != nil {
		managedPreNative = domain.SafeError(err).Code != domain.RecoveryRequired
		return nil, err
	}
	captured, closed := false, false
	unregister := func() {}
	closeNative := func() error {
		if closed {
			return nil
		}
		unregister()
		if managed != nil && !captured {
			captured = true
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			managedLatest, err = native.ManagedBundle(bounded, false)
			stop()
			managedSuccess = err == nil
		}
		if err := native.Close(); err != nil {
			return err
		}
		closed = true
		nativeClosed = true
		if managed != nil {
			managedCleanup = cleanupExecutionAuthentication(nativeHome, managedLatest, managed.response.Bundle) == nil
			if !managedCleanup || !managedSuccess {
				return subscription.Invalid()
			}
		}
		return nil
	}
	defer func() {
		if err := closeNative(); err != nil {
			cleanupFailed = true
			output, returned = nil, err
		}
	}()
	if managed != nil && config.observations != nil {
		unregister = config.observations.register(i.Assignment.AccountID, native, managed)
	}

	phase = compactionCommand
	var intent directoryNativeIntent
	bound, err := native.ResumeDirectory(ctx, i.RequestID, before, settings, func(nativeIntent codex.DirectoryIntent) error {
		if err := selection.Verify(); err != nil {
			return err
		}
		intent = directoryNativeIntent{Ownership: ownership, GenerationID: i.GenerationID, RegistrationDigest: executionInputDigest(mustDirectoryJSON(registrationClaim)), Native: nativeIntent}
		return writeDirectoryClaim(config.Root, owner, directoryNativeClaim, intent)
	})
	if err != nil {
		return nil, err
	}
	if bound.Effective == nil {
		return nil, domain.DirectoryUncertain()
	}
	if contextProof != nil {
		_, err = native.VerifyDirectoryCompactedContinuation(ctx, domain.NewID(), *contextProof, *bound.Effective)
	} else {
		_, err = native.VerifyDirectoryContinuation(ctx, domain.NewID(), before, *bound.Effective, codex.ContinueAfterSuccess)
	}
	if err != nil {
		return nil, err
	}
	if err := native.RequireDirectoryQuiescence(ctx, domain.NewID()); err != nil {
		return nil, err
	}
	reload, err := native.ReadDirectoryReloadEvidence(ctx, domain.NewID(), bound)
	if err != nil {
		return nil, err
	}
	if err := selection.Verify(); err != nil {
		return nil, err
	}
	selected := before
	selected.Effective = *bound.Effective
	phase = compactionCleanup
	if err := closeNative(); err != nil {
		return nil, err
	}
	if proxy != nil {
		if err := proxy.Close(); err != nil {
			return nil, err
		}
		proxyClosed = true
	}
	if err := process.ReconcileOwnerContext(ctx, filepath.Join(config.Root, "processes"), owner); err != nil {
		return nil, err
	}
	if err := lease.Close(); err != nil {
		return nil, err
	}
	workspaceClosed = true
	if !nativeClosed || !workspaceClosed || !proxyClosed || cleanupFailed {
		return nil, domain.DirectoryUncertain()
	}
	// Managed finish runs on return. Retain the generation only after its original
	// dirty authentication bundle and cleanup have reached a conclusive receipt.
	if managed != nil {
		if err := managed.finish(managedLatest, managedCleanup, false, managedSuccess); err != nil {
			return nil, &managedExecutionUncertain{err}
		}
		if !managedCleanup || !managedSuccess {
			return nil, domain.DirectoryUncertain()
		}
		authenticationClosed = true
		clear(managed.response.Bundle)
		managed = nil
	}
	if !authenticationClosed {
		return nil, domain.DirectoryUncertain()
	}
	checkpoint := codexDirectoryCheckpoint{Context: contextProof, Version: 1, Ownership: ownership, Input: i, RegistrationDigest: executionInputDigest(mustDirectoryJSON(registrationClaim)), NativeIntentDigest: executionInputDigest(mustDirectoryJSON(intent)), Source: source, Before: before, Selected: selected, Reload: reload}
	ref, err := retainDirectoryCheckpoint(config.Root, checkpoint)
	if err != nil {
		return nil, err
	}
	result := domain.SessionDirectoryResult{Version: 1, RequestID: i.RequestID, GenerationID: i.GenerationID, ExecutionID: i.Assignment.ExecutionID, Checkpoint: ref, CleanupVerified: true}
	if result.Validate() != nil {
		return nil, domain.DirectoryUncertain()
	}
	logger.InfoContext(ctx, "session_directory_cleanup_verified")
	return json.Marshal(result)
}

func directoryThreadSettings(observed codex.EffectiveSettings, cwd string, roots []string, instructions string, options domain.AgentOptions) codex.ThreadSettings {
	options.Permission = map[codex.SandboxType]domain.PermissionMode{codex.ReadOnly: domain.PermissionReadOnly, codex.WorkspaceWrite: domain.PermissionWorkspaceWrite, codex.FullAccess: domain.PermissionFullAccess}[observed.Sandbox.Type]
	options.ApprovalPolicy = string(observed.ApprovalPolicy)
	options.ApprovalsReviewer = domain.ApprovalsReviewer(observed.ApprovalsReviewer)
	effort := ""
	if observed.Effort != nil {
		effort = *observed.Effort
	}
	options.ServiceTier = ""
	if observed.ServiceTier != nil {
		options.ServiceTier = *observed.ServiceTier
	}
	return codex.ThreadSettings{Model: observed.Model, Provider: observed.Provider, Effort: effort, Cwd: cwd, WorkspaceRoots: roots, Instructions: instructions, Options: options}
}
