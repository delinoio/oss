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

type codexSessionCompactionCheckpoint struct {
	Version            uint32                        `json:"version"`
	ServerID           domain.ID                     `json:"server_id"`
	DeviceID           domain.ID                     `json:"device_id"`
	JobID              domain.ID                     `json:"job_id"`
	AssignmentRevision uint64                        `json:"assignment_revision"`
	InstanceID         domain.ID                     `json:"instance_id"`
	AssignmentDigest   string                        `json:"assignment_digest"`
	RegistrationDigest string                        `json:"registration_digest"`
	CommandDigest      string                        `json:"command_digest"`
	Input              domain.SessionCompactionInput `json:"input"`
	Native             codex.CompactedCheckpoint     `json:"native"`
}

func codexCheckpointForContinuation(root string, input domain.ExecutionJobInput, manifest workspace.Manifest) (CodexExecutionCheckpoint, error) {
	c := input.Continuation
	if c == nil {
		return CodexExecutionCheckpoint{}, domain.CompactionUncertain()
	}
	raw, err := hex.DecodeString(c.PromptDigest)
	if err != nil || len(raw) != sha256.Size {
		return CodexExecutionCheckpoint{}, domain.CompactionUncertain()
	}
	var digest [sha256.Size]byte
	copy(digest[:], raw)
	account, connection := input.AccountID, input.ConnectionID
	if c.PreviousAccountID != "" {
		account, connection = c.PreviousAccountID, c.PreviousConnectionID
	}
	return ReadCodexExecutionCheckpoint(root, ExecutionCheckpointRef{Subscription: input.Configuration.Subscription, JobID: c.Previous.JobID, SessionID: input.SessionID, MachineID: input.MachineID, HistoryExecutionID: c.HistoryExecutionID, AssignmentInputDigest: c.AssignmentInputDigest, ConfigurationDigest: input.ConfigurationDigest, AccountID: account, ConnectionID: connection, Completion: c.Completion, InputMode: c.InputMode, PromptDigest: digest, AcceptedInputs: c.Previous.AcceptedInputs, WorkspaceRoots: nativeWorkspaceRoots(manifest)})
}

func executeCodexSessionCompaction(ctx context.Context, config Config, owner domain.ID, job domain.Job, i domain.SessionCompactionInput) (output json.RawMessage, returned error) {
	c := config.execution
	if c == nil || c.Assignment == nil || config.executionContext == nil || i.Validate() != nil || i.Version != 2 || i.Assignment.Configuration.Harness != domain.Codex || domain.ID(c.Assignment.Id) != owner {
		return nil, domain.CompactionUncertain()
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("job_id", owner, "action_id", i.ActionID, "session_id", i.Assignment.SessionID)
	phase := compactionPrepare
	defer func() {
		if returned != nil {
			logger.WarnContext(ctx, "codex_session_compaction_uncertain", "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	var prep workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(i.Assignment.Preparation, &prep) != nil || domain.Decode(i.Assignment.Manifest, &manifest) != nil || workspace.ValidateResult(prep, manifest, runtime.GOOS) != nil {
		return nil, domain.CompactionUncertain()
	}
	manager := &workspace.Manager{Root: config.Root, Logger: logger}
	previous := workspace.ExecutionPredecessor{JobID: i.SourceJobID, ExecutionID: i.Assignment.ExecutionID}
	if i.Previous != nil {
		previous = workspace.ExecutionPredecessor{JobID: i.Previous.JobID, ExecutionID: i.Previous.ActionID}
	}
	lease, err := manager.ClaimContinuation(ctx, owner, i.ActionID, previous, prep, manifest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := lease.Close(); err != nil {
			output, returned = nil, err
		}
	}()
	executable := i.Assignment.Installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.CompactionUncertain()
	}
	runtimeRoot := filepath.Join(config.Root, "runtimes")
	if security.PrivateDir(runtimeRoot) != nil {
		return nil, domain.CompactionUncertain()
	}
	home := filepath.Join(runtimeRoot, string(i.ActionID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, domain.CompactionUncertain()
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	nativeHome := filepath.Join(runtimeRoot, string(i.Restore.Continuation.HistoryExecutionID), "codex")
	env, err = replaceCodexHome(env, nativeHome)
	if err != nil {
		return nil, err
	}
	phase = compactionRestore
	source, err := codexCheckpointForContinuation(config.Root, i.Restore, manifest)
	if err != nil {
		return nil, err
	}
	var prior *codex.CompactedCheckpoint
	if i.Previous != nil {
		restored, err := readCodexSessionCompactionCheckpoint(ctx, config.Root, c.Credential, i.Restore, *i.Previous, source)
		if err != nil {
			return nil, err
		}
		prior = &restored
	} else if err := codex.VerifyContinuationContextRollout(ctx, nativeHome, source.Native); err != nil {
		return nil, err
	}
	if prior != nil {
		if err := codex.VerifyCompactionRollout(ctx, nativeHome, *prior); err != nil {
			return nil, err
		}
	}
	settings := codex.ThreadSettings{Model: i.Assignment.Configuration.NativeModel, Provider: codexExecutionProvider(i.Assignment.Configuration.Subscription), Effort: i.Assignment.Configuration.Effort, Cwd: lease.WorkingDirectory(), WorkspaceRoots: nativeWorkspaceRoots(manifest), Instructions: i.Assignment.Configuration.Instructions, Options: i.Assignment.Configuration.Options}
	if codex.ValidateThreadSettings(settings) != nil {
		return nil, domain.CompactionUncertain()
	}
	var managed *managedSubscriptionLease
	var managedLatest []byte
	var closeManagedRPC func()
	managedCleanup, managedSuccess, managedUnused, managedPreNative := false, false, false, false
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
			output, returned = nil, &managedExecutionUncertain{err}
		} else if !conclusive {
			output, returned = nil, &managedExecutionUncertain{subscription.Invalid()}
		}
		clear(managed.response.Bundle)
	}()
	if i.Assignment.Configuration.Subscription {
		if settings.Options.Permission != domain.PermissionReadOnly && settings.Options.Permission != domain.PermissionWorkspaceWrite {
			return nil, domain.CompactionUncertain()
		}
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
	registrationClaim := sessionCompactionRegistration{ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, RequestID: registration, CredentialDigest: hex.EncodeToString(digest[:])}
	if err := writeCompactionClaim(config.Root, owner, compactionRegistrationClaim, registrationClaim); err != nil {
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
		return nil, domain.CompactionUncertain()
	}
	nativeCtx, cancel := context.WithCancel(config.executionContext)
	defer cancel()
	cancelAction := context.AfterFunc(ctx, cancel)
	defer cancelAction()
	nativeConfig := codex.Config{Mode: codex.ThreadProtocol, Version: i.Assignment.Installation.Version, Home: nativeHome, API: &codex.APIConfig{ServerOrigin: c.Credential.Endpoint, Token: token}, Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: settings.Cwd, Env: env, Logger: logger}}
	if i.Assignment.Configuration.SidechatPolicy == domain.CodexReadOnlySidechatV1 {
		nativeConfig.Sidechat = codex.ReadOnlySidechatV1
	}
	var proxy *nativeproxy.Proxy
	if managed != nil {
		nativeConfig.API = nil
		nativeConfig.ManagedAuthentication = true
		nativeConfig.QuotaObserver = managed.publishRollingQuota
	} else {
		proxy, err = openCodexNativeProxy(nativeCtx, config, i.ActionID)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			nativeConfig.API.LoopbackProxyURL = proxy.NativeURL()
			nativeConfig.Process.ProtectedValues = proxy.ProtectedValues()
			defer func() {
				if err := proxy.Close(); err != nil {
					output, returned = nil, err
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
			output, returned = nil, err
		}
	}()
	if managed != nil && config.observations != nil {
		unregister = config.observations.register(i.Assignment.AccountID, native, managed)
	}
	if _, err := native.ResumeThread(ctx, i.Restore.ThreadRequestID, source.Native.ThreadID, settings); err != nil {
		return nil, err
	}
	if prior != nil {
		_, err = native.VerifyCompactedContinuation(ctx, i.Restore.Continuation.HistoryRequestID, *prior)
	} else {
		_, err = native.VerifyContinuation(ctx, i.Restore.Continuation.HistoryRequestID, source.Native, codex.ContinueAfterSuccess)
	}
	if err != nil {
		return nil, err
	}
	commandClaim := sessionCompactionCommand{ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, RegistrationRequestID: registration, CredentialDigest: registrationClaim.CredentialDigest}
	if err := writeCompactionClaim(config.Root, owner, compactionCommandClaim, commandClaim); err != nil {
		return nil, err
	}
	phase = compactionCommand
	if err := native.StartCompaction(ctx, i.ActionID, source.Native, prior); err != nil {
		return nil, err
	}
	phase = compactionSettle
	usages := []domain.NativeResponseUsage{}
	for count := 0; ; count++ {
		if count >= domain.MaxExecutionEvents {
			return nil, domain.CompactionUncertain()
		}
		event, err := native.NextEvent(ctx)
		if err != nil {
			return nil, err
		}
		if event.Kind == codex.ResponseUsageEvent && !event.Late {
			if event.ResponseUsage == nil || event.ResponseUsage.Validate() != nil {
				return nil, domain.CompactionUncertain()
			}
			usages = append(usages, *event.ResponseUsage)
		}
		if event.Kind == codex.CompactionEvent && (event.Compaction == nil || event.Compaction.ActionID != i.ActionID || event.Compaction.Trigger != codex.ManualCompaction) {
			return nil, domain.CompactionUncertain()
		}
		if event.Kind == codex.TurnCompletedEvent && !event.Late {
			if event.Turn == nil || event.Turn.Status != codex.TurnCompleted {
				return nil, domain.CompactionUncertain()
			}
			break
		}
	}
	retained, err := native.RetainCompactedCheckpoint(ctx)
	if err != nil {
		return nil, err
	}
	phase = compactionCleanup
	if err := closeNative(); err != nil {
		return nil, err
	}
	if proxy != nil {
		if err := proxy.Close(); err != nil {
			return nil, err
		}
	}
	if err := lease.Close(); err != nil {
		return nil, err
	}
	phase = compactionRetain
	p := codexSessionCompactionCheckpoint{Version: 1, ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, JobID: owner, AssignmentRevision: c.Assignment.Revision, InstanceID: c.Instance, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), RegistrationDigest: compactionClaimDigest(registrationClaim), CommandDigest: compactionClaimDigest(commandClaim), Input: i, Native: retained}
	if err := readCompactionClaimRecords(config.Root, owner, i, p.RegistrationDigest, p.CommandDigest); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil || len(data) > maxCompactionCheckpoint {
		return nil, domain.CompactionUncertain()
	}
	nativeBytes, err := json.Marshal(retained)
	if err != nil {
		return nil, domain.CompactionUncertain()
	}
	if security.PrivateDir(filepath.Join(config.Root, "compaction-checkpoints")) != nil {
		return nil, domain.CompactionUncertain()
	}
	path, err := compactionCheckpointPath(config.Root, i.ActionID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, domain.CompactionUncertain()
	}
	if err := security.WriteAtomic(path, data); err != nil {
		return nil, domain.CompactionUncertain()
	}
	record := retained.Records[len(retained.Records)-1]
	result := domain.SessionCompactionResult{Version: 2, Harness: domain.Codex, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: domain.SessionCompactionRef{JobID: owner, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, CheckpointDigest: executionInputDigest(data), NativeDigest: executionInputDigest(nativeBytes)}, Codex: &domain.CodexCompactionResult{NativeThreadID: domain.NativeIdentity(source.Native.ThreadID), SourceNativeTurnID: domain.NativeIdentity(source.Native.TurnID), NativeTurnID: domain.NativeIdentity(record.TurnID), LiveItemID: record.ItemID, HistoryItemID: record.HistoryItemID, HistoryDigest: retained.HistoryDigest, Actions: uint32(len(retained.Records)), Acknowledged: true, LifecycleCompleted: true, ResponseUsages: usages}}
	if result.Validate() != nil {
		return nil, domain.CompactionUncertain()
	}
	logger.InfoContext(ctx, "codex_session_compaction_cleanup_verified", "native_actions", len(retained.Records))
	return json.Marshal(result)
}

func readCodexSessionCompactionCheckpoint(ctx context.Context, root string, credential Credential, input domain.ExecutionJobInput, ref domain.SessionCompactionRef, source CodexExecutionCheckpoint) (codex.CompactedCheckpoint, error) {
	var empty codex.CompactedCheckpoint
	if ref.Validate() != nil || ref.RequiresResume || input.Continuation == nil || ref.ExecutionID != input.Continuation.Previous.ExecutionID || input.Configuration.Harness != domain.Codex {
		return empty, domain.CompactionUncertain()
	}
	path, err := compactionCheckpointPath(root, ref.ActionID)
	if err != nil {
		return empty, err
	}
	data, err := security.ReadPrivate(path, maxCompactionCheckpoint)
	if err != nil || executionInputDigest(data) != ref.CheckpointDigest {
		return empty, domain.CompactionUncertain()
	}
	var p codexSessionCompactionCheckpoint
	if domain.Decode(data, &p) != nil || p.Version != 1 || p.Input.Validate() != nil || p.Input.Version != 2 || p.Input.Assignment.Configuration.Harness != domain.Codex || p.JobID != ref.JobID || p.Input.ActionID != ref.ActionID || p.ServerID != credential.ServerID || p.DeviceID != credential.DeviceID || p.Input.Assignment.ExecutionID != ref.ExecutionID || p.Input.Assignment.SessionID != input.SessionID || p.Input.Assignment.ConfigurationDigest != input.ConfigurationDigest || p.Input.Assignment.AccountID != input.AccountID || p.Input.Assignment.ConnectionID != input.ConnectionID || p.Input.SourceJobID != input.Continuation.Previous.JobID {
		return empty, domain.CompactionUncertain()
	}
	canonical, err := json.Marshal(p)
	original, originalErr := json.Marshal(p.Input.Assignment)
	native, nativeErr := json.Marshal(p.Native)
	sourceBytes, sourceErr := json.Marshal(source.Native)
	retainedSource, retainedSourceErr := json.Marshal(p.Native.Source)
	if err != nil || originalErr != nil || nativeErr != nil || sourceErr != nil || retainedSourceErr != nil || !bytes.Equal(data, canonical) || !bytes.Equal(sourceBytes, retainedSource) || executionInputDigest(original) != input.Continuation.AssignmentInputDigest || executionInputDigest(native) != ref.NativeDigest || len(p.Native.Records) == 0 || p.Native.Records[len(p.Native.Records)-1].ActionID != ref.ActionID {
		return empty, domain.CompactionUncertain()
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(ref.JobID)+".json"), 2<<20)
	var journal journal
	var result domain.SessionCompactionResult
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.InstanceID.Validate() != nil || journal.ReportID.Validate() != nil || journal.Revision != p.AssignmentRevision || p.AssignmentRevision == 0 || journal.InstanceID != p.InstanceID || journal.JobID != ref.JobID || journal.Digest != p.AssignmentDigest || journal.State != journalFinished && journal.State != journalReported || journal.Problem != nil || domain.Decode(journal.Output, &result) != nil || result.Validate() != nil || result.Harness != domain.Codex || result.Checkpoint != ref || result.Codex.HistoryDigest != p.Native.HistoryDigest || result.Codex.NativeThreadID != domain.NativeIdentity(source.Native.ThreadID) || result.Codex.SourceNativeTurnID != domain.NativeIdentity(source.Native.TurnID) {
		return empty, domain.CompactionUncertain()
	}
	last := p.Native.Records[len(p.Native.Records)-1]
	if result.Codex.NativeTurnID != domain.NativeIdentity(last.TurnID) || result.Codex.LiveItemID != last.ItemID || result.Codex.HistoryItemID != last.HistoryItemID || result.Codex.Actions != uint32(len(p.Native.Records)) {
		return empty, domain.CompactionUncertain()
	}
	if err := readCompactionClaimRecords(root, p.JobID, p.Input, p.RegistrationDigest, p.CommandDigest); err != nil {
		return empty, err
	}
	return p.Native, nil
}
