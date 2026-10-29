// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Terminal struct {
	In  *bufio.Reader
	Out io.Writer
}

func (t Terminal) Confirm(message string) bool {
	fmt.Fprintln(t.Out, message+" [y/N]")
	line, e := t.In.ReadString('\n')
	return e == nil && strings.EqualFold(strings.TrimSpace(line), "y")
}

type Application struct {
	Native     NativeRuntimeController
	Disk       Disk
	Store      Credentials
	Services   Services
	Resolver   Resolver
	API        func(string) (ClientAPI, error)
	Prompt     Prompter
	Out        io.Writer
	Browser    func(string) error
	Executable string
	Clock      Clock
}

func main() {
	var logOutput io.Writer = os.Stderr
	if os.Getenv("NO_COLOR") == "" {
		logOutput = coloredLog{os.Stderr}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logOutput, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	home, e := os.UserHomeDir()
	if e != nil {
		fmt.Fprintln(os.Stderr, "cannot resolve home directory")
		os.Exit(1)
	}
	home, e = filepath.EvalSymlinks(home)
	if e != nil {
		fmt.Fprintln(os.Stderr, "cannot resolve home directory")
		os.Exit(1)
	}
	state := filepath.Join(home, ".config", "tairouter")
	if runtime.GOOS == "windows" {
		state = filepath.Join(home, "AppData", "Local", "tairouter")
	}
	env := map[string]string{}
	for _, entry := range os.Environ() {
		pair := strings.SplitN(entry, "=", 2)
		env[pair[0]] = pair[1]
	}
	disk := Disk{state, NativeProtector{}}
	u, _ := user.Current()
	uid := ""
	if u != nil {
		uid = u.Uid
	}
	exe, _ := os.Executable()
	app := Application{Disk: disk, Store: NativeCredentials{}, Resolver: Resolver{home, runtime.GOOS, env}, API: func(s string) (ClientAPI, error) { return NewConnectAPI(s) }, Prompt: Terminal{bufio.NewReader(os.Stdin), os.Stdout}, Out: os.Stdout, Executable: exe, Browser: openBrowser}
	app.Services = UserServices{runtime.GOOS, home, uid, NativeRunner{}, disk}
	if e = app.Run(ctx, os.Args[1:]); e != nil {
		slog.Error("CLI operation failed", "reason", e.Error())
		os.Exit(1)
	}
}
func openBrowser(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	}
	return cmd.Run()
}
func (a *Application) Run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(a.Out, "tairouter connect|profiles|configure|disconnect|restore|port|service|relay|clients|client-profiles|recover|native|context\nFlags: --profile NAME --server HTTPS_ORIGIN --client CLIENT --port PORT --state-dir ABSOLUTE_PATH\ncontext: --action enable|disable|bind --session SESSION_ID; configure --client codex --context-management previews local opt-in.\nservice: --action install|status|stop|remove\nnative: --action plan|enable|status|stop|disable|serve|launch --mode MODE --build BUILD --native-port PORT\nconfigure: --mode MODE selects an enabled, trusted native runtime; omit for gateway settings.\nDisconnect: offers settings restoration; preserves conflicts and reports incomplete revocation.\nConfiguration previews always require interactive confirmation; remote-key storage requires separate consent.\nSet NO_COLOR=1 to disable ANSI log colors.")
		return nil
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("profile", "default", "")
	server := flags.String("server", "", "")
	client := flags.String("client", "", "")
	port := flags.Int("port", DefaultPort, "")
	stateDir := flags.String("state-dir", a.Disk.Root, "")
	action := flags.String("action", "status", "")
	deviceName := flags.String("device-name", "", "")
	contextSession := flags.String("session", "", "")
	contextManagement := flags.Bool("context-management", false, "")
	nativeMode := flags.String("mode", "", "")
	nativeBuild := flags.String("build", "", "")
	nativePort := flags.Int("native-port", DefaultNativePort, "")
	if e := flags.Parse(args[1:]); e != nil || flags.NArg() != 0 {
		return errors.New("invalid arguments; run tairouter help")
	}
	a.Disk.Root = *stateDir
	if native, ok := a.Services.(UserServices); ok {
		native.Disk = a.Disk
		a.Services = native
	}
	if command == "clients" {
		for _, c := range clients {
			mode := "direct remote key (separate consent)"
			if c.Loopback {
				mode = "loopback relay"
			}
			fmt.Fprintf(a.Out, "%s: %s; %s; %s\n", c.ID, c.Format, mode, c.Platforms)
		}
		return nil
	}
	if command == "client-profiles" {
		profiles, e := a.Resolver.DiscoverProfiles(*client)
		if e != nil {
			return e
		}
		for _, p := range profiles {
			fmt.Fprintf(a.Out, "%s current=%t path=%q\n", p.ID, p.Current, p.Path)
		}
		return nil
	}
	if !namePattern.MatchString(*name) {
		return errors.New("invalid profile name")
	}
	if command == "native" {
		switch *action {
		case "serve":
			return a.nativeController().Serve(ctx, *name)
		case "stop":
			return a.nativeController().Stop(ctx, *name)
		case "status":
			state := emptyState()
			return a.runNative(ctx, &state, *name, *action, "", "", 0)
		}
	}
	if command == "relay" {
		s, e := a.Disk.Load()
		if e != nil {
			return e
		}
		p, ok := s.Profiles[*name]
		if !ok || p.RevocationPending {
			return errors.New("profile is unavailable")
		}
		return RunRelay(ctx, p, a.Store)
	}
	unlock, e := a.Disk.Lock()
	if e != nil {
		return e
	}
	defer unlock()
	s, e := a.Disk.Load()
	if e != nil {
		return e
	}
	if command == "recover" {
		return a.Recover(&s)
	}
	if exists(filepath.Join(a.Disk.Root, "pending.json")) && command != "disconnect" && !(command == "native" && *action == "disable") {
		return errors.New("unfinished settings transaction; run tairouter recover before changing state")
	}
	switch command {
	case "profiles":
		names := []string{}
		for n := range s.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p := s.Profiles[n]
			fmt.Fprintf(a.Out, "%s  %s  organization=%s  port=%d  revocation-pending=%t\n", n, p.Server, p.Organization, p.Port, p.RevocationPending)
		}
		keys := []string{}
		for c := range s.Active {
			keys = append(keys, c)
		}
		sort.Strings(keys)
		for _, c := range keys {
			fmt.Fprintf(a.Out, "%s -> %s\n", c, s.Active[c])
		}
		return nil
	case "connect":
		if _, ok := s.Profiles[*name]; ok {
			return errors.New("profile already exists; use configure to switch a client or disconnect this profile first")
		}
		p := Profile{Name: *name, Server: *server, Port: *port}
		if e = p.Validate(); e != nil {
			return e
		}
		for _, other := range s.Profiles {
			if other.Port == p.Port {
				return errors.New("another profile uses this fixed port; choose a distinct --port")
			}
		}
		api, e := a.API(p.Server)
		if e != nil {
			return e
		}
		if *deviceName == "" {
			*deviceName, _ = os.Hostname()
		}
		p, e = (Connector{api, a.Store, a.Clock, a.Out, a.Browser}).Connect(ctx, p, *deviceName)
		if e != nil {
			return e
		}
		s.Profiles[p.Name] = p
		if e = a.Disk.Save(s); e != nil {
			key, _ := a.Store.Get(credentialID(p))
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			revoked := api.Revoke(cleanup, p.KeyID, key)
			_ = a.Store.Delete(credentialID(p))
			if revoked != nil {
				return fmt.Errorf("cannot save profile; server revocation incomplete. %s", revokeHint(p))
			}
			return errors.New("cannot save profile; key revoked")
		}
		fmt.Fprintln(a.Out, "Profile connected. Run configure to preview client settings.")
		return nil
	}
	if command == "restore" {
		if *client == "" {
			return errors.New("restore requires --client")
		}
		if !a.Prompt.Confirm("Restore this client's protected backup while preserving any conflicting edits?") {
			return errors.New("restoration declined")
		}
		return a.Disk.Restore(&s, *client)
	}
	if command == "native" {
		return a.runNative(ctx, &s, *name, *action, NativeCompatibilityMode(*nativeMode), *nativeBuild, *nativePort)
	}
	p, ok := s.Profiles[*name]
	if !ok {
		return errors.New("profile not found")
	}
	if command == "disconnect" {
		a.nativeController()
		return a.Disconnect(ctx, &s, p)
	}
	if p.RevocationPending {
		return errors.New("profile is disconnected; revoke the outstanding key in the web console")
	}
	switch command {
	case "context":
		return a.configureContext(ctx, p, *action, *contextSession)
	case "service":
		switch *action {
		case "install":
			if !a.Prompt.Confirm(fmt.Sprintf("Install a login service on 127.0.0.1:%d? Other local processes can use this profile without local authentication.", p.Port)) {
				return errors.New("service installation declined")
			}
			return a.Services.Install(ctx, p, a.Executable, a.Disk.Root)
		case "status":
			status, e := a.Services.Status(ctx, p)
			if e == nil {
				fmt.Fprintln(a.Out, status)
			}
			return e
		case "stop":
			return a.Services.Stop(ctx, p)
		case "remove":
			return a.Services.Remove(ctx, p)
		default:
			return errors.New("invalid service action")
		}
	case "configure":
		spec, e := clientSpec(*client)
		if e != nil {
			return e
		}
		key, e := a.Store.Get(credentialID(p))
		if e != nil {
			return ErrUnavailable
		}
		api, e := a.API(p.Server)
		if e != nil {
			return e
		}
		models, e := modelsForClient(ctx, api, key, *client)
		if e != nil {
			return e
		}
		changes, e := a.planConfiguration(ctx, s, *client, p, key, models, NativeCompatibilityMode(*nativeMode))
		if e != nil {
			return e
		}
		if *contextManagement {
			if *client != "codex" || *nativeMode != "" {
				return errors.New("context management requires Codex gateway configuration")
			}
			changes, e = withContextManagement(changes)
			if e != nil {
				return e
			}
		}
		Preview(a.Out, changes)
		if spec.Loopback {
			fmt.Fprintf(a.Out, "Requires the profile relay at 127.0.0.1:%d; local processes share access. Install it with service --action install.\n", p.Port)
		}
		if (*client == "cline" || *client == "claude-desktop" || *nativeMode != "") && !a.Prompt.Confirm("Stop the client before applying these paired files. Is the client stopped?") {
			return errors.New("client must be stopped")
		}
		return a.Disk.Apply(&s, *client, p.Name, changes, a.Prompt)
	case "port":
		if *port == p.Port {
			return nil
		}
		next := p
		next.Port = *port
		if e = next.Validate(); e != nil {
			return e
		}
		for n, other := range s.Profiles {
			if n != p.Name && other.Port == next.Port {
				return errors.New("another profile uses the requested port")
			}
		}
		key, e := a.Store.Get(credentialID(p))
		if e != nil {
			return ErrUnavailable
		}
		api, e := a.API(p.Server)
		if e != nil {
			return e
		}
		models, e := api.Models(ctx, key)
		if e != nil {
			return e
		}
		plans := map[string][]Change{}
		for id, active := range s.Active {
			if active != p.Name {
				continue
			}
			spec, _ := clientSpec(id)
			if !spec.Loopback {
				continue
			}
			changes, e := PlanSettings(a.Resolver, s, id, next, key, models)
			if e != nil {
				return e
			}
			plans[id] = changes
			Preview(a.Out, changes)
		}
		if !a.Prompt.Confirm(fmt.Sprintf("Change fixed relay port from %d to %d and apply all displayed settings? The relay will stop and must be installed again.", p.Port, next.Port)) {
			return errors.New("port change declined")
		}
		if e = a.Services.Stop(ctx, p); e != nil {
			return e
		}
		return a.Disk.ChangePort(&s, next, plans)
	default:
		return errors.New("unknown command; run tairouter help")
	}
}

type approved struct{}

func (approved) Confirm(string) bool { return true }
func (a *Application) Disconnect(ctx context.Context, s *State, p Profile) error {
	var failures []string
	key, e := a.Store.Get(credentialID(p))
	revoked := false
	if e == nil {
		if api, err := a.API(p.Server); err == nil {
			revocation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			revoked = api.Revoke(revocation, p.KeyID, key) == nil
			cancel()
		}
	}
	if !revoked {
		failures = append(failures, "server revocation incomplete. "+revokeHint(p))
	}
	// Local cleanup must continue after network, credential-store and restore failures.
	if a.Native != nil {
		if e = a.Native.Stop(context.WithoutCancel(ctx), p.Name); e != nil {
			failures = append(failures, "native listener stop incomplete")
		}
	}
	if e = a.Services.Stop(context.WithoutCancel(ctx), p); e != nil {
		if errors.Is(e, errRelayOwnership) {
			failures = append(failures, "relay stop blocked by an ownership conflict; use the original state directory or OS service manager")
		} else {
			failures = append(failures, "relay stop failed; stop it with the OS service manager")
		}
	}
	if e = a.Services.Remove(context.WithoutCancel(ctx), p); e != nil {
		if errors.Is(e, errRelayOwnership) {
			failures = append(failures, "login service removal blocked by an ownership conflict")
		} else {
			failures = append(failures, "login service removal incomplete")
		}
	}
	restore := a.Prompt.Confirm("Restore settings owned by this profile? Conflicts retain protected backups for manual recovery.")
	for client, record := range s.Records {
		if record.Profile != p.Name {
			continue
		}
		if restore {
			if e = a.Disk.Restore(s, client); e != nil {
				failures = append(failures, "settings conflict for "+client+"; settings and recovery backup retained")
			}
		} else {
			failures = append(failures, "restoration declined for "+client+"; settings and backup retained")
		}
	}
	if a.Native != nil {
		status, err := a.Native.Status(context.WithoutCancel(ctx), p.Name)
		if err != nil {
			failures = append(failures, "native runtime status unavailable; owned trust may remain")
		} else if status.Enabled || status.Trusted {
			if a.Prompt.Confirm("Remove this profile’s owned native runtime trust certificate? Unrelated trust is preserved.") {
				if err = a.Native.Disable(context.WithoutCancel(ctx), p.Name); err != nil {
					failures = append(failures, "native trust cleanup incomplete; use native --action disable")
				}
			} else {
				failures = append(failures, "native trust cleanup declined; use native --action disable")
			}
		}
	}
	if e = a.Store.Delete(credentialID(p)); e != nil {
		failures = append(failures, "OS credential deletion failed; remove the profile credential manually")
	}
	if !revoked {
		p.RevocationPending = true
		s.Profiles[p.Name] = p
	} else {
		delete(s.Profiles, p.Name)
	}
	if e = a.Disk.Save(*s); e != nil {
		failures = append(failures, "local state save failed")
	}
	if exists(filepath.Join(a.Disk.Root, "pending.json")) {
		failures = append(failures, "unfinished settings transaction retained; run tairouter recover")
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	fmt.Fprintln(a.Out, "Profile disconnected; key revoked and backups removed after restoration.")
	return nil
}

// Keep the structured fields intact; color is an operator-facing presentation
// layer and NO_COLOR disables it for plain-text collection.
type coloredLog struct{ output io.Writer }

func (w coloredLog) Write(p []byte) (int, error) {
	line := append([]byte("\x1b[31m"), p...)
	line = append(line, []byte("\x1b[0m")...)
	_, e := w.output.Write(line)
	if e != nil {
		return 0, e
	}
	return len(p), nil
}
