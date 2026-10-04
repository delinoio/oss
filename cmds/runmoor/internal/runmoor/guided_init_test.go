package runmoor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeGuidedRuntime struct {
	image       *Image
	done        chan error
	createCount int
	actions     []string
	probeReady  bool
	verifyFail  bool
	startError  error
	storage     Storage
	createFail  bool
	probe       func(context.Context) error
}

func (*fakeGuidedRuntime) Check(context.Context, Config) error { return nil }
func (f *fakeGuidedRuntime) Start(_ context.Context, _ string, c Config, _ io.Writer) (<-chan error, <-chan error) {
	f.storage = c.Storage
	f.done = make(chan error, 1)
	ready := make(chan error, 1)
	ready <- f.startError
	if f.startError != nil {
		f.done <- f.startError
	}
	return f.done, ready
}
func (f *fakeGuidedRuntime) Control(ctx context.Context, _ Config, req ControlRequest) (ControlResponse, error) {
	resp := ControlResponse{SchemaVersion: 1}
	switch req.Action {
	case "status":
		resp.Status = &Status{Running: true}
	case "images":
		if f.image != nil {
			resp.Images = []*Image{f.image}
		}
	case "stop":
		select {
		case f.done <- nil:
		default:
		}
	case "image":
		f.actions = append(f.actions, req.Image.Action)
		switch req.Image.Action {
		case "create":
			if req.Image.IPSW != "latest" {
				return resp, errors.New("unexpected IPSW")
			}
			f.createCount++
			f.image = &Image{ID: req.Image.ID, Phase: ImagePreparing, CreationComplete: !f.createFail}
			if f.createFail {
				return resp, problem(ErrPreparation, "Tart create failed after claiming the VM.", "Inspect the owned VM.")
			}
		case "open":
			f.image.Phase = ImageOpen
		case "close":
			if f.image != nil {
				f.image.Phase = ImagePreparing
			}
		case "probe":
			if f.probe != nil {
				return resp, f.probe(ctx)
			}
			if !f.probeReady {
				return resp, problem(ErrPreparation, "Guest not ready.", "Complete setup.")
			}
		case "verify-boot":
			if !f.probeReady {
				return resp, errors.New("guest verification skipped")
			}
			if f.verifyFail {
				return resp, problem(ErrPreparation, "Guest did not restart.", "Correct the login agent and retry.")
			}
		case "seal":
			f.image.Phase = ImageSealed
		}
		resp.Image = f.image
	}
	return resp, nil
}

func guidedFailureFixture(t *testing.T) (string, InitOptions) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Tart configuration requires an Apple Silicon Mac")
	}
	dir, err := os.MkdirTemp("/private/tmp", "rm-gd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "s"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "d"))
	return filepath.Join(dir, "config.toml"), InitOptions{Backend: string(Tart), Target: "https://github.com/example/repo", Auth: string(PAT), CredentialEnv: "RUNMOOR_TEST_UNUSED_PAT"}
}

func assertGuidedFailureRetained(t *testing.T, path string, fake *fakeGuidedRuntime) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed setup published a final configuration")
	}
	journal, _, _ := guidedPaths(path)
	if _, err := os.Stat(journal); err != nil {
		t.Fatal("failed setup discarded its owned image journal")
	}
	for _, action := range fake.actions {
		if action == "verify-boot" || action == "seal" {
			t.Fatal("failed readiness reached guest verification or sealing")
		}
	}
}

func TestGuidedInitObservesManagerExitDuringGuestSetup(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		for _, safeReason := range []bool{false, true} {
			t.Run(fmt.Sprintf("confirmed=%t/safe=%t", confirmed, safeReason), func(t *testing.T) {
				path, opts := guidedFailureFixture(t)
				fake := &fakeGuidedRuntime{}
				probes := 0
				fake.probe = func(context.Context) error {
					probes++
					if confirmed && probes == 1 {
						return problem(ErrPreparation, "Guest not ready.", "Complete setup.")
					}
					var exit error = errors.New("private manager failure details")
					if safeReason {
						exit = problem(ErrState, "Owned setup state is unavailable.", "Preserve setup state and retry.")
					}
					select {
					case fake.done <- exit:
					default:
					}
					return problem(ErrControl, "Cannot contact the local manager.", "Check manager status.")
				}
				input, writer := io.Pipe()
				defer input.Close()
				defer writer.Close()
				var reader io.Reader = input
				if confirmed {
					reader = strings.NewReader("\n")
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				var output bytes.Buffer
				err := startGuidedInit(ctx, path, opts, "latest", bufio.NewReader(reader), &output, fake)
				if safeReason {
					requireCode(t, err, ErrState)
					if !strings.Contains(err.Error(), "Owned setup state") {
						t.Fatal("safe manager failure reason was discarded", err)
					}
				} else {
					requireCode(t, err, ErrControl)
				}
				if strings.Contains(output.String(), "private manager") || strings.Contains(output.String(), "stop could not be confirmed") {
					t.Fatal("manager exit leaked details or attempted control after exit", output.String())
				}
				for _, action := range fake.actions {
					if action == "close" {
						t.Fatal("already exited manager received image cleanup")
					}
				}
				assertGuidedFailureRetained(t, path, fake)
			})
		}
	}
}

type guidedObservationWriter struct {
	bytes.Buffer
	observe func([]byte)
}

func (w *guidedObservationWriter) Write(body []byte) (int, error) {
	if w.observe != nil {
		w.observe(body)
	}
	return w.Buffer.Write(body)
}

func TestGuidedInitObservesManagerExitWhileAwaitingConfirmation(t *testing.T) {
	path, opts := guidedFailureFixture(t)
	fake := &fakeGuidedRuntime{probeReady: true}
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	output := &guidedObservationWriter{observe: func(body []byte) {
		if bytes.Contains(body, []byte("Guest Agent RPC is ready; waiting")) {
			fake.done <- nil
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := startGuidedInit(ctx, path, opts, "latest", bufio.NewReader(input), output, fake)
	requireCode(t, err, ErrControl)
	if !strings.Contains(err.Error(), "exited before VM setup") {
		t.Fatal("unexpected successful manager exit lost its setup diagnostic", err)
	}
	assertGuidedFailureRetained(t, path, fake)
}

func TestGuidedInitRetriesUnavailableAgentAndHonorsCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			path, opts := guidedFailureFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := &fakeGuidedRuntime{}
			probes := 0
			fake.probe = func(context.Context) error {
				probes++
				if canceled {
					cancel()
					return context.Canceled
				}
				if probes == 1 {
					return problem(ErrPreparation, "Guest not ready.", "Complete setup.")
				}
				fake.probeReady = true
				return nil
			}
			err := startGuidedInit(ctx, path, opts, "latest", bufio.NewReader(strings.NewReader("\n")), io.Discard, fake)
			if canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("setup cancellation was discarded", err)
				}
				assertGuidedFailureRetained(t, path, fake)
			} else if err != nil || fake.image.Phase != ImageSealed || probes != 2 {
				t.Fatal("transient readiness did not recover into a verified sealed image", err)
			}
		})
	}
}

func TestGuidedInitRejectsInvalidGuestAfterConfirmation(t *testing.T) {
	path, opts := guidedFailureFixture(t)
	fake := &fakeGuidedRuntime{probe: func(context.Context) error {
		return problem(ErrImage, "The macOS guest is not prepared for Runmoor.", "Install Guest Agent 0.14.2 in the runner account.")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := startGuidedInit(ctx, path, opts, "latest", bufio.NewReader(strings.NewReader("\n")), io.Discard, fake)
	requireCode(t, err, ErrImage)
	assertGuidedFailureRetained(t, path, fake)
}

func TestGuidedTartInitResumesAndProtectsConfiguration(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Tart configuration requires an Apple Silicon Mac")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storage, err := os.MkdirTemp("/private/tmp", "rm-init-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(storage) })
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_STATE_HOME", filepath.Join(storage, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(storage, "data"))
	defaultState := xdg("XDG_STATE_HOME", ".local/state")
	if err = os.MkdirAll(defaultState, 0700); err != nil {
		t.Fatal(err)
	}
	defaultDatabase := filepath.Join(defaultState, "state.sqlite")
	if err = os.WriteFile(defaultDatabase, []byte("existing installation"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	opts := InitOptions{Backend: string(Tart), Target: "https://github.com/example/repo", Auth: string(PAT), CredentialEnv: "RUNMOOR_PAT"}
	fake := &fakeGuidedRuntime{}
	var output bytes.Buffer
	err = initializeWithContext(context.Background(), path, opts, strings.NewReader("create\nlatest\n"), &output, true, fake)
	if err == nil {
		t.Fatal("missing setup confirmation was accepted")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("incomplete setup published a final configuration")
	}
	if fake.createCount != 1 || fake.image == nil || fake.image.Phase != ImagePreparing {
		t.Fatalf("interrupted setup lost or recreated the owned image: err=%v created=%d image=%+v output=%s", err, fake.createCount, fake.image, output.String())
	}
	if fake.storage.State == defaultState || filepath.Base(fake.storage.State) != "init-"+fake.image.ID || filepath.Base(fake.storage.Data) != "init-"+fake.image.ID {
		t.Fatalf("setup manager did not use private image storage: %+v", fake.storage)
	}
	if existing, e := os.ReadFile(defaultDatabase); e != nil || string(existing) != "existing installation" {
		t.Fatal("guided setup modified an existing installation")
	}
	_, _, staged := guidedPaths(path)
	originalStage, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(staged, bytes.ReplaceAll(originalStage, []byte("RUNMOOR_PAT"), []byte("OTHER_PAT")), 0600); err != nil {
		t.Fatal(err)
	}
	if err = initializeWithContext(context.Background(), path, InitOptions{}, strings.NewReader("\n"), io.Discard, true, fake); err == nil {
		t.Fatal("altered staged configuration was accepted")
	}
	if fake.createCount != 1 {
		t.Fatal("staged-config rejection mutated the image")
	}
	if err = os.WriteFile(staged, originalStage, 0600); err != nil {
		t.Fatal(err)
	}
	versionKey := bytes.Index(originalStage, []byte("runner_version ="))
	if versionKey < 0 {
		t.Fatal("staged configuration lacks a runner version")
	}
	versionLineEnd := versionKey + bytes.IndexByte(originalStage[versionKey:], '\n') + 1
	alteredStage := append(append([]byte{}, originalStage[:versionLineEnd]...), []byte("runner_path = '/Users/other/actions-runner'\n")...)
	alteredStage = append(alteredStage, originalStage[versionLineEnd:]...)
	if err = os.WriteFile(staged, alteredStage, 0600); err != nil {
		t.Fatal(err)
	}
	if altered, e := LoadConfig(staged); e != nil || altered.Pools[0].RunnerPath != "/Users/other/actions-runner" {
		t.Fatalf("runner-path fixture must be a valid changed config: %+v %v", altered.Pools, e)
	}
	beforeActions := len(fake.actions)
	if err = initializeWithContext(context.Background(), path, InitOptions{}, strings.NewReader("\n"), io.Discard, true, fake); err == nil || len(fake.actions) != beforeActions {
		t.Fatal("altered runner path was accepted or image work began")
	}
	if err = os.WriteFile(staged, originalStage, 0600); err != nil {
		t.Fatal(err)
	}
	_, bootstrap, _ := guidedPaths(path)
	originalBootstrap, err := os.ReadFile(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(bootstrap, append(append([]byte{}, originalBootstrap...), []byte("\n[logging]\nlevel = 'debug'\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if altered, e := LoadConfig(bootstrap); e != nil || altered.Logging.Level != "debug" {
		t.Fatalf("bootstrap fixture must be a valid changed config: %+v %v", altered.Logging, e)
	}
	if err = initializeWithContext(context.Background(), path, InitOptions{}, strings.NewReader("\n"), io.Discard, true, fake); err == nil || len(fake.actions) != beforeActions {
		t.Fatal("altered bootstrap configuration was accepted or image work began")
	}
	if err = os.WriteFile(bootstrap, originalBootstrap, 0600); err != nil {
		t.Fatal(err)
	}
	fake.probeReady = true
	fake.verifyFail = true
	if err = initializeWithContext(context.Background(), path, InitOptions{}, strings.NewReader("\n"), io.Discard, true, fake); err == nil {
		t.Fatal("failed headless guest verification was accepted")
	}
	if fake.image.Phase != ImagePreparing || fake.createCount != 1 {
		t.Fatal("failed verification did not retain the owned image for retry")
	}
	fake.verifyFail = false
	output.Reset()
	if err = initializeWithContext(context.Background(), path, InitOptions{}, strings.NewReader("\n"), &output, true, fake); err != nil {
		t.Fatal(err)
	}
	if fake.createCount != 1 || fake.image.Phase != ImageSealed {
		t.Fatal("resume did not reuse and seal the same image")
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Pools) != 1 || config.Pools[0].Image != fake.image.ID || config.Pools[0].RunnerVersion != LatestRunner {
		t.Fatal("final configuration does not select the sealed managed image")
	}
	if config.Storage != fake.storage {
		t.Fatal("final configuration lost the setup manager's isolated storage")
	}
	if !strings.Contains(strings.Join(fake.actions, ","), "verify-boot,seal") {
		t.Fatal("guest reboot verification did not precede sealing")
	}
	journal, bootstrap, finalStage := guidedPaths(path)
	for _, pending := range []string{journal, bootstrap, finalStage} {
		if _, err := os.Stat(pending); !os.IsNotExist(err) {
			t.Fatalf("pending setup file remained after success: %s", pending)
		}
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(original, []byte("runner_version = 'latest'")) && !bytes.Contains(original, []byte("runner_version = \"latest\"")) {
		t.Fatal("guided config did not explicitly request latest runner management")
	}
	if err = initializeWithContext(context.Background(), path, opts, strings.NewReader(""), io.Discard, false, fake); err == nil {
		t.Fatal("existing final configuration was overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("existing final configuration changed")
	}
}

func TestGuidedIPSWSelection(t *testing.T) {
	localIPSW := filepath.Join(t.TempDir(), "restore.ipsw")
	for _, input := range []string{"latest", localIPSW} {
		if !validGuidedIPSW(input) {
			t.Fatalf("rejected valid IPSW source %q", input)
		}
	}
	for _, input := range []string{"", "relative.ipsw", filepath.Join(t.TempDir(), "other.txt"), localIPSW + "\n"} {
		if validGuidedIPSW(input) {
			t.Fatalf("accepted invalid IPSW source %q", input)
		}
	}
}

func TestGuidedInitRefusesAmbiguousCreatedVM(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Tart configuration requires an Apple Silicon Mac")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storage, err := os.MkdirTemp("/private/tmp", "rm-a-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(storage) })
	t.Setenv("XDG_STATE_HOME", filepath.Join(storage, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(storage, "data"))
	path := filepath.Join(dir, "config.toml")
	opts := InitOptions{Backend: string(Tart), Target: "https://github.com/example/repo", Auth: string(PAT), CredentialEnv: "RUNMOOR_PAT"}
	fake := &fakeGuidedRuntime{createFail: true}
	firstErr := startGuidedInit(context.Background(), path, opts, "latest", bufio.NewReader(strings.NewReader("")), io.Discard, fake)
	if firstErr == nil {
		t.Fatal("failed Tart creation was accepted")
	}
	fake.createFail = false
	if err := resumeGuidedInit(context.Background(), path, strings.NewReader("\n"), io.Discard, fake); err == nil {
		t.Fatal("ambiguous image creation was resumed")
	}
	if fake.createCount != 1 || strings.Contains(strings.Join(fake.actions, ","), "open") {
		t.Fatalf("ambiguous image was recreated or opened: first=%v created=%d actions=%v", firstErr, fake.createCount, fake.actions)
	}
}

func TestGuidedStorageRejectsExistingPaths(t *testing.T) {
	root := t.TempDir()
	storage := Storage{State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := requireFreshGuidedStorage(storage); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{storage.State, storage.Data} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := requireFreshGuidedStorage(storage); err == nil {
			t.Fatalf("accepted preexisting guided storage: %s", path)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalidGuidedInputsDoNotPublishJournal(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Tart configuration requires an Apple Silicon Mac")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	opts := InitOptions{Backend: string(Tart), Auth: string(PAT), CredentialEnv: "RUNMOOR_PAT"}
	if err = startGuidedInit(context.Background(), path, opts, "latest", bufio.NewReader(strings.NewReader("")), io.Discard, &fakeGuidedRuntime{}); err == nil {
		t.Fatal("invalid GitHub target was accepted")
	}
	journal, _, _ := guidedPaths(path)
	if _, err = os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("invalid setup published a durable journal")
	}
}

func TestGuidedInitRefusesConflictingManagerBeforeImageWork(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Tart configuration requires an Apple Silicon Mac")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storage, err := os.MkdirTemp("/private/tmp", "rm-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(storage) })
	t.Setenv("XDG_STATE_HOME", filepath.Join(storage, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(storage, "data"))
	path := filepath.Join(dir, "config.toml")
	fake := &fakeGuidedRuntime{startError: problem(ErrLocked, "Another manager owns the state.", "Stop it before setup.")}
	opts := InitOptions{Backend: string(Tart), Target: "https://github.com/example/repo", Auth: string(PAT), CredentialEnv: "RUNMOOR_PAT"}
	err = startGuidedInit(context.Background(), path, opts, "latest", bufio.NewReader(strings.NewReader("")), io.Discard, fake)
	if err == nil || fake.createCount != 0 {
		t.Fatal("conflicting manager allowed image work")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("manager conflict published a final configuration")
	}
}

func TestImageCreatePassesLatestSupportedIPSWToTart(t *testing.T) {
	c, store := fixtureStore(t)
	driver, fixture := fakeTart(c)
	image, err := (&ImageManager{Store: store, Tart: driver}).Operate(context.Background(), c, ImageRequest{Action: "create", Name: "fresh-mac", IPSW: "latest", Resources: Resources{2, 4096}})
	if err != nil {
		t.Fatal(err)
	}
	if !image.CreationComplete {
		t.Fatal("successful Tart creation was not durably marked complete")
	}
	found := false
	for _, args := range fixture.commands {
		if len(args) == 4 && args[0] == "create" && args[1] == "--from-ipsw" && args[2] == "latest" && args[3] == creationVMName(image.ID) {
			found = true
		}
	}
	if !found {
		t.Fatal("image create did not pass latest to Tart")
	}
}

func TestTartImageOnlyInitKeepsMacOSDiskReserve(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Tart configuration requires an Apple Silicon Mac")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "image-only.toml")
	if err = initialize(path, InitOptions{Backend: string(Tart), ImageOnly: true}, strings.NewReader(""), io.Discard, false); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host.MinFreeDiskMiB != 20480 {
		t.Fatal("Tart image setup lost the macOS disk reserve")
	}
}

func TestGuidedImageRequiresReadyGuestAfterCleanBoot(t *testing.T) {
	c, store := fixtureStore(t)
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: store, Tart: driver}
	ctx := context.Background()
	image, err := images.Operate(ctx, c, ImageRequest{Action: "create", Name: "fresh-mac", IPSW: "latest", Resources: Resources{2, 4096}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = images.Operate(ctx, c, ImageRequest{Action: "open", ID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = images.Operate(ctx, c, ImageRequest{Action: "probe", ID: image.ID}); err != nil {
		t.Fatal(err)
	}
	before := len(fixture.commands)
	if _, err = images.Operate(ctx, c, ImageRequest{Action: "verify-boot", ID: image.ID}); err != nil {
		t.Fatal(err)
	}
	commands := fixture.commands[before:]
	stop, run, exec := -1, -1, -1
	for index, args := range commands {
		switch args[0] {
		case "stop":
			stop = index
		case "run":
			run = index
		case "exec":
			exec = index
		}
	}
	if stop < 0 || run <= stop || exec <= run {
		t.Fatal("guest was not validated after a stop and headless boot")
	}
}

func TestGuidedImageRejectsInvalidGuestAfterCleanBoot(t *testing.T) {
	c, store := fixtureStore(t)
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: store, Tart: driver}
	ctx := context.Background()
	image, err := images.Operate(ctx, c, ImageRequest{Action: "create", Name: "fresh-mac", IPSW: "latest", Resources: Resources{2, 4096}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = images.Operate(ctx, c, ImageRequest{Action: "open", ID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = images.Operate(ctx, c, ImageRequest{Action: "probe", ID: image.ID}); err != nil {
		t.Fatal(err)
	}
	fixture.rejectGuest = true
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = images.Operate(deadline, c, ImageRequest{Action: "verify-boot", ID: image.ID})
	p, ok := err.(*Problem)
	if !ok || p.Code != ErrImage {
		t.Fatalf("invalid guest after reboot must fail immediately with IMAGE_INVALID: %v", err)
	}
}
