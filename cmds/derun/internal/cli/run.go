package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/capture"
	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/logging"
	"github.com/delinoio/oss/cmds/derun/internal/retention"
	"github.com/delinoio/oss/cmds/derun/internal/session"
	"github.com/delinoio/oss/cmds/derun/internal/state"
	"github.com/delinoio/oss/cmds/derun/internal/transport"
)

const defaultRetention = 24 * time.Hour

type sessionIDRejectionReason string

const (
	sessionIDRejectionReasonMetadataExists sessionIDRejectionReason = "metadata_exists"
	sessionIDRejectionReasonInvalid        sessionIDRejectionReason = "invalid_session_id"
)

func ExecuteRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var sessionID string
	retentionDuration := defaultRetention
	fs.StringVar(&sessionID, "session-id", "", "explicit session id")
	fs.DurationVar(&retentionDuration, "retention", defaultRetention, "retention duration")

	flagArgs, commandArgs := splitRunArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(commandArgs) == 0 {
		commandArgs = fs.Args()
	}
	if len(commandArgs) == 0 {
		fmt.Fprintln(os.Stderr, "run command requires target command")
		return 2
	}
	if err := validateRetentionDuration(retentionDuration); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	stateRoot, err := resolveStateRootForRun()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve state root: %v\n", err)
		return 1
	}

	store, err := state.New(stateRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init state store: %v\n", err)
		return 1
	}
	logger, err := logging.New(stateRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		return 1
	}
	defer logger.Close()

	cleanupResult, cleanupErr := retention.Sweep(store, retentionDuration, logger)
	if cleanupErr != nil {
		logger.Event("cleanup_result", map[string]any{"cleanup_result": "error", "error": cleanupErr.Error()})
	} else {
		logger.Event("cleanup_result", map[string]any{"cleanup_result": "ok", "checked": cleanupResult.Checked, "removed": cleanupResult.Removed})
	}

	if sessionID == "" {
		sessionID, err = generateUniqueSessionID(store, logger)
		if err != nil {
			fmt.Fprintf(os.Stderr, "generate session id: %v\n", err)
			return 1
		}
	} else {
		hasMetadata, err := store.HasSessionMetadata(sessionID)
		if err != nil {
			if errors.Is(err, state.ErrInvalidSessionID) {
				logger.Event("session_id_rejected", map[string]any{
					"session_id": sessionID,
					"reason":     string(sessionIDRejectionReasonInvalid),
				})
				fmt.Fprintf(os.Stderr, "invalid session id: %s\n", sessionID)
				return 2
			}
			fmt.Fprintf(os.Stderr, "check session metadata: %v\n", err)
			return 1
		}
		if hasMetadata {
			logger.Event("session_id_rejected", map[string]any{
				"session_id": sessionID,
				"reason":     string(sessionIDRejectionReasonMetadataExists),
			})
			fmt.Fprintf(os.Stderr, "session id already exists: %s\n", sessionID)
			return 2
		}
	}

	if err := store.EnsureSessionDir(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "prepare session directory: %v\n", err)
		return 1
	}

	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve working directory: %v\n", err)
		return 1
	}

	ttyAttached := isTerminal(os.Stdin) && isTerminal(os.Stdout)
	transportMode := selectTransportMode(ttyAttached, runtime.GOOS)

	startedAt := time.Now().UTC()
	meta := session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        sessionID,
		Command:          append([]string(nil), commandArgs...),
		WorkingDirectory: workingDir,
		StartedAt:        startedAt,
		RetentionSeconds: int64(retentionDuration.Seconds()),
		TransportMode:    transportMode,
		TTYAttached:      ttyAttached,
		PID:              0,
	}
	if err := store.WriteMeta(meta); err != nil {
		fmt.Fprintf(os.Stderr, "write metadata: %v\n", err)
		return 1
	}

	logger.Event("state_transition", map[string]any{
		"session_id":       sessionID,
		"transport_mode":   transportMode,
		"tty_attached":     ttyAttached,
		"state_transition": string(contracts.DerunSessionStateStarting) + "->" + string(contracts.DerunSessionStateRunning),
	})

	ctx := context.Background()
	onStart := func(pid int) error {
		if pid <= 0 {
			return nil
		}
		meta.PID = pid
		if err := store.WriteMeta(meta); err != nil {
			return fmt.Errorf("write meta file: %w", err)
		}
		return nil
	}

	var runResult transport.RunResult
	var runErr error

	switch transportMode {
	case contracts.DerunTransportModePosixPTY:
		ptyCapture := capture.NewWriter(store, logger, sessionID, contracts.DerunOutputChannelPTY)
		ptyOutput := io.MultiWriter(os.Stdout, ptyCapture)
		runResult, runErr = transport.RunPosixPTY(ctx, commandArgs, workingDir, onStart, ptyOutput)
	case contracts.DerunTransportModeWindowsConPTY:
		ptyCapture := capture.NewWriter(store, logger, sessionID, contracts.DerunOutputChannelPTY)
		ptyOutput := io.MultiWriter(os.Stdout, ptyCapture)
		runResult, runErr = transport.RunWindowsConPTY(ctx, commandArgs, workingDir, onStart, ptyOutput)
	default:
		stdoutCapture := capture.NewWriter(store, logger, sessionID, contracts.DerunOutputChannelStdout)
		stderrCapture := capture.NewWriter(store, logger, sessionID, contracts.DerunOutputChannelStderr)
		stdoutOutput := io.MultiWriter(os.Stdout, stdoutCapture)
		stderrOutput := io.MultiWriter(os.Stderr, stderrCapture)
		runResult, runErr = transport.RunPipe(ctx, commandArgs, workingDir, onStart, stdoutOutput, stderrOutput)
	}

	final := session.Final{
		SchemaVersion: "v1alpha1",
		SessionID:     sessionID,
		EndedAt:       time.Now().UTC(),
	}
	if runErr != nil {
		final.State = contracts.DerunSessionStateFailed
		final.Error = runErr.Error()
		logger.Event("state_transition", map[string]any{
			"session_id":       sessionID,
			"state_transition": string(contracts.DerunSessionStateRunning) + "->" + string(contracts.DerunSessionStateFailed),
		})
	} else if runResult.Signal != "" {
		final.State = contracts.DerunSessionStateSignaled
		final.Signal = runResult.Signal
		logger.Event("state_transition", map[string]any{
			"session_id":       sessionID,
			"signal":           runResult.Signal,
			"state_transition": string(contracts.DerunSessionStateRunning) + "->" + string(contracts.DerunSessionStateSignaled),
		})
	} else {
		final.State = contracts.DerunSessionStateExited
		final.ExitCode = runResult.ExitCode
		logger.Event("state_transition", map[string]any{
			"session_id":       sessionID,
			"exit_code":        derefInt(runResult.ExitCode),
			"state_transition": string(contracts.DerunSessionStateRunning) + "->" + string(contracts.DerunSessionStateExited),
		})
	}

	if err := store.WriteFinal(final); err != nil {
		fmt.Fprintf(os.Stderr, "write final metadata: %v\n", err)
		return 1
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "run command: %v\n", runErr)
		return 1
	}
	if runResult.SignalNum > 0 {
		return 128 + runResult.SignalNum
	}
	if runResult.ExitCode != nil {
		return *runResult.ExitCode
	}
	return 1
}

func splitRunArgs(args []string) ([]string, []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func selectTransportMode(ttyAttached bool, goos string) contracts.DerunTransportMode {
	if !ttyAttached {
		return contracts.DerunTransportModePipe
	}
	if goos == "windows" {
		return contracts.DerunTransportModeWindowsConPTY
	}
	return contracts.DerunTransportModePosixPTY
}

func validateRetentionDuration(retentionDuration time.Duration) error {
	if retentionDuration <= 0 {
		return errors.New("retention must be positive")
	}
	if retentionDuration%time.Second != 0 {
		return errors.New("retention must be a whole number of seconds (for example: 1s, 30s, 5m)")
	}
	return nil
}

func resolveStateRootForRun() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("DERUN_STATE_ROOT")); explicit != "" {
		return explicit, nil
	}
	return state.ResolveStateRoot()
}

func generateUniqueSessionID(store *state.Store, logger *logging.Logger) (string, error) {
	for attempt := 1; attempt <= 5; attempt++ {
		sessionID, err := session.NewULID(time.Now().UTC())
		if err != nil {
			return "", err
		}
		hasMetadata, err := store.HasSessionMetadata(sessionID)
		if err != nil {
			return "", err
		}
		if !hasMetadata {
			return sessionID, nil
		}
		logger.Event("session_id_collision", map[string]any{
			"session_id": sessionID,
			"attempt":    attempt,
		})
	}
	return "", fmt.Errorf("too many session id collisions")
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
