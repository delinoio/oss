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
	if input.Version != 1 || input.Continuation != nil || input.Installation.Version != grok.SupportedVersion {
		return nil, domain.Fail(domain.Unsupported, "Grok execution requires the original first-text profile.", "Retain existing native history; a replacement input or continuation is not authorized.")
	}
	contextTokens, err := input.Configuration.GrokFirstTextContext(input.Input.Mode)
	if err != nil {
		return nil, err
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.SessionID != input.SessionID || preparation.MachineID != input.MachineID || workspace.ValidateResult(preparation, manifest, runtime.GOOS) != nil {
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
	lease, err := manager.ClaimFirstExecution(ctx, owner, input.ExecutionID, preparation, manifest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := lease.Close(); err != nil {
			output, returned = nil, err
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
			output, returned = nil, err
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
	nativeConfig := grok.APIExecutionConfig{Probe: grok.ProbeConfig{Process: process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: home, Env: env, Logger: logger}, Version: input.Installation.Version, Home: filepath.Join(home, "grok")}, Workspace: lease.WorkingDirectory(), Model: input.Configuration.NativeModel, ContextTokens: contextTokens, Mode: input.Input.Mode, ServerOrigin: connection.Credential.Endpoint, Token: token}
	api, err := grok.OpenOwnedAPIWithStop(nativeCtx, nativeConfig, binding.Creation, binding.Input, binding.Closure, binding.Stop)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := api.Close(); err != nil {
			output, returned = nil, err
		}
	}()
	if _, err := api.Create(nativeCtx, input.ThreadRequestID, input.SessionID); err != nil {
		return nil, err
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
	_, err = api.RunText(nativeCtx, input.TurnRequestID, input.Input.Prompt, func(callback context.Context, value grok.InputObservation) error {
		switch value.Kind {
		case grok.InputAccepted:
			if err := publish(callback, binding.AcceptInput(callback, value)); err != nil {
				return err
			}
			logger.InfoContext(callback, "native_execution_input_accepted", "input_id", input.InputID)
			if !cancelTargeted() {
				return publicationUncertain()
			}
			stopControl = startGrokStopControl(ctx, nativeCtx, cancelNative, api, logger)
			return nil
		case grok.InputText, grok.InputResponse:
			return publish(callback, binding.ObserveContent(callback, value))
		case grok.InputTitle, grok.InputCompleted, grok.StopSettled:
			// Callback copies do not prove original native closure/Stop, history
			// or workspace cleanup. Independent controller comparison follows.
			return nil
		default:
			return publicationUncertain()
		}
	})
	stopControl.join()
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
	completion, err := binding.TextCompletion()
	if err != nil {
		return nil, err
	}
	if err := lease.Close(); err != nil {
		return nil, err
	}
	logger.InfoContext(nativeCtx, "grok_execution_workspace_closed", "last_sequence", completion.LastSequence)
	return json.Marshal(completion)
}
