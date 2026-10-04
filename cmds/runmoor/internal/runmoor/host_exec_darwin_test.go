//go:build darwin

package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func buildHostNativeHelper(t *testing.T) string {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "runmoor")
	cmd := exec.Command("go", "build", "-o", helper, "../..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build native fixture helper: %v: %s", err, out)
	}
	return helper
}

func hostNativeDirectory(t *testing.T, kind HostDirectoryKind) (Config, *Store, *os.Root, HostBootstrap) {
	t.Helper()
	c, store := fixtureStore(t)
	d, root, err := createHostDirectory(context.Background(), store, c, newID(), kind)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	for _, path := range []string{"runner/bin", "home", "tmp", "work"} {
		if err := root.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	in := HostBootstrap{Directory: d, Storage: c.Storage, JIT: "PRIVATE-JIT-FIXTURE", Deadline: time.Now().Add(time.Minute)}
	if kind == HostWorkspace {
		err = store.Update(func(s *Snapshot) error {
			s.Runners[d.ID] = &Runner{ID: d.ID, Phase: Preparing, Backend: Host, Deadline: in.Deadline}
			s.HostExecutions[d.ID] = &HostExecution{LaunchPending: true}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return c, store, root, in
}

func writeHostNativeFixture(t *testing.T, root *os.Root, path, body string) {
	t.Helper()
	f, err := root.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(body)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal("write native fixture", err, closeErr)
	}
}

func waitHostFixtureFile(t *testing.T, root *os.Root, path string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if body, err := root.ReadFile(path); err == nil {
			return body
		}
		if !waitContext(ctx, 10*time.Millisecond) {
			t.Fatal("native fixture did not publish its readiness marker")
		}
	}
}

func TestNativeHostVersion(t *testing.T) {
	helper := buildHostNativeHelper(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, script string
		code         ErrorCode
	}{
		{"match", "#!/bin/sh\nprintf '2.337.0\\n'\n", ""},
		{"mismatch", "#!/bin/sh\nprintf '2.336.0\\n'\n", ErrRunnerVersion},
		{"exit", "#!/bin/sh\nprintf 'RAW-SENSITIVE-FIXTURE' >&2\nexit 7\n", ErrPreparation},
		{"missing", "", ErrPreparation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, root, _ := hostNativeDirectory(t, HostDistribution)
			if tc.script != "" {
				writeHostNativeFixture(t, root, "runner/bin/Runner.Listener", tc.script)
			}
			var logs bytes.Buffer
			h := nativeHost{executable: helper, log: slog.New(slog.NewJSONHandler(&logs, nil))}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := h.Version(ctx, root, "2.337.0")
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireCode(t, err, tc.code)
			}
			if strings.Contains(logs.String(), "RAW-SENSITIVE") || err != nil && strings.Contains(err.Error(), "RAW-SENSITIVE") {
				t.Fatal("runner stderr escaped into diagnostics")
			}
			if tc.code == ErrPreparation && !strings.Contains(logs.String(), `"stage":"version"`) {
				t.Fatal("missing safe execution-stage diagnostic")
			}
		})
	}
	current, err := os.Getwd()
	if err != nil || current != cwd {
		t.Fatal("native version changed the manager working directory")
	}
}

func TestNativeHostDirectoryAnchorsAndOwnership(t *testing.T) {
	helper := buildHostNativeHelper(t)
	t.Run("renamed-and-replaced", func(t *testing.T) {
		c, _, root, in := hostNativeDirectory(t, HostDistribution)
		writeHostNativeFixture(t, root, "runner/bin/Runner.Listener", "#!/bin/sh\n[ \"$HOME\" = \"$(cd .. && pwd)/home\" ] || exit 4\nprintf '2.337.0\\n'\n")
		original := hostDirectoryPath(c, in.Directory)
		if err := os.Rename(original, original+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(original, 0700); err != nil {
			t.Fatal(err)
		}
		if err := (nativeHost{executable: helper}).Version(context.Background(), root, "2.337.0"); err != nil {
			t.Fatal("opened directory was redirected to its replacement", err)
		}
	})
	for _, change := range []string{"marker", "permissions", "runner-symlink", "entrypoint-symlink"} {
		t.Run(change, func(t *testing.T) {
			_, _, root, in := hostNativeDirectory(t, HostDistribution)
			writeHostNativeFixture(t, root, "runner/bin/Runner.Listener", "#!/bin/sh\nprintf '2.337.0\\n'\n")
			switch change {
			case "marker":
				in.Directory.Identity = "foreign"
				if err := hostRootWrite(root, hostOwnerFile, in.Directory); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := root.Chmod(".", 0755); err != nil {
					t.Fatal(err)
				}
			case "runner-symlink":
				if err := root.Rename("runner", "other"); err != nil {
					t.Fatal(err)
				}
				if err := root.Symlink("other", "runner"); err != nil {
					t.Fatal(err)
				}
			case "entrypoint-symlink":
				if err := root.Rename("runner/bin/Runner.Listener", "runner/bin/other"); err != nil {
					t.Fatal(err)
				}
				if err := root.Symlink("other", "runner/bin/Runner.Listener"); err != nil {
					t.Fatal(err)
				}
			}
			requireCode(t, (nativeHost{executable: helper}).Version(context.Background(), root, "2.337.0"), ErrOwnership)
		})
	}
	t.Run("invalid-handle", func(t *testing.T) {
		cmd := exec.Command(helper, "__host-exec", "version")
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err == nil {
			t.Fatal("private helper accepted missing inherited descriptors")
		}
	})
}

func TestNativeHostVersionCancellation(t *testing.T) {
	helper := buildHostNativeHelper(t)
	for _, timed := range []bool{false, true} {
		t.Run(strconv.FormatBool(timed), func(t *testing.T) {
			_, _, root, _ := hostNativeDirectory(t, HostDistribution)
			writeHostNativeFixture(t, root, "runner/bin/Runner.Listener", "#!/bin/sh\nprintf '%s' \"$$\" > .fixture-pid\nexec /bin/sleep 30\n")
			var ctx context.Context
			var cancel context.CancelFunc
			if timed {
				ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- (nativeHost{executable: helper}).Version(ctx, root, "2.337.0") }()
			body := waitHostFixtureFile(t, root, "runner/.fixture-pid")
			pid, err := strconv.Atoi(string(body))
			if err != nil {
				t.Fatal(err)
			}
			if !timed {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, ctx.Err()) {
					t.Fatalf("cancellation classification: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("version cancellation did not reap its child")
			}
			if _, err := nativeHostProcess(pid); !os.IsNotExist(err) {
				t.Fatal("cancelled version process remains alive", err)
			}
		})
	}
}

func TestNativeHostWorkerAndSupervisor(t *testing.T) {
	helper := buildHostNativeHelper(t)
	for _, supervisor := range []bool{false, true} {
		t.Run(strconv.FormatBool(supervisor), func(t *testing.T) {
			_, _, root, in := hostNativeDirectory(t, HostWorkspace)
			writeHostNativeFixture(t, root, "runner/run.sh", "#!/bin/sh\n[ \"$1\" = '--jitconfig' ] && [ \"$2\" = 'PRIVATE-JIT-FIXTURE' ] || exit 7\nprintf '%s' \"$$\" > .fixture-pid\nexec /bin/sleep 30\n")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var process HostProcess
			var done <-chan int
			if supervisor {
				err := (nativeHost{executable: helper}).Launch(ctx, root, in, func(p HostProcess) error { process = p; return nil })
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				process, done, err = (nativeHostWorker{executable: helper}).Start(ctx, root, in)
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { _ = signalHostProcess(process, true) })
			pidBody := waitHostFixtureFile(t, root, "runner/.fixture-pid")
			pid, _ := strconv.Atoi(string(pidBody))
			if !supervisor && (process.PID != pid || process.Start == "" || process.Group != pid) {
				t.Fatal("exec did not preserve the owned worker identity")
			}
			if supervisor {
				for {
					body, err := root.ReadFile("status.json")
					var status HostExecutionStatus
					if err == nil && json.Unmarshal(body, &status) == nil && status.Phase == HostRunning {
						if status.Worker.PID != pid {
							t.Fatal("supervisor published a different worker")
						}
						break
					}
					if !waitContext(ctx, 10*time.Millisecond) {
						t.Fatal("supervisor never confirmed worker startup")
					}
				}
			}
			if err := signalHostProcess(process, false); err != nil {
				t.Fatal(err)
			}
			if done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("worker did not exit")
				}
			} else {
				for {
					alive, err := (nativeHost{}).Alive(process)
					if err != nil {
						t.Fatal(err)
					}
					if !alive {
						break
					}
					if !waitContext(ctx, 10*time.Millisecond) {
						t.Fatal("supervisor cleanup did not complete")
					}
				}
			}
			if _, err := nativeHostProcess(pid); !os.IsNotExist(err) {
				t.Fatal("native worker was not reaped", err)
			}
		})
	}
}

func TestNativeHostWorkerRejectsDurableStop(t *testing.T) {
	helper := buildHostNativeHelper(t)
	_, store, root, in := hostNativeDirectory(t, HostWorkspace)
	writeHostNativeFixture(t, root, "runner/run.sh", "#!/bin/sh\nprintf unexpected > .fixture-started\n")
	// Build the command before Stop so the child, rather than the parent's
	// existing precheck, must reject the later durable decision.
	cmd, outcome, closeFiles, err := hostRunnerCommand(context.Background(), helper, hostExecWorker, root, in)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFiles()
	cmd.Env = hostEnvironment(root.Name())
	if err := store.Update(func(s *Snapshot) error { s.Runners[in.Directory.ID].Forced = true; return nil }); err != nil {
		t.Fatal(err)
	}
	err = cmd.Start()
	closeHostChildFiles(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil || hostReadExecOutcome(outcome) != hostExecStale {
		t.Fatal("child accepted a durable stop")
	}
	if _, err := root.Stat("runner/.fixture-started"); !os.IsNotExist(err) {
		t.Fatal("stopped runner executed code")
	}
}

func TestNativeHostWorkerFailureAndCancellation(t *testing.T) {
	helper := buildHostNativeHelper(t)
	for _, tc := range []string{"missing", "not-executable", "symlink", "cancelled"} {
		t.Run(tc, func(t *testing.T) {
			_, _, root, in := hostNativeDirectory(t, HostWorkspace)
			if tc != "missing" {
				writeHostNativeFixture(t, root, "runner/run.sh", "#!/bin/sh\nprintf unexpected > .fixture-started\n")
			}
			switch tc {
			case "not-executable":
				if err := root.Chmod("runner/run.sh", 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := root.Rename("runner/run.sh", "runner/other"); err != nil {
					t.Fatal(err)
				}
				if err := root.Symlink("other", "runner/run.sh"); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if tc == "cancelled" {
				cancel()
			}
			process, done, err := (nativeHostWorker{executable: helper}).Start(ctx, root, in)
			if tc == "cancelled" {
				if !errors.Is(err, context.Canceled) || done != nil || process.PID != 0 {
					t.Fatal("cancelled preparation spawned a worker")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = signalHostProcess(process, true) })
			expected := 100 + int(hostExecRun)
			if tc == "symlink" {
				expected = 100 + int(hostExecOwnership)
			}
			select {
			case code := <-done:
				if code != expected {
					t.Fatalf("unsafe/missing failure classification: %d", code)
				}
			case <-ctx.Done():
				t.Fatal("failed worker was not reaped")
			}
			if _, err := root.Stat("runner/.fixture-started"); !os.IsNotExist(err) {
				t.Fatal("invalid runner entrypoint executed code")
			}
		})
	}
}

func TestOfficialHostRunnerVersion(t *testing.T) {
	if os.Getenv("RUNMOOR_HOST_VERSION_TEST") != "1" {
		t.Skip("set RUNMOOR_HOST_VERSION_TEST=1 to verify an official macOS arm64 runner without registration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	helper := buildHostNativeHelper(t)
	c, store := fixtureStore(t)
	client := defaultReleaseClient()
	releases, err := fetchRunnerReleases(ctx, client)
	if err != nil || len(releases) == 0 {
		t.Fatal("official release lookup failed", err)
	}
	release := releases[0]
	a := RunnerArtifact{ID: newID(), Backend: Host, Phase: ArtifactPreparing, Pool: "macos-host"}
	if err := store.Update(func(s *Snapshot) error { s.Artifacts[a.ID] = &a; return nil }); err != nil {
		t.Fatal(err)
	}
	p := Pool{Name: a.Pool, Backend: Host, Arch: "arm64"}
	builder := ManagedImageBuilder{Store: store, Client: client, Host: nativeHost{executable: helper}}
	resolved, err := builder.prepareHost(ctx, c, p, a, release)
	if err != nil {
		t.Fatal("official archive/version preparation failed", err)
	}
	if resolved.RunnerVersion != release.Version() || store.View().HostDirectories[a.ID].Digest == "" {
		t.Fatal("official version was not sealed")
	}
	t.Logf("Verified official macOS arm64 archive and --version %s; no registration or job execution", resolved.RunnerVersion)
}
