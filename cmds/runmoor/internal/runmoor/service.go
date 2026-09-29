package runmoor

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const serviceLabel = "io.delino.runmoor"
const systemdServiceName = "runmoor.service"

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(s) + `"`
}
func serviceDefinition(goos, binary, config string) (string, error) {
	if strings.ContainsAny(binary+config, "\x00\n\r") {
		return "", problem(ErrConfig, "Service paths cannot contain control characters.", "Use clean absolute executable and configuration paths.")
	}
	switch goos {
	case "darwin":
		return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + serviceLabel + `</string>
<key>ProgramArguments</key><array><string>` + xmlText(binary) + `</string><string>run</string><string>--config</string><string>` + xmlText(config) + `</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>AbandonProcessGroup</key><true/>
<key>ProcessType</key><string>Background</string>
<key>Umask</key><integer>63</integer>
</dict></plist>
`, nil
	case "linux":
		return `[Unit]
Description=Runmoor ephemeral GitHub Actions runner manager
After=network-online.target

[Service]
Type=simple
ExecStart=` + systemdQuote(binary) + ` run --config ` + systemdQuote(config) + `
ExecStop=` + systemdQuote(binary) + ` stop --config ` + systemdQuote(config) + `
TimeoutStopSec=infinity
KillMode=process
Restart=on-failure
RestartSec=5
UMask=0077

[Install]
WantedBy=default.target
`, nil
	default:
		return "", problem(ErrPlatform, "User services are unsupported on this platform.", "Use launchd on macOS or systemd on Ubuntu.")
	}
}
func servicePath() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", systemdServiceName)
}

func unloadLaunchd(ctx context.Context, domain, unit string, exec CommandExecutor) error {
	if _, err := exec.Run(ctx, "launchctl", []string{"bootout", domain, unit}, minimalEnv(), nil); err == nil {
		return nil
	}
	// A failed bootout is not proof that launchd forgot the definition. Probe
	// the exact service separately, and accept only launchctl's service-not-found
	// exit status (113), with its GUI domain still reachable. Never parse stderr.
	_, err := exec.Run(ctx, "launchctl", []string{"print", domain + "/" + serviceLabel}, minimalEnv(), nil)
	var status interface{ ExitCode() int }
	if errors.As(err, &status) && status.ExitCode() == 113 {
		if _, err := exec.Run(ctx, "launchctl", []string{"print", domain}, minimalEnv(), nil); err == nil {
			return nil
		}
	}
	return problem(ErrDependency, "Cannot confirm that launchd unloaded the Runmoor service.", "Check the logged-in launchd user session and retry service stop or uninstall; the service definition has been preserved.")
}

func serviceCommandEnv(goos, command string) []string {
	env := minimalEnv()
	if goos != "linux" || command != "systemctl" {
		return env
	}
	// OSCommand replaces the inherited process environment with the supplied
	// allowlist, while systemctl --user locates the logged-in manager through
	// these selectors. Keep this exception limited to Linux systemctl; remove it
	// only if service commands preserve session lookup through another tested
	// mechanism. Do not inherit arbitrary variables or invent a session address.
	for _, key := range []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if value, present := os.LookupEnv(key); present {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func systemdMainPID(ctx context.Context, exec CommandExecutor) (int, error) {
	output, err := exec.Run(ctx, "systemctl", []string{"--user", "show", "--property=MainPID", "--value", systemdServiceName}, serviceCommandEnv("linux", "systemctl"), nil)
	if err != nil {
		slog.Warn("systemd_service_identity_check_failed", "reason", "main_pid_unavailable")
		return 0, problem(ErrDependency, "Cannot inspect the systemd user service.", "Check the logged-in systemd user session and retry service start.")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid < 0 {
		slog.Warn("systemd_service_identity_check_failed", "reason", "main_pid_invalid")
		return 0, problem(ErrDependency, "Cannot inspect the systemd user service.", "Check the logged-in systemd user session and retry service start.")
	}
	return pid, nil
}

func systemdProcessCommandLine(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, errInvalidServiceDefinition
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, errInvalidServiceDefinition
	}
	return parseSystemdProcessCommandLine(data)
}

func parseSystemdProcessCommandLine(data []byte) ([]string, error) {
	if len(data) < 2 || data[len(data)-1] != 0 {
		return nil, errInvalidServiceDefinition
	}
	parts := bytes.Split(data[:len(data)-1], []byte{0})
	args := make([]string, len(parts))
	for i, part := range parts {
		if len(part) == 0 {
			return nil, errInvalidServiceDefinition
		}
		args[i] = string(part)
	}
	return args, nil
}

func requireSystemdActiveIdentity(ctx context.Context, exec CommandExecutor, definition []byte) error {
	pid, err := systemdMainPID(ctx, exec)
	if err != nil || pid == 0 {
		return err
	}
	actual, err := systemdProcessCommandLine(pid)
	if err != nil {
		slog.Warn("systemd_service_identity_check_failed", "reason", "active_process_unreadable")
		return problem(ErrDependency, "Cannot verify the active systemd service.", "Keep the service definition unchanged and retry from the logged-in service user's session.")
	}
	expected, err := systemdServiceInvocation(definition)
	if err != nil {
		return problem(ErrConfig, "The installed systemd service definition is invalid.", "Restore a valid Runmoor service definition before retrying.")
	}
	if len(actual) != len(expected) {
		slog.Warn("systemd_service_identity_mismatch", "service", systemdServiceName)
		return problem(ErrConfig, "The active systemd service does not match its installed definition.", "Gracefully stop the active manager with `runmoor stop --config <original-path>`, then retry service start.")
	}
	for i := range expected {
		if actual[i] != expected[i] {
			slog.Warn("systemd_service_identity_mismatch", "service", systemdServiceName)
			return problem(ErrConfig, "The active systemd service does not match its installed definition.", "Gracefully stop the active manager with `runmoor stop --config <original-path>`, then retry service start.")
		}
	}
	return nil
}

func Service(ctx context.Context, action, path string, c Config, exec CommandExecutor) error {
	unit := servicePath()
	uid := strconv.Itoa(os.Getuid())
	domain := "gui/" + uid
	var definitionSnapshot *serviceDefinitionSnapshot
	run := func(name string, args ...string) error {
		_, e := exec.Run(ctx, name, args, serviceCommandEnv(runtime.GOOS, name), nil)
		if e != nil {
			return problem(ErrDependency, "User service command failed.", "Check the logged-in launchd or systemd user session and run doctor.")
		}
		return nil
	}
	switch action {
	case "install":
		binary, e := os.Executable()
		if e != nil {
			return e
		}
		binary, e = filepath.Abs(binary)
		if e != nil {
			return e
		}
		text, e := serviceDefinition(runtime.GOOS, binary, path)
		if e != nil {
			return e
		}
		// Service definitions contain references only. Environment credentials
		// must be supplied by the user's session; file references survive reboot.
		if e = os.MkdirAll(filepath.Dir(unit), 0700); e != nil {
			return e
		}
		f, e := openPrivate(unit, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if e != nil {
			return problem(ErrPermission, "A service definition already exists or cannot be created securely.", "Uninstall the previous Runmoor service before installing this binary.")
		}
		_, e = f.WriteString(text)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if runtime.GOOS == "linux" {
			return run("systemctl", "--user", "daemon-reload")
		}
		return nil
	case "start":
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			snapshot, e := validateServiceConfigMatch(runtime.GOOS, unit, path)
			if e != nil {
				return e
			}
			definitionSnapshot = &snapshot
		} else if _, e := readPrivate(unit, 64<<10); e != nil {
			return e
		}
		if runtime.GOOS == "darwin" {
			if _, e := exec.Run(ctx, "launchctl", []string{"print", domain + "/" + serviceLabel}, minimalEnv(), nil); e == nil {
				// launchctl caches a job's arguments when it is bootstrapped, and
				// `print` output is not a stable interface for verifying them. Remove
				// the loaded job, then bootstrap the securely validated on-disk plist.
				if e := requireServiceDefinitionUnchanged(runtime.GOOS, unit, path, *definitionSnapshot); e != nil {
					return e
				}
				if e := run("launchctl", "bootout", domain, unit); e != nil {
					return e
				}
			}
			if e := requireServiceDefinitionUnchanged(runtime.GOOS, unit, path, *definitionSnapshot); e != nil {
				return e
			}
			return run("launchctl", "bootstrap", domain, unit)
		}
		if runtime.GOOS == "linux" {
			// Check the running process before daemon-reload: a changed on-disk
			// unit must not replace ExecStop for a still-running old invocation.
			if e := requireSystemdActiveIdentity(ctx, exec, definitionSnapshot.data); e != nil {
				return e
			}
			if e := requireServiceDefinitionUnchanged(runtime.GOOS, unit, path, *definitionSnapshot); e != nil {
				return e
			}
			if e := run("systemctl", "--user", "daemon-reload"); e != nil {
				return e
			}
			if e := requireServiceDefinitionUnchanged(runtime.GOOS, unit, path, *definitionSnapshot); e != nil {
				return e
			}
		}
		return run("systemctl", "--user", "enable", "--now", systemdServiceName)
	case "stop", "uninstall":
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			snapshot, e := validateServiceConfigMatch(runtime.GOOS, unit, path)
			if e != nil {
				return e
			}
			definitionSnapshot = &snapshot
		}
		if runtime.GOOS == "linux" {
			// Reject a stale active manager before sending drain control to the
			// requested configuration or opening its offline state.
			if e := requireSystemdActiveIdentity(ctx, exec, definitionSnapshot.data); e != nil {
				return e
			}
		} else if runtime.GOOS == "darwin" {
			if e := requireLaunchdActiveIdentity(ctx, exec, definitionSnapshot.data); e != nil {
				return e
			}
		}
		if _, e := SendControl(ctx, c, ControlRequest{Action: "stop"}); e == nil {
			if e = waitStopped(ctx, c, ""); e != nil {
				return e
			}
		} else {
			store, se := OpenStore(c)
			if se != nil {
				return e
			}
			s := store.View()
			store.Close()
			if !allTerminated(s) {
				return problem(ErrControl, "Service is unreachable while owned executions may still be active.", "Restart the manager to reconcile and drain before removing the service.")
			}
		}
		if definitionSnapshot != nil {
			if e := requireServiceDefinitionUnchanged(runtime.GOOS, unit, path, *definitionSnapshot); e != nil {
				return e
			}
		}
		if runtime.GOOS == "darwin" {
			if e := requireLaunchdActiveIdentity(ctx, exec, definitionSnapshot.data); e != nil {
				return e
			}
			if e := unloadLaunchd(ctx, domain, unit, exec); e != nil {
				return e
			}
		} else {
			// Recheck after draining in case the active service changed while
			// control or offline cleanup was in progress. Refresh systemd's
			// cached ExecStop from the validated on-disk snapshot before --now
			// can stop the unit, then verify that snapshot remained unchanged.
			if runtime.GOOS == "linux" {
				if e := requireSystemdActiveIdentity(ctx, exec, definitionSnapshot.data); e != nil {
					return e
				}
				if e := requireServiceDefinitionUnchanged(runtime.GOOS, unit, path, *definitionSnapshot); e != nil {
					return e
				}
				if e := run("systemctl", "--user", "daemon-reload"); e != nil {
					return e
				}
				if e := requireServiceDefinitionUnchanged(runtime.GOOS, unit, path, *definitionSnapshot); e != nil {
					return e
				}
				if e := requireSystemdActiveIdentity(ctx, exec, definitionSnapshot.data); e != nil {
					return e
				}
			}
			if e := run("systemctl", "--user", "disable", "--now", systemdServiceName); e != nil {
				return e
			}
		}
		if action == "uninstall" {
			if definitionSnapshot != nil {
				if e := requireServiceDefinitionUnchanged(runtime.GOOS, unit, path, *definitionSnapshot); e != nil {
					return e
				}
			} else if _, e := readPrivate(unit, 64<<10); e != nil {
				return e
			}
			if e := os.Remove(unit); e != nil {
				return e
			}
			if runtime.GOOS == "linux" {
				return run("systemctl", "--user", "daemon-reload")
			}
		}
		return nil
	default:
		return problem(ErrConfig, "Unknown service action.", "Use service install, start, stop or uninstall.")
	}
}
