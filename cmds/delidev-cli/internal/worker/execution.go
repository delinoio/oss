package worker

import (
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
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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
		return nil, err
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	logger = logger.With("job_id", owner, "execution_id", input.ExecutionID, "session_id", input.SessionID, "harness", input.Configuration.Harness)
	logger.InfoContext(ctx, "native_execution_started")
	defer func() {
		if returned != nil {
			logger.WarnContext(ctx, "native_execution_requires_reconciliation", "code", domain.SafeError(returned).Code)
		} else {
			logger.InfoContext(ctx, "native_execution_cleanup_verified")
		}
	}()
	if input.Configuration.Harness != domain.Codex || input.Installation.Version != codex.SupportedVersion {
		return nil, domain.Fail(domain.Unsupported, "This native execution profile is not implemented.", "Select a verified installed Codex profile; no fallback harness is used.")
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.SessionID != input.SessionID || preparation.MachineID != input.MachineID || workspace.ValidateResult(preparation, manifest, runtime.GOOS) != nil {
		return nil, workspace.ResultUncertain()
	}
	if len(preparation.Repositories) > 1 {
		return nil, domain.Fail(domain.Unsupported, "Multi-repository native permission roots require their execution adapter.", "Keep the prepared workspaces; do not broaden native permissions implicitly.")
	}
	executable := input.Installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.Fail(domain.RecoveryRequired, "The selected native executable identity changed.", "Refresh Worker discovery before another execution; no PATH fallback is used.")
	}
	manager := &workspace.Manager{Root: config.Root, Logger: config.Logger}
	lease, err := manager.ClaimFirstExecution(ctx, owner, input.ExecutionID, preparation, manifest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := lease.Close(); err != nil {
			output, returned = nil, err
		}
	}()
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
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	settings := codex.ThreadSettings{Model: input.Configuration.NativeModel, Provider: codex.APIProvider, Effort: input.Configuration.Effort, Cwd: lease.WorkingDirectory(), Instructions: input.Configuration.Instructions, Options: input.Configuration.Options}
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
	if registered == nil || registered.Msg == nil || registered.Msg.ProxyPath != "/api-proxy/v1" {
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
	client, err := codex.Open(nativeCtx, codex.Config{Mode: codex.ThreadProtocol, Version: input.Installation.Version, Home: filepath.Join(home, "codex"), API: &codex.APIConfig{ServerOrigin: connection.Credential.Endpoint, Token: token}, Process: process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: settings.Cwd, Env: env, Logger: config.Logger}})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := client.Close(); err != nil {
			output, returned = nil, domain.SafeError(err)
		}
	}()
	mapper := NewCodexEventPublisher(publisher)
	bound, err := client.StartThread(ctx, input.ThreadRequestID, settings)
	if err != nil {
		return nil, err
	}
	if err := mapper.BindThread(ctx, bound); err != nil {
		return nil, err
	}
	logger.InfoContext(ctx, "native_execution_thread_bound")
	turn, err := client.StartTurn(ctx, input.TurnRequestID, input.InputID, input.Input)
	if err != nil {
		return nil, err
	}
	if err := mapper.AcceptInput(ctx, turn); err != nil {
		return nil, err
	}
	if !cancelBeforeAcceptance() {
		return nil, domain.SafeError(context.Canceled)
	}
	logger.InfoContext(ctx, "native_execution_input_accepted", "input_id", input.InputID)
	readContext, publicationContext := ctx, nativeCtx
	stopping := false
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
		handled, err := mapper.PublishCore(publicationContext, event)
		if err != nil {
			return nil, err
		}
		if !handled {
			return nil, domain.Fail(domain.Unsupported, "The native execution produced an unsupported event family.", "Retain its native history for the required typed adapter; input is never replayed automatically.")
		}
		if event.Kind != codex.TurnCompletedEvent {
			continue
		}
		// A terminal event is not cleanup. Close/join the native scope, prove
		// the workspace lease's process index, then form a completion result.
		if err := client.Close(); err != nil {
			return nil, err
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
		completion := domain.ExecutionCompletion{Version: 1, ExecutionID: input.ExecutionID, InputID: input.InputID, NativeThreadID: bound.Thread.ID, NativeTurnID: turn.TurnID, LastSequence: sequence, Outcome: outcome, CleanupVerified: true}
		if err := completion.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(completion)
	}
}
