// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
}
type NativeRunner struct{}

func (NativeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	b, e := exec.CommandContext(ctx, name, args...).Output()
	if e != nil {
		var exit *exec.ExitError
		if ctx.Err() == nil && name == "launchctl" && len(args) == 2 && args[0] == "print" && errors.As(e, &exit) && launchAgentAbsentDiagnostic(string(exit.Stderr), args[1]) {
			return "", errLaunchAgentNotLoaded
		}
		return "", errors.New("native service command failed; inspect the OS service manager")
	}
	return strings.TrimSpace(string(b)), nil
}

type UserServices struct {
	OS, Home, UID string
	Runner        Runner
	Disk          Disk
}

func (s UserServices) name(p Profile) string { return "tairouter-" + p.Name }
func (s UserServices) path(p Profile) string {
	if s.OS == "darwin" {
		return filepath.Join(s.Home, "Library", "LaunchAgents", s.name(p)+".plist")
	}
	return filepath.Join(s.Home, ".config", "systemd", "user", s.name(p)+".service")
}
func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$", "\n", `\n`, "\r", `\r`).Replace(s) + `"`
}
func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func (s UserServices) Install(ctx context.Context, p Profile, executable, stateRoot string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(stateRoot) {
		return errors.New("service executable and state directory must be absolute")
	}
	args := []string{executable, "relay", "--profile", p.Name, "--state-dir", stateRoot}
	switch s.OS {
	case "linux":
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = systemdQuote(a)
		}
		unit := "[Unit]\nDescription=TaiRouter loopback relay\nAfter=network-online.target\n[Service]\nType=simple\nExecStart=" + strings.Join(quoted, " ") + "\nRestart=on-failure\nRestartSec=5\nUMask=0077\n[Install]\nWantedBy=default.target\n"
		if e := s.writeService(p, []byte(unit)); e != nil {
			return e
		}
		if _, e := s.Runner.Run(ctx, "systemctl", "--user", "daemon-reload"); e != nil {
			return e
		}
		_, e := s.Runner.Run(ctx, "systemctl", "--user", "enable", "--now", s.name(p)+".service")
		return e
	case "darwin":
		var values string
		for _, a := range args {
			values += "<string>" + xmlText(a) + "</string>"
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + s.name(p) + `</string><key>ProgramArguments</key><array>` + values + `</array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/></dict></plist>`
		if _, e := strconv.Atoi(s.UID); e != nil {
			return errors.New("invalid user service UID")
		}
		if e := s.writeService(p, []byte(plist)); e != nil {
			return e
		}
		_, e := s.Runner.Run(ctx, "launchctl", "bootstrap", "gui/"+s.UID, s.path(p))
		return e
	case "windows":
		// A task runs only for the interactive current user, at login. No password,
		// elevation, shell command, or machine-wide service is installed.
		task := `<?xml version="1.0" encoding="UTF-16"?><Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlText(s.UID) + `</UserId></LogonTrigger></Triggers><Principals><Principal id="Author"><UserId>` + xmlText(s.UID) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings><Actions Context="Author"><Exec><Command>` + xmlText(executable) + `</Command><Arguments>` + xmlText("relay --profile "+p.Name+" --state-dir "+windowsQuote(stateRoot)) + `</Arguments></Exec></Actions></Task>`
		// schtasks accepts UTF-8 XML when the declaration matches the bytes.
		task = strings.Replace(task, "UTF-16", "UTF-8", 1)
		path := filepath.Join(s.Disk.Root, s.name(p)+".xml")
		if e := s.Disk.write(path, []byte(task)); e != nil {
			return e
		}
		defer os.Remove(path)
		if _, e := s.Runner.Run(ctx, "schtasks.exe", "/Create", "/TN", s.name(p), "/XML", path); e != nil {
			return e
		}
		_, e := s.Runner.Run(ctx, "schtasks.exe", "/Run", "/TN", s.name(p))
		return e
	default:
		return errors.New("user services unsupported on this OS")
	}
}
func windowsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
		}
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}
func (s UserServices) Status(ctx context.Context, p Profile) (string, error) {
	switch s.OS {
	case "linux":
		return s.Runner.Run(ctx, "systemctl", "--user", "show", s.name(p)+".service", "--property=ActiveState,SubState")
	case "darwin":
		output, e := s.Runner.Run(ctx, "launchctl", "print", "gui/"+s.UID+"/"+s.name(p))
		if e != nil {
			return "", e
		}
		var status []string
		// launchctl print can include inherited environment variables. Expose
		// only process state, never the complete native diagnostic document.
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "state = ") || strings.HasPrefix(line, "pid = ") || strings.HasPrefix(line, "last exit code = ") {
				status = append(status, line)
			}
		}
		if len(status) == 0 {
			return "service registered", nil
		}
		return strings.Join(status, "\n"), nil
	case "windows":
		return s.Runner.Run(ctx, "schtasks.exe", "/Query", "/TN", s.name(p), "/FO", "LIST")
	}
	return "", errors.New("unsupported service OS")
}
func (s UserServices) Stop(ctx context.Context, p Profile) error {
	if e := s.verifyOwnership(ctx, p); e != nil {
		return e
	}
	var e error
	switch s.OS {
	case "linux":
		_, e = s.Runner.Run(ctx, "systemctl", "--user", "stop", s.name(p)+".service")
	case "darwin":
		_, e = s.Runner.Run(ctx, "launchctl", "bootout", "gui/"+s.UID+"/"+s.name(p))
	case "windows":
		_, e = s.Runner.Run(ctx, "schtasks.exe", "/End", "/TN", s.name(p))
	default:
		e = errors.New("unsupported service OS")
	}
	return e
}
func (s UserServices) Remove(ctx context.Context, p Profile) error {
	ownership := s.verifyOwnership(ctx, p)
	if ownership != nil && !(s.OS == "darwin" && errors.Is(ownership, errLaunchAgentNotLoaded)) {
		return ownership
	}
	switch s.OS {
	case "linux":
		if _, e := s.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", s.name(p)+".service"); e != nil {
			return e
		}
		if e := os.Remove(s.path(p)); e != nil && !os.IsNotExist(e) {
			return e
		}
		_, e := s.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
		return e
	case "darwin":
		before, _, e := s.launchAgentDefinition(p)
		if e != nil {
			return e
		}
		// bootout removes the registered definition. A prior Stop may already
		// have done that; the on-disk owned plist still needs removal.
		if ownership == nil {
			if _, e := s.Runner.Run(ctx, "launchctl", "bootout", "gui/"+s.UID+"/"+s.name(p)); e != nil {
				return e
			}
		}
		after, _, e := s.launchAgentDefinition(p)
		if e != nil || !bytes.Equal(before, after) {
			return errRelayOwnership
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		e = os.Remove(s.path(p))
		if os.IsNotExist(e) {
			return nil
		}
		return e
	case "windows":
		if e := s.Stop(ctx, p); e != nil {
			return e
		}
		_, e := s.Runner.Run(ctx, "schtasks.exe", "/Delete", "/TN", s.name(p), "/F")
		return e
	}
	return fmt.Errorf("unsupported service OS")
}

func (s UserServices) writeService(p Profile, content []byte) error {
	before, exists, e := readConfig(s.path(p))
	if e != nil {
		return e
	}
	if exists && !bytes.Equal(before, content) {
		return errors.New("service definition differs from the proposed definition; preserve it and remove the old service explicitly")
	}
	return s.Disk.write(s.path(p), content)
}
