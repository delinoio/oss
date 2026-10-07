package worker

import (
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func executeOpenCodeSession(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput, logger *slog.Logger) (output json.RawMessage, returned error) {
	phase := "selection"
	defer func() {
		if returned != nil && logger != nil {
			logger.WarnContext(ctx, "opencode_execution_phase_failed", "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()

	if input.Version != 4 && input.Installation.Version != opencode.SupportedVersion {
		return nil, domain.Fail(domain.Unsupported, "This OpenCode execution requires a separately verified continuation profile.", "Retain the original native history; do not start a replacement input.")
	}
	requested, err := openCodeExecutionSettings(input.Configuration, input.Input.Mode, "DeliDev session")
	if err != nil {
		return nil, err
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.SessionID != input.SessionID ||
		domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(owner), preparation.MachineID != input.MachineID) ||
		workspace.ValidateResult(preparation, manifest, runtime.GOOS) != nil {
		return nil, workspace.ResultUncertain()
	}
	executable := input.Installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.Fail(domain.RecoveryRequired, "The selected native executable identity changed.", "Review the selected executable path and recover original history; no PATH fallback is used.")
	}
	manager := &workspace.Manager{Root: config.Root, Logger: config.Logger}
	var lease *workspace.ExecutionLease
	if retry := input.Retry; retry != nil {
		lease, err = manager.ClaimUnsentRetry(ctx, owner, input.ExecutionID, workspace.ExecutionPredecessor{JobID: retry.JobID, ExecutionID: retry.ExecutionID}, preparation, manifest, retryOriginalWorkspace(input)...)
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
	defer func() {
		if err := lease.Close(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
	}()
	phase = "workspace-root"
	nativeRoot, err := openCodeWorkspaceRoot(manifest, lease.WorkingDirectory())
	if err != nil {
		return nil, err
	}
	phase = "private-predecessor"
	var forkSeed *openCodeForkCheckpoint
	if input.Fork != nil {
		value, err := readOpenCodeForkCheckpoint(ctx, manager.Root, config.execution.Credential, input)
		if err != nil {
			return nil, err
		}
		forkSeed = &value
	}
	var checkpoint *openCodeExecutionCheckpoint
	if input.Continuation != nil {
		value, err := readOpenCodeContinuationCheckpoint(ctx, manager.Root, config.execution.Credential, input)
		if err != nil {
			logger.WarnContext(ctx, "opencode_continuation_checkpoint_failed", "execution_id", input.ExecutionID, "code", domain.SafeError(err).Code)
			return nil, err
		}
		checkpoint = &value
		logger.InfoContext(ctx, "opencode_continuation_checkpoint_verified", "execution_id", input.ExecutionID, "previous_execution_id", input.Continuation.Previous.ExecutionID)
	}
	phase = "runtime"
	runtimeRoot := filepath.Join(manager.Root, "runtimes")
	if err := security.PrivateDir(runtimeRoot); err != nil {
		return nil, domain.SafeError(err)
	}
	home := filepath.Join(runtimeRoot, string(input.ExecutionID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, publicationUncertain()
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	connection := config.execution
	publicationConfig := *connection
	publicationConfig.Root = manager.Root
	phase = "publication"
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
	intent := struct {
		Version     uint32    `json:"version"`
		JobID       domain.ID `json:"job_id"`
		ExecutionID domain.ID `json:"execution_id"`
		RequestID   domain.ID `json:"request_id"`
		TokenDigest string    `json:"token_digest"`
	}{1, owner, input.ExecutionID, registration, hex.EncodeToString(digest[:])}
	// OpenCode's private runtime must remain empty before native scanners run.
	// Registration belongs to the Worker journal outside that runtime.
	if err := writeJSON(filepath.Join(manager.Root, "jobs", string(owner), "opencode-registration.json"), intent); err != nil {
		return nil, publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	phase = "registration"
	registered, err := connection.Client.RegisterExecution(bounded, authenticated(connection.Credential, &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(registration), Id: string(owner), ExpectedRevision: connection.Assignment.Revision}, MachineId: string(input.MachineID), InstanceId: string(connection.Instance), CredentialDigest: digest[:]}))
	cancel()
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if registered == nil || registered.Msg == nil || registered.Msg.ProxyPath != "/api-proxy/v1" {
		return nil, publicationUncertain()
	}
	// The stream owns native lifetime. Targeted Stop only kills startup until
	// original input acceptance is durable; afterwards it owns an abort grace.
	nativeCtx, cancelNative := context.WithCancel(config.executionContext)
	defer cancelNative()
	cancelBeforeAcceptance := context.AfterFunc(ctx, cancelNative)
	defer cancelBeforeAcceptance()
	nativeConfig := opencode.APIExecutionConfig{
		Probe:     opencode.ProbeConfig{Process: process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: home, Env: env, Logger: logger}, Version: input.Installation.Version, Home: filepath.Join(home, "opencode")},
		Workspace: lease.WorkingDirectory(), Root: nativeRoot, ServerOrigin: connection.Credential.Endpoint, Token: token,
		References: openCodeWorkspaceReferences(manifest),
		Settings:   requested.Session, Instructions: requested.Instructions, Rejection: requested.Rejection,
	}
	if input.Configuration.OpenCodeContext != nil {
		nativeConfig.ContextLimit = int64(input.Configuration.OpenCodeContext.Tokens)
		nativeConfig.Prune = input.Configuration.OpenCodeContext.Policy == domain.OpenCodeNativeContextV1
	}
	var compacted *openCodeSessionCompactionCheckpoint
	if checkpoint != nil && input.Continuation.Compaction != nil {
		value, err := readOpenCodeSessionCompactionCheckpoint(ctx, manager.Root, connection.Credential, input, *input.Continuation.Compaction, *checkpoint, 0)
		if err != nil {
			return nil, err
		}
		compacted = &value
		// Preserve the independent ordinary assignment/report reference while
		// restoring only the separately accepted action's native snapshot.
		valueCheckpoint := *checkpoint
		valueCheckpoint.Native, valueCheckpoint.NativeReference = value.Native, value.NativeReference
		checkpoint = &valueCheckpoint
	}
	var resumeClaim *opencode.SessionClaim
	if forkSeed != nil {
		claim, err := opencode.CheckpointResumeClaim(nativeConfig, forkSeed.NativeReference, input.ThreadRequestID)
		if err != nil {
			return nil, err
		}
		resumeClaim = &claim
	}
	if checkpoint != nil {
		claim, err := opencode.CheckpointResumeClaim(nativeConfig, checkpoint.NativeReference, input.ThreadRequestID)
		if err != nil {
			return nil, err
		}
		resumeClaim = &claim
	}
	phase = "binding"
	var binding *OpenCodeBindingPublisher
	if forkSeed != nil {
		binding, err = openOpenCodeForkBinding(publisher, forkSeed, resumeClaim)
	} else {
		binding, err = openOpenCodeBinding(publisher, checkpoint, resumeClaim)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := binding.Close(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
	}()
	nativeConfig.Claim = binding.Claim
	phase = "native-restore"
	config.startup.setPhase(domain.StartupInitialize)
	var api *opencode.OwnedAPI
	if forkSeed != nil {
		previousHome := filepath.Join(runtimeRoot, string(input.Fork.RuntimeID), "native")
		api, err = opencode.OpenResumedAPI(nativeCtx, nativeConfig, previousHome, forkSeed.Native, forkSeed.NativeReference, input.ThreadRequestID, opencode.BuildAgent, false)
	} else if checkpoint != nil {
		previousHome := filepath.Join(runtimeRoot, string(checkpoint.Reference.Claim.ExecutionID))
		if compacted != nil {
			previousHome = filepath.Join(runtimeRoot, string(compacted.Input.ActionID))
		}
		previousAgent, err := input.Configuration.OpenCodePrimaryForInput(input.Continuation.InputMode)
		if err != nil {
			return nil, err
		}
		api, err = opencode.OpenResumedAPI(nativeCtx, nativeConfig, previousHome, checkpoint.Native, checkpoint.NativeReference, input.ThreadRequestID, previousAgent, input.Continuation.Intent == domain.ContinueExplicitly)
	} else {
		api, err = opencode.OpenOwnedAPI(nativeCtx, nativeConfig)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := api.Close(cleanup); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
	}()
	phase = "native-selection"
	observed, err := api.InitialSettings(ctx)
	if err != nil {
		return nil, err
	}
	var session string
	if forkSeed != nil {
		session = forkSeed.NativeReference.SessionID
	} else if checkpoint != nil {
		session = checkpoint.NativeReference.SessionID
	} else {
		session, err = api.CreateSession(ctx, input.ThreadRequestID)
		if err != nil {
			return nil, err
		}
	}
	if forkSeed != nil {
		if err := binding.prepareForkInput(); err != nil {
			return nil, err
		}
	} else if err := binding.BindSession(ctx, input.ThreadRequestID, session, observed); err != nil {
		return nil, err
	}
	if forkSeed != nil {
		logger.InfoContext(ctx, "native_execution_private_fork_ready")
	} else {
		logger.InfoContext(ctx, "native_execution_thread_bound")
	}
	// StartText's context owns the original subscription, so it cannot be the
	// targeted job context that later wakes the event reader to request Stop.
	if err := config.startup.ready(ctx, api.Version()); err != nil {
		return nil, err
	}
	input.Installation.Version = api.Version()
	publisher.nativeVersion = api.Version()
	config.startup.claimInput()
	if _, err := api.StartText(nativeCtx, input.TurnRequestID, input.Input.Prompt); err != nil {
		return nil, err
	}
	phase = "first-input"
	prefix, err := acceptOpenCodeInput(ctx, api, binding)
	if err != nil {
		return nil, err
	}
	if !cancelBeforeAcceptance() {
		return nil, domain.SafeError(context.Canceled)
	}
	config.startup.acknowledgeInput()
	logger.InfoContext(ctx, "native_execution_input_accepted", "input_id", input.InputID)
	mapper, err := OpenOpenCodeEventPublisher(binding, api)
	if err != nil {
		return nil, err
	}
	for _, frozen := range prefix {
		observation, err := frozen.Thaw()
		if err != nil {
			return nil, err
		}
		if err := mapper.PublishObservation(nativeCtx, observation); err != nil {
			return nil, err
		}
	}
	prefix = nil
	finishControls := startOpenCodeControls(ctx, nativeCtx, cancelNative, config, mapper)
	defer func() {
		if err := finishControls(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
	}()
	readContext, publicationContext := ctx, nativeCtx
	stopping := false
	for {
		progress, err := api.Progress(publicationContext)
		if err != nil || progress.NeedsRecovery {
			return nil, publicationUncertain()
		}
		if ctx.Err() != nil && !stopping {
			if nativeCtx.Err() != nil {
				return nil, domain.SafeError(nativeCtx.Err())
			}
			stopping = true
			grace, cancel := context.WithTimeout(nativeCtx, 15*time.Second)
			defer cancel()
			readContext, publicationContext = grace, grace
			if !progress.TerminalObserved {
				request := domain.NewID()
				logger.InfoContext(grace, "native_execution_interruption_requested", "request_id", request)
				if progress.AssistantID == "" || (progress.Status != opencode.NativeStatusBusy && progress.Status != opencode.NativeStatusRetry) {
					// Unobserved scheduling permits owned termination, never an
					// invented native abort or original-input completion report.
					if _, err := api.ClaimOwnedStop(grace, request); err != nil {
						return nil, err
					}
					if _, err := api.FinishStopCleanup(grace); err != nil {
						return nil, err
					}
					return nil, publicationUncertain()
				}
				receipt, err := mapper.RequestStop(grace, request)
				if err != nil && !receipt.NativeAttempted {
					return nil, err
				}
				// Lost HTTP leaves this original attempt pending. Continue its
				// stream; only native history plus cleanup can settle it.
			}
		}
		if progress.SettledObserved {
			if _, err := mapper.PublishTerminal(publicationContext); err != nil {
				return nil, err
			}
			if err := finishControls(); err != nil {
				return nil, err
			}
			_, err := mapper.Complete(publicationContext)
			if err != nil {
				return nil, err
			}
			// Keep workspace ownership through the original snapshot export and
			// join its separately journaled Git children before closing the lease.
			completion, err := mapper.RetainCompletion(publicationContext)
			if err != nil {
				return nil, err
			}
			if err := lease.Close(); err != nil {
				return nil, err
			}
			completion.CleanupVerified = lease.CleanupConfirmed()
			return json.Marshal(completion)
		}
		observation, err := api.Next(readContext)
		if err != nil {
			if !stopping && ctx.Err() != nil && nativeCtx.Err() == nil {
				continue
			}
			return nil, err
		}
		if err := mapper.PublishObservation(publicationContext, observation); err != nil {
			return nil, err
		}
	}
}

// The accepted manifest and owned lease select the closed root profile. Native
// metadata cannot nominate filesystem authority or replace enclosing Git roots.
func openCodeWorkspaceRoot(manifest workspace.Manifest, cwd string) (opencode.WorkspaceRoot, error) {
	if cwd != manifest.PrimaryPath || !filepath.IsAbs(cwd) || manifest.Type == domain.GeneralChat && len(manifest.Repositories) != 0 {
		return opencode.WorkspaceRoot{}, workspace.ResultUncertain()
	}
	if manifest.Type == domain.GeneralChat {
		return opencode.GlobalWorkspaceRoot(cwd)
	}
	return opencode.GitWorkspaceRoot(cwd, cwd)
}

// Call only after the complete manifest and original workspace lease agree.
func openCodeWorkspaceReferences(manifest workspace.Manifest) []opencode.WorkspaceReference {
	var references []opencode.WorkspaceReference
	for _, repository := range manifest.Repositories {
		if repository.Path != manifest.PrimaryPath {
			references = append(references, opencode.WorkspaceReference{RepositoryID: repository.ID, Path: repository.Path})
		}
	}
	return references
}

func acceptOpenCodeInput(ctx context.Context, api *opencode.OwnedAPI, binding *OpenCodeBindingPublisher) ([]*opencode.FrozenObservation, error) {
	var prefix []*opencode.FrozenObservation
	bytes := 0
	for len(prefix) < 65536 {
		observation, err := api.Next(ctx)
		if err != nil {
			return nil, err
		}
		frozen, err := observation.Freeze()
		if err != nil || frozen.Bytes() <= 0 || frozen.Bytes() > maxOpenCodeTextBytes-bytes {
			return nil, publicationUncertain()
		}
		prefix, bytes = append(prefix, frozen), bytes+frozen.Bytes()
		progress, err := api.Progress(ctx)
		if err != nil || progress.NeedsRecovery {
			return nil, publicationUncertain()
		}
		if progress.UserSeen && progress.InputPartSeen {
			receipt, err := api.InspectInput(ctx)
			if err != nil {
				return nil, err
			}
			if binding.fork != nil {
				observed, err := api.VerifiedForkSettings(ctx)
				if err != nil {
					return nil, err
				}
				if err := binding.BindSession(ctx, binding.reference.ThreadRequestID, receipt.SessionID, observed); err != nil {
					return nil, err
				}
			}
			if err := binding.AcceptInput(ctx, receipt); err != nil {
				return nil, err
			}
			return prefix, nil
		}
	}
	return nil, publicationUncertain()
}
