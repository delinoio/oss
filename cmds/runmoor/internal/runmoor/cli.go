package runmoor

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

const helpText = ` - local ephemeral GitHub Actions runners

Usage: runmoor [--config PATH] COMMAND [OPTIONS]

  init                      Create a minimal configuration (interactive in a terminal)
  config validate           Validate TOML schema v1 and automatic or explicit budgets
  config show --resolved    Show calculated settings and committed runner versions
  runner update [--pool NAME] Check and prepare managed runner updates now
  run                       Run the foreground manager
  status [--json]            Inspect pools, capacity, active work and cleanup
  doctor [--json]            Check credentials, dependencies, images and power
  reload                    Reload settings; upgrade an older user-service manager to this CLI
  pause [--pool NAME]        Stop accepting work; preserve busy runners
  resume [--pool NAME]       Revalidate and resume paused or suspended pools
  drain [--pool NAME]        Pause and wait for owned jobs and local cleanup
  stop [--pool NAME] [--force] Drain a pool, or drain and exit the manager
  service install|start|stop|uninstall
  image create|open|seal|list|remove
  version

All commands accept --config PATH and --no-color. NO_COLOR disables ANSI output.
status and doctor --json use schema_version 1. Product output is English.
GitHub live compatibility and real Tart/host execution have not been certified.
`

func printHelp(out io.Writer) {
	fmt.Fprint(out, "Runmoor ", Version, helpText)
}

func Execute(args []string, out, errOut io.Writer) int {
	if len(args) == 3 && args[0] == "__service-reload-handoff" {
		return serviceReloadHandoff(args[1], args[2])
	}
	if len(args) == 2 && args[0] == "__host-exec" {
		return hostExecute(args[1])
	}
	if len(args) == 1 && args[0] == "__host-supervisor" {
		return hostSupervise()
	}
	if len(args) > 0 && strings.HasPrefix(args[0], "__guest-") {
		return guestExecute(args[0], args[1:], out)
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printHelp(out)
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
		printHelp(out)
		return 2
	}
	command := args[0]
	args = args[1:]
	sub := ""
	if command == "config" || command == "service" || command == "image" || command == "runner" {
		if len(args) == 0 {
			printHelp(out)
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
	resolved := fs.Bool("resolved", false, "show effective configuration")
	initOpts := InitOptions{}
	fs.StringVar(&initOpts.Target, "target", "", "GitHub repository or organization URL")
	fs.StringVar(&initOpts.Backend, "backend", "", "docker, tart or host")
	fs.StringVar(&initOpts.Auth, "auth", "", "pat or app")
	fs.StringVar(&initOpts.CredentialEnv, "credential-env", "", "credential environment variable name")
	fs.StringVar(&initOpts.CredentialFile, "credential-file", "", "absolute private credential file")
	fs.StringVar(&initOpts.ClientID, "client-id", "", "GitHub App client ID")
	fs.Int64Var(&initOpts.InstallationID, "installation-id", 0, "GitHub App installation ID")
	fs.StringVar(&initOpts.Image, "image", "", "immutable Docker image or sealed Tart UUID")
	fs.StringVar(&initOpts.ImageSource, "image-source", "", "prepared Tart source")
	fs.BoolVar(&initOpts.ImageOnly, "image-only", false, "create configuration for initial image preparation")
	im := ImageRequest{Action: sub}
	fs.StringVar(&im.ID, "id", "", "image revision UUID")
	fs.StringVar(&im.Name, "name", "", "image display name")
	fs.StringVar(&im.IPSW, "ipsw", "", "latest supported Apple IPSW or absolute local .ipsw path")
	fs.StringVar(&im.From, "from", "", "local Tart name, .tvm, sealed UUID or oci:// reference")
	fs.StringVar(&im.SourceHome, "source-home", "", "external Tart home for a local source name")
	fs.IntVar(&im.Resources.CPU, "cpu", 0, "image setup CPU cores")
	fs.Int64Var(&im.Resources.MemoryMiB, "memory-mib", 0, "image setup memory MiB")
	fs.StringVar(&im.RunnerPath, "runner-path", "", "absolute guest runner path")
	fs.StringVar(&im.RunnerVersion, "runner-version", "", "latest or exact installed runner version")
	if e := fs.Parse(args); e == flag.ErrHelp {
		printHelp(out)
		if command == "image" {
			fmt.Fprintln(out, "Image options: create --name NAME (--ipsw latest|PATH | --from SOURCE) [--cpu N --memory-mib N]; open/seal/remove --id UUID; seal [--runner-version latest|VERSION] [--runner-path PATH]")
		}
		if command == "init" {
			fmt.Fprintln(out, "Init options: --target URL --backend docker|tart --auth pat|app (--credential-env NAME | --credential-file PATH) [--client-id ID --installation-id N] [--image DIGEST_OR_UUID | --image-source SOURCE --source-home PATH]; --image-only prepares a configuration without pools.")
		}
		return 0
	} else if e != nil || fs.NArg() != 0 {
		return printFailure(errOut, *jsonOutput, problem(ErrConfig, "Invalid command arguments.", "Run the command with --help; image revisions use --id UUID."))
	}
	if *force && command != "stop" {
		return printFailure(errOut, *jsonOutput, problem(ErrConfig, "--force is accepted only by stop.", "Use 'runmoor stop --force' to terminate owned work."))
	}
	invalidFlag := false
	fs.Visit(func(f *flag.Flag) {
		allowed := f.Name == "config" || f.Name == "no-color"
		switch f.Name {
		case "json":
			allowed = command == "status" || command == "doctor" || command == "config" || (command == "image" && sub == "list")
		case "pool":
			allowed = command == "pause" || command == "resume" || command == "drain" || command == "stop" || (command == "runner" && sub == "update")
		case "force":
			allowed = command == "stop"
		case "id":
			allowed = command == "image" && (sub == "open" || sub == "seal" || sub == "remove")
		case "target", "backend", "auth", "credential-env", "credential-file", "client-id", "installation-id", "image", "image-source", "image-only":
			allowed = command == "init"
		case "resolved":
			allowed = command == "config" && sub == "show"
		case "source-home":
			allowed = command == "init" || (command == "image" && sub == "create")
		case "name", "ipsw", "from", "cpu", "memory-mib":
			allowed = command == "image" && sub == "create"
		case "runner-path", "runner-version":
			allowed = command == "image" && sub == "seal"
		}
		invalidFlag = invalidFlag || !allowed
	})
	if invalidFlag {
		return printFailure(errOut, *jsonOutput, problem(ErrConfig, "Flag is not supported by this command.", "Run the command with --help."))
	}

	if command == "image" && sub == "create" {
		bad := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "cpu" && im.Resources.CPU <= 0 || f.Name == "memory-mib" && im.Resources.MemoryMiB <= 0 {
				bad = true
			}
		})
		if bad {
			return printFailure(errOut, *jsonOutput, problem(ErrConfig, "Explicit setup resources must be positive.", "Omit the options to use automatic defaults."))
		}
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return printFailure(errOut, *jsonOutput, problem(ErrConfig, "Invalid configuration path.", "Use a valid --config path."))
	}
	path = abs
	if command == "version" {
		fmt.Fprintf(out, "runmoor %s (%s)\n", Version, Revision)
		return 0
	}
	if command == "init" {
		initOpts.SourceHome = im.SourceHome
		initCtx, stopInit := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stopInit()
		if e = initializeWithContext(initCtx, path, initOpts, os.Stdin, out, term.IsTerminal(int(os.Stdin.Fd())), defaultGuidedRuntime{}); e != nil {
			return printFailure(errOut, *jsonOutput, e)
		}
		return 0
	}
	var c Config
	useCommitted := false
	if command == "run" {
		startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		c, useCommitted, _, e = newServiceReloader(errOut).startup(startup, path, c)
		cancel()
	}
	if e == nil && !useCommitted {
		c, e = LoadConfig(path)
	}
	if e != nil {
		return printFailure(errOut, *jsonOutput, e)
	}
	c.Logging.NoColor = c.Logging.NoColor || noColor
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch command {
	case "config":
		if sub == "show" && *resolved {
			resp, err := SendControl(ctx, c, ControlRequest{Action: "config"})
			if err == nil && resp.Config != nil {
				writeJSON(out, resp.Config)
				return 0
			}
			if p, ok := err.(*Problem); err != nil && (!ok || p.Code != ErrControl) {
				return printFailure(errOut, *jsonOutput, err)
			}
			snapshot, err := ReadSnapshot(c)
			if err == nil {
				writeJSON(out, displayResolved(snapshot))
				return 0
			}
			if !os.IsNotExist(err) {
				return printFailure(errOut, *jsonOutput, err)
			}
			// Initial resolution has no committed runner version until run.
			writeJSON(out, c)
			return 0
		}
		if sub != "validate" {
			e = problem(ErrConfig, "Unknown config command.", "Use 'runmoor config validate'.")
		} else {
			if *jsonOutput {
				writeJSON(out, struct {
					SchemaVersion int  `json:"schema_version"`
					Valid         bool `json:"valid"`
				}{1, true})
			} else {
				fmt.Fprintln(out, "Configuration is valid (schema v1).")
			}
			return 0
		}
	case "runner":
		if sub != "update" {
			e = problem(ErrConfig, "Unknown runner command.", "Use runner update [--pool NAME].")
		} else {
			_, e = SendControl(ctx, c, ControlRequest{Action: "runner-update", Pool: *pool})
			if e == nil {
				fmt.Fprintln(out, "Runner update requested; inspect status for progress.")
			}
		}
	case "run":
		e = runForeground(ctx, path, c, errOut)
	case "service":
		e = Service(ctx, sub, path, c, OSCommand{})
		if e == nil {
			fmt.Fprintf(out, "User service %s completed.\n", sub)
		}
	case "status", "image", "doctor":
		if command == "doctor" {
			probe, cancel := context.WithTimeout(ctx, 90*time.Second)
			resp, err := SendControl(probe, c, ControlRequest{Action: "doctor"})
			cancel()
			if err == nil && resp.Doctor != nil {
				printDoctor(out, *resp.Doctor, *jsonOutput)
				return doctorExit(*resp.Doctor)
			}
			if q, ok := err.(*Problem); err != nil && (!ok || q.Code != ErrControl) {
				return printFailure(errOut, *jsonOutput, err)
			}
		}
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
			return printFailure(errOut, *jsonOutput, problem(ErrControl, "Image changes require a running manager to supervise setup and sleep inhibition.", "Start 'runmoor run' or the user service with this configuration. For initial image preparation, omit pools until a revision is sealed."))
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
		_ = images.Reconcile(probe, c)
		var all []*Image
		for _, v := range store.View().Images {
			all = append(all, v)
		}
		writeJSON(out, ControlResponse{SchemaVersion: 1, Images: all})
		return 0
	case "reload":
		probe, cancel := context.WithTimeout(ctx, 2*time.Minute)
		e = newServiceReloader(errOut).Reload(probe, path, c)
		cancel()
		if e == nil {
			fmt.Fprintln(out, "reload completed.")
		}
	case "pause", "resume", "drain", "stop":
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
	return runForegroundReady(ctx, path, c, out, nil)
}

func runForegroundReady(ctx context.Context, path string, c Config, out io.Writer, ready chan<- error) (result error) {
	announced := false
	defer func() {
		if ready != nil && !announced {
			ready <- result
		}
	}()
	startup, startupCancel := context.WithTimeout(ctx, 10*time.Second)
	reloader := newServiceReloader(out)
	committed, preserveStop, recovery, e := reloader.startup(startup, path, c)
	startupCancel()
	if e != nil {
		return e
	}
	c = committed
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	resolved, capacityErr := resolveDockerCapacity(probe, c)
	cancel()
	if capacityErr == nil {
		c = resolved
	}
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
	if capacityErr != nil {
		c.DockerCapacityPending = true
		cached := store.View().Config.DockerBudget
		if validResources(cached) {
			c.DockerBudget = cached
			var err error
			c, err = resolveDefaults(c, Resources{c.Host.CPU, c.Host.MemoryMiB})
			if err != nil {
				return err
			}
		}
		logProblem(logger, "docker_capacity_retry", classify(capacityErr, ErrRetry, "Docker capacity is temporarily unavailable.", "Restore the engine; managed pools retry automatically."))
	}
	m := NewManager(store, path, logger)
	m.PreserveStop = preserveStop
	if e = m.initializeRun(c); e != nil {
		return e
	}
	server, e := m.ServeControl()
	if e != nil {
		return e
	}
	defer server.Close()
	if recovery != nil {
		if e = reloader.retire(recovery); e != nil {
			return e
		}
	}
	if ready != nil {
		ready <- nil
		announced = true
	}
	logger.Info("manager_started", "version", Version, "installation", store.View().Installation)
	return m.runActivated(ctx)
}
func waitStopped(ctx context.Context, c Config, pool string) error {
	for {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		resp, e := SendControl(probe, c, ControlRequest{Action: "status"})
		cancel()
		if e != nil {
			store, se := OpenStore(c)
			if se != nil {
				if !waitContext(ctx, time.Second) {
					return problem(ErrControl, "Waiting for drain was interrupted; the manager keeps draining.", "Inspect status before stopping the service.")
				}
				continue
			}
			// Apply the same pool/generation selection when the manager's socket
			// disappears but detached work in other pools is still active.
			resp.Status = statusOf(store.View(), false)
			store.Close()
		}
		active := false
		if pool == "" {
			for _, im := range resp.Status.Images {
				if im.Phase == ImageOpen || im.Phase == ImageRemoving {
					active = true
				}
			}
		}
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
			return problem(ErrControl, "Waiting for drain was interrupted; the manager keeps draining.", "Inspect status; finish open image setup or pending removal. Use stop --force only when job termination is intended.")
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
	fmt.Fprintf(w, "Runmoor %s | manager running: %t | paused: %t | stopping: %t\n", s.Version, s.Running, s.Paused, s.Stopping)
	fmt.Fprintf(w, "Reserved: %d CPU, %d MiB, %d executions/setup VMs; macOS VMs: %d/2; pending cleanup: %d\n", s.Reserved.CPU, s.Reserved.MemoryMiB, s.Active, s.VMs, s.PendingCleanup)
	fmt.Fprintf(w, "Budget: %d CPU, %d MiB; concurrency: %d\n", s.Budget.CPU, s.Budget.MemoryMiB, s.Budget.MaxRunners)
	if validResources(s.DockerBudget) {
		fmt.Fprintf(w, "Docker engine budget: %d CPU, %d MiB\n", s.DockerBudget.CPU, s.DockerBudget.MemoryMiB)
	}
	if s.DockerCapacityPending {
		fmt.Fprintln(w, "Docker capacity is awaiting verification; new Docker work is paused until the engine is reachable.")
	}
	names := []string{}
	for name := range s.Managed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		q := s.Managed[name]
		version := "not prepared"
		if q.Current != nil {
			version = q.Current.RunnerVersion
		}
		fmt.Fprintf(w, "Managed runner %s: %s, mode=%s, version=%s, candidate=%s, last check=%s, next check=%s; %d CPU, %d MiB, concurrency=%d\n", name, q.Phase, q.Mode, version, q.CandidateVersion, q.LastCheck.Format(time.RFC3339), q.NextCheck.Format(time.RFC3339), q.Resources.CPU, q.Resources.MemoryMiB, q.MaxRunners)
		if q.Problem != nil {
			fmt.Fprintln(w, q.Problem.Error())
		}
	}
	for _, p := range s.Pools {
		fmt.Fprintf(w, "%s [%s] %s: demand=%d total=%d busy=%d; mode=%s version=%s, %d CPU, %d MiB, concurrency=%d\n", p.Name, p.Generation, p.Phase, p.Demand, p.Total, p.Busy, p.RunnerMode, p.RunnerVersion, p.Resources.CPU, p.Resources.MemoryMiB, p.MaxRunners)
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
	for _, im := range s.Images {
		if im.Phase != ImageSealed || im.Problem != nil {
			fmt.Fprintf(w, "Image %s: %s\n", im.ID, im.Phase)
			if im.Problem != nil {
				fmt.Fprintln(w, im.Problem.Error())
			}
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
