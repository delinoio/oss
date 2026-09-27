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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func executeClaudeSession(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput, logger *slog.Logger) (output json.RawMessage, returned error) {
	if input.Version != 1 || input.Continuation != nil || input.Installation.Version != claude.SupportedVersion {
		return nil, domain.Fail(domain.Unsupported, "Claude execution requires its original verified first-input profile.", "Preserve original history; do not start a replacement input.")
	}
	permission, effort, err := claudeExecutionSettings(input.Configuration, input.Input.Mode)
	if err != nil {
		return nil, err
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.SessionID != input.SessionID || preparation.MachineID != input.MachineID || workspace.ValidateResult(preparation, manifest, runtime.GOOS) != nil || len(manifest.Repositories) > 1 {
		return nil, workspace.ResultUncertain()
	}
	executable := input.Installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.Fail(domain.RecoveryRequired, "The selected native executable identity changed.", "Refresh Worker discovery before execution; no PATH fallback is used.")
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
	binding, err := OpenClaudeBindingPublisher(publisher)
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
	if err := writeJSON(filepath.Join(manager.Root, "jobs", string(owner), "claude-registration.json"), intent); err != nil {
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
	nativeCtx, cancelNative := context.WithCancel(config.executionContext)
	defer cancelNative()
	// Until the separately correlated public Stop adapter is composed, targeted
	// cancellation contains the original process and preserves recovery. It
	// cannot synthesize a native interruption result or a successful cleanup.
	stopOnCancellation := context.AfterFunc(ctx, cancelNative)
	defer stopOnCancellation()
	api, err := claude.OpenAPISession(nativeCtx, claude.APIStreamConfig{
		Process: process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: home, Env: env, Logger: logger},
		Version: input.Installation.Version, Home: filepath.Join(home, "claude"), Workspace: lease.WorkingDirectory(), SessionID: input.SessionID, Model: input.Configuration.NativeModel,
		Permission: permission, Effort: effort, Instructions: input.Configuration.Instructions, API: claude.APIConfig{ServerOrigin: connection.Credential.Endpoint, Token: token},
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := api.Close(); err != nil {
			output, returned = nil, err
		}
	}()
	if err := binding.ClaimInput(ctx, input.TurnRequestID, input.InputID, input.Input.Prompt); err != nil {
		return nil, err
	}
	applied, err := api.SendInput(ctx, input.InputID, input.Input.Prompt, claude.ContinueSuccessfulRun)
	if err != nil {
		return nil, err
	}
	var display *ClaudeContentPublisher
	var finishControls func() error
	defer func() {
		if finishControls != nil {
			if err := finishControls(); err != nil {
				output, returned = nil, err
			}
		}
	}()
	for {
		o, err := api.Next(nativeCtx)
		if err != nil {
			return nil, err
		}
		handled := false
		switch o.Kind {
		case claude.SessionInitialized:
			err, handled = binding.BindSession(nativeCtx, o, applied), true
			if err == nil {
				logger.InfoContext(nativeCtx, "native_execution_thread_bound")
			}
		case claude.InputAccepted:
			err = binding.AcceptInput(nativeCtx, o)
			if err == nil {
				display, err = OpenClaudeContentPublisher(binding)
			}
			if err == nil {
				err = display.PublishInput(nativeCtx)
			}
			if err == nil {
				logger.InfoContext(nativeCtx, "native_execution_input_accepted", "input_id", input.InputID)
				finishControls = startClaudeControls(ctx, nativeCtx, cancelNative, config, display, api)
			}
			handled = true
		case claude.ProgressObserved:
			handled, err = binding.PublishProgressObservation(nativeCtx, o)
		case claude.ContentObserved:
			if display != nil {
				handled, err = display.PublishObservation(nativeCtx, o)
				if err == nil && len(o.Content) == 1 && o.Content[0].Usage != nil {
					_, err = display.PublishUsageObservation(nativeCtx, o)
				}
			}
		case claude.InteractionObserved:
			if display != nil {
				handled, err = display.PublishInteractionObservation(nativeCtx, o)
			}
		case claude.InputFinished:
			if display != nil {
				handled, err = display.PublishUsageObservation(nativeCtx, o)
			}
		case claude.CommandObserved:
			if o.Command == claude.CommandQueued || o.Command == claude.CommandStarted {
				handled = true
			} else if display != nil {
				handled, err = display.PublishBoundaryObservation(nativeCtx, o)
			}
		case claude.RunStateObserved:
			if o.Run != nil && (o.Run.State == claude.RunRunning || o.Run.State == claude.RunRequiresAction) {
				handled = true
			} else if display != nil {
				handled, err = display.PublishBoundaryObservation(nativeCtx, o)
				if err == nil && handled {
					if err := finishControls(); err != nil {
						return nil, err
					}
					completion, err := display.Complete(nativeCtx, api)
					if err != nil {
						return nil, err
					}
					if err := lease.Close(); err != nil {
						return nil, err
					}
					return json.Marshal(completion)
				}
			}
		case claude.CallbackInterruptResultObserved:
			if display != nil {
				handled, err = display.PublishObservation(nativeCtx, o)
			}
			if err == nil && handled {
				return nil, publicationUncertain()
			}
		}
		if err != nil {
			return nil, err
		}
		if !handled {
			logger.WarnContext(nativeCtx, "claude_execution_observation_requires_reconciliation", "kind", o.Kind, "code", domain.Unsupported)
			return nil, domain.Fail(domain.Unsupported, "This native Claude observation requires additional publication evidence.", "Retain the original runtime and outbox; do not resend the input.")
		}
	}
}
