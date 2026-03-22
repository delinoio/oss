package cli

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
	"github.com/delinoio/oss/cmds/derun/internal/logging"
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

var (
	runPipeTransport          = transport.RunPipe
	runPosixPTYTransport      = transport.RunPosixPTY
	runWindowsConPTYTransport = transport.RunWindowsConPTY
	isConPTYUnavailableError  = transport.IsConPTYUnavailableError
	terminalProbe             = isTerminal
	runtimeGOOS               = runtime.GOOS
)

func ExecuteRun(args []string) int {
	request, exitCode := parseRunRequest(args)
	if exitCode != 0 {
		return exitCode
	}

	runtimeState, exitCode := initRunRuntime(request)
	if exitCode != 0 {
		return exitCode
	}
	defer runtimeState.Close()

	preparedSession, exitCode := prepareSession(runtimeState, request)
	if exitCode != 0 {
		return exitCode
	}

	execution, exitCode := executeTransport(runtimeState, preparedSession)
	if exitCode != 0 {
		return exitCode
	}

	if exitCode := persistFinalState(runtimeState, preparedSession, execution); exitCode != 0 {
		return exitCode
	}

	return resolveRunExitCode(execution)
}

func splitRunArgs(args []string) ([]string, []string, bool) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
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
		return errors.New(formatUsageErrorWithDetails(
			"retention must be positive",
			"use values like 1s, 5m, or 24h",
			map[string]any{
				"retention":    retentionDuration.String(),
				"retention_ms": retentionDuration.Milliseconds(),
			},
		))
	}
	if retentionDuration%time.Second != 0 {
		return errors.New(formatUsageErrorWithDetails(
			"retention must use whole-second precision",
			"use values like 1s, 30s, or 5m",
			map[string]any{
				"retention":    retentionDuration.String(),
				"retention_ms": retentionDuration.Milliseconds(),
			},
		))
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
	const maxAttempts = 5
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		sessionID, err := session.NewULID(time.Now().UTC())
		if err != nil {
			return "", errors.New(formatRuntimeErrorWithDetails("generate candidate session id", err, map[string]any{
				"attempt": attempt,
			}))
		}
		hasMetadata, err := store.HasSessionMetadata(sessionID)
		if err != nil {
			return "", errors.New(formatRuntimeErrorWithDetails("check candidate session metadata", err, map[string]any{
				"attempt":    attempt,
				"session_id": sessionID,
			}))
		}
		if !hasMetadata {
			return sessionID, nil
		}
		logger.Event("session_id_collision", map[string]any{
			"session_id": sessionID,
			"attempt":    attempt,
		})
	}
	return "", errmsg.Error("too many session id collisions", map[string]any{
		"attempt_limit": maxAttempts,
	})
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
