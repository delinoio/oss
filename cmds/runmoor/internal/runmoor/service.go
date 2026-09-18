package runmoor

import (
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const serviceLabel = "io.delino.runmoor"

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
	return filepath.Join(base, "systemd", "user", "runmoor.service")
}
func Service(ctx context.Context, action, path string, c Config, exec CommandExecutor) error {
	unit := servicePath()
	uid := strconv.Itoa(os.Getuid())
	domain := "gui/" + uid
	run := func(name string, args ...string) error {
		_, e := exec.Run(ctx, name, args, minimalEnv(), nil)
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
		if _, e := readPrivate(unit, 64<<10); e != nil {
			return e
		}
		if runtime.GOOS == "darwin" {
			if _, e := exec.Run(ctx, "launchctl", []string{"print", domain + "/" + serviceLabel}, minimalEnv(), nil); e == nil {
				return run("launchctl", "kickstart", domain+"/"+serviceLabel)
			}
			return run("launchctl", "bootstrap", domain, unit)
		}
		return run("systemctl", "--user", "enable", "--now", "runmoor.service")
	case "stop", "uninstall":
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
		if runtime.GOOS == "darwin" {
			_, _ = exec.Run(ctx, "launchctl", []string{"bootout", domain, unit}, minimalEnv(), nil)
		} else {
			if e := run("systemctl", "--user", "disable", "--now", "runmoor.service"); e != nil {
				return e
			}
		}
		if action == "uninstall" {
			if _, e := readPrivate(unit, 64<<10); e != nil {
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
