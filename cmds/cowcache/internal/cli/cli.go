// SPDX-License-Identifier: Apache-2.0
// Package cli implements CowCache's public command boundaries.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/delinoio/oss/cmds/cowcache/internal/process"
	"github.com/delinoio/oss/cmds/cowcache/internal/repository"
	"github.com/delinoio/oss/cmds/cowcache/internal/shell"
	"github.com/delinoio/oss/cmds/cowcache/internal/shim"
	"github.com/delinoio/oss/cmds/cowcache/internal/state"
	"golang.org/x/term"
)

type options struct {
	preset, source, file                         string
	allowCopy, replace, json, yes, verbose, help bool
	args, command                                []string
}
type failure struct {
	Code        int    `json:"code"`
	Cause       string `json:"cause"`
	Remediation string `json:"remediation"`
}

func (f *failure) Error() string { return f.Cause }
func bad(s string) error {
	return &failure{2, s, "Run cowc --help for supported commands and options."}
}
func parse(args []string) (o options, e error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			o.command = append([]string(nil), args[i+1:]...)
			break
		}
		key, value, has := strings.Cut(a, "=")
		switch key {
		case "--preset", "--source", "--file":
			if !has {
				i++
				if i == len(args) {
					return o, bad(key + " requires a value")
				}
				value = args[i]
			}
			if value == "" {
				return o, bad(key + " requires a value")
			}
			switch key {
			case "--preset":
				o.preset = value
			case "--source":
				o.source = value
			case "--file":
				o.file = value
			}
		case "--allow-copy", "--replace", "--json", "--yes", "--verbose", "--help", "-h":
			if has {
				return o, bad("boolean options do not accept values")
			}
			switch key {
			case "--allow-copy":
				o.allowCopy = true
			case "--replace":
				o.replace = true
			case "--json":
				o.json = true
			case "--yes":
				o.yes = true
			case "--verbose":
				o.verbose = true
			default:
				o.help = true
			}
		default:
			if strings.HasPrefix(a, "-") {
				return o, bad("unknown option " + a)
			}
			o.args = append(o.args, a)
		}
	}
	if o.preset != "" && !shim.Tool(o.preset).Valid() {
		return o, bad("unknown preset " + o.preset)
	}
	return o, nil
}

const usage = `cowc run [--preset cargo|rspack|rsbuild] [--source WORKTREE] [--allow-copy] [--replace] -- COMMAND [ARGS...]
cowc prepare|status|clean|doctor [--preset PRESET] [--source WORKTREE] [--json]
cowc prepare [--replace] [--allow-copy] [-- COMMAND [ARGS...]]
cowc config get [source] | config set source WORKTREE | config unset source
cowc shim install|uninstall|list|doctor [--json]
cowc shell init|setup|remove bash|zsh|powershell [--file FILE] [--yes]
cowc --version
Use --verbose for diagnostics; NO_COLOR disables ANSI notices.
prepare never runs a build. Use its optional command after -- to discover exact build settings.
`

type app struct {
	store *state.Store
	o     options
	input *bufio.Reader
	ctx   context.Context
}

func Main(version string) int {
	_, shimInvocation := shim.FromArgv0(os.Args[0])
	if !shimInvocation && len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("cowc " + version)
		return 0
	}
	// A shim never parses the real tool's arguments as CowCache options.
	t, isShim := shim.FromArgv0(os.Args[0])
	var o options
	var e error
	if !isShim {
		o, e = parse(os.Args[1:])
	}
	a := &app{o: o, input: bufio.NewReader(os.Stdin)}
	if e != nil {
		return a.fail(e)
	}
	if !isShim && (o.help || len(o.args) == 0) {
		fmt.Print(usage)
		return 0
	}
	level := slog.LevelWarn
	if o.verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	var stop context.CancelFunc
	a.ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.store, e = state.Open("")
	if e != nil {
		return a.fail(e)
	}
	if isShim {
		return a.shimRun(t, os.Args[1:])
	}
	code, e := a.run()
	if e != nil {
		return a.fail(e)
	}
	return code
}
func (a *app) fail(e error) int {
	f := &failure{125, e.Error(), "Check the cause, run cowc doctor --verbose, and retry after correcting configuration, source selection, permissions, or active cache use."}
	var own *failure
	if errors.As(e, &own) {
		f = own
	}
	var x interface{ ExitCode() int }
	if errors.As(e, &x) {
		f.Code = x.ExitCode()
	}
	if a.o.json {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"schemaVersion": 1, "success": false, "error": f})
	} else {
		a.notice("error: %s\nRemediation: %s", f.Cause, f.Remediation)
	}
	return f.Code
}
func (a *app) notice(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if _, off := os.LookupEnv("NO_COLOR"); !off {
		s = "\x1b[36mcowc:\x1b[0m " + s
	} else {
		s = "cowc: " + s
	}
	fmt.Fprintln(os.Stderr, s)
}
func (a *app) output(v any) error {
	if a.o.json {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"schemaVersion": 1, "success": true, "result": v})
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e == nil {
		fmt.Println(string(b))
	}
	return e
}
func (a *app) interactive() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
func (a *app) answer(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	s, e := a.input.ReadString('\n')
	return strings.TrimSpace(s), e
}
func (a *app) confirm(prompt string) (bool, error) {
	s, e := a.answer(prompt + " [y/N] ")
	return strings.EqualFold(s, "y") || strings.EqualFold(s, "yes"), e
}
func (a *app) resolve(t shim.Tool) (shim.Resolution, error) {
	exe, e := os.Executable()
	if e != nil {
		return shim.Resolution{}, e
	}
	return shim.Resolve(t, shim.ResolveOptions{Path: os.Getenv("PATH"), PathExt: os.Getenv("PATHEXT"), ShimDir: filepath.Join(a.store.Root, "shims"), CowcExecutable: exe})
}
func (a *app) command(argv []string, cwd string) (process.Command, error) {
	if len(argv) == 0 {
		return process.Command{}, bad("run requires -- COMMAND [ARGS...]")
	}
	path := argv[0]
	if !strings.ContainsAny(path, "/\\") {
		if t, ok := shim.FromArgv0(path); ok {
			r, e := a.resolve(t)
			if e != nil {
				return process.Command{}, e
			}
			path = r.Path
		} else {
			p, e := exec.LookPath(path)
			if e != nil {
				return process.Command{}, &failure{executionCode(e), e.Error(), "Install the command and add its executable directory to PATH."}
			}
			path = p
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path, e := filepath.Abs(path)
	if e != nil {
		return process.Command{}, e
	}
	return process.Command{Path: path, Args: append([]string(nil), argv...), Dir: cwd, Env: os.Environ()}, nil
}
func executionCode(e error) int {
	if errors.Is(e, exec.ErrNotFound) || errors.Is(e, os.ErrNotExist) {
		return 127
	}
	return 126
}
func (a *app) run() (int, error) {
	cmd := a.o.args[0]
	if (a.o.replace || a.o.allowCopy) && cmd != "run" && cmd != "prepare" {
		return 0, bad("--replace and --allow-copy apply only to run and prepare")
	}
	if (a.o.yes || a.o.file != "") && cmd != "shell" {
		return 0, bad("--yes and --file apply only to shell setup/remove")
	}
	if (cmd == "config" || cmd == "shim" || cmd == "shell") && (a.o.source != "" || a.o.preset != "" || len(a.o.command) > 0) {
		return 0, bad("cache options and child argv do not apply to this command")
	}
	if cmd == "clean" && a.o.source != "" {
		return 0, bad("clean selects the current worktree cache, not a source")
	}

	switch a.o.args[0] {
	case "shim":
		return 0, a.shims()
	case "shell":
		return 0, a.shell()
	case "config":
		e := a.config()
		if e != nil {
			return 0, &failure{2, e.Error(), "Check config get/set/unset arguments, saved record version, and same-repository source selection."}
		}
		return 0, nil
	case "run", "prepare", "status", "clean", "doctor":
		if len(a.o.args) != 1 {
			return 0, bad("unexpected positional arguments; put the child command after --")
		}
		if a.o.args[0] == "run" && a.o.json {
			return 0, bad("--json is for management commands; run preserves child streams")
		}
		return a.managed(a.o.args[0], a.o.command)
	default:
		return 0, bad("unknown command " + a.o.args[0])
	}
}
func (a *app) config() error {
	if len(a.o.args) < 2 {
		return bad("config requires get, set, or unset")
	}
	cwd, e := os.Getwd()
	if e != nil {
		return e
	}
	r, e := repository.Discover(a.ctx, cwd)
	if e != nil {
		return e
	}
	args := a.o.args[1:]
	switch args[0] {
	case "get":
		if len(args) > 2 || (len(args) == 2 && args[1] != "source") {
			return bad("config get [source]")
		}
		v, e := a.store.Settings(r.ID)
		if e != nil {
			return e
		}
		return a.output(v)
	case "set":
		if len(args) != 3 || args[1] != "source" {
			return bad("config set source WORKTREE")
		}
		s, e := repository.SelectSource(a.ctx, r, args[2], "")
		if e != nil {
			return e
		}
		if e = a.store.SetSource(r.ID, s.Repository.Root); e != nil {
			return e
		}
	case "unset":
		if len(args) != 2 || args[1] != "source" {
			return bad("config unset source")
		}
		if e = a.store.SetSource(r.ID, ""); e != nil {
			return e
		}
	default:
		return bad("unknown config operation")
	}
	return a.output(map[string]bool{"updated": true})
}
func (a *app) shims() error {
	if len(a.o.args) != 2 {
		return bad("shim requires install, uninstall, list, or doctor")
	}
	dir := filepath.Join(a.store.Root, "shims")
	switch a.o.args[1] {
	case "install":
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		if e = shim.Install(dir, exe); e != nil {
			return e
		}
	case "uninstall":
		if e := shim.Uninstall(dir); e != nil {
			return e
		}
		a.notice("Shell registration blocks remain. Use cowc shell remove SHELL to remove them.")
	case "list":
	case "doctor":
		entries, e := shim.List(dir)
		if e != nil {
			return e
		}
		selected := map[shim.Tool]any{}
		for _, t := range shim.Tools() {
			r, e := a.resolve(t)
			if e != nil {
				selected[t] = map[string]any{"resolution": r, "cause": e.Error()}
			} else {
				selected[t] = r
			}
		}
		return a.output(map[string]any{"installation": entries, "tools": selected, "shimDirectory": dir})
	default:
		return bad("unknown shim operation")
	}
	entries, e := shim.List(dir)
	if e != nil {
		return e
	}
	return a.output(entries)
}
func (a *app) shell() error {
	if len(a.o.args) != 3 {
		return bad("shell requires init, setup, or remove and bash, zsh, or powershell")
	}
	kind := shell.Kind(a.o.args[2])
	dir := filepath.Join(a.store.Root, "shims")
	if a.o.args[1] == "init" {
		if a.o.file != "" || a.o.yes {
			return bad("shell init emits code; --file and --yes apply to setup/remove")
		}

		if a.o.json {
			return bad("shell init emits shell code and does not support --json")
		}
		s, e := shell.Init(kind, dir)
		if e == nil {
			fmt.Print(s)
		}
		return e
	}
	action := shell.Action(a.o.args[1])
	if action != shell.Setup && action != shell.Remove {
		return bad("unknown shell operation")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	path, e := shell.SelectFile(kind, shell.Selection{File: a.o.file, Home: home, ZDotDir: os.Getenv("ZDOTDIR")})
	if e != nil {
		return e
	}
	p, e := shell.Prepare(kind, action, path, dir)
	if e != nil {
		return e
	}
	a.notice("Shell configuration target: %s\nProposed contents:\n%s", p.Path, p.After)
	e = p.Apply(shell.Approval{Yes: a.o.yes, Interactive: a.interactive(), Confirm: func(*shell.Plan) (bool, error) { return a.confirm("Apply this shell configuration change?") }})
	if e != nil {
		return e
	}
	a.notice("%s", p.CurrentSessionInstruction())
	return a.output(map[string]any{"path": p.Path, "changed": p.Changed()})
}
func (a *app) shimRun(t shim.Tool, args []string) int {
	r, e := a.resolve(t)
	if e != nil {
		return a.fail(e)
	}
	cwd, e := os.Getwd()
	if e != nil {
		return a.fail(e)
	}
	classification := shim.Classify(t, args, true, os.Getenv("COWC_BYPASS"))
	if classification.Mode != shim.Passthrough {
		if _, gitErr := repository.Discover(a.ctx, cwd); gitErr != nil {
			for dir := cwd; ; dir = filepath.Dir(dir) {
				if _, e := os.Lstat(filepath.Join(dir, ".git")); e == nil {
					return a.fail(gitErr)
				}
				if filepath.Dir(dir) == dir {
					break
				}
			}
			classification = shim.Classify(t, args, false, os.Getenv("COWC_BYPASS"))
		}
	}
	argv := append([]string{string(t)}, args...)
	if classification.Mode == shim.Passthrough {
		if classification.Reason == shim.Unknown {
			a.notice("Unrecognized command passes through. Use cowc run -- for cache management.")
		}
		code, e := direct(process.Command{Path: r.Path, Args: argv, Dir: cwd, Env: os.Environ()})
		if e != nil {
			return a.fail(&failure{code, e.Error(), "Check the real executable and its permissions."})
		}
		return code
	}
	a.o.preset = string(t)
	action := "run"
	if classification.Mode == shim.Clean {
		action = "tool-clean"
	}
	code, e := a.managed(action, argv)
	if e != nil {
		return a.fail(e)
	}
	return code
}
