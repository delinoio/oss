//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests execute the actual ccow binary and native process ownership on the
// current host. Cargo/rustc are deterministic fixtures, not tool-floor acceptance.
type fixture struct {
	t                          *testing.T
	dir, bin, main, work, home string
	env                        []string
}

func writeTest(t *testing.T, p, text string, mode os.FileMode) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(text), mode); e != nil {
		t.Fatal(e)
	}
}
func gitTest(t *testing.T, cwd string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git %v: %s %v", args, b, e)
	}
}

var testBinary string

func TestMain(m *testing.M) {
	dir, e := os.MkdirTemp("", "ccow-executable-tests-")
	if e != nil {
		panic(e)
	}
	testBinary = filepath.Join(dir, "ccow")
	cmd := exec.Command("go", "build", "-race", "-o", testBinary, "../..")
	if b, e := cmd.CombinedOutput(); e != nil {
		fmt.Fprintf(os.Stderr, "build: %s %v", b, e)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	base, e := filepath.EvalSymlinks(base)
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{t: t, dir: base, bin: filepath.Join(base, "bin"), main: filepath.Join(base, "main"), work: filepath.Join(base, "work 한 space"), home: filepath.Join(base, "home")}
	for _, p := range []string{f.main, f.home, f.bin} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	writeTest(t, filepath.Join(f.bin, "cargo"), `#!/bin/sh
case "$1" in
 --version) echo 'cargo 1.99.1 (fixture)' ;;
 metadata) target="${CARGO_TARGET_DIR:-$PWD/target}"; printf '{"version":1,"workspace_root":"%s","target_directory":"%s","build_directory":"%s"}\n' "$PWD" "$target" "$target" ;;
 clean) rm -rf target; printf 'clean:%s\n' "$*" ;;
 *) mkdir -p target; if [ -f scenario ]; then exec /bin/sh "$PWD/scenario" "$@"; fi; printf 'source artifact' > target/data ;;
esac
`, 0700)
	writeTest(t, filepath.Join(f.bin, "rustc"), "#!/bin/sh\nprintf 'rustc 1.99.1 (fixture)\\nhost: aarch64-apple-darwin\\nrelease: 1.99.1\\n'\n", 0700)
	writeTest(t, filepath.Join(f.main, "Cargo.toml"), "[package]\nname='fixture'\nversion='0.1.0'\n", 0600)
	writeTest(t, filepath.Join(f.main, ".gitignore"), "target/\n", 0600)
	gitTest(t, f.main, "init")
	gitTest(t, f.main, "add", ".")
	gitTest(t, f.main, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture")
	gitTest(t, f.main, "worktree", "add", "-b", "derived", f.work)
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		switch key {
		case "HOME", "XDG_CONFIG_HOME", "CARGO_HOME", "PATH", "RUSTC", "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER", "CCOW_SESSION":
			continue
		}
		f.env = append(f.env, v)
	}
	f.env = append(f.env, "HOME="+f.home, "XDG_CONFIG_HOME="+filepath.Join(f.home, "config"), "CARGO_HOME="+filepath.Join(f.home, "cargo"), "PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "NO_COLOR=1")
	return f
}
func (f *fixture) cmd(cwd string, args ...string) *exec.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	f.t.Cleanup(cancel)
	c := exec.CommandContext(ctx, testBinary, args...)
	c.Dir = cwd
	c.Env = f.env
	return c
}
func (f *fixture) run(cwd string, want int, args ...string) (string, string) {
	f.t.Helper()
	return f.runEnv(cwd, nil, want, args...)
}

// runEnv runs ccow with extra environment entries that apply to this invocation only.
func (f *fixture) runEnv(cwd string, extra []string, want int, args ...string) (string, string) {
	f.t.Helper()
	c := f.cmd(cwd, args...)
	c.Env = append(append([]string(nil), f.env...), extra...)
	var out, errout bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errout
	e := c.Run()
	code := 0
	if e != nil {
		if c.ProcessState == nil {
			f.t.Fatal(e)
		}
		code = c.ProcessState.ExitCode()
	}
	if code != want {
		f.t.Fatalf("ccow %v: exit %d want %d\nstdout=%s\nstderr=%s", args, code, want, out.String(), errout.String())
	}
	return out.String(), errout.String()
}
func TestExecutableOutputExitAndCWD(t *testing.T) {
	f := newFixture(t)
	out, _ := f.run(f.main, 0, "--version")
	if out != "ccow 0.1.0\n" {
		t.Fatal(out)
	}
	writeTest(t, filepath.Join(f.main, "scenario"), "printf 'OUT:%s|%s' \"$PWD\" \"$*\"; printf 'ERR' >&2; exit 37\n", 0600)
	out, errout := f.run(f.main, 37, "run", "--", "cargo", "build", "space arg", "--json")
	if out != "OUT:"+f.main+"|build space arg --json" || errout != "ERR" {
		t.Fatalf("streams %q %q", out, errout)
	}
}
func TestExecutablePreparationReplacementAndFailure(t *testing.T) {
	f := newFixture(t)
	f.run(f.main, 0, "run", "--", "cargo", "build")
	f.run(f.work, 0, "prepare", "--allow-copy", "--", "cargo", "build")
	p := filepath.Join(f.work, "target", "data")
	writeTest(t, p, "local", 0600)
	f.run(f.work, 0, "prepare", "--", "cargo", "build")
	b, _ := os.ReadFile(p)
	if string(b) != "local" {
		t.Fatal("replaced warm cache")
	}
	f.run(f.work, 0, "prepare", "--replace", "--allow-copy", "--", "cargo", "build")
	b, _ = os.ReadFile(p)
	if string(b) != "source artifact" {
		t.Fatal(string(b))
	}
	writeTest(t, p, "preserve", 0600)
	if e := os.Symlink(f.home, filepath.Join(f.main, "target", "bad-link")); e != nil {
		t.Fatal(e)
	}
	f.run(f.work, 125, "prepare", "--replace", "--allow-copy", "--", "cargo", "build")
	b, _ = os.ReadFile(p)
	if string(b) != "preserve" {
		t.Fatal("failed clone destroyed cache")
	}
	entries, _ := os.ReadDir(f.work)
	for _, v := range entries {
		if strings.Contains(v.Name(), ".ccow-") {
			t.Fatal("staging leaked", v.Name())
		}
	}
}
func TestExecutableNoninteractiveMissingAndSettings(t *testing.T) {
	f := newFixture(t)
	out, _ := f.run(f.work, 125, "prepare", "--json")
	if !strings.Contains(out, `"schemaVersion":1`) || !strings.Contains(out, `"success":false`) {
		t.Fatal(out)
	}
	f.run(f.work, 0, "config", "set", "source", f.main)
	out, _ = f.run(f.work, 0, "config", "get", "source", "--json")
	if !strings.Contains(out, f.main) {
		t.Fatal(out)
	}
	f.run(f.work, 0, "config", "unset", "source")
	if _, e := os.Stat(filepath.Join(f.work, "target")); !os.IsNotExist(e) {
		t.Fatal("prepare invented a build")
	}
}
func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(path); e == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting", path)
}
func TestExecutableContentionAndParentDeath(t *testing.T) {
	f := newFixture(t)
	writeTest(t, filepath.Join(f.main, "scenario"), "trap '' TERM\nprintf '%s' $$ > target/pid\ntouch target/ready\nwhile :; do sleep 1; done\n", 0600)
	c := f.cmd(f.main, "run", "--", "cargo", "build")
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { c.Process.Kill(); c.Wait() }()
	waitFile(t, filepath.Join(f.main, "target", "ready"))
	f.run(f.main, 125, "clean", "--preset", "cargo")
	start := time.Now()
	if e := c.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = c.Wait()
	// A second writer must wait through the first supervisor's five-second drain.
	writeTest(t, filepath.Join(f.main, "scenario"), "printf 'after'\n", 0600)
	f.run(f.main, 0, "run", "--", "cargo", "build")
	if time.Since(start) < 4*time.Second {
		t.Fatal("released lease before descendant shutdown")
	}
	b, _ := os.ReadFile(filepath.Join(f.main, "target", "pid"))
	var pid int
	fmt.Sscan(string(b), &pid)
	if pid > 1 && syscall.Kill(pid, 0) == nil {
		t.Fatal("writer survived parent death")
	}
}
func TestExecutableNestedSameAndDifferentCache(t *testing.T) {
	f := newFixture(t)
	// A project in the same primary checkout is an independently managed cache.
	sub := filepath.Join(f.main, "member")
	writeTest(t, filepath.Join(sub, "Cargo.toml"), "[package]\nname='member'\nversion='0.1.0'\n", 0600)
	writeTest(t, filepath.Join(sub, "scenario"), "printf 'different-cache'\n", 0600)
	writeTest(t, filepath.Join(f.main, "scenario"), fmt.Sprintf("if [ \"$2\" = inner ]; then printf 'same-cache|'; exit 0; fi\n'%s' run -- cargo build inner || exit $?\ncd ./member || exit 88\nexec '%s' run -- cargo build\n", testBinary, testBinary), 0600)
	out, _ := f.run(f.main, 0, "run", "--", "cargo", "build")
	if out != "same-cache|different-cache" {
		t.Fatal(out)
	}
}
func TestExecutableForgedSessionRejected(t *testing.T) {
	f := newFixture(t)
	f.env = append(f.env, "CCOW_SESSION=/tmp/not-a-session|"+strings.Repeat("a", 64))
	f.run(f.main, 125, "run", "--", "cargo", "build")
}
func TestExecutablePassthroughAndCargoClean(t *testing.T) {
	f := newFixture(t)
	f.run(f.main, 0, "shim", "install")
	stateBase := filepath.Join(f.home, "Library", "Application Support", "cachecow")
	if _, e := os.Stat(stateBase); e != nil {
		stateBase = filepath.Join(f.home, "config", "cachecow")
	}
	f.env = append(f.env, "PATH="+filepath.Join(stateBase, "shims")+string(os.PathListSeparator)+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := exec.Command(filepath.Join(stateBase, "shims", "cargo"), "--version")
	c.Dir = f.work
	c.Env = f.env
	b, e := c.CombinedOutput()
	if e != nil || string(b) != "cargo 1.99.1 (fixture)\n" {
		t.Fatal(string(b), e)
	}
	f.run(f.main, 0, "run", "--", "cargo", "build")
	c = exec.Command(filepath.Join(stateBase, "shims", "cargo"), "clean", "--release")
	c.Dir = f.main
	c.Env = f.env
	b, e = c.CombinedOutput()
	if e != nil || string(b) != "clean:clean --release\n" {
		t.Fatal(string(b), e)
	}
	if _, e = os.Stat(filepath.Join(f.main, "target")); !os.IsNotExist(e) {
		t.Fatal("cargo clean did not retain its semantics")
	}
}
func TestArgumentErrors(t *testing.T) {
	for _, args := range [][]string{{"run", "--wat"}, {"run", "--source"}, {"run", "--replace=true"}, {"--preset", "unknown"}} {
		if _, e := parse(args); e == nil {
			t.Fatal(args)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx
}

func TestInteractiveSourceChoices(t *testing.T) {
	for _, choice := range []string{"current", "source", "select", "cancel", "prepare"} {
		t.Run(choice, func(t *testing.T) {
			f := newFixture(t)
			args := []string{"run", "--allow-copy", "--", "cargo", "build"}
			answers := "1\n"
			want := 0
			switch choice {
			case "source":
				answers = "3\ny\n"
				// Tools such as direnv export the worktree path; the source build must still be accepted.
				f.env = append(f.env, "DIRENV_DIR=-"+f.work)
			case "select":
				f.run(f.main, 0, "run", "--", "cargo", "build")
				args = []string{"run", "--source", filepath.Join(f.dir, "missing"), "--allow-copy", "--", "cargo", "build"}
				answers = "2\n" + f.main + "\n"
			case "cancel":
				answers = "4\n"
				want = 125
			case "prepare":
				args = []string{"prepare", "--", "cargo", "build"}
				answers = "4\n"
				want = 125
			}
			master, slave := terminalInput(t)
			c := f.cmd(f.work, args...)
			c.Stdin = slave
			var out, stderr bytes.Buffer
			c.Stdout = &out
			c.Stderr = &stderr
			if e := c.Start(); e != nil {
				t.Fatal(e)
			}
			master.WriteString(answers)
			e := c.Wait()
			code := 0
			if e != nil {
				code = c.ProcessState.ExitCode()
			}
			if code != want {
				t.Fatalf("exit %d want %d: %s", code, want, stderr.String())
			}
			if choice == "source" {
				if b, _ := os.ReadFile(filepath.Join(f.work, "target", "data")); string(b) != "source artifact" {
					t.Fatalf("derived worktree did not clone the rebuilt source cache: %q\n%s", b, stderr.String())
				}
			}
			if choice == "source" && (!strings.Contains(stderr.String(), "Exact requested argv") || !strings.Contains(stderr.String(), f.main)) {
				t.Fatal("missing exact source-build confirmation", stderr.String())
			}
			if choice == "prepare" && strings.Contains(stderr.String(), "3 build source") {
				t.Fatal("prepare offered an invented build")
			}
		})
	}
}
func TestInheritedCapabilityCannotImpersonateScope(t *testing.T) {
	f := newFixture(t)
	writeTest(t, filepath.Join(f.main, "scenario"), "printf '%s' \"$CCOW_SESSION\" > target/capability\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n", 0600)
	c := f.cmd(f.main, "run", "--", "cargo", "build")
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { c.Process.Kill(); c.Wait() }()
	path := filepath.Join(f.main, "target", "capability")
	waitFile(t, path)
	capability, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	f.env = append(f.env, "CCOW_SESSION="+string(capability))
	_, stderr := f.run(f.main, 125, "run", "--", "cargo", "build")
	if !strings.Contains(stderr, "authentication failed") {
		t.Fatal(stderr)
	}
}
func TestPrepareCannotCertifyUnknownSource(t *testing.T) {
	f := newFixture(t)
	writeTest(t, filepath.Join(f.main, "target", "data"), "unrecorded", 0600)
	f.run(f.main, 0, "prepare", "--", "cargo", "build")
	f.run(f.work, 125, "prepare", "--allow-copy", "--", "cargo", "build")
}

func TestSourceReadLeaseEndsBeforeDerivedBuild(t *testing.T) {
	f := newFixture(t)
	f.run(f.main, 0, "run", "--", "cargo", "build")
	writeTest(t, filepath.Join(f.work, "scenario"), "touch target/ready\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n", 0600)
	c := f.cmd(f.work, "run", "--allow-copy", "--", "cargo", "build")
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { c.Process.Kill(); c.Wait() }()
	waitFile(t, filepath.Join(f.work, "target", "ready"))
	// The source can build after cloning completes, while the derived writer lives.
	f.run(f.main, 0, "run", "--", "cargo", "build")
	if e := c.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	if e := c.Wait(); e != nil {
		t.Fatalf("derived shutdown: %v %s", e, stderr.String())
	}
}
func TestCargoCleanWaitsWithoutPreparing(t *testing.T) {
	f := newFixture(t)
	f.run(f.main, 0, "shim", "install")
	stateBase := filepath.Join(f.home, "Library", "Application Support", "cachecow")
	if _, e := os.Stat(stateBase); e != nil {
		stateBase = filepath.Join(f.home, "config", "cachecow")
	}
	writeTest(t, filepath.Join(f.main, "scenario"), "touch target/ready\nsleep 4\n", 0600)
	build := f.cmd(f.main, "run", "--", "cargo", "build")
	if e := build.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { build.Process.Kill(); build.Wait() }()
	waitFile(t, filepath.Join(f.main, "target", "ready"))
	clean := exec.Command(filepath.Join(stateBase, "shims", "cargo"), "clean", "--release")
	clean.Dir = f.main
	clean.Env = f.env
	start := time.Now()
	b, e := clean.CombinedOutput()
	if e != nil || !strings.Contains(string(b), "clean:clean --release") {
		t.Fatal(string(b), e)
	}
	if time.Since(start) < 2*time.Second {
		t.Fatal("cargo clean did not wait")
	}
	if _, e = os.Stat(filepath.Join(f.main, "target")); !os.IsNotExist(e) {
		t.Fatal("clean prepared or retained cache")
	}
}

func TestExplicitCargoPathDiscovery(t *testing.T) {
	f := newFixture(t)
	// PATH Cargo is supported, but the requested executable is not. Querying
	// PATH would certify its target directory and launch this unrelated wrapper.
	writeTest(t, filepath.Join(f.main, "custom-cargo"), "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'cargo 1.98.0'; else touch WRONG-TOOL-RAN; fi\n", 0700)
	_, diagnostic := f.run(f.main, 125, "run", "--preset", "cargo", "--", "./custom-cargo", "build")
	if !strings.Contains(diagnostic, "1.99.1") {
		t.Fatal(diagnostic)
	}
	if _, e := os.Stat(filepath.Join(f.main, "WRONG-TOOL-RAN")); !os.IsNotExist(e) {
		t.Fatal("launched unverified tool")
	}
}

func TestNestedCancellationDrainsOuterScope(t *testing.T) {
	f := newFixture(t)
	writeTest(t, filepath.Join(f.main, "scenario"), fmt.Sprintf(`if [ "$2" = inner ]; then
  trap 'touch target/graceful; trap "" TERM' TERM
  (trap '' TERM; while :; do sleep 1; done) &
  touch target/inner-ready
  while :; do sleep 1; done
fi
trap '' TERM
'%s' run -- cargo build inner &
echo $! > target/nested-pid
wait
`, testBinary), 0600)
	c := f.cmd(f.main, "run", "--", "cargo", "build")
	var diagnostics bytes.Buffer
	c.Stderr = &diagnostics
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	defer c.Process.Kill()
	waitFile(t, filepath.Join(f.main, "target", "inner-ready"))
	b, e := os.ReadFile(filepath.Join(f.main, "target", "nested-pid"))
	if e != nil {
		t.Fatal(e)
	}
	var pid int
	if _, e := fmt.Sscan(string(b), &pid); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e := syscall.Kill(pid, syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	waitFile(t, filepath.Join(f.main, "target", "graceful"))
	// The child deliberately ignores subsequent TERM signals. Protection must
	// remain active during the graceful interval, before the forced drain.
	f.run(f.main, 125, "clean")
	_ = c.Wait()
	if elapsed := time.Since(start); elapsed < 4*time.Second || elapsed > 9*time.Second {
		t.Fatalf("nested cancellation took %s: %s", elapsed, diagnostics.String())
	}
	f.run(f.main, 0, "clean")
}

func TestPrepareWaitsForSourceProvenance(t *testing.T) {
	f := newFixture(t)
	writeTest(t, filepath.Join(f.main, "scenario"), "touch target/ready\nwhile [ ! -f target/release ]; do sleep 0.05; done\nprintf 'finished source' > target/data\n", 0600)
	source := f.cmd(f.main, "run", "--", "cargo", "build")
	if err := source.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { source.Process.Kill(); source.Wait() }()
	waitFile(t, filepath.Join(f.main, "target", "ready"))
	waiter := f.cmd(f.work, "prepare", "--allow-copy", "--preset", "cargo", "--", "cargo", "build")
	var stderr bytes.Buffer
	waiter.Stderr = &stderr
	if err := waiter.Start(); err != nil {
		t.Fatal(err)
	}
	defer waiter.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- waiter.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("source waiter exited before build completion: %v %s", err, &stderr)
	case <-time.After(800 * time.Millisecond):
	}
	writeTest(t, filepath.Join(f.main, "target", "release"), "release", 0600)
	if err := source.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("waiter: %v %s", err, &stderr)
	}
	data, err := os.ReadFile(filepath.Join(f.work, "target", "data"))
	if err != nil || string(data) != "finished source" {
		t.Fatalf("incomplete source clone: %q %v", data, err)
	}
}

func TestWaitingCommandRejectsChangedCargoConfiguration(t *testing.T) {
	f := newFixture(t)
	config := filepath.Join(f.main, ".cargo", "config.toml")
	writeTest(t, config, "[build]\ntarget-dir='target'\n", 0600)
	writeTest(t, filepath.Join(f.main, "scenario"), "touch target/ready\nwhile [ ! -f target/release ]; do sleep 0.05; done\n", 0600)
	owner := f.cmd(f.main, "run", "--", "cargo", "build")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { owner.Process.Kill(); owner.Wait() }()
	waitFile(t, filepath.Join(f.main, "target", "ready"))
	waiter := f.cmd(f.main, "run", "--", "cargo", "build")
	var stderr bytes.Buffer
	waiter.Stderr = &stderr
	if err := waiter.Start(); err != nil {
		t.Fatal(err)
	}
	defer waiter.Process.Kill()
	// A second operation journal proves discovery finished and the supervisor
	// received the plan before the configuration changes.
	deadline := time.Now().Add(10 * time.Second)
	for {
		plans, _ := filepath.Glob(filepath.Join(f.home, "config", "cachecow", "processes", "*", "operation.json"))
		macPlans, _ := filepath.Glob(filepath.Join(f.home, "Library", "Application Support", "cachecow", "processes", "*", "operation.json"))
		plans = append(plans, macPlans...)
		if len(plans) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiter did not finish discovery")
		}
		time.Sleep(20 * time.Millisecond)
	}
	writeTest(t, config, "[build]\ntarget-dir='other'\n", 0600)
	writeTest(t, filepath.Join(f.main, "target", "release"), "release", 0600)
	if err := owner.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := waiter.Wait(); err == nil || waiter.ProcessState.ExitCode() != 125 {
		t.Fatalf("waiter did not reject changed configuration: %v", err)
	}
	if !strings.Contains(stderr.String(), "cache-selection configuration changed") {
		t.Fatal(stderr.String())
	}
}

func TestLookPathUsesTranslatedPath(t *testing.T) {
	dir := t.TempDir()
	writeTest(t, filepath.Join(dir, "bin", "tool"), "#!/bin/sh\n", 0700)
	got, err := lookPath("tool", []string{"PATH=" + filepath.Join(dir, "missing") + string(os.PathListSeparator) + filepath.Join(dir, "bin")})
	if err != nil || got != filepath.Join(dir, "bin", "tool") {
		t.Fatal(got, err)
	}
	if _, err := lookPath("tool", []string{"PATH=" + filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("found a tool outside the translated PATH")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

// Settings that differ only by the worktree root must not make the source cache incompatible.
func TestDerivedWorktreeAcceptsSourceWithWorktreePathSettings(t *testing.T) {
	type variant struct {
		name string
		// settings returns the per-worktree environment and the arguments after "cargo build".
		settings func(root string) ([]string, []string)
	}
	for _, v := range []variant{
		{"direnv", func(root string) ([]string, []string) { return []string{"DIRENV_DIR=-" + root}, nil }},
		{"virtualenv-and-path", func(root string) ([]string, []string) {
			return []string{"VIRTUAL_ENV=" + filepath.Join(root, ".venv"), "TOOLS=/opt/tools" + string(os.PathListSeparator) + filepath.Join(root, "bin")}, nil
		}},
		{"target-dir-flag", func(root string) ([]string, []string) {
			return nil, []string{"--target-dir", filepath.Join(root, "target")}
		}},
		{"target-dir-env", func(root string) ([]string, []string) {
			return []string{"CARGO_TARGET_DIR=" + filepath.Join(root, "target")}, nil
		}},
		{"manifest-path", func(root string) ([]string, []string) {
			return nil, []string{"--manifest-path=" + filepath.Join(root, "Cargo.toml")}
		}},
	} {
		for _, action := range []string{"prepare", "run"} {
			t.Run(v.name+"/"+action, func(t *testing.T) {
				f := newFixture(t)
				mainEnv, mainArgs := v.settings(f.main)
				workEnv, workArgs := v.settings(f.work)
				f.runEnv(f.main, mainEnv, 0, append([]string{"run", "--", "cargo", "build"}, mainArgs...)...)
				if readFile(t, filepath.Join(f.main, "target", "data")) != "source artifact" {
					t.Fatal("source build did not run")
				}
				// The scenario records what the destination cache held when the build started.
				writeTest(t, filepath.Join(f.work, "scenario"), "cat target/data > target/seen\nprintf 'rebuilt' > target/data\n", 0600)
				args := append([]string{action, "--allow-copy", "--", "cargo", "build"}, workArgs...)
				f.runEnv(f.work, workEnv, 0, args...)
				if action == "prepare" {
					if got := readFile(t, filepath.Join(f.work, "target", "data")); got != "source artifact" {
						t.Fatalf("prepare data %q", got)
					}
				} else if got := readFile(t, filepath.Join(f.work, "target", "seen")); got != "source artifact" {
					t.Fatalf("run started without the cloned source cache: %q", got)
				}
			})
		}
	}
}

func TestDerivedWorktreeRejectsDifferentNonPathSetting(t *testing.T) {
	f := newFixture(t)
	f.runEnv(f.main, []string{"X=1"}, 0, "run", "--", "cargo", "build")
	_, stderr := f.runEnv(f.work, []string{"X=2"}, 125, "prepare", "--allow-copy", "--", "cargo", "build")
	if !strings.Contains(stderr, "source provenance does not match current settings") {
		t.Fatal(stderr)
	}
	if _, e := os.Stat(filepath.Join(f.work, "target")); !os.IsNotExist(e) {
		t.Fatal("incompatible source was cloned")
	}
}

// A root that is only a string prefix of another path is a different path. It is not
// translated, so the settings keep differing and the source stays incompatible.
func TestDerivedWorktreePathPrefixSettingIsNotTranslated(t *testing.T) {
	f := newFixture(t)
	f.runEnv(f.main, []string{"X=" + f.main + "-other"}, 0, "run", "--", "cargo", "build")
	_, stderr := f.runEnv(f.work, []string{"X=" + f.work + "-other"}, 125, "prepare", "--allow-copy", "--", "cargo", "build")
	if !strings.Contains(stderr, "build settings are incompatible") {
		t.Fatal(stderr)
	}
}

func unlockCacheTree(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e == nil && d.IsDir() {
			_ = os.Chmod(p, 0700)
		}
		return nil
	})
}
func TestCleanRemovesReadOnlyClonedCache(t *testing.T) {
	f := newFixture(t)
	t.Cleanup(func() { unlockCacheTree(f.dir) })
	f.run(f.main, 0, "run", "--", "cargo", "build")
	sub := filepath.Join(f.main, "target", "ro", "sub")
	if e := os.MkdirAll(sub, 0700); e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(sub, "f"), "x", 0400)
	for _, d := range []string{sub, filepath.Dir(sub)} {
		if e := os.Chmod(d, 0555); e != nil {
			t.Fatal(e)
		}
	}
	// --allow-copy keeps the test independent of copy-on-write support; the
	// prepared tree must carry the same read-only directory modes either way.
	f.run(f.work, 0, "prepare", "--replace", "--allow-copy", "--", "cargo", "build")
	info, e := os.Stat(filepath.Join(f.work, "target", "ro"))
	if e != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("clone did not preserve read-only directory: %v %v", info, e)
	}
	f.run(f.work, 0, "clean", "--preset", "cargo")
	if _, e = os.Lstat(filepath.Join(f.work, "target")); !os.IsNotExist(e) {
		t.Fatalf("cache remains: %v", e)
	}
}
func TestCleanRefusesNonDirectoryCache(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(f.dir, "outside")
	if e := os.MkdirAll(outside, 0500); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { unlockCacheTree(f.dir) })
	target := filepath.Join(f.work, "target")
	if e := os.Symlink(outside, target); e != nil {
		t.Skip("symlinks unavailable:", e)
	}
	f.run(f.work, 125, "clean", "--preset", "cargo")
	if info, e := os.Stat(outside); e != nil || info.Mode().Perm() != 0500 {
		t.Fatalf("symlink target changed: %v %v", info, e)
	}
	if _, e := os.Lstat(target); e != nil {
		t.Fatal("symlink was removed")
	}
}
