package runmoor

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

const helpText = `Runmoor 0.1.0 preview - local ephemeral GitHub Actions runners

Usage: runmoor [--config PATH] COMMAND [OPTIONS]

  init                      Create a documented TOML skeleton (never overwrite)
  config validate           Validate TOML schema v1 and host resource budgets
  run                       Run the foreground manager
  status [--json]            Inspect pools, capacity, active work and cleanup
  doctor [--json]            Check credentials, dependencies, images and power
  reload                    Atomically accept a completely validated configuration
  pause [--pool NAME]        Stop accepting work; preserve busy runners
  resume [--pool NAME]       Revalidate and resume paused or suspended pools
  drain [--pool NAME]        Pause and wait for owned jobs and local cleanup
  stop [--force]             Drain and exit; force terminates owned jobs
  service install|start|stop|uninstall
  image create|open|seal|list|remove
  version

All commands accept --config PATH and --no-color. NO_COLOR disables ANSI output.
status and doctor --json use schema_version 1. Product output is English.
GitHub live compatibility and real Tart execution have not been certified.
`

func Execute(args []string, out, errOut io.Writer) int {
	if len(args) > 0 && strings.HasPrefix(args[0], "__guest-") {
		return guestExecute(args[0], args[1:], out)
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, helpText)
		return 0
	}
	path := DefaultConfigPath()
	noColor := false
	root := flag.NewFlagSet("runmoor", flag.ContinueOnError)
	root.SetOutput(io.Discard)
	root.StringVar(&path, "config", path, "configuration path")
	root.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	if e := root.Parse(args); e != nil {
		return printFailure(errOut, false, problem(ErrConfig, "Invalid global flags.", "Run 'runmoor --help'."))
	}
	args = root.Args()
	if len(args) == 0 {
		fmt.Fprint(out, helpText)
		return 2
	}
	command := args[0]
	args = args[1:]
	sub := ""
	if command == "config" || command == "service" || command == "image" {
		if len(args) == 0 {
			fmt.Fprint(out, helpText)
			return 2
		}
		sub = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&path, "config", path, "configuration path")
	fs.BoolVar(&noColor, "no-color", noColor, "disable ANSI color")
	jsonOutput := fs.Bool("json", false, "versioned JSON output")
	pool := fs.String("pool", "", "pool name")
	force := fs.Bool("force", false, "terminate owned work during stop")
	im := ImageRequest{Action: sub}
	fs.StringVar(&im.ID, "id", "", "image revision UUID")
	fs.StringVar(&im.Name, "name", "", "image display name")
	fs.StringVar(&im.IPSW, "ipsw", "", "absolute local IPSW path")
	fs.StringVar(&im.From, "from", "", "local Tart name, .tvm, sealed UUID or oci:// reference")
	fs.StringVar(&im.SourceHome, "source-home", "", "external Tart home for a local source name")
	fs.IntVar(&im.Resources.CPU, "cpu", 0, "image setup CPU cores")
	fs.Int64Var(&im.Resources.MemoryMiB, "memory-mib", 0, "image setup memory MiB")
	fs.StringVar(&im.RunnerPath, "runner-path", "", "absolute guest runner path")
	fs.StringVar(&im.RunnerVersion, "runner-version", "", "exact installed runner version")
	if e := fs.Parse(args); e == flag.ErrHelp {
		fmt.Fprint(out, helpText)
		if command == "image" {
			fmt.Fprintln(out, "Image options: --name NAME (--ipsw /local/image.ipsw | --from SOURCE) --cpu N --memory-mib N; open/seal/remove --id UUID; seal --runner-version VERSION [--runner-path PATH]")
		}
		return 0
	} else if e != nil || fs.NArg() != 0 {
		return printFailure(errOut, *jsonOutput, problem(ErrConfig, "Invalid command arguments.", "Run the command with --help; image revisions use --id UUID."))
	}
	if *force && command != "stop" {
		return printFailure(errOut, *jsonOutput, problem(ErrConfig, "--force is accepted only by stop.", "Use 'runmoor stop --force' to terminate owned work."))
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return printFailure(errOut, *jsonOutput, problem(ErrConfig, "Invalid configuration path.", "Use a valid --config path."))
	}
	path = abs
	if command == "version" {
		fmt.Fprintf(out, "runmoor %s preview (%s)\n", Version, Revision)
		return 0
	}
	if command == "init" {
		if e = InitConfig(path); e != nil {
			return printFailure(errOut, *jsonOutput, e)
		}
		fmt.Fprintln(out, "Created TOML schema v1 skeleton. Set credentials, pinned images and resource budgets before running.")
		return 0
	}
	c, e := LoadConfig(path)
	if e != nil {
		return printFailure(errOut, *jsonOutput, e)
	}
	c.Logging.NoColor = c.Logging.NoColor || noColor
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch command {
	case "config":
		if sub != "validate" {
			e = problem(ErrConfig, "Unknown config command.", "Use 'runmoor config validate'.")
		} else {
			fmt.Fprintln(out, "Configuration is valid (schema v1).")
			return 0
		}
	case "run":
		e = runForeground(ctx, path, c, errOut)
	case "service":
		e = Service(ctx, sub, path, c, OSCommand{})
		if e == nil {
			fmt.Fprintf(out, "User service %s completed.\n", sub)
		}
	case "status", "image", "doctor":
		if command == "image" && sub != "list" {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
			defer cancel()
			resp, sendErr := SendControl(ctx, c, ControlRequest{Action: "image", Image: &im})
			if sendErr == nil {
				writeJSON(out, resp.Image)
				return 0
			}
			if p, ok := sendErr.(*Problem); !ok || p.Code != ErrControl {
				return printFailure(errOut, *jsonOutput, sendErr)
			}
		}
		if command == "status" || command == "image" && sub == "list" {
			action := "status"
			if command == "image" {
				action = "images"
			}
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			resp, sendErr := SendControl(probe, c, ControlRequest{Action: action})
			cancel()
			if sendErr == nil {
				if command == "status" {
					printStatus(out, resp.Status, *jsonOutput)
				} else {
					writeJSON(out, resp)
				}
				return 0
			}
			if p, ok := sendErr.(*Problem); !ok || p.Code != ErrControl {
				return printFailure(errOut, *jsonOutput, sendErr)
			}
		}
		store, openErr := OpenStore(c)
		if openErr != nil {
			if command == "doctor" {
				probe, cancel := context.WithTimeout(ctx, 90*time.Second)
				defer cancel()
				report := Doctor(probe, c, Snapshot{Images: map[string]*Image{}}, NewGitHub, defaultDriver)
				report.Checks = append(report.Checks, Check{Name: "state", Problem: classify(openErr, ErrState, "State unavailable.", "Inspect manager status.")})
				printDoctor(out, report, *jsonOutput)
				return doctorExit(report)
			}
			e = openErr
			break
		}
		defer store.Close()
		if command == "status" {
			printStatus(out, statusOf(store.View(), false), *jsonOutput)
			return 0
		}
		if command == "doctor" {
			probe, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			report := Doctor(probe, c, store.View(), NewGitHub, defaultDriver)
			printDoctor(out, report, *jsonOutput)
			return doctorExit(report)
		}
		images := &ImageManager{Store: store, Tart: &TartDriver{Exec: OSCommand{}}}
		probe, cancel := context.WithTimeout(ctx, 2*time.Hour)
		defer cancel()
		if sub == "list" {
			_ = images.Reconcile(probe, c)
			var all []*Image
			for _, v := range store.View().Images {
				all = append(all, v)
			}
			writeJSON(out, ControlResponse{SchemaVersion: 1, Images: all})
			return 0
		}
		v, er := images.Operate(probe, c, im)
		e = er
		if e == nil {
			writeJSON(out, v)
		}
	case "reload", "pause", "resume", "drain", "stop":
		probe, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, e = SendControl(probe, c, ControlRequest{Action: command, Pool: *pool, Force: *force})
		cancel()
		if e == nil && (command == "drain" || command == "stop") {
			e = waitStopped(ctx, c, *pool)
		}
		if e == nil {
			fmt.Fprintf(out, "%s completed.\n", command)
		}
	default:
		e = problem(ErrConfig, "Unknown command.", "Run 'runmoor --help'.")
	}
	if e != nil {
		return printFailure(errOut, *jsonOutput, e)
	}
	return 0
}
func runForeground(ctx context.Context, path string, c Config, out io.Writer) error {
	if e := platformCheck(ctx); e != nil {
		return e
	}
	store, e := OpenStore(c)
	if e != nil {
		return e
	}
	defer store.Close()
	logger, e := NewLogger(c, out)
	if e != nil {
		return e
	}
	m := NewManager(store, path, logger)
	if e = m.activate(c); e != nil {
		return e
	}
	server, e := m.ServeControl()
	if e != nil {
		return e
	}
	defer server.Close()
	logger.Info("manager_started", "version", Version, "installation", store.View().Installation)
	return m.Run(ctx, c)
}
func waitStopped(ctx context.Context, c Config, pool string) error {
	for {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		resp, e := SendControl(probe, c, ControlRequest{Action: "status"})
		cancel()
		if e != nil {
			store, se := OpenStore(c)
			if se == nil {
				s := store.View()
				store.Close()
				if allTerminated(s) {
					return nil
				}
			}
			if !waitContext(ctx, time.Second) {
				return problem(ErrControl, "Waiting for drain was interrupted; the manager keeps draining.", "Inspect status before stopping the service.")
			}
			continue
		}
		active := false
		for _, r := range resp.Status.Runners {
			if r.Terminated && r.LocalCleaned {
				continue
			}
			matches := pool == ""
			for _, p := range resp.Status.Pools {
				if p.ID == r.PoolID && p.Name == pool {
					matches = true
				}
			}
			if matches {
				active = true
			}
		}
		if !active {
			return nil
		}
		if !waitContext(ctx, time.Second) {
			return problem(ErrControl, "Waiting for drain was interrupted; the manager keeps draining.", "Inspect status; use stop --force only when termination is intended.")
		}
	}
}
func printFailure(w io.Writer, jsonOutput bool, err error) int {
	p := classify(err, ErrState, "Operation failed.", "Run doctor and inspect the private diagnostic logs.")
	if jsonOutput {
		writeJSON(w, ControlResponse{SchemaVersion: 1, Problem: p})
	} else {
		fmt.Fprintln(w, p.Error())
	}
	if p.Code == ErrConfig {
		return 2
	}
	return 1
}
func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
func printStatus(w io.Writer, s *Status, jsonOutput bool) {
	if jsonOutput {
		writeJSON(w, s)
		return
	}
	fmt.Fprintf(w, "Runmoor %s preview | manager running: %t | paused: %t | stopping: %t\n", s.Version, s.Running, s.Paused, s.Stopping)
	fmt.Fprintf(w, "Reserved: %d CPU, %d MiB, %d executions/setup VMs; macOS VMs: %d/2; pending cleanup: %d\n", s.Reserved.CPU, s.Reserved.MemoryMiB, s.Active, s.VMs, s.PendingCleanup)
	for _, p := range s.Pools {
		fmt.Fprintf(w, "%s [%s] %s: demand=%d total=%d busy=%d\n", p.Name, p.Generation, p.Phase, p.Demand, p.Total, p.Busy)
		if p.Problem != nil {
			fmt.Fprintln(w, p.Problem.Error())
		}
	}
	for _, r := range s.Runners {
		fmt.Fprintf(w, "  %s %s %s\n", r.ID, r.Backend, r.Phase)
		if r.Problem != nil {
			fmt.Fprintln(w, r.Problem.Error())
		}
	}
	if s.Power != nil {
		fmt.Fprintln(w, s.Power.Error())
	}
}
func printDoctor(w io.Writer, r DoctorReport, jsonOutput bool) {
	if jsonOutput {
		writeJSON(w, r)
		return
	}
	for _, c := range r.Checks {
		state := "OK"
		if !c.OK {
			state = "FAIL"
			if c.Warning {
				state = "WARN"
			}
		}
		fmt.Fprintf(w, "%s %s %s\n", state, c.Name, c.Pool)
		if c.Problem != nil {
			fmt.Fprintln(w, c.Problem.Error())
		}
	}
}
func doctorExit(r DoctorReport) int {
	for _, c := range r.Checks {
		if !c.OK && !c.Warning {
			return 1
		}
	}
	return 0
}

var _ = slog.LevelInfo
