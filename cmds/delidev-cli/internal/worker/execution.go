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

type codexTurnStarter interface {
	StartTurn(context.Context, domain.ID, domain.ID, domain.SessionInput) (codex.TurnResult, error)
}

func startCodexTurnWithRetryAuthority(ctx context.Context, connection *PublicationConfig, registration *pb.RegisterExecutionRequest, input domain.ExecutionJobInput, managed bool, client codexTurnStarter, logger *slog.Logger) (codex.TurnResult, error) {
	if input.SidechatRetry != nil && managed {
		// Managed retry inference bypasses the API relay. Replaying the exact
		// registration receipt forces a current actor and binding check after
		// native startup and binding, before the first input.
		bounded, stop := context.WithTimeout(ctx, 30*time.Second)
		fresh, checkErr := connection.Client.RegisterExecution(bounded, authenticated(connection.Credential, registration))
		stop()
		if checkErr != nil {
			clientErr := rpc.ClientError(checkErr)
			logger.WarnContext(ctx, "sidechat_retry_presend_denied", "generation_id", input.SidechatRetry.GenerationID, "request_id", registration.Mutation.RequestId, "code", domain.SafeError(clientErr).Code)
			return codex.TurnResult{}, clientErr
		}
		if fresh == nil || fresh.Msg == nil || !fresh.Msg.Replayed || fresh.Msg.ProxyPath != "" {
			logger.WarnContext(ctx, "sidechat_retry_presend_unverified", "generation_id", input.SidechatRetry.GenerationID, "request_id", registration.Mutation.RequestId)
			return codex.TurnResult{}, publicationUncertain()
		}
		logger.InfoContext(ctx, "sidechat_retry_presend_authorized", "generation_id", input.SidechatRetry.GenerationID, "request_id", registration.Mutation.RequestId)
	}
	return client.StartTurn(ctx, input.TurnRequestID, input.InputID, input.Input)
}

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
		config.progress.native(domain.StartupResolve, domain.StartupProgressRunning)
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
	manager := &workspace.Manager{NamedDirectories: config.namedWorkspaceDirectories, Root: config.Root, Logger: config.Logger}
	var lease *workspace.ExecutionLease
	var sharedMember *codexSharedMember
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
		if sharedMember != nil && !sharedMember.proven {
			return
		}
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
	// The per-execution directory is only the operation journal. It must be fresh
	// so an interrupted first execution is never inherited; native state lives in
	// the persistent conversation profile resolved below.
	runtimeRoot := filepath.Join(manager.Root, "runtimes")
	if err := security.PrivateDir(runtimeRoot); err != nil {
		return nil, domain.SafeError(err)
	}
	home := filepath.Join(runtimeRoot, string(input.ExecutionID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, publicationUncertain()
	}
	if err := security.CreatePrivateDirExclusive(home); err != nil {
		return nil, publicationUncertain()
	}
	var ordinaryTools executionenv.Ordinary
	if input.Configuration.SidechatPolicy == "" {
		ordinaryTools = ordinaryExecutionTools(config.Logger)
	}
	profile, err := codexExecutionProfile(ctx, config, input)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := profile.Close(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
	}()
	env, err := harness.PrivateRuntimeEnvironment(profile.Root)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	nativeHome := filepath.Join(profile.Root, "codex")
	sharedCodex := config.codexMembers && input.SharedCodexOwnerEligible()
	if sharedCodex {
		// The shared account history home is registered in place in the
		// persistent profile registry; its lock is taken by the founding owner.
		sharedInfo, err := resolveSharedCodexHome(ctx, config, input.MachineID, input.AccountID)
		if err != nil {
			return nil, err
		}
		nativeHome = sharedInfo.Root
	}
	env, err = replaceCodexHome(env, nativeHome)
	if err != nil {
		return nil, err
	}
	var checkpoint CodexExecutionCheckpoint
	var compacted *codex.CompactedCheckpoint
	// accountHold is the shared account home lock of an exclusive version 2
	// continuation. It is released, verified, before the managed Finish.
	var accountHold *sharedHomeHold
	// pendingV1Import is the version 1 checkpoint of a shared continuation whose
	// retained rollout is copied into the shared account home only after the
	// request is admitted under that home's lock; see the import below.
	var pendingV1Import *ExecutionCheckpointRef
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
		if sharedCodex && (account != input.AccountID || connection != input.ConnectionID) {
			return nil, executionCheckpointUncertain()
		}
		checkpointRef := ExecutionCheckpointRef{ContextRevision: c.Previous.ContextRevision, ApprovalsReviewer: input.Configuration.Options.ApprovalsReviewer, Subscription: input.Configuration.Subscription, JobID: c.Previous.JobID, SessionID: input.SessionID, MachineID: input.MachineID, HistoryExecutionID: c.HistoryExecutionID, AssignmentInputDigest: c.AssignmentInputDigest, ConfigurationDigest: input.ConfigurationDigest, AccountID: account, ConnectionID: connection, Completion: c.Completion, InputMode: c.InputMode, PromptDigest: promptDigest, AcceptedInputs: c.Previous.AcceptedInputs, WorkspaceRoots: nativeWorkspaceRoots(manifest)}
		checkpoint, err = ReadCodexExecutionCheckpoint(manager.Root, checkpointRef)
		if err != nil {
			return nil, err
		}
		if sharedCodex {
			if checkpoint.Version == 1 {
				// The copy writes the account-wide home, which an exclusive
				// continuation or a running owner may hold, so it waits until
				// this request holds or joins that home's lock.
				pendingV1Import = &checkpointRef
			}
		} else if checkpoint.Version == 2 {
			if checkpoint.AccountID != input.AccountID || checkpoint.ConnectionID != input.ConnectionID {
				return nil, executionCheckpointUncertain()
			}
			nativeHome = checkpoint.AccountHome
			// An exclusive continuation of the account history locks the same
			// registered profile that a shared owner holds, so both can never
			// write the home at once. The managed Finish releases it first.
			hold, err := holdSharedCodexHome(ctx, config, input.MachineID, input.AccountID)
			if err != nil {
				return nil, err
			}
			if hold.root() != nativeHome {
				if err := hold.release(); err != nil {
					config.codexOwners.parkUnverifiedRelease(input.AccountID, hold)
				}
				return nil, executionCheckpointUncertain()
			}
			accountHold = hold
			defer func() {
				if changed, settled := retryHeldRelease(config, input.AccountID, accountHold, returned); changed {
					output, returned = nil, settled
				}
			}()
		}
		env, err = replaceCodexHome(env, nativeHome)
		if err != nil {
			return nil, err
		}
		if security.CheckPrivateDir(nativeHome) != nil {
			return nil, executionCheckpointUncertain()
		}
		if c.Compaction == nil && pendingV1Import == nil {
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
		if security.CheckPrivateDir(nativeHome) != nil {
			return nil, executionCheckpointUncertain()
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
	accountHistory := sharedCodex || checkpoint.Version == 2
	if input.Configuration.Subscription {
		if err := validateManagedAuthenticationHome(nativeHome, manifest.WorkspaceRoots()); err != nil {
			return nil, err
		}
	}
	var managed *managedSubscriptionLease
	var sharedOwner *codexSharedOwner
	var sharedFounder bool
	var releaseCodexAdmission func()
	var managedLatest []byte
	var closeManagedRPC func()
	managedCleanup, managedSuccess := false, false
	managedUnusedOriginal := false
	managedPreNativeCleanup, managedHelperRetained := false, false
	defer func() {
		founderAbort := sharedMember != nil && sharedFounder && sharedOwner.runtime == nil
		var founderErr error
		if founderAbort {
			// Keep admission serialized until the original owner Finish settles.
			// A waiting Take must not join an owner without a native runtime.
			founderErr = sharedOwner.abortFounder(nativeHome, sharedMember)
		}
		if releaseCodexAdmission != nil {
			releaseCodexAdmission()
		}
		if closeManagedRPC != nil {
			defer closeManagedRPC()
		}
		if sharedMember != nil {
			finishErr := founderErr
			if !founderAbort {
				finishErr = sharedOwner.gate.finishMember(sharedOwner, sharedMember, sharedMember.proven)
			}
			var releaseFailure *sharedHomeReleaseError
			if errors.As(finishErr, &releaseFailure) {
				// The owner Finish was published; only the shared home release is
				// unverified. Report it like an exclusive continuation does, and
				// keep it for the next holder of the account home.
				output, returned = nil, config.startup.cleanupFailure(returned, finishErr)
			} else if finishErr != nil {
				output, returned = nil, &managedExecutionUncertain{finishErr}
			}
			clear(managed.response.Bundle)
			return
		}
		if managed != nil {
			// Finish may acknowledge a fenced account without establishing safe
			// ownership. Only a captured bundle or the verified unused original,
			// together with independent cleanup, permits ordinary job reporting.
			conclusive := managedCleanup && (managedSuccess || managedUnusedOriginal)
			// The server frees the account lease when it commits Finish, so the
			// shared home lock is released and verified before that request: a
			// continuation already retrying its Take would otherwise be admitted
			// and meet profile_busy.
			if changed, settled := settleExclusiveFinish(config, input.AccountID, accountHold, returned, conclusive, func() error {
				return managed.finish(managedLatest, managedCleanup, false, managedSuccess)
			}); changed {
				output, returned = nil, settled
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
		if sharedCodex {
			managed, releaseCodexAdmission, err = takeManagedSubscriptionAdmitted(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, owner, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, config.codexOwners.gate(input.AccountID))
		} else {
			managed, err = takeManagedSubscription(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, owner, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
		}
		if err != nil {
			return nil, err
		}
		if sharedCodex {
			gate := config.codexOwners.gate(input.AccountID)
			sharedOwner, sharedMember, sharedFounder, err = gate.register(config, managed, closeRPC, owner, lease)
			if err != nil {
				scope := pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER
				bundle := []byte(nil)
				if managed.response.OwnerLeaseId == "" {
					scope, bundle = pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, managed.response.Bundle
				}
				finishErr := managed.finishCodexScopedRetry(bundle, false, false, scope, managed.response.LeaseRevision)
				clear(managed.response.Bundle)
				managed = nil
				gate.fence()
				releaseCodexAdmission()
				releaseCodexAdmission = nil
				if finishErr != nil {
					return nil, &managedExecutionUncertain{finishErr}
				}
				return nil, err
			}
			if !sharedFounder {
				releaseCodexAdmission()
				releaseCodexAdmission = nil
			} else if _, _, err := subscription.Parse(managed.response.Bundle); err != nil {
				return nil, err
			}
			closeManagedRPC = nil // The registered member closes its own transport.
		} else if _, _, err := subscription.Parse(managed.response.Bundle); err != nil {
			return nil, err
		}
		// Install cleanup before the atomic write: even a synchronization error
		// may leave the owned plaintext file committed. Publisher/registration
		// failures occur before any native process can own this authentication.
		managedPreNativeCleanup = !sharedCodex
		defer func() {
			if managedPreNativeCleanup {
				var authErr error
				managedCleanup, authErr = cleanupPreNativeAuthentication(config.cleanupContext(), nativeHome, managed.response.Bundle, managedHelperRetained)
				if managedHelperRetained {
					// The original native tree was joined, so authentication cleanup
					// runs independently; unretired helpers still retain the lease.
					logExecutionAuthenticationCleanup(config.Logger, owner, authErr)
				}
				if managedCleanup {
					// This closed pre-native outcome returns unused original bytes;
					// it does not claim native execution or authentication success.
					managedLatest = bytes.Clone(managed.response.Bundle)
					managedUnusedOriginal = true
				} else if managedHelperRetained {
					if authErr != nil {
						output, returned = nil, errors.Join(returned, subscription.Invalid())
					}
				} else {
					output, returned = nil, subscription.Invalid()
				}
			}
		}()
		if sharedCodex && sharedFounder {
			// The founding owner locks the registered account home until the
			// owner retires; joined members rely on that lock.
			sharedOwner.profile, err = holdSharedCodexHome(ctx, config, input.MachineID, input.AccountID)
			if err != nil {
				return nil, err
			}
		}
		if pendingV1Import != nil {
			// The founder holds the account home lock and a joined member runs
			// under the founder's hold, so the copy never races an exclusive
			// continuation of the account history.
			importedHome := ""
			checkpoint, importedHome, err = importCodexV1History(ctx, manager.Root, *pendingV1Import, sharedOwner.profile)
			if err != nil {
				return nil, err
			}
			if importedHome != nativeHome || security.CheckPrivateDir(nativeHome) != nil {
				return nil, executionCheckpointUncertain()
			}
			if err := codex.VerifyContinuationContextRollout(ctx, nativeHome, checkpoint.Native); err != nil {
				return nil, err
			}
			logger.InfoContext(ctx, "native_execution_v1_history_imported", "history_execution_id", pendingV1Import.HistoryExecutionID)
		}
		if !sharedCodex || sharedFounder {
			if sharedCodex || checkpoint.Version == 2 {
				if _, statErr := os.Lstat(filepath.Join(nativeHome, "auth.json")); !errors.Is(statErr, os.ErrNotExist) {
					return nil, subscription.Invalid()
				}
			}
			if err := managed.recordAuthenticationHome(nativeHome); err != nil {
				return nil, err
			}
			if err := subscription.WriteCredentialFile(filepath.Join(nativeHome, "auth.json"), managed.response.Bundle); err != nil {
				return nil, subscription.Invalid()
			}
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
	registrationRequest := &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(registration), Id: string(owner), ExpectedRevision: connection.Assignment.Revision}, MachineId: string(input.MachineID), InstanceId: string(connection.Instance), CredentialDigest: digest[:]}
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
	registered, err := connection.Client.RegisterExecution(bounded, authenticated(connection.Credential, registrationRequest))
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
	nativeConfig := codex.Config{DynamicTools: input.NativeDynamicTools, EnableImageGeneration: input.NativeImageGeneration, RevertHistory: input.ContextRevision > 0 || checkpoint.Native.PaginatedHistory || checkpoint.Native.ContextRevision > 0 || compacted != nil && compacted.Revert != nil, ManagedForkHistory: input.Configuration.Subscription && input.Fork != nil && input.Configuration.SidechatPolicy == "", OrdinaryTools: ordinaryTools, SkillsRoot: config.Root, ImageRoot: config.Root, ImageMachineID: input.MachineID, ImageInputDeclared: input.Configuration.ImageInputDeclared, Mode: codex.ThreadProtocol, Version: input.Installation.Version, Home: nativeHome, API: &codex.APIConfig{ServerOrigin: connection.Credential.Endpoint, Token: token}, Process: process.Config{StartupObserver: config.progress.native, Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: settings.Cwd, Env: env, Logger: config.Logger}}
	if input.Configuration.SidechatPolicy == domain.CodexReadOnlySidechatV1 {
		nativeConfig.Sidechat = codex.ReadOnlySidechatV1
	}
	var ownedProxy *nativeproxy.Proxy
	var nativeRoute *networkSnapshot
	if managed == nil {
		nativeRoute = captureNativeRoute(config)
		proxy, err := openCodexNativeProxy(nativeCtx, config, input.ExecutionID, nativeRoute)
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
	if managed == nil && ownedProxy == nil {
		// A Local desktop Worker that follows a relocated runtime must not hand
		// Codex the retired pairing address. The forwarder is joined after the
		// native process (deferred earlier than its close) and before success.
		forwarder, err := openCodexNativeForwarder(nativeCtx, config, input.ExecutionID, token, false, nativeRoute)
		if err != nil {
			return nil, err
		}
		if forwarder != nil {
			nativeConfig.API.ForwarderOrigin = forwarder.NativeOrigin()
			defer func() {
				if err := forwarder.Close(); err != nil {
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
	// recovery-required startup must retain its authentication and lease, except
	// that a joined tree with failed helper retirement still runs authentication
	// cleanup while keeping the lease.
	managedPreNativeCleanup = false
	config.startup.setPhase(domain.StartupInitialize)
	var client *codex.Client
	if sharedCodex {
		if sharedFounder {
			var nativeOwner *codex.Client
			ownerConfig := nativeConfig
			ownerConfig.Process.Cwd = nativeHome
			ownerConfig.Process.OwnerID = sharedOwner.ownerProcessID()
			nativeOwner, err = codex.Open(config.executionContext, ownerConfig)
			if err == nil {
				var sharedRuntime *codex.SharedRuntime
				sharedRuntime, err = nativeOwner.StartSharedRuntime()
				if err == nil {
					err = sharedOwner.ready(sharedRuntime, nativeHome)
				}
				if err != nil {
					var nativeErr, helperErr error
					if sharedRuntime != nil {
						nativeErr, helperErr = sharedRuntime.Close()
					} else {
						nativeErr, helperErr = nativeOwner.CloseWithExecutionHelpers()
					}
					sharedOwner.nativeUncertain = nativeErr != nil || helperErr != nil
				}
			}
			if err != nil {
				sharedOwner.nativeUncertain = sharedOwner.nativeUncertain || domain.SafeError(err).Code == domain.RecoveryRequired
				return nil, err
			}
			if config.observations != nil {
				sharedOwner.unregisterObservations = config.observations.register(input.AccountID, nativeOwner, sharedOwner.lease)
			}
			releaseCodexAdmission()
			releaseCodexAdmission = nil
		}
		client, err = sharedOwner.runtime.NewMember(nativeConfig)
		if err != nil {
			return nil, err
		}
	} else {
		client, err = codex.Open(nativeCtx, nativeConfig)
		if err != nil {
			managedHelperRetained = codex.NativeJoinedHelperRetirementFailed(err)
			managedPreNativeCleanup = managedHelperRetained || domain.SafeError(err).Code != domain.RecoveryRequired
			return nil, err
		}
	}
	managedBundleAttempted := false
	captureManagedBundle := func() {
		if managed == nil || sharedCodex || managedBundleAttempted {
			return
		}
		managedBundleAttempted = true
		bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		bundle, bundleErr := client.ManagedBundle(bounded, false)
		managedLatest, managedSuccess = bundle, bundleErr == nil
	}
	defer func() {
		if sharedCodex {
			return
		}
		captureManagedBundle()
		nativeErr, helperErr := client.CloseWithExecutionHelpers()
		if nativeErr != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, nativeErr)
			return
		}
		if managed != nil {
			// The original process join authorizes the independent final auth
			// comparison/removal and complete retained-file scan. A failed helper
			// owner must not skip that work or release the credential lease.
			authErr := managed.cleanupAuthentication(config.cleanupContext(), nativeHome, managedLatest, managed.response.Bundle)
			managedCleanup = helperErr == nil && authErr == nil
			logExecutionAuthenticationCleanup(config.Logger, owner, authErr)
			if !managedCleanup || !managedSuccess {
				output, returned = nil, errors.Join(returned, subscription.Invalid())
			}
		}
	}()
	var unregisterObservations func()
	if managed != nil && !sharedCodex && config.observations != nil {
		unregisterObservations = config.observations.register(input.AccountID, client, managed)
		defer unregisterObservations()
	}
	input.Installation.Version = client.Version()
	publisher.nativeVersion = client.Version()
	mapper := NewCodexEventPublisher(publisher, config.functionOutputsSupported)
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
	turn, err := startCodexTurnWithRetryAuthority(ctx, connection, registrationRequest, input, managed != nil, client, logger)
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
	terminalPublished := false
	var dynamicDrainCancel context.CancelFunc
	var dynamicDrainParent context.Context
	dynamicDrainExpired := false
	var handled bool
	for {
		if ctx.Err() != nil && !stopping {
			if err := nativeCtx.Err(); err != nil {
				return nil, domain.SafeError(err)
			}
			stopping = true
			if dynamicDrainCancel != nil {
				dynamicDrainCancel()
				dynamicDrainCancel = nil
				dynamicDrainParent = nil
			}
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
			if (stopping || dynamicDrainCancel != nil) && nativeCtx.Err() == nil && errors.Is(err, context.DeadlineExceeded) && parentTerminal != nil && terminalPublished && parentTerminal.Turn != nil && (parentTerminal.Turn.Status == codex.TurnFailed || parentTerminal.Turn.Status == codex.TurnInterrupted) {
				// Failed terminals remain failures; after their bounded drain, close
				// the native scope when descendants are closed even if a tool stays unsettled.
				_, childrenClosed := mapper.childState()
				if !childrenClosed {
					return nil, err
				}
				if dynamicDrainCancel != nil {
					dynamicDrainCancel()
					dynamicDrainCancel = nil
					dynamicDrainParent = nil
				}
				dynamicDrainExpired = true
				goto terminalCleanup
			}
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
		if event.Kind == codex.TurnCompletedEvent {
			terminal := event
			parentTerminal = &terminal
			if !mapper.dynamicToolsSettled() {
				if dynamicDrainCancel == nil {
					dynamicDrainParent = readContext
					var drainContext context.Context
					drainContext, dynamicDrainCancel = context.WithTimeout(dynamicDrainParent, 15*time.Second)
					defer dynamicDrainCancel()
					readContext = drainContext
				}
				// Successful completion needs settled dynamic evidence. Failed and
				// interrupted outcomes are publishable with their original unsettled
				// evidence, so publish those before continuing the bounded drain.
				if event.Turn != nil && event.Turn.Status == codex.TurnCompleted {
					continue
				}
			}
		}
		handled, err = mapper.PublishCore(publicationContext, event)
		if err != nil {
			logger.WarnContext(publicationContext, "native_execution_publication_failed", "event_kind", event.Kind, "correlated", event.Correlated, "late", event.Late, "code", domain.SafeError(err).Code)
			return nil, err
		}
		if !handled {
			logger.WarnContext(publicationContext, "native_execution_event_unhandled", "event_kind", event.Kind, "metadata", event.Metadata, "extension_stage", event.ExtensionStage, "correlated", event.Correlated, "late", event.Late)
			return nil, domain.Fail(domain.Unsupported, "The native execution produced an unsupported event family.", "Retain its native history for the required typed adapter; input is never replayed automatically.")
		}
		if event.Kind == codex.DynamicToolEvent && event.DynamicTool != nil && event.DynamicTool.Stage == domain.DynamicRequested {
			if err := mapper.deliverDynamicUnavailable(ctx, publicationContext, event, client); err != nil {
				return nil, err
			}
		}
		if event.Kind == codex.TurnCompletedEvent {
			terminalPublished = true
		}
		if parentTerminal != nil && mapper.dynamicToolsSettled() {
			if dynamicDrainCancel != nil {
				dynamicDrainCancel()
				dynamicDrainCancel = nil
				readContext = dynamicDrainParent
				dynamicDrainParent = nil
			}
			if !terminalPublished {
				handled, err := mapper.PublishCore(publicationContext, *parentTerminal)
				if err != nil || !handled {
					if err == nil {
						err = publicationUncertain()
					}
					return nil, err
				}
				terminalPublished = true
			}
		}
	terminalCleanup:
		hasChildren, childrenClosed := mapper.childState()
		if parentTerminal == nil || !terminalPublished || !childrenClosed || (!mapper.dynamicToolsSettled() && !dynamicDrainExpired) {
			continue
		}

		event = *parentTerminal
		cleanupContext := ctx
		if stopping || dynamicDrainExpired {
			cleanupContext = nativeCtx
		}
		if managed != nil && config.quotaBlockSupported && event.Turn != nil && event.Turn.Status == codex.TurnFailed && event.Turn.QuotaBlock.Valid() && input.Input.Mode == domain.ExecuteMode {
			managed.publishQuotaBlock(cleanupContext, config.observations, domain.SubscriptionQuotaBlock{SessionID: input.SessionID, ExecutionID: input.ExecutionID, NativeThreadID: domain.NativeIdentity(bound.Thread.ID), NativeTurnID: domain.NativeIdentity(turn.TurnID), Reason: event.Turn.QuotaBlock})
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
		// Unresolved tool history cannot prove continuation. The observed failed
		// or interrupted root still joins its independent original cleanup.
		if !hasChildren && mapper.dynamicToolsSettled() {
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
			proofs, proofErr := client.SkillInputProofs(cleanupContext)
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
			contextProof, err = client.RetainContinuationContext(cleanupContext, original)
			if err != nil {
				return nil, err
			}
		}
		captureManagedBundle()
		var historyProof *codex.ThreadHistoryProof
		if sharedCodex {
			bounded, stop := context.WithTimeout(cleanupContext, 15*time.Second)
			closeErr := client.RetireSharedMember(bounded, domain.NewID())
			stop()
			if closeErr != nil {
				return nil, closeErr
			}
			if !mapper.dynamicToolsSettled() || !childrenClosed {
				return nil, sharedOwnerUncertain()
			}
			if !hasChildren {
				proof, proofErr := client.RetainThreadHistory(cleanupContext)
				if proofErr != nil {
					return nil, proofErr
				}
				historyProof = &proof
				if contextProof != nil {
					if contextProof.RolloutPath != proof.Path {
						return nil, executionCheckpointUncertain()
					}
					contextProof.RolloutDigest = proof.Digest
				}
			}
		} else {
			if accountHistory && !hasChildren {
				proof, proofErr := client.RetainThreadHistory(cleanupContext)
				if proofErr != nil {
					return nil, proofErr
				}
				historyProof = &proof
				if contextProof != nil {
					if contextProof.RolloutPath != proof.Path {
						return nil, executionCheckpointUncertain()
					}
					contextProof.RolloutDigest = proof.Digest
				}
			}
			if err := client.Close(); err != nil {
				return nil, err
			}
			if historyProof != nil && codex.VerifyThreadHistory(cleanupContext, nativeHome, *historyProof) != nil {
				return nil, executionCheckpointUncertain()
			}
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
		if sharedMember != nil {
			sharedMember.proven = true
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
		if hasChildren || !mapper.dynamicToolsSettled() {
			return json.Marshal(completion)
		}
		// Preserve exact native continuation evidence before the operation
		// journal/report can announce cleanup. This carries no prompt, answer or
		// bearer and cannot authorize Resume without the server's matching proof.
		acceptedInputs, err := mapper.completionInputs()
		if err != nil {
			return nil, err
		}
		var checkpointDigest string
		if accountHistory {
			if historyProof == nil {
				return nil, executionCheckpointUncertain()
			}
			checkpointDigest, err = retainCodexCompletionWithHistory(manager.Root, owner, job, acceptedInput, bound, completion, acceptedInputs, contextProof, historyProof, nativeHome)
		} else {
			checkpointDigest, err = retainCodexCompletion(manager.Root, owner, job, acceptedInput, bound, completion, acceptedInputs, contextProof)
		}
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
