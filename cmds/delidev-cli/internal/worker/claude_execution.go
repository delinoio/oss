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
	if input.Validate() != nil || input.Installation.Version != claude.SupportedVersion {
		return nil, domain.Fail(domain.Unsupported, "Claude execution requires its original verified input and history profile.", "Preserve original history; do not start a replacement input.")
	}
	permission, effort, err := claudeExecutionSettings(input.Configuration, input.Input.Mode)
	if err != nil {
		return nil, err
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.SessionID != input.SessionID || preparation.MachineID != input.MachineID || workspace.ValidateResult(preparation, manifest, runtime.GOOS) != nil {
		return nil, workspace.ResultUncertain()
	}
	executable := input.Installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.Fail(domain.RecoveryRequired, "The selected native executable identity changed.", "Refresh Worker discovery before execution; no PATH fallback is used.")
	}
	manager := &workspace.Manager{Root: config.Root, Logger: logger}
	var lease *workspace.ExecutionLease
	if c := input.Continuation; c != nil {
		lease, err = manager.ClaimContinuation(ctx, owner, input.ExecutionID, workspace.ExecutionPredecessor{JobID: c.Previous.JobID, ExecutionID: c.Previous.ExecutionID}, preparation, manifest)
	} else {
		lease, err = manager.ClaimFirstExecution(ctx, owner, input.ExecutionID, preparation, manifest)
	}
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
	registrationIntent := struct {
		Version     uint32    `json:"version"`
		JobID       domain.ID `json:"job_id"`
		ExecutionID domain.ID `json:"execution_id"`
		RequestID   domain.ID `json:"request_id"`
		TokenDigest string    `json:"token_digest"`
	}{1, owner, input.ExecutionID, registration, hex.EncodeToString(digest[:])}
	if err := writeJSON(filepath.Join(manager.Root, "jobs", string(owner), "claude-registration.json"), registrationIntent); err != nil {
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
	// Targeted cancellation contains startup until acceptance is durable. The
	// accepted original controller then owns a bounded native interrupt grace.
	stopOnCancellation := context.AfterFunc(ctx, cancelNative)
	defer stopOnCancellation()
	nativeConfig := claude.APIStreamConfig{
		Process: process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: home, Env: env, Logger: logger},
		Version: input.Installation.Version, Home: filepath.Join(home, "claude"), Workspace: lease.WorkingDirectory(), WorkspaceRoots: nativeWorkspaceRoots(manifest), SessionID: input.SessionID, Model: input.Configuration.NativeModel,
		Permission: permission, Effort: effort, Instructions: input.Configuration.Instructions, API: claude.APIConfig{ServerOrigin: connection.Credential.Endpoint, Token: token},
	}
	var api *claude.APISession
	intent := claude.ContinueSuccessfulRun
	if input.Continuation != nil {
		// Restore derives original paths from the immutable history identity.
		// It retains only bounded lookup environment from this fresh runtime,
		// never its paths or credentials, before reconstructing native settings.
		closed, restoreErr := readClaudeContinuation(ctx, manager.Root, connection.Credential, input, manifest, nativeConfig)
		if restoreErr != nil {
			logger.WarnContext(ctx, "claude_continuation_checkpoint_failed", "execution_id", input.ExecutionID, "code", domain.SafeError(restoreErr).Code)
			return nil, restoreErr
		}
		logger.InfoContext(ctx, "claude_continuation_checkpoint_verified", "execution_id", input.ExecutionID, "previous_execution_id", input.Continuation.Previous.ExecutionID)
		if input.Continuation.Intent == domain.ContinueExplicitly {
			intent = claude.ResumeTerminalRun
		}
		api, err = claude.ContinueAPISession(nativeCtx, closed, owner, nativeConfig.API, intent)
	} else {
		api, err = claude.OpenAPISession(nativeCtx, nativeConfig)
	}
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
	applied, err := api.SendInput(ctx, input.InputID, input.Input.Prompt, intent)
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
	readContext, publicationContext := ctx, nativeCtx
	stopping := false
	for {
		if ctx.Err() != nil && !stopping {
			if nativeCtx.Err() != nil || display == nil {
				return nil, publicationUncertain()
			}
			if err := finishControls(); err != nil {
				return nil, err
			}
			stopping = true
			grace, cancel := context.WithTimeout(nativeCtx, 15*time.Second)
			defer cancel()
			readContext, publicationContext = grace, grace
			request := domain.NewID()
			logger.InfoContext(grace, "native_execution_interruption_requested", "request_id", request)
			if err := display.RequestStop(grace, api, request); err != nil {
				return nil, err
			}
		}
		o, err := api.Next(readContext)
		if err != nil {
			if !stopping && ctx.Err() != nil && nativeCtx.Err() == nil {
				continue
			}
			return nil, err
		}
		if stopping {
			handled, err := display.ObserveStop(o)
			if err != nil {
				return nil, err
			}
			if handled {
				if o.Run != nil && o.Run.State == claude.RunIdle {
					completion, err := display.PublishStopped(publicationContext, api)
					if err != nil {
						return nil, err
					}
					if err := lease.Close(); err != nil {
						return nil, err
					}
					return json.Marshal(completion)
				}
				continue
			}
		}
		handled := false
		switch o.Kind {
		case claude.SessionInitialized:
			err, handled = binding.BindSession(publicationContext, o, applied), true
			if err == nil {
				logger.InfoContext(publicationContext, "native_execution_thread_bound")
			}
		case claude.InputAccepted:
			err = binding.AcceptInput(publicationContext, o)
			if err == nil {
				display, err = OpenClaudeContentPublisher(binding)
			}
			if err == nil {
				err = display.PublishInput(publicationContext)
			}
			if err == nil {
				if !stopOnCancellation() {
					return nil, publicationUncertain()
				}
				logger.InfoContext(publicationContext, "native_execution_input_accepted", "input_id", input.InputID)
				finishControls = startClaudeControls(ctx, nativeCtx, cancelNative, config, display, api)
			}
			handled = true
		case claude.TaskObserved:
			if display != nil {
				handled, err = display.PublishTaskObservation(publicationContext, o)
			}
		case claude.CompactionObserved, claude.CompactionSummaryObserved:
			handled, err = binding.PublishCompactionObservation(publicationContext, o)
		case claude.ProgressObserved:
			handled, err = binding.PublishProgressObservation(publicationContext, o)
			if !handled && err == nil && display != nil {
				handled, err = display.PublishToolProgressObservation(publicationContext, o)
			}
		case claude.ContentObserved:
			if display != nil {
				handled, err = display.PublishObservation(publicationContext, o)
				if err == nil && len(o.Content) == 1 && o.Content[0].Usage != nil {
					_, err = display.PublishUsageObservation(publicationContext, o)
				}
			}
		case claude.InteractionObserved:
			if display != nil {
				handled, err = display.PublishInteractionObservation(publicationContext, o)
			}
		case claude.InputFinished:
			if display != nil {
				handled, err = display.PublishUsageObservation(publicationContext, o)
			}
		case claude.CommandObserved:
			if o.Command == claude.CommandQueued || o.Command == claude.CommandStarted {
				handled = true
			} else if display != nil {
				handled, err = display.PublishBoundaryObservation(publicationContext, o)
			}
		case claude.RunStateObserved:
			if o.Run != nil && (o.Run.State == claude.RunRunning || o.Run.State == claude.RunRequiresAction) {
				handled = true
			} else if display != nil {
				err = display.PublishChildHistory(publicationContext, nativeConfig)
				if err == nil {
					handled, err = display.PublishBoundaryObservation(publicationContext, o)
				}
			}
		case claude.CallbackInterruptResultObserved:
			if display != nil {
				handled, err = display.PublishObservation(publicationContext, o)
			}
		}
		if err != nil {
			// Native bodies can contain commands, answers and private paths.
			// Report only the adapter's closed observation classifications.
			var contentKind claude.ContentEventKind
			if len(o.Content) != 0 {
				contentKind = o.Content[0].Kind
			}
			logger.WarnContext(publicationContext, "claude_execution_publication_rejected", "kind", o.Kind, "content_kind", contentKind, "code", domain.RecoveryRequired)
			return nil, err
		}
		if !handled {
			logger.WarnContext(publicationContext, "claude_execution_observation_requires_reconciliation", "kind", o.Kind, "code", domain.Unsupported)
			return nil, domain.Fail(domain.Unsupported, "This native Claude observation requires additional publication evidence.", "Retain the original runtime and outbox; do not resend the input.")
		}
		if display != nil && display.pendingBoundaryReady() {
			if err := display.PublishChildHistory(publicationContext, nativeConfig); err != nil {
				return nil, err
			}
			if _, err := display.PublishPendingBoundary(publicationContext); err != nil {
				return nil, err
			}
		}
		if display != nil && display.terminalPublished() {
			if err := finishControls(); err != nil {
				return nil, err
			}
			completion, err := display.Complete(publicationContext, api)
			if err != nil {
				return nil, err
			}
			if err := lease.Close(); err != nil {
				return nil, err
			}
			completion, err = display.RetainCompletion(publicationContext, api, completion)
			if err != nil {
				return nil, err
			}
			return json.Marshal(completion)
		}
	}
}
