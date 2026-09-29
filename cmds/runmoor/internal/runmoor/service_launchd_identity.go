package runmoor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxLaunchdArgumentBytes = 1 << 20
const maxLaunchdArgumentCount = 4096
const maxLaunchdServiceListBytes = 1 << 20

func requireLaunchdActiveIdentity(ctx context.Context, exec CommandExecutor, definition []byte) error {
	// The documented three-column table is available only when list has no
	// label argument. A label-specific query returns detailed job information.
	// Parse the bounded table and select only Runmoor's exact label row.
	output, err := exec.Run(ctx, "launchctl", []string{"list"}, minimalEnv(), nil)
	if err != nil {
		slog.Warn("launchd_service_identity_check_failed", "reason", "service_list_unavailable")
		return problem(ErrDependency, "Cannot inspect the launchd user service.", "Check the logged-in launchd GUI session and retry service stop or uninstall.")
	}
	pid, loaded, err := parseLaunchdServiceListPID(output)
	if err != nil {
		slog.Warn("launchd_service_identity_check_failed", "reason", "service_list_invalid")
		return problem(ErrDependency, "Cannot inspect the launchd user service.", "Check the logged-in launchd GUI session and retry service stop or uninstall.")
	}
	if !loaded {
		return nil
	}
	if pid == 0 {
		// launchctl list confirms that the job is loaded but exposes no process
		// whose kernel arguments could establish which cached definition launchd
		// has. Do not drain or boot out a potentially unrelated stale job.
		slog.Warn("launchd_service_identity_check_failed", "reason", "loaded_process_inactive")
		return problem(ErrDependency, "Cannot verify the loaded launchd service while it is inactive.", "Inspect the loaded job in the logged-in GUI session, reconcile it with the installed plist, then retry service stop or uninstall.")
	}
	actual, err := launchdProcessCommandLine(pid)
	if err != nil {
		slog.Warn("launchd_service_identity_check_failed", "reason", "active_process_unreadable")
		return problem(ErrDependency, "Cannot verify the active launchd service.", "Keep the service definition unchanged and retry from the logged-in service user's session.")
	}
	expected, err := launchdServiceArguments(definition)
	if err != nil {
		return problem(ErrConfig, "The installed launchd service definition is invalid.", "Restore a valid Runmoor service definition before retrying.")
	}
	if !sameServiceArguments(actual, expected) {
		slog.Warn("launchd_service_identity_mismatch", "service", serviceLabel)
		return problem(ErrConfig, "The active launchd service does not match its installed definition.", "Gracefully stop the active manager with `runmoor stop --config <original-path>`, then retry the service action.")
	}
	return nil
}

func parseLaunchdServiceListPID(output []byte) (pid int, loaded bool, err error) {
	if len(output) > maxLaunchdServiceListBytes || !utf8.Valid(output) || bytes.IndexByte(output, 0) >= 0 {
		return 0, false, errInvalidServiceDefinition
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 1024), 1024)
	found := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) == 3 && fields[0] == "PID" && fields[1] == "Status" && fields[2] == "Label" {
			continue
		}
		if len(fields) != 3 {
			return 0, false, errInvalidServiceDefinition
		}
		servicePID := 0
		if fields[0] != "-" {
			servicePID, err = strconv.Atoi(fields[0])
			if err != nil || servicePID <= 0 {
				return 0, false, errInvalidServiceDefinition
			}
		}
		if _, err := strconv.Atoi(fields[1]); err != nil {
			return 0, false, errInvalidServiceDefinition
		}
		if fields[2] == serviceLabel {
			if found {
				return 0, false, errInvalidServiceDefinition
			}
			found = true
			pid = servicePID
		}
	}
	if scanner.Err() != nil {
		return 0, false, errInvalidServiceDefinition
	}
	return pid, found, nil
}

func parseLaunchdProcessArguments(data []byte) ([]string, error) {
	if len(data) < 6 || len(data) > maxLaunchdArgumentBytes {
		return nil, errInvalidServiceDefinition
	}
	argc := binary.NativeEndian.Uint32(data[:4])
	if argc == 0 || argc > maxLaunchdArgumentCount {
		return nil, errInvalidServiceDefinition
	}
	firstEnd := bytes.IndexByte(data[4:], 0)
	if firstEnd <= 0 {
		return nil, errInvalidServiceDefinition
	}
	index := 4 + firstEnd + 1
	// KERN_PROCARGS2 aligns the executable-path string with extra NUL bytes.
	for index < len(data) && data[index] == 0 {
		index++
	}
	args := make([]string, 0, int(argc))
	for range argc {
		if index >= len(data) {
			return nil, errInvalidServiceDefinition
		}
		next := bytes.IndexByte(data[index:], 0)
		if next < 0 {
			return nil, errInvalidServiceDefinition
		}
		args = append(args, string(data[index:index+next]))
		index += next + 1
	}
	return args, nil
}

func sameServiceArguments(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
