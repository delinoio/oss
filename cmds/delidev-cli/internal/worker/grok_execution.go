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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func executeGrokSession(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput, logger *slog.Logger) (output json.RawMessage, returned error) {
	if input.Continuation != nil || input.Fork != nil || (input.Version != 4 && input.Installation.Version != grok.SupportedVersion) {
		return nil, domain.Fail(domain.Unsupported, "Grok execution requires the original pinned first-input profile.", "Retain existing native history; a replacement input or continuation is not authorized.")
	}
	contextTokens, err := input.Configuration.GrokFirstInputContext(input.Input.Mode)
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
	// Repository instruction/configuration and native Git identity have separate
	// profiles. Do not silently drop additional roots or adopt a project here.
	if preparation.Type != domain.GeneralChat || len(manifest.Repositories) != 0 {
		return nil, domain.Fail(domain.Unsupported, "This Grok runner requires an owned General Chat workspace.", "Keep repository workspaces for their separately verified native profile.")
	}
	executable := input.Installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.Fail(domain.RecoveryRequired, "The selected native executable identity changed.", "Refresh Worker discovery; no PATH fallback is used.")
	}
	manager := &workspace.Manager{Root: config.Root, Logger: logger}
	var lease *workspace.ExecutionLease
	if retry := input.Retry; retry != nil {
		lease, err = manager.ClaimUnsentRetry(ctx, owner, input.ExecutionID, workspace.ExecutionPredecessor{JobID: retry.JobID, ExecutionID: retry.ExecutionID}, preparation, manifest, retryOriginalWorkspace(input)...)
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
	publisher, err := OpenExecutionPublisher(publicationConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			output, returned = nil, publicationUncertain()
		}
	}()
	binding, err := OpenGrokBindingPublisher(publisher)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := binding.Close(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
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
	// Grok inspects the initially empty private runtime. Retain only registration
	// metadata in the Worker job directory, never the raw scoped credential.
	if err := writeJSON(filepath.Join(manager.Root, "jobs", string(owner), "grok-registration.json"), intent); err != nil {
		return nil, publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	registered, err := connection.Client.RegisterExecution(bounded, authenticated(connection.Credential, &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(registration), Id: string(owner), ExpectedRevision: connection.Assignment.Revision}, MachineId: string(input.MachineID), InstanceId: string(connection.Instance), CredentialDigest: digest[:]}))
	cancel()
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if registered == nil || registered.Msg == nil || registered.Msg.ProxyPath != "/api-proxy/v1" {
		return nil, publicationUncertain()
	}
	// Startup cancellation and stream loss join the owned process immediately.
	// Accepted input uses the separate original Stop controller below.
	nativeCtx, cancelNative := context.WithCancel(config.executionContext)
	defer cancelNative()
	cancelTargeted := context.AfterFunc(ctx, cancelNative)
	defer cancelTargeted()
	nativeConfig := grok.APIExecutionConfig{Probe: grok.ProbeConfig{Process: process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: home, Env: env, Logger: logger}, Version: input.Installation.Version, Home: filepath.Join(home, "grok")}, Workspace: lease.WorkingDirectory(), Model: input.Configuration.NativeModel, Instructions: input.Configuration.Instructions, ContextTokens: contextTokens, Mode: input.Input.Mode, ServerOrigin: connection.Credential.Endpoint, Token: token}
	tools, err := OpenGrokEventPublisher(binding)
	if err != nil {
		return nil, err
	}
	config.startup.setPhase(domain.StartupInitialize)
	api, err := grok.OpenOwnedAPIWithPlanning(nativeCtx, nativeConfig, grok.PlanningRecorders{Creation: binding.Creation, Input: binding.Input, Mode: binding.Mode, Closure: binding.Closure, Stop: binding.Stop, File: tools.FileReply, Question: tools.QuestionReply, Plan: tools.PlanReply})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := api.Close(); err != nil {
			output, returned = nil, config.startup.cleanupFailure(returned, err)
		}
	}()
	publisher.nativeVersion = api.Version()
	if _, err := api.Create(nativeCtx, input.ThreadRequestID, input.SessionID); err != nil {
		return nil, err
	}
	if input.Input.Mode == domain.PlanMode {
		if _, err := api.SelectPlan(nativeCtx, domain.NewID()); err != nil {
			return nil, err
		}
	}
	observed, err := api.SessionBinding(nativeCtx)
	if err != nil {
		return nil, err
	}
	// A retry can only acknowledge a retained outbox receipt under unchanged
	// native claims. It never repeats any native operation or observation.
	publish := func(callback context.Context, err error) error {
		if err == nil {
			return nil
		}
		if binding.ReplayPending(callback) == nil {
			logger.InfoContext(callback, "grok_execution_receipt_reconciled")
			return nil
		}
		return err
	}
	if err := publish(nativeCtx, binding.BindSession(nativeCtx, observed)); err != nil {
		return nil, err
	}
	logger.InfoContext(nativeCtx, "native_execution_thread_bound")
	var stopControl *grokStopControl
	var joinResponses func() error
	if err := config.startup.ready(nativeCtx, api.Version()); err != nil {
		return nil, err
	}
	config.startup.claimInput()
	_, err = api.RunFirstInput(nativeCtx, input.TurnRequestID, input.Input.Prompt, func(callback context.Context, value grok.InputObservation) error {
		switch value.Kind {
		case grok.InputAccepted:
			config.startup.acknowledgeInput()
			if err := publish(callback, binding.AcceptInput(callback, value)); err != nil {
				return err
			}
			logger.InfoContext(callback, "native_execution_input_accepted", "input_id", input.InputID)
			if !cancelTargeted() {
				return publicationUncertain()
			}
			stopControl = startGrokStopControl(ctx, nativeCtx, cancelNative, api, logger)
			joinResponses = startGrokControls(ctx, nativeCtx, cancelNative, config, tools, api)
			return nil
		case grok.InputText, grok.InputResponse:
			return publish(callback, binding.ObserveContent(callback, value))
		case grok.InputFileTool, grok.InputQuestion, grok.InputPlan:
			return tools.Observe(callback, value)
		case grok.InputPermissionRejected:
			return nil
		case grok.InputTitle, grok.InputCompleted, grok.StopSettled:
			// Callback copies do not prove original native closure/Stop, history
			// or workspace cleanup. Independent controller comparison follows.
			return nil
		default:
			return publicationUncertain()
		}
	})
	stopControl.join()
	if joinResponses != nil {
		if responseErr := joinResponses(); responseErr != nil {
			return nil, responseErr
		}
	}
	var completion domain.ExecutionCompletion
	if api.HasOriginalTools() {
		if err != nil && domain.SafeError(err).Code != domain.Canceled {
			return nil, err
		}
		completion, err = tools.CloseTools(nativeCtx, api)
		if err != nil {
			return nil, err
		}
	} else {
		if _, stopErr := api.InspectStop(); stopErr == nil {
			if err != nil && domain.SafeError(err).Code != domain.Canceled {
				return nil, err
			}
			if err := publish(nativeCtx, binding.PublishStopped(nativeCtx, api)); err != nil {
				return nil, err
			}
		} else {
			if err != nil {
				return nil, err
			}
			if err := publish(nativeCtx, binding.CloseText(nativeCtx, api)); err != nil {
				return nil, err
			}
		}
		completion, err = binding.TextCompletion()
		if err != nil {
			return nil, err
		}
	}
	if err := lease.Close(); err != nil {
		return nil, err
	}
	logger.InfoContext(nativeCtx, "grok_execution_workspace_closed", "last_sequence", completion.LastSequence)
	completion.CleanupVerified = lease.CleanupConfirmed()
	return json.Marshal(completion)
}
