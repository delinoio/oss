package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/executionenv"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func executeSession(ctx context.Context, config Config, owner domain.ID, job domain.Job) (output json.RawMessage, returned error) {
	connection := config.execution
	if connection == nil || connection.Assignment == nil || domain.ID(connection.Assignment.Id) != owner || config.executionContext == nil {
		return nil, domain.Fail(domain.Unsupported, "Native execution requires the owning authenticated Worker stream.", "Use the accepted immutable assignment on its original Worker.")
	}
	var input domain.ExecutionJobInput
	if err := domain.Decode(job.Input, &input); err != nil {
		return nil, err
	}
	if err := input.Validate(); err != nil {
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "native_execution_settings_rejected", "job_id", owner, "stage", "assignment-validation", "options", input.Configuration.SelectedNativeOptionNames(), "code", domain.SafeError(err).Code)
		}
		return nil, err
	}
	if err := (skills.Manager{Root: config.Root}).CheckContext(input.Input.Skills, input); err != nil {
		return nil, err
	}
	// Direct startup resolves and verifies Installation on this local copy.
	// Completion must retain the immutable accepted assignment, whose v4
	// installation is deliberately absent; executable evidence has its own
	// original startup journal and must not rewrite the assignment digest.
	acceptedInput := input
	if input.Version == 4 {
		config.startup = newExecutionStartupAttempt(config, owner, input)
		defer func() { returned = config.startup.finish(returned) }()
		var err error
		input.Installation, err = resolveExecutionStartup(ctx, config, owner, input)
		if err != nil {
			return nil, err
		}
		config.startup.observation.ExecutableSHA256 = input.Installation.ExecutableSHA256
		config.startup.setPhase(domain.StartupLaunch)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	logger = logger.With("job_id", owner, "execution_id", input.ExecutionID, "session_id", input.SessionID, "harness", input.Configuration.Harness, "options", input.Configuration.SelectedNativeOptionNames())
	logger.InfoContext(ctx, "native_execution_started")
	defer func() {
		if workspace.IsPRStartupRejection(returned) {
			output, returned = reportPRStartupRejection(config, owner, job, returned)
			if returned == nil {
				logger.InfoContext(ctx, "native_execution_startup_rejected")
				return
			}
		}
		if returned != nil {
			logger.WarnContext(ctx, "native_execution_requires_reconciliation", "code", domain.SafeError(returned).Code)
		} else {
			logger.InfoContext(ctx, "native_execution_cleanup_verified")
		}
	}()
	if input.Configuration.Harness == domain.ClaudeCode {
		return executeClaudeSession(ctx, config, owner, input, logger)
	}
	if input.Configuration.Harness == domain.OpenCode {
		return executeOpenCodeSession(ctx, config, owner, input, logger)
	}
	if input.Configuration.Harness == domain.GrokBuild {
		return executeGrokSession(ctx, config, owner, input, logger)
	}
	if input.Configuration.Harness != domain.Codex || (input.Version != 4 && !domain.CodexVersionAllowed(input.Installation.Version)) {
		return nil, domain.Fail(domain.Unsupported, "This native execution profile is not implemented.", "Select a verified installed Codex profile; no fallback harness is used.")
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.SessionID != input.SessionID || preparation.MachineID != input.MachineID || workspace.ValidateResult(preparation, manifest, runtime.GOOS) != nil {
		return nil, workspace.ResultUncertain()
	}
	executable := input.Installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.Fail(domain.RecoveryRequired, "The selected native executable identity changed.", "Review the selected executable path and recover original history; no PATH fallback is used.")
	}
	if input.SidechatRetry != nil && (config.execution == nil || config.execution.Instance != input.SidechatRetry.WorkerInstanceID || config.execution.Credential.DeviceID != input.SidechatRetry.WorkerDeviceID) {
		return nil, executionCheckpointUncertain()
	}
	manager := &workspace.Manager{Root: config.Root, Logger: config.Logger}
	var lease *workspace.ExecutionLease
	if retry := input.Retry; retry != nil {
		lease, err = manager.ClaimUnsentRetry(ctx, owner, input.ExecutionID, workspace.ExecutionPredecessor{JobID: retry.JobID, ExecutionID: retry.ExecutionID}, preparation, manifest, retryOriginalWorkspace(input)...)
	} else if retry := input.SidechatRetry; retry != nil {
		lease, err = manager.ClaimContinuation(ctx, owner, input.ExecutionID, workspace.ExecutionPredecessor{JobID: retry.PreviousJobID, ExecutionID: retry.PreviousExecutionID}, preparation, manifest)
	} else if c := input.Continuation; c != nil {
		previous := workspace.ExecutionPredecessor{JobID: c.Previous.JobID, ExecutionID: c.Previous.ExecutionID}
		if c.Compaction != nil {
			previous = workspace.ExecutionPredecessor{JobID: c.Compaction.JobID, ExecutionID: c.Compaction.ActionID}
		}
		lease, err = manager.ClaimContinuation(ctx, owner, input.ExecutionID, previous, preparation, manifest)
	} else {
		lease, err = manager.ClaimFirstExecution(ctx, owner, input.ExecutionID, preparation, manifest)
	}
	if err != nil {
		return nil, err
	}
	config.startup.claimedWorkspace()
	defer func() {
		if err := lease.Close(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
	}()
	var prGit *workspace.PRGitTool
	if input.Remediation != nil {
		prGit, err = lease.PreparePRGitTool(ctx, *input.Remediation, preparation, manifest)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err := prGit.Close(); err != nil {
				output, returned = nil, config.startup.cleanupFailure(returned, err)
			}
		}()
	}
	// The private runtime is retained for native resume/reconciliation. Never
	// inherit an existing directory after an interrupted first execution.
	runtimeRoot := filepath.Join(manager.Root, "runtimes")
	if err := security.PrivateDir(runtimeRoot); err != nil {
		return nil, domain.SafeError(err)
	}
	home := filepath.Join(runtimeRoot, string(input.ExecutionID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, publicationUncertain()
	}
	var ordinaryTools executionenv.Ordinary
	if input.Configuration.SidechatPolicy == "" {
		ordinaryTools = ordinaryExecutionTools(config.Logger)
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	nativeHome := filepath.Join(home, "codex")
	var checkpoint CodexExecutionCheckpoint
	var compacted *codex.CompactedCheckpoint
	if c := input.Continuation; c != nil {
		rawDigest, err := hex.DecodeString(c.PromptDigest)
		if err != nil || len(rawDigest) != sha256.Size {
			return nil, executionCheckpointUncertain()
		}
		var promptDigest [sha256.Size]byte
		copy(promptDigest[:], rawDigest)
		account, connection := input.AccountID, input.ConnectionID
		if c.PreviousAccountID != "" {
			account, connection = c.PreviousAccountID, c.PreviousConnectionID
		}
		checkpoint, err = ReadCodexExecutionCheckpoint(manager.Root, ExecutionCheckpointRef{ContextRevision: c.Previous.ContextRevision, ApprovalsReviewer: input.Configuration.Options.ApprovalsReviewer, Subscription: input.Configuration.Subscription, JobID: c.Previous.JobID, SessionID: input.SessionID, MachineID: input.MachineID, HistoryExecutionID: c.HistoryExecutionID, AssignmentInputDigest: c.AssignmentInputDigest, ConfigurationDigest: input.ConfigurationDigest, AccountID: account, ConnectionID: connection, Completion: c.Completion, InputMode: c.InputMode, PromptDigest: promptDigest, AcceptedInputs: c.Previous.AcceptedInputs, WorkspaceRoots: nativeWorkspaceRoots(manifest)})
		if err != nil {
			return nil, err
		}
		nativeHome = filepath.Join(runtimeRoot, string(c.HistoryExecutionID), "codex")
		replaced := 0
		for i, entry := range env {
			if strings.HasPrefix(entry, "CODEX_HOME=") {
				env[i] = "CODEX_HOME=" + nativeHome
				replaced++
			}
		}
		if replaced != 1 {
			return nil, executionCheckpointUncertain()
		}
		if c.Compaction == nil {
			if err := codex.VerifyContinuationContextRollout(ctx, nativeHome, checkpoint.Native); err != nil {
				return nil, err
			}
		}
		if c.Compaction != nil {
			retained, err := readCodexSessionCompactionCheckpoint(ctx, manager.Root, config.execution.Credential, input, *c.Compaction, checkpoint)
			if err != nil {
				return nil, err
			}
			if err := codex.VerifyCompactionRollout(ctx, nativeHome, retained); err != nil {
				return nil, err
			}
			compacted = &retained
		}
		logger.InfoContext(ctx, "native_execution_predecessor_verified", "previous_execution_id", c.Previous.ExecutionID)
	}
	if f := input.Fork; f != nil {
		native, err := readForkCheckpoint(manager.Root, input)
		if err != nil {
			return nil, err
		}
		checkpoint.Native = native
		nativeHome = filepath.Join(runtimeRoot, string(f.RuntimeID), "codex")
		env, err = replaceCodexHome(env, nativeHome)
		if err != nil {
			return nil, err
		}
	}
	if prGit != nil {
		env = prGit.Environment(env)
	}
	instructions, err := input.Configuration.NativeInstructions(input.Input.Mode)
	if err != nil {
		return nil, err
	}
	settings := codex.ThreadSettings{Model: input.Configuration.NativeModel, Provider: codexExecutionProvider(input.Configuration.Subscription), Effort: input.Configuration.Effort, Cwd: lease.WorkingDirectory(), Instructions: instructions, Options: input.Configuration.Options}
	settings.WorkspaceRoots = nativeWorkspaceRoots(manifest)
	if input.Fork != nil {
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
		}
	}
	if input.Configuration.Subscription {
		if err := validateManagedAuthenticationHome(nativeHome, manifest.WorkspaceRoots()); err != nil {
			return nil, err
		}
	}
	var managed *managedSubscriptionLease
	var managedLatest []byte
	var closeManagedRPC func()
	managedCleanup, managedSuccess := false, false
	managedUnusedOriginal := false
	managedPreNativeCleanup := false
	defer func() {
		if closeManagedRPC != nil {
			defer closeManagedRPC()
		}
		if managed != nil {
			// Finish may acknowledge a fenced account without establishing safe
			// ownership. Only a captured bundle or the verified unused original,
			// together with independent cleanup, permits ordinary job reporting.
			conclusive := managedCleanup && (managedSuccess || managedUnusedOriginal)
			if err := managed.finish(managedLatest, managedCleanup, false, managedSuccess); err != nil {
				output, returned = nil, &managedExecutionUncertain{err}
			} else if !conclusive {
				if returned == nil {
					returned = subscription.Invalid()
				}
				output, returned = nil, &managedExecutionUncertain{returned}
			}
			clear(managed.response.Bundle)
		}
	}()
	// Native subscription authentication is selected explicitly by the server's
	// immutable assignment, independently of the existing API token profile.
	if input.Configuration.Subscription {
		client, closeRPC, err := subscriptionRPC(ctx, config, connection.Credential)
		if err != nil {
			return nil, err
		}
		closeManagedRPC = closeRPC
		managed, err = takeManagedSubscription(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, owner, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
		if err != nil {
			return nil, err
		}
		if _, _, err := subscription.Parse(managed.response.Bundle); err != nil {
			return nil, err
		}
		// Install cleanup before the atomic write: even a synchronization error
		// may leave the owned plaintext file committed. Publisher/registration
		// failures occur before any native process can own this authentication.
		managedPreNativeCleanup = true
		defer func() {
			if managedPreNativeCleanup {
				managedCleanup = cleanupUnusedExecutionAuthentication(nativeHome, managed.response.Bundle) == nil
				if managedCleanup {
					// This closed pre-native outcome returns unused original bytes;
					// it does not claim native execution or authentication success.
					managedLatest = bytes.Clone(managed.response.Bundle)
					managedUnusedOriginal = true
				} else {
					output, returned = nil, subscription.Invalid()
				}
			}
		}()
		if err := security.WriteAtomic(filepath.Join(nativeHome, "auth.json"), managed.response.Bundle); err != nil {
			return nil, subscription.Invalid()
		}
	}
	if err := codex.ValidateThreadSettings(settings); err != nil {
		return nil, err
	}
	publicationConfig := *connection
	publicationConfig.Root = manager.Root
	publisher, err := OpenExecutionPublisher(publicationConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			output, returned = nil, publicationUncertain()
		}
	}()
	rawToken, err := security.RandomToken()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	token := apiproxy.TokenPrefix + rawToken
	digest := sha256.Sum256([]byte(token))
	registration := domain.NewID()
	// Retain only digest/request metadata before registration. A process restart
	// cannot reconstruct the token or replay the native first input from this.
	intent := struct {
		Version     uint32    `json:"version"`
		JobID       domain.ID `json:"job_id"`
		ExecutionID domain.ID `json:"execution_id"`
		RequestID   domain.ID `json:"request_id"`
		TokenDigest string    `json:"token_digest"`
	}{1, owner, input.ExecutionID, registration, hex.EncodeToString(digest[:])}
	if err := writeJSON(filepath.Join(home, "execution.json"), intent); err != nil {
		return nil, publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	registered, err := connection.Client.RegisterExecution(bounded, authenticated(connection.Credential, &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(registration), Id: string(owner), ExpectedRevision: connection.Assignment.Revision}, MachineId: string(input.MachineID), InstanceId: string(connection.Instance), CredentialDigest: digest[:]}))
	cancel()
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	expectedProxyPath := "/api-proxy/v1"
	if managed != nil {
		expectedProxyPath = ""
	}
	if registered == nil || registered.Msg == nil || registered.Msg.ProxyPath != expectedProxyPath {
		return nil, publicationUncertain()
	}
	// Stream authority always owns the process lifetime. Before input has a
	// durable native binding, a targeted cancellation also kills immediately.
	// Afterwards it can request one bounded native interruption while still
	// receiving/publishing terminal facts; it never outlives the work stream.
	nativeCtx, cancelNative := context.WithCancel(config.executionContext)
	defer cancelNative()
	cancelBeforeAcceptance := context.AfterFunc(ctx, cancelNative)
	defer cancelBeforeAcceptance()
	nativeConfig := codex.Config{EnableImageGeneration: input.NativeImageGeneration, RevertHistory: input.ContextRevision > 0 || checkpoint.Native.PaginatedHistory || checkpoint.Native.ContextRevision > 0 || compacted != nil && compacted.Revert != nil, ManagedForkHistory: input.Configuration.Subscription && input.Fork != nil && input.Configuration.SidechatPolicy == "", OrdinaryTools: ordinaryTools, SkillsRoot: config.Root, ImageRoot: config.Root, ImageMachineID: input.MachineID, Mode: codex.ThreadProtocol, Version: input.Installation.Version, Home: nativeHome, API: &codex.APIConfig{ServerOrigin: connection.Credential.Endpoint, Token: token}, Process: process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: settings.Cwd, Env: env, Logger: config.Logger}}
	if input.Configuration.SidechatPolicy == domain.CodexReadOnlySidechatV1 {
		nativeConfig.Sidechat = codex.ReadOnlySidechatV1
	}
	var ownedProxy *nativeproxy.Proxy
	if managed == nil {
		proxy, err := openCodexNativeProxy(nativeCtx, config, input.ExecutionID)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			ownedProxy = proxy
			nativeConfig.API.LoopbackProxyURL = proxy.NativeURL()
			nativeConfig.Process.ProtectedValues = proxy.ProtectedValues()
			defer func() {
				if err := proxy.Close(); err != nil {
					output, returned = nil, config.startup.cleanupFailure(returned, err)
				}
			}()
		}
	}
	if managed != nil {
		nativeConfig.API = nil
		nativeConfig.ManagedAuthentication = true
		nativeConfig.QuotaObserver = managed.publishRollingQuota
	}
	// Once startup may own a process, only independently joined native cleanup
	// may authorize removal. A definite failed Open already proves that closure;
	// recovery-required startup must retain its authentication and lease.
	managedPreNativeCleanup = false
	config.startup.setPhase(domain.StartupInitialize)
	client, err := codex.Open(nativeCtx, nativeConfig)
	if err != nil {
		managedPreNativeCleanup = domain.SafeError(err).Code != domain.RecoveryRequired
		return nil, err
	}
	managedBundleAttempted := false
	captureManagedBundle := func() {
		if managed == nil || managedBundleAttempted {
			return
		}
		managedBundleAttempted = true
		bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		bundle, bundleErr := client.ManagedBundle(bounded, false)
		managedLatest, managedSuccess = bundle, bundleErr == nil
	}
	defer func() {
		captureManagedBundle()
		if err := client.Close(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
			return
		}
		if managed != nil {
			managedCleanup = cleanupExecutionAuthentication(nativeHome, managedLatest, managed.response.Bundle) == nil
			if !managedCleanup || !managedSuccess {
				output, returned = nil, subscription.Invalid()
			}
		}
	}()
	var unregisterObservations func()
	if managed != nil && config.observations != nil {
		unregisterObservations = config.observations.register(input.AccountID, client, managed)
		defer unregisterObservations()
	}
	input.Installation.Version = client.Version()
	publisher.nativeVersion = client.Version()
	mapper := NewCodexEventPublisher(publisher)
	var bound codex.ThreadResult
	if c := input.Continuation; c != nil {
		bound, err = client.ResumeThread(ctx, input.ThreadRequestID, checkpoint.Native.ThreadID, settings)
		if err == nil {
			intent := codex.ContinueAfterSuccess
			if c.Intent == domain.ContinueExplicitly {
				intent = codex.ResumeAfterTerminal
			}
			if compacted != nil {
				_, err = client.VerifyCompactedContinuation(ctx, c.HistoryRequestID, *compacted)
			} else {
				_, err = client.VerifyContinuation(ctx, c.HistoryRequestID, checkpoint.Native, intent)
			}
		}
	} else if f := input.Fork; f != nil {
		bound, err = client.ResumeThread(ctx, input.ThreadRequestID, checkpoint.Native.ThreadID, settings)
		if err == nil {
			_, err = client.VerifyContinuation(ctx, f.HistoryRequestID, checkpoint.Native, codex.ContinueAfterSuccess)
		}
	} else {
		bound, err = client.StartThread(ctx, input.ThreadRequestID, settings)
	}
	if err != nil {
		return nil, err
	}
	if err := mapper.BindThread(ctx, bound); err != nil {
		return nil, err
	}
	logger.InfoContext(ctx, "native_execution_thread_bound")
	if err := config.startup.ready(ctx, client.Version()); err != nil {
		return nil, err
	}
	config.startup.claimInput()
	turn, err := client.StartTurn(ctx, input.TurnRequestID, input.InputID, input.Input)
	if err != nil {
		if proofErr := config.startup.captureImageRejection(turn); proofErr != nil {
			if config.startup != nil {
				config.startup.firstFailure = err
			}
			return nil, proofErr
		}
		return nil, err
	}
	if err := mapper.AcceptInput(ctx, turn); err != nil {
		return nil, err
	}
	if !cancelBeforeAcceptance() {
		return nil, domain.SafeError(context.Canceled)
	}
	config.startup.acknowledgeInput()
	logger.InfoContext(ctx, "native_execution_input_accepted", "input_id", input.InputID)
	finishResponses := startQuestionResponseController(ctx, nativeCtx, cancelNative, config.questionControls, mapper, client)
	finishApprovals := startApprovalResponseController(ctx, nativeCtx, cancelNative, config.approvalControls, mapper, client)
	finishSteers := startSteerController(ctx, nativeCtx, cancelNative, config.steerControls, mapper, client)
	defer func() {
		if err := finishSteers(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
		if err := finishApprovals(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
		if err := finishResponses(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
	}()
	readContext, publicationContext := ctx, nativeCtx
	stopping := false
	var parentTerminal *codex.Event
	for {
		if ctx.Err() != nil && !stopping {
			if err := nativeCtx.Err(); err != nil {
				return nil, domain.SafeError(err)
			}
			stopping = true
			bounded, cancel := context.WithTimeout(nativeCtx, 15*time.Second)
			defer cancel()
			readContext, publicationContext = bounded, bounded
			request := domain.NewID()
			intent := struct {
				Version     uint32    `json:"version"`
				ExecutionID domain.ID `json:"execution_id"`
				RequestID   domain.ID `json:"request_id"`
				ThreadID    domain.ID `json:"thread_id"`
				TurnID      domain.ID `json:"turn_id"`
			}{1, input.ExecutionID, request, bound.Thread.ID, turn.TurnID}
			if err := writeJSON(filepath.Join(home, "interruption.json"), intent); err != nil {
				return nil, publicationUncertain()
			}
			logger.InfoContext(bounded, "native_execution_interruption_requested", "request_id", request)
			if _, err := client.Interrupt(bounded, request, turn.TurnID); err != nil && domain.SafeError(err).Code != domain.Conflict {
				return nil, err
			}
			// A definitive native conflict may race an already completed turn.
			// Consume its bounded retained facts; never retry the interruption.
		}
		event, err := client.NextEvent(readContext)
		if err != nil {
			if !stopping && ctx.Err() != nil && nativeCtx.Err() == nil {
				continue
			}
			return nil, err
		}
		// Completion must inspect the native inventory even when spawn events
		// were missed. Root completion alone cannot release live descendants.
		if event.Kind == codex.SubagentActivityEvent || event.Kind == codex.TurnCompletedEvent {
			inspection, err := client.InspectDescendants(publicationContext)
			if err != nil {
				logger.WarnContext(publicationContext, "native_subagent_inspection_failed", "code", domain.SafeError(err).Code)
				return nil, err
			}
			if _, err := mapper.PublishCore(publicationContext, inspection); err != nil {
				return nil, err
			}
		}
		if event.Kind == codex.SubagentActivityEvent {
			activity, err := client.ObserveResolvedActivity(publicationContext, event)
			if err != nil {
				return nil, err
			}
			event = activity
		}
		handled, err := mapper.PublishCore(publicationContext, event)
		if err != nil {
			logger.WarnContext(publicationContext, "native_execution_publication_failed", "event_kind", event.Kind, "correlated", event.Correlated, "late", event.Late, "code", domain.SafeError(err).Code)
			return nil, err
		}
		if !handled {
			logger.WarnContext(publicationContext, "native_execution_event_unhandled", "event_kind", event.Kind, "metadata", event.Metadata, "extension_stage", event.ExtensionStage, "correlated", event.Correlated, "late", event.Late)
			return nil, domain.Fail(domain.Unsupported, "The native execution produced an unsupported event family.", "Retain its native history for the required typed adapter; input is never replayed automatically.")
		}
		if event.Kind == codex.TurnCompletedEvent {
			copy := event
			parentTerminal = &copy
		}
		hasChildren, childrenClosed := mapper.childState()
		if parentTerminal == nil || !childrenClosed {
			continue
		}

		event = *parentTerminal
		if managed != nil && event.Turn != nil && event.Turn.Status == codex.TurnFailed && event.Turn.QuotaBlock.Valid() && input.Input.Mode == domain.ExecuteMode {
			managed.publishQuotaBlock(ctx, config.observations, domain.SubscriptionQuotaBlock{SessionID: input.SessionID, ExecutionID: input.ExecutionID, NativeThreadID: domain.NativeIdentity(bound.Thread.ID), NativeTurnID: domain.NativeIdentity(turn.TurnID), Reason: event.Turn.QuotaBlock})
		}
		// Fence and join original observations before native credential capture and
		// terminal cleanup, including an admitted operation with a lost publication.
		if unregisterObservations != nil {
			unregisterObservations()
		}
		// A terminal event is not cleanup. Close/join the native scope, prove
		// the workspace lease's process index, then form a completion result.
		// Read native identity and final credentials while its wire is still
		// open; the defer only reads on earlier exits and never retries this read.
		var contextProof *codex.ContinuationContextCheckpoint
		if !hasChildren {
			bindings, err := mapper.completionInputs()
			if err != nil {
				return nil, err
			}
			nativeInputs := make([]codex.HistoricalInput, len(bindings))
			for n, binding := range bindings {
				nativeInputs[n].ID = binding.InputID
				bytes, err := hex.DecodeString(binding.PromptDigest)
				if err != nil || len(bytes) != 32 {
					return nil, executionCheckpointUncertain()
				}
				copy(nativeInputs[n].PromptDigest[:], bytes)
			}
			proofs, proofErr := client.SkillInputProofs(ctx)
			if proofErr != nil {
				return nil, proofErr
			}
			bound.SkillInputs = proofs
			for i := range nativeInputs {
				for _, proof := range proofs {
					if proof.ID == nativeInputs[i].ID && proof.PromptDigest == nativeInputs[i].PromptDigest {
						nativeInputs[i].SkillDigest = proof.SkillDigest
					}
				}
			}
			original := codex.ContinuationCheckpoint{PaginatedHistory: bound.Thread.History == codex.PaginatedHistory, ContextRevision: input.ContextRevision, ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: turn.TurnID, Status: event.Turn.Status, Mode: input.Input.Mode, Inputs: nativeInputs, Effective: *bound.Effective}
			contextProof, err = client.RetainContinuationContext(ctx, original)
			if err != nil {
				return nil, err
			}
		}
		captureManagedBundle()
		if err := client.Close(); err != nil {
			return nil, err
		}
		if ownedProxy != nil {
			if err := ownedProxy.Close(); err != nil {
				return nil, err
			}
		}
		var push *domain.PRPushProof
		if prGit != nil {
			if err := prGit.Close(); err != nil {
				return nil, err
			}
			bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			// VerifyPush first joins the original native process owner while the
			// lease remains active; lease.Close is the later release boundary.
			proof := prGit.VerifyPush(bounded)
			cancel()
			push = &proof
		}
		if err := lease.Close(); err != nil {
			return nil, err
		}
		sequence, err := publisher.acknowledgedSequence()
		if err != nil {
			return nil, err
		}
		outcome := domain.ExecutionSucceeded
		switch event.Turn.Status {
		case codex.TurnCompleted:
		case codex.TurnFailed:
			outcome = domain.ExecutionFailed
		case codex.TurnInterrupted:
			outcome = domain.ExecutionStopped
		default:
			return nil, publicationUncertain()
		}
		completion := domain.ExecutionCompletion{Version: 1, ExecutionID: input.ExecutionID, InputID: input.InputID, NativeThreadID: domain.NativeIdentity(bound.Thread.ID), NativeTurnID: domain.NativeIdentity(turn.TurnID), LastSequence: sequence, Outcome: outcome, CleanupVerified: true, PRPush: push}
		if err := completion.Validate(); err != nil {
			return nil, err
		}
		if hasChildren {
			return json.Marshal(completion)
		}
		// Preserve exact native continuation evidence before the operation
		// journal/report can announce cleanup. This carries no prompt, answer or
		// bearer and cannot authorize Resume without the server's matching proof.
		acceptedInputs, err := mapper.completionInputs()
		if err != nil {
			return nil, err
		}
		checkpointDigest, err := retainCodexCompletion(manager.Root, owner, job, acceptedInput, bound, completion, acceptedInputs, contextProof)
		if err != nil {
			logger.WarnContext(ctx, "native_execution_checkpoint_retention_failed", "code", domain.SafeError(err).Code)
			return nil, err
		}
		completion.Version, completion.NativeCheckpointDigest = 2, checkpointDigest
		if err := completion.Validate(); err != nil {
			return nil, err
		}
		logger.InfoContext(ctx, "native_execution_checkpoint_retained")
		return json.Marshal(completion)
	}
}

// validateManagedAuthenticationHome keeps the managed auth bundle outside every
// native workspace root. The native read-only/workspace-write sandbox then
// keeps tool commands from reading CODEX_HOME; unrestricted same-user access
// to the Worker process remains outside this guarantee.
func validateManagedAuthenticationHome(home string, workspaceRoots []string) error {
	canonicalHome, err := canonicalManagedPath(home, "The managed authentication home is not a private directory.", "Preserve the execution for reconciliation; do not materialize credentials through a path alias.")
	if err != nil {
		return err
	}
	for _, root := range workspaceRoots {
		canonicalRoot, err := canonicalManagedPath(root, "A managed execution workspace root is not a real directory.", "Preserve the execution for reconciliation; do not start native work with ambiguous credential confinement.")
		if err != nil {
			return err
		}
		if managedPathsOverlap(canonicalHome, canonicalRoot) {
			return domain.Fail(domain.RecoveryRequired, "The managed authentication home overlaps a native workspace root.", "Choose a private Worker runtime outside the workspace roots before materializing subscription credentials.")
		}
	}
	return nil
}

func canonicalManagedPath(path, message, remediation string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", domain.Fail(domain.RecoveryRequired, message, remediation)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", domain.Fail(domain.RecoveryRequired, message, remediation)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", domain.Fail(domain.RecoveryRequired, message, remediation)
	}
	return canonical, nil
}

func managedPathsOverlap(first, second string) bool {
	for _, pair := range [][2]string{{first, second}, {second, first}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
