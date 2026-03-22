package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/capture"
	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/logging"
	"github.com/delinoio/oss/cmds/derun/internal/retention"
	"github.com/delinoio/oss/cmds/derun/internal/session"
	"github.com/delinoio/oss/cmds/derun/internal/state"
	"github.com/delinoio/oss/cmds/derun/internal/transport"
)

type runRequest struct {
	sessionID         string
	retentionDuration time.Duration
	commandArgs       []string
}

type runRuntime struct {
	store  *state.Store
	logger *logging.Logger
}

type preparedRunSession struct {
	sessionID     string
	commandArgs   []string
	workingDir    string
	transportMode contracts.DerunTransportMode
	meta          session.Meta
}

type runExecutionResult struct {
	runResult transport.RunResult
	runErr    error
}

func (runtimeState *runRuntime) Close() {
	if runtimeState == nil || runtimeState.logger == nil {
		return
	}
	_ = runtimeState.logger.Close()
}

func parseRunRequest(args []string) (runRequest, int) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = printRunUsage

	request := runRequest{retentionDuration: defaultRetention}
	fs.StringVar(&request.sessionID, "session-id", "", "explicit session id")
	fs.DurationVar(&request.retentionDuration, "retention", defaultRetention, "retention duration")

	flagArgs, commandArgs, hasSeparator := splitRunArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return runRequest{}, 2
		}
		return runRequest{}, 2
	}
	if !hasSeparator {
		fmt.Fprintln(
			os.Stderr,
			formatUsageErrorWithDetails(
				"run command requires '--' separator before target command",
				"use `derun run [flags] -- <command> [args...]`",
				map[string]any{
					"arg_count":      len(args),
					"flag_arg_count": len(flagArgs),
					"has_separator":  hasSeparator,
				},
			),
		)
		return runRequest{}, 2
	}
	if len(commandArgs) == 0 {
		fmt.Fprintln(
			os.Stderr,
			formatUsageErrorWithDetails(
				"run command requires target command",
				"provide a command after `--`",
				map[string]any{
					"arg_count":         len(args),
					"command_arg_count": len(commandArgs),
				},
			),
		)
		return runRequest{}, 2
	}
	if err := validateRetentionDuration(request.retentionDuration); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return runRequest{}, 2
	}

	request.commandArgs = append([]string(nil), commandArgs...)
	return request, 0
}

func initRunRuntime(request runRequest) (*runRuntime, int) {
	stateRoot, err := resolveStateRootForRun()
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("resolve state root", err, map[string]any{
			"has_derun_state_root": os.Getenv("DERUN_STATE_ROOT") != "",
		}))
		return nil, 1
	}

	store, err := state.New(stateRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("initialize state store", err, map[string]any{
			"state_root": stateRoot,
		}))
		return nil, 1
	}
	logger, err := logging.New(stateRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("initialize logger", err, map[string]any{
			"state_root": stateRoot,
		}))
		return nil, 1
	}

	runtimeState := &runRuntime{
		store:  store,
		logger: logger,
	}

	cleanupResult, cleanupErr := retention.Sweep(store, request.retentionDuration, logger)
	if cleanupErr != nil {
		logger.Event("cleanup_result", map[string]any{"cleanup_result": "error", "error": cleanupErr.Error()})
	} else {
		logger.Event("cleanup_result", map[string]any{"cleanup_result": "ok", "checked": cleanupResult.Checked, "removed": cleanupResult.Removed})
	}

	return runtimeState, 0
}

func prepareSession(runtimeState *runRuntime, request runRequest) (*preparedRunSession, int) {
	sessionID := request.sessionID
	var err error
	if sessionID == "" {
		sessionID, err = generateUniqueSessionID(runtimeState.store, runtimeState.logger)
		if err != nil {
			fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("generate session id", err, map[string]any{
				"session_id_source": "generated",
			}))
			return nil, 1
		}
	} else {
		hasMetadata, err := runtimeState.store.HasSessionMetadata(sessionID)
		if err != nil {
			if errors.Is(err, state.ErrInvalidSessionID) {
				runtimeState.logger.Event("session_id_rejected", map[string]any{
					"session_id": sessionID,
					"reason":     string(sessionIDRejectionReasonInvalid),
				})
				fmt.Fprintln(
					os.Stderr,
					formatUsageErrorWithDetails(
						fmt.Sprintf("invalid session id %q", sessionID),
						"use a unique path-safe id (for example: 01J0S444444444444444444444)",
						map[string]any{"session_id": sessionID},
					),
				)
				return nil, 2
			}
			fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("check session metadata", err, map[string]any{
				"session_id": sessionID,
			}))
			return nil, 1
		}
		if hasMetadata {
			runtimeState.logger.Event("session_id_rejected", map[string]any{
				"session_id": sessionID,
				"reason":     string(sessionIDRejectionReasonMetadataExists),
			})
			fmt.Fprintln(
				os.Stderr,
				formatUsageErrorWithDetails(
					fmt.Sprintf("session id already exists %q", sessionID),
					"omit `--session-id` or choose a different id",
					map[string]any{"session_id": sessionID},
				),
			)
			return nil, 2
		}
	}

	if err := runtimeState.store.EnsureSessionDir(sessionID); err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("prepare session directory", err, map[string]any{
			"session_id": sessionID,
		}))
		return nil, 1
	}

	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("resolve working directory", err, map[string]any{
			"has_pwd_env": os.Getenv("PWD") != "",
		}))
		return nil, 1
	}

	ttyAttached := terminalProbe(os.Stdin) && terminalProbe(os.Stdout)
	transportMode := selectTransportMode(ttyAttached, runtimeGOOS)

	meta := session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        sessionID,
		Command:          append([]string(nil), request.commandArgs...),
		WorkingDirectory: workingDir,
		StartedAt:        time.Now().UTC(),
		RetentionSeconds: int64(request.retentionDuration.Seconds()),
		TransportMode:    transportMode,
		TTYAttached:      ttyAttached,
		PID:              0,
	}
	if err := runtimeState.store.WriteMeta(meta); err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("write session metadata", err, map[string]any{
			"session_id":     sessionID,
			"transport_mode": transportMode,
			"tty_attached":   ttyAttached,
		}))
		return nil, 1
	}

	runtimeState.logger.Event("state_transition", map[string]any{
		"session_id":       sessionID,
		"transport_mode":   transportMode,
		"tty_attached":     ttyAttached,
		"state_transition": string(contracts.DerunSessionStateStarting) + "->" + string(contracts.DerunSessionStateRunning),
	})

	return &preparedRunSession{
		sessionID:     sessionID,
		commandArgs:   append([]string(nil), request.commandArgs...),
		workingDir:    workingDir,
		transportMode: transportMode,
		meta:          meta,
	}, 0
}

func executeTransport(runtimeState *runRuntime, preparedSession *preparedRunSession) (runExecutionResult, int) {
	ctx := context.Background()
	onStart := func(pid int) error {
		if pid <= 0 {
			return nil
		}
		preparedSession.meta.PID = pid
		if err := runtimeState.store.WriteMeta(preparedSession.meta); err != nil {
			return errors.New(formatRuntimeErrorWithDetails("write session metadata file", err, map[string]any{
				"session_id": preparedSession.sessionID,
				"pid":        pid,
			}))
		}
		return nil
	}

	runPipe := func() (transport.RunResult, error) {
		stdoutCapture := capture.NewWriter(runtimeState.store, runtimeState.logger, preparedSession.sessionID, contracts.DerunOutputChannelStdout)
		stderrCapture := capture.NewWriter(runtimeState.store, runtimeState.logger, preparedSession.sessionID, contracts.DerunOutputChannelStderr)
		stdoutOutput := io.MultiWriter(os.Stdout, stdoutCapture)
		stderrOutput := io.MultiWriter(os.Stderr, stderrCapture)
		return runPipeTransport(ctx, preparedSession.commandArgs, preparedSession.workingDir, onStart, stdoutOutput, stderrOutput)
	}

	var result transport.RunResult
	var runErr error

	switch preparedSession.transportMode {
	case contracts.DerunTransportModePosixPTY:
		ptyCapture := capture.NewWriter(runtimeState.store, runtimeState.logger, preparedSession.sessionID, contracts.DerunOutputChannelPTY)
		ptyOutput := io.MultiWriter(os.Stdout, ptyCapture)
		result, runErr = runPosixPTYTransport(ctx, preparedSession.commandArgs, preparedSession.workingDir, onStart, ptyOutput)
	case contracts.DerunTransportModeWindowsConPTY:
		ptyCapture := capture.NewWriter(runtimeState.store, runtimeState.logger, preparedSession.sessionID, contracts.DerunOutputChannelPTY)
		ptyOutput := io.MultiWriter(os.Stdout, ptyCapture)
		result, runErr = runWindowsConPTYTransport(ctx, preparedSession.commandArgs, preparedSession.workingDir, onStart, ptyOutput)
		if runErr != nil && isConPTYUnavailableError(runErr) {
			runtimeState.logger.Event("transport_fallback", map[string]any{
				"session_id":      preparedSession.sessionID,
				"fallback_from":   contracts.DerunTransportModeWindowsConPTY,
				"fallback_to":     contracts.DerunTransportModePipe,
				"fallback_reason": runErr.Error(),
			})
			preparedSession.transportMode = contracts.DerunTransportModePipe
			preparedSession.meta.TransportMode = preparedSession.transportMode
			if err := runtimeState.store.WriteMeta(preparedSession.meta); err != nil {
				fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("write fallback metadata", err, map[string]any{
					"session_id":    preparedSession.sessionID,
					"fallback_from": contracts.DerunTransportModeWindowsConPTY,
					"fallback_to":   contracts.DerunTransportModePipe,
				}))
				return runExecutionResult{}, 1
			}
			result, runErr = runPipe()
		}
	default:
		result, runErr = runPipe()
	}

	return runExecutionResult{runResult: result, runErr: runErr}, 0
}

func persistFinalState(runtimeState *runRuntime, preparedSession *preparedRunSession, execution runExecutionResult) int {
	final := session.Final{
		SchemaVersion: "v1alpha1",
		SessionID:     preparedSession.sessionID,
		EndedAt:       time.Now().UTC(),
	}
	if execution.runErr != nil {
		final.State = contracts.DerunSessionStateFailed
		final.Error = execution.runErr.Error()
		runtimeState.logger.Event("state_transition", map[string]any{
			"session_id":       preparedSession.sessionID,
			"state_transition": string(contracts.DerunSessionStateRunning) + "->" + string(contracts.DerunSessionStateFailed),
		})
	} else if execution.runResult.Signal != "" {
		final.State = contracts.DerunSessionStateSignaled
		final.Signal = execution.runResult.Signal
		runtimeState.logger.Event("state_transition", map[string]any{
			"session_id":       preparedSession.sessionID,
			"signal":           execution.runResult.Signal,
			"state_transition": string(contracts.DerunSessionStateRunning) + "->" + string(contracts.DerunSessionStateSignaled),
		})
	} else {
		final.State = contracts.DerunSessionStateExited
		final.ExitCode = execution.runResult.ExitCode
		runtimeState.logger.Event("state_transition", map[string]any{
			"session_id":       preparedSession.sessionID,
			"exit_code":        derefInt(execution.runResult.ExitCode),
			"state_transition": string(contracts.DerunSessionStateRunning) + "->" + string(contracts.DerunSessionStateExited),
		})
	}

	if err := runtimeState.store.WriteFinal(final); err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("write final metadata", err, map[string]any{
			"session_id": preparedSession.sessionID,
			"state":      final.State,
		}))
		return 1
	}

	return 0
}

func resolveRunExitCode(execution runExecutionResult) int {
	if execution.runErr != nil {
		runtimeDetails := map[string]any{}
		if execution.runResult.Signal != "" {
			runtimeDetails["signal"] = execution.runResult.Signal
		}
		if execution.runResult.ExitCode != nil {
			runtimeDetails["exit_code"] = *execution.runResult.ExitCode
		}
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("execute command", execution.runErr, runtimeDetails))
		return 1
	}
	if execution.runResult.SignalNum > 0 {
		return 128 + execution.runResult.SignalNum
	}
	if execution.runResult.ExitCode != nil {
		return *execution.runResult.ExitCode
	}
	return 1
}
