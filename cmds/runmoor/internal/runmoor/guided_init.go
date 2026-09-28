package runmoor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

type guidedInitJournal struct {
	SchemaVersion int         `json:"schema_version"`
	ConfigPath    string      `json:"config_path"`
	ImageID       string      `json:"image_id"`
	IPSW          string      `json:"ipsw"`
	Storage       Storage     `json:"storage"`
	Options       InitOptions `json:"options"`
}

type guidedRuntime interface {
	Check(context.Context, Config) error
	Start(context.Context, string, Config, io.Writer) (<-chan error, <-chan error)
	Control(context.Context, Config, ControlRequest) (ControlResponse, error)
}

type defaultGuidedRuntime struct{}

func (defaultGuidedRuntime) Check(ctx context.Context, c Config) error {
	return (&TartDriver{Exec: OSCommand{}}).check(ctx, c)
}
func (defaultGuidedRuntime) Start(ctx context.Context, path string, c Config, output io.Writer) (<-chan error, <-chan error) {
	done := make(chan error, 1)
	ready := make(chan error, 1)
	go func() { done <- runForegroundReady(ctx, path, c, output, ready) }()
	return done, ready
}
func (defaultGuidedRuntime) Control(ctx context.Context, c Config, req ControlRequest) (ControlResponse, error) {
	return SendControl(ctx, c, req)
}

func guidedPaths(path string) (string, string, string) {
	return path + ".runmoor-init.json", path + ".runmoor-bootstrap.toml", path + ".runmoor-final.toml"
}

// Publish a complete owner-only file without replacing an existing path. The
// hard link is the atomic publication point; a crash before it leaves only a
// disposable temporary file and never a partially readable configuration.
func writePrivateExclusive(path string, body []byte) error {
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".runmoor-new-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func guidedJournalExists(path string) (bool, error) {
	journal, _, _ := guidedPaths(path)
	_, err := os.Lstat(journal)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, problem(ErrPermission, "Cannot inspect pending Tart setup.", "Check the configuration directory.")
}
func validGuidedIPSW(ipsw string) bool {
	return ipsw == "latest" || filepath.IsAbs(ipsw) && strings.HasSuffix(strings.ToLower(ipsw), ".ipsw") && !strings.ContainsAny(ipsw, "\x00\r\n")
}
func guidedStorage(id string) Storage {
	name := "init-" + id
	return Storage{
		State: filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), name),
		Data:  filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), name),
	}
}
func requireFreshGuidedStorage(storage Storage) error {
	for _, path := range []string{storage.State, storage.Data} {
		if _, err := os.Lstat(path); err == nil {
			return problem(ErrOwnership, "Guided Tart setup storage already exists.", "Choose a fresh configuration path and inspect the existing storage before retrying.")
		} else if !os.IsNotExist(err) {
			return problem(ErrPermission, "Cannot inspect guided Tart setup storage.", "Check access to the state and data directories before retrying.")
		}
	}
	return nil
}
func startGuidedInit(ctx context.Context, path string, opts InitOptions, ipsw string, reader *bufio.Reader, output io.Writer, runtime guidedRuntime) error {
	if !validGuidedIPSW(ipsw) || opts.ImageOnly || opts.Image != "" || opts.ImageSource != "" || opts.SourceHome != "" {
		return problem(ErrConfig, "Invalid guided Tart image setup.", "Use 'latest' or an absolute .ipsw path without another image selection.")
	}
	id := newID()
	opts.Image = id
	opts.RunnerVersion = LatestRunner
	opts.Storage = guidedStorage(id)
	if err := requireFreshGuidedStorage(opts.Storage); err != nil {
		return err
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	// Validate the complete final configuration before publishing a durable
	// setup record, so a typo cannot strand a first-run wizard before VM work.
	preflight := path + ".runmoor-preflight-" + id + ".toml"
	defer os.Remove(preflight)
	if err := initialize(preflight, opts, strings.NewReader(""), io.Discard, false); err != nil {
		return err
	}
	preflightConfig, err := LoadConfig(preflight)
	if err != nil {
		return err
	}
	if err = runtime.Check(ctx, preflightConfig); err != nil {
		return err
	}
	j := guidedInitJournal{SchemaVersion: 1, ConfigPath: path, ImageID: id, IPSW: ipsw, Storage: opts.Storage, Options: opts}
	journal, _, _ := guidedPaths(path)
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err = writePrivateExclusive(journal, body); err != nil {
		return err
	}
	return runGuidedInit(ctx, j, reader, output, runtime)
}
func resumeGuidedInit(ctx context.Context, path string, input io.Reader, output io.Writer, runtime guidedRuntime) error {
	journal, _, _ := guidedPaths(path)
	body, err := readPrivate(journal, 1<<20)
	if err != nil {
		return err
	}
	var j guidedInitJournal
	if json.Unmarshal(body, &j) != nil || j.SchemaVersion != 1 || j.ConfigPath != path || !validID(j.ImageID) || !validGuidedIPSW(j.IPSW) || j.Options.Backend != string(Tart) || j.Options.Image != j.ImageID || j.Options.ImageSource != "" || j.Options.ImageOnly || j.Options.RunnerVersion != LatestRunner || j.Options.Storage != j.Storage || filepath.Base(j.Storage.State) != "init-"+j.ImageID || filepath.Base(j.Storage.Data) != "init-"+j.ImageID {
		return problem(ErrConfig, "Pending Tart setup record is invalid.", "Preserve the setup files and inspect them before continuing.")
	}
	fmt.Fprintln(output, "Resuming the owned macOS image setup.")
	return runGuidedInit(ctx, j, bufio.NewReader(input), output, runtime)
}
func expectedGuidedConfig(path string, opts InitOptions) (Config, error) {
	temporary := path + ".runmoor-expected-" + newID() + ".toml"
	defer os.Remove(temporary)
	if err := initialize(temporary, opts, strings.NewReader(""), io.Discard, false); err != nil {
		return Config{}, err
	}
	return LoadConfig(temporary)
}
func ensureGuidedFiles(j guidedInitJournal) (Config, error) {
	_, bootstrap, final := guidedPaths(j.ConfigPath)
	if _, err := os.Lstat(final); os.IsNotExist(err) {
		if err = initialize(final, j.Options, strings.NewReader(""), io.Discard, false); err != nil {
			return Config{}, err
		}
	} else if err != nil {
		return Config{}, err
	}
	finalConfig, err := LoadConfig(final)
	if err != nil {
		return Config{}, err
	}
	expectedFinal, err := expectedGuidedConfig(j.ConfigPath, j.Options)
	if err != nil {
		return Config{}, err
	}
	if !reflect.DeepEqual(finalConfig, expectedFinal) {
		return Config{}, problem(ErrConfig, "Pending final configuration changed.", "Preserve the setup files and inspect them; Runmoor will not overwrite the file.")
	}
	bootstrapOptions := InitOptions{Backend: string(Tart), ImageOnly: true, Storage: j.Storage}
	if _, err = os.Lstat(bootstrap); os.IsNotExist(err) {
		if err = initialize(bootstrap, bootstrapOptions, strings.NewReader(""), io.Discard, false); err != nil {
			return Config{}, err
		}
	} else if err != nil {
		return Config{}, err
	}
	c, err := LoadConfig(bootstrap)
	if err != nil {
		return Config{}, err
	}
	expectedBootstrap, err := expectedGuidedConfig(j.ConfigPath, bootstrapOptions)
	if err != nil {
		return Config{}, err
	}
	if !reflect.DeepEqual(c, expectedBootstrap) {
		return Config{}, problem(ErrConfig, "Pending bootstrap configuration changed.", "Preserve the setup files and inspect them; Runmoor will not adopt another configuration.")
	}
	return c, nil
}
func guidedControl(ctx context.Context, runtime guidedRuntime, c Config, req ControlRequest) (ControlResponse, error) {
	resp, err := runtime.Control(ctx, c, req)
	if err != nil {
		return resp, err
	}
	if resp.Problem != nil {
		return resp, resp.Problem
	}
	return resp, nil
}
func guidedImage(ctx context.Context, runtime guidedRuntime, c Config, action, id string, extra ImageRequest) (*Image, error) {
	extra.Action, extra.ID = action, id
	resp, err := guidedControl(ctx, runtime, c, ControlRequest{Action: "image", Image: &extra})
	return resp.Image, err
}
func runGuidedInit(ctx context.Context, j guidedInitJournal, reader *bufio.Reader, output io.Writer, runtime guidedRuntime) (result error) {
	journal, bootstrap, final := guidedPaths(j.ConfigPath)
	if _, err := os.Lstat(j.ConfigPath); err == nil {
		actual, e := readPrivate(j.ConfigPath, 1<<20)
		expected, expectedErr := readPrivate(final, 1<<20)
		if e != nil || expectedErr != nil || !bytes.Equal(actual, expected) {
			return problem(ErrConfig, "Final configuration already exists.", "Runmoor will not overwrite a configuration that it cannot match to this setup.")
		}
		_ = os.Remove(journal)
		_ = os.Remove(bootstrap)
		_ = os.Remove(final)
		fmt.Fprintln(output, "Configuration is already complete.")
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	c, err := ensureGuidedFiles(j)
	if err != nil {
		return err
	}
	if err = runtime.Check(ctx, c); err != nil {
		return err
	}
	managerCtx, cancelManager := context.WithCancel(context.Background())
	done, ready := runtime.Start(managerCtx, bootstrap, c, output)
	stopped := false
	managerFinished := false
	defer func() {
		if stopped || managerFinished {
			cancelManager()
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if result != nil {
			if _, closeErr := guidedImage(cleanup, runtime, c, "close", j.ImageID, ImageRequest{}); closeErr != nil {
				if p, ok := closeErr.(*Problem); !ok || p.Code != ErrImage {
					fmt.Fprintln(output, "Setup VM could not be closed cleanly; inspect the owned image before resuming:", closeErr)
				}
			}
		}
		if _, stopErr := guidedControl(cleanup, runtime, c, ControlRequest{Action: "stop"}); stopErr != nil {
			fmt.Fprintln(output, "Setup manager stop could not be confirmed:", stopErr)
		}
		select {
		case <-done:
		case <-cleanup.Done():
			fmt.Fprintln(output, "Setup manager shutdown timed out; inspect the owned image before resuming.")
			cancelManager()
		}
		cancelManager()
	}()
	select {
	case err = <-ready:
		if err != nil {
			managerFinished = true
			return err
		}
	case <-time.After(15 * time.Second):
		return problem(ErrControl, "Setup manager did not become ready.", "Check the local manager lock and retry runmoor init.")
	case <-ctx.Done():
		return ctx.Err()
	}
	resp, err := guidedControl(ctx, runtime, c, ControlRequest{Action: "images"})
	if err != nil {
		return err
	}
	var im *Image
	for _, candidate := range resp.Images {
		if candidate.ID == j.ImageID {
			im = candidate
		}
	}
	if im == nil {
		fmt.Fprintln(output, "Creating a macOS VM from the selected Apple IPSW. This download may take time.")
		im, err = guidedImage(ctx, runtime, c, "create", j.ImageID, ImageRequest{Name: "macos", IPSW: j.IPSW})
		if err != nil {
			return err
		}
	}
	if im.Phase != ImageSealed {
		if !im.CreationComplete {
			return problem(ErrOwnership, "Setup VM creation did not complete conclusively.", "Inspect the journaled image and Tart VM; Runmoor will not resume or seal an uncertain creation.")
		}
		if im.Phase != ImagePreparing && im.Phase != ImageOpen {
			return problem(ErrImage, "Setup image is in an unexpected phase.", "Inspect 'runmoor image list' using the pending bootstrap configuration.")
		}
		if _, err = guidedImage(ctx, runtime, c, "open", j.ImageID, ImageRequest{}); err != nil {
			return err
		}
		fmt.Fprintln(output, "In the VM, finish macOS setup and log in as a non-root runner user.")
		fmt.Fprintln(output, "Install Tart Guest Agent 0.14.2 as a login agent with --run-agent (RPC); a normal Homebrew install may select another version.")
		fmt.Fprintln(output, "Install your required tools, accept any Xcode license, and keep the runner directory free of credentials and old workspaces.")
		fmt.Fprintln(output, "Guest Agent source and launch-agent guidance: https://github.com/openai/tart-guest-agent/tree/v0.14.2")
		fmt.Fprintln(output, "Press Enter here only when VM setup is complete. Runmoor will then reboot, verify and seal it.")
		confirmed := make(chan error, 1)
		go func() { _, e := reader.ReadString('\n'); confirmed <- e }()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		userDone, agentReady, reportedReady := false, false, false
		lastProblem := ""
		for !userDone || !agentReady {
			probe, stop := context.WithTimeout(ctx, 5*time.Second)
			_, e := guidedImage(probe, runtime, c, "probe", j.ImageID, ImageRequest{})
			stop()
			agentReady = e == nil
			if agentReady && !reportedReady {
				fmt.Fprintln(output, "Guest Agent RPC is ready; waiting for your VM setup confirmation.")
				reportedReady = true
			}
			if !agentReady && userDone && !reportedReady {
				fmt.Fprintln(output, "Waiting for the non-root Guest Agent 0.14.2 RPC to become ready.")
				reportedReady = true
			}
			if !agentReady && userDone && e != nil && e.Error() != lastProblem {
				fmt.Fprintln(output, e)
				lastProblem = e.Error()
			}
			if userDone && agentReady {
				break
			}
			select {
			case e := <-confirmed:
				if e != nil {
					return problem(ErrConfig, "VM setup confirmation was interrupted.", "Run runmoor init again to resume the owned image.")
				}
				userDone = true
			case <-ticker.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		fmt.Fprintln(output, "Rebooting the VM to verify Guest Agent starts without manual intervention.")
		if _, err = guidedImage(ctx, runtime, c, "verify-boot", j.ImageID, ImageRequest{}); err != nil {
			return err
		}
		fmt.Fprintln(output, "Installing the verified latest runner and sealing the image.")
		if _, err = guidedImage(ctx, runtime, c, "seal", j.ImageID, ImageRequest{}); err != nil {
			return err
		}
	}
	if _, err = guidedControl(ctx, runtime, c, ControlRequest{Action: "stop"}); err != nil {
		return err
	}
	select {
	case err = <-done:
		managerFinished = true
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	stopped = true
	body, err := readPrivate(final, 1<<20)
	if err != nil {
		return err
	}
	if err = writePrivateExclusive(j.ConfigPath, body); err != nil {
		return problem(ErrConfig, "Final configuration already exists or cannot be created.", "Preserve the sealed image and rerun init after resolving the path conflict.")
	}
	_ = os.Remove(journal)
	_ = os.Remove(bootstrap)
	_ = os.Remove(final)
	fmt.Fprintln(output, "Created configuration with a sealed macOS image. Run 'runmoor run' to start accepting work.")
	return nil
}
