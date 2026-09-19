package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/async-commit-hook/internal/core"
)

const Help = `ach — asynchronous committed checks

Usage: ach <command> [options]

init                         Trust/register this worktree and install safe post-commit integration
config validate              Validate working-tree configuration without running commands
run                          Submit exact committed configuration; returns a durable receipt
status | inbox               Inspect a run (--run ID) or repository history (--repo .)
wait                         Wait for --run ID [--timeout 60]; never cancels execution
logs                         Read --run ID --check NAME [--offset 0 --limit 65536]
check                        Gate the exact --commit SHA against the latest compatible attempt
ack | failures | cancel      Explicitly acknowledge, inspect failures or cancel --run ID
plan | doctor                Inspect committed configuration and diagnose local prerequisites
compare                      Compare --run ID [--previous ID]
rerun                        Rerun --run ID [--failed] in a fresh workspace
ui                           Print a paired connection URL [--run ID]
hooks install | uninstall    Preserve existing hooks; --pre-push explicitly enables the push gate
agent install | uninstall    --client codex|claude-code|opencode [--scope user|project]
agent-guide                  Print the supplied validation skill
daemon start | status | stop Stop drains; --force requests cancellation
browser list | revoke        Revoke requires --browser ID
mcp                          Serve MCP on stdio without an idle daemon
pre-push                     Validate branch tips on Git stdin [--policy block|wait|run-and-wait]
prune                        --dry-run [--max-age-days N --max-bytes N]
self-update                  [--version MAJOR.MINOR.PATCH] or --recover
version                      Show product and contract versions

Common options: --repo PATH (default .), --commit REF (default HEAD), --json,
--config PATH (isolated personal configuration). ACH_CONFIG also selects configuration.
Exit codes: 0 operation success, 1 incomplete/failed validation, 2 invalid usage/config,
3 runner/storage failure, 4 wait expiration. A status query can succeed for a failed run.
`

type options struct {
	flags map[string]string
	words []string
}

func parse(args []string) (options, error) {
	o := options{flags: map[string]string{}}
	bools := map[string]bool{"json": true, "help": true, "automatic": true, "failed": true, "force": true, "dry-run": true, "pre-push": true, "recover": true}
	values := map[string]bool{"repo": true, "commit": true, "run": true, "check": true, "offset": true, "limit": true, "cursor": true, "timeout": true, "previous": true, "config": true, "client": true, "scope": true, "browser": true, "policy": true, "max-age-days": true, "max-bytes": true, "version": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-h" {
			a = "--help"
		}
		if !strings.HasPrefix(a, "--") {
			o.words = append(o.words, a)
			continue
		}
		key, value, has := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if _, ok := o.flags[key]; ok {
			return o, core.E("duplicate-option", "duplicate option --"+key, 2)
		}
		if bools[key] {
			if has {
				return o, core.E("invalid-option", "boolean options do not take a value", 2)
			}
			o.flags[key] = "true"
		} else if values[key] {
			if !has {
				i++
				if i == len(args) {
					return o, core.E("missing-option-value", "missing value for --"+key, 2)
				}
				value = args[i]
			}
			o.flags[key] = value
		} else {
			return o, core.E("unknown-option", "unknown option --"+key, 2)
		}
	}
	return o, nil
}
func (o options) get(k, def string) string {
	if v, ok := o.flags[k]; ok {
		return v
	}
	return def
}
func (o options) has(k string) bool { return o.flags[k] == "true" }
func (o options) number(k string, def int64) (int64, error) {
	v, ok := o.flags[k]
	if !ok {
		return def, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 0 {
		return 0, core.E("invalid-number", "--"+k+" requires a nonnegative integer", 2)
	}
	return n, nil
}
func Run(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) int {
	o, e := parse(args)
	if e != nil {
		return emit(out, diagnostics, o.has("json"), nil, e, 0)
	}
	if o.has("help") || len(o.words) == 0 {
		fmt.Fprint(out, Help)
		return 0
	}
	command := o.words[0]
	sub := ""
	if len(o.words) > 1 {
		sub = o.words[1]
	}
	if command == "version" {
		return emit(out, diagnostics, o.has("json"), map[string]any{"version": core.Version, "schema_version": 1, "api_version": 1}, nil, 0)
	}
	if command == "agent-guide" {
		if o.has("json") {
			return emit(out, diagnostics, true, map[string]string{"guide": core.AgentGuide}, nil, 0)
		}
		fmt.Fprint(out, core.AgentGuide)
		return 0
	}
	paths, e := core.DefaultPaths()
	if e != nil {
		return emit(out, diagnostics, o.has("json"), nil, e, 0)
	}
	if p := o.flags["config"]; p != "" {
		paths.Config, e = filepath.Abs(p)
		if e != nil {
			return emit(out, diagnostics, o.has("json"), nil, e, 0)
		}
	}
	s, e := core.Open(paths)
	if e != nil {
		return emit(out, diagnostics, o.has("json"), nil, e, 0)
	}
	defer s.Close()
	if command != "internal" && command != "mcp" && command != "self-update" {
		_, leave, err := s.Enter("cli")
		if err != nil {
			return emit(out, diagnostics, o.has("json"), nil, err, 0)
		}
		defer leave()
	}
	repo := o.get("repo", ".")
	id := o.flags["run"]
	var result any
	exit := 0
	switch command {
	case "init":
		var r core.Repository
		var w core.Worktree
		r, w, e = s.Init(ctx, repo)
		if e == nil {
			var hooks []core.InstallResult
			hooks, e = s.Hook(ctx, repo, o.has("pre-push"), false)
			result = map[string]any{"repository": r, "worktree": w, "hooks": hooks, "next": "edit and commit .config/async-commit-hook.toml; ach agent-guide"}
		}
	case "config":
		if sub != "validate" {
			e = core.E("invalid-command", "use ach config validate", 2)
			break
		}
		var b []byte
		b, e = os.ReadFile(filepath.Join(repo, core.ProjectFile))
		if e == nil {
			result, e = core.ParseProject(b)
		}
	case "run":
		var receipt core.Receipt
		receipt, e = s.Submit(ctx, repo, o.flags["commit"], o.has("automatic"))
		if e == nil {
			if err := s.Start(s.Personal.Mode); err != nil {
				receipt.Diagnostic = &core.Diagnostic{Code: "startup-failed", Message: err.Error()}
				e = err
			}
		}
		result = receipt
	case "status", "inbox":
		if id != "" {
			result, e = s.Store.Run(id)
		} else {
			var repoID string
			repoID, e = s.RepositoryID(ctx, repo)
			if e == nil {
				var limit int64
				limit, e = o.number("limit", 50)
				if e == nil {
					result, e = s.Store.List(repoID, command == "inbox", o.flags["cursor"], int(limit))
				}
			}
		}
	case "wait":
		var seconds int64
		seconds, e = o.number("timeout", 60)
		if e == nil {
			wait, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
			defer cancel()
			result, e = s.Wait(wait, id)
		}
	case "logs":
		var offset, limit int64
		offset, e = o.number("offset", 0)
		if e == nil {
			limit, e = o.number("limit", 65536)
		}
		if e == nil {
			result, e = s.Logs(id, o.flags["check"], offset, int(limit))
			if e == nil && !o.has("json") {
				fmt.Fprint(out, result.(core.LogPage).Text)
				return 0
			}
		}
	case "check":
		var gate core.Gate
		gate, e = s.Gate(ctx, repo, o.flags["commit"])
		result = gate
		if e == nil && !gate.Passed {
			exit = 1
		}
	case "ack":
		e = s.Store.Ack(id)
		result = map[string]string{"run_id": id, "acknowledged": "true"}
	case "failures":
		var r core.Run
		r, e = s.Store.Run(id)
		if e == nil {
			result = core.Failures(r)
		}
	case "cancel":
		e = s.Store.Cancel(id, core.Cancelled)
		result = map[string]string{"run_id": id, "state": "cancellation-requested"}
	case "plan":
		result, e = s.Plan(ctx, repo, o.flags["commit"])
	case "doctor":
		result = s.Doctor(ctx, repo)
	case "compare":
		result, e = s.Compare(id, o.flags["previous"])
	case "rerun":
		result, e = s.Rerun(id, o.has("failed"))
		if e == nil {
			e = s.Start(s.Personal.Mode)
		}
	case "hooks":
		if sub != "install" && sub != "uninstall" {
			e = core.E("invalid-command", "use hooks install/uninstall", 2)
		} else {
			result, e = s.Hook(ctx, repo, o.has("pre-push"), sub == "uninstall")
		}
	case "agent":
		if sub != "install" && sub != "uninstall" {
			e = core.E("invalid-command", "use agent install/uninstall", 2)
		} else {
			result, e = s.Agent(o.flags["client"], o.get("scope", "user"), repo, sub == "uninstall")
		}
	case "mcp":
		e = s.MCP(ctx)
		if e == nil {
			return 0
		}
	case "daemon":
		switch sub {
		case "start":
			e = s.Start(core.Daemon)
		case "status":
			result, e = s.Active()
		case "stop":
			e = s.Stop(o.has("force"))
		default:
			e = core.E("invalid-command", "use daemon start/status/stop", 2)
		}
	case "ui":
		var code string
		code, e = s.PairingCode()
		if e == nil {
			address := s.WebURL(id, code)
			if s.Personal.Mode == core.Daemon {
				e = s.Start(core.Daemon)
				result = map[string]string{"url": address}
			} else {
				e = s.Serve(ctx, false, func() {
					_ = emit(out, diagnostics, o.has("json"), map[string]string{"url": address, "state": "viewer-active; stop with Ctrl-C"}, nil, 0)
				})
				if e == nil {
					return 0
				}
			}
		}
	case "browser":
		switch sub {
		case "list":
			result, e = s.Browsers()
		case "revoke":
			e = s.Revoke(o.flags["browser"])
		default:
			e = core.E("invalid-command", "use browser list/revoke --browser ID", 2)
		}
	case "pre-push":
		result, e = s.PrePush(ctx, repo, in, core.PushPolicy(o.flags["policy"]))
	case "prune":
		var age, bytes int64
		age, e = o.number("max-age-days", int64(s.Personal.Retention.MaxAgeDays))
		if e == nil {
			bytes, e = o.number("max-bytes", s.Personal.Retention.MaxBytes)
		}
		if e == nil {
			result, e = s.Prune(o.has("dry-run"), int(age), bytes)
		}
	case "self-update":
		if o.has("recover") {
			result, e = s.RecoverUpdate()
		} else {
			result, e = s.SelfUpdate(ctx, o.flags["version"])
		}
	case "internal":
		switch sub {
		case "worker":
			e = s.Worker(ctx)
		case "serve":
			e = s.Serve(ctx, true, nil)
		case "apply-update":
			e = s.ApplyUpdate()
		default:
			e = core.E("invalid-command", "unknown internal command", 2)
		}
	default:
		e = core.E("invalid-command", "unknown command; use ach --help", 2)
	}
	return emit(out, diagnostics, o.has("json"), result, e, exit)
}
func emit(out, diagnostics io.Writer, machine bool, result any, err error, exit int) int {
	response := core.Output{SchemaVersion: 1, Result: result}
	if err != nil {
		var typed *core.Error
		if errors.As(err, &typed) {
			response.Error = &typed.Diagnostic
			exit = typed.Exit
		} else {
			response.Error = &core.Diagnostic{Code: "runner-error", Message: err.Error()}
			exit = 3
		}
		fmt.Fprintln(diagnostics, response.Error.Code+": "+response.Error.Message)
	}
	if machine {
		_ = json.NewEncoder(out).Encode(response)
	} else if result != nil {
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(out, string(b))
	}
	return exit
}
