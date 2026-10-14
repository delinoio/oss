package nativetest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativetest"
)

// stopSignal unwinds a recording test double after Skipf or Fatalf, matching
// how the real testing.T stops the calling test.
type stopSignal struct{ skipped bool }

type recordingTB struct {
	testing.TB
	logs []string
}

func (r *recordingTB) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}
func (r *recordingTB) Skipf(format string, args ...any) {
	r.logs = append(r.logs, "skip: "+fmt.Sprintf(format, args...))
	panic(stopSignal{skipped: true})
}
func (r *recordingTB) Fatalf(format string, args ...any) {
	r.logs = append(r.logs, "fatal: "+fmt.Sprintf(format, args...))
	panic(stopSignal{})
}

type outcome string

const (
	outcomeRun   outcome = "run"
	outcomeSkip  outcome = "skip"
	outcomeFatal outcome = "fatal"
)

func resolve(t *testing.T) (outcome, string, string, []string) {
	t.Helper()
	recorder := &recordingTB{TB: t}
	var path, version string
	result := outcomeRun
	func() {
		defer func() {
			if value := recover(); value != nil {
				signal, ok := value.(stopSignal)
				if !ok {
					panic(value)
				}
				result = outcomeFatal
				if signal.skipped {
					result = outcomeSkip
				}
			}
		}()
		path, version = nativetest.NativeOpenCodeExecutable(recorder)
	}()
	return result, path, version, recorder.logs
}

func fakeOpenCode(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake shell executables are not available on Windows")
	}
	path := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeOpenCodeExecutableVersionRule(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   outcome
	}{
		{"below-minimum", "printf '1.18.20\\n'", outcomeSkip},
		{"minimum", "printf '1.18.32\\n'", outcomeRun},
		{"newer-patch", "printf '1.18.33\\n'", outcomeRun},
		{"newer-prerelease-major", "printf '2.0.0-rc.1\\n'", outcomeRun},
		{"leading-v", "printf 'v1.19.0\\n'", outcomeRun},
		{"garbage", "printf 'not a version\\n'", outcomeFatal},
		{"prerelease-leading-zero", "printf '1.18.33-01\\n'", outcomeFatal},
		{"prerelease-empty-identifier", "printf '1.18.33-..\\n'", outcomeFatal},
		{"prerelease-trailing-dot", "printf '1.18.33-rc.\\n'", outcomeFatal},
		{"build-metadata-empty-identifier", "printf '1.18.33+a..b\\n'", outcomeFatal},
		{"leading-zero-core", "printf '01.18.33\\n'", outcomeFatal},
		{"endless-output", "exec yes", outcomeFatal},
		{"endless-output-without-newline", "while :; do printf 'xxxxxxxxxxxxxxxx'; done", outcomeFatal},
		{"oversized-line", "i=0; while [ $i -lt 200 ]; do printf '0123456789'; i=$((i+1)); done; printf '\\n'", outcomeFatal},
		{"two-lines", "printf '1.18.33\\n1.18.34\\n'", outcomeFatal},
		{"empty", "exit 0", outcomeFatal},
		{"failing", "exit 3", outcomeFatal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := fakeOpenCode(t, test.script)
			t.Setenv(nativetest.OpenCodeExecutableEnv, path)
			got, gotPath, version, logs := resolve(t)
			if got != test.want {
				t.Fatalf("outcome=%s want %s; logs=%v", got, test.want, logs)
			}
			joined := strings.Join(logs, "\n")
			switch got {
			case outcomeRun:
				if gotPath != path || !domain.HarnessVersionAllowed(domain.OpenCode, version) {
					t.Fatalf("path=%q version=%q", gotPath, version)
				}
				if !strings.Contains(joined, "installed_version="+version) || !strings.Contains(joined, "baseline_version="+domain.OpenCodeProtocolVersion) {
					t.Fatalf("versions were not logged: %v", logs)
				}
			case outcomeSkip:
				if !strings.Contains(joined, "1.18.20") || !strings.Contains(joined, domain.OpenCodeProtocolVersion) {
					t.Fatalf("skip message must name installed and minimum versions: %v", logs)
				}
			}
		})
	}
}

// A runaway executable must end the probe at the output bound, quickly and with
// a fatal result, instead of being buffered until the 20-second timeout.
func TestObserveOpenCodeVersionBoundsOutput(t *testing.T) {
	path := fakeOpenCode(t, "exec yes")
	started := time.Now()
	version, err := nativetest.ObserveOpenCodeVersion(t.TempDir(), path)
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("version=%q err=%v, want an output-bound error", version, err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("probe was not terminated at the bound: %s", elapsed)
	}
}

func TestNativeOpenCodeExecutableUnsetSkips(t *testing.T) {
	t.Setenv(nativetest.OpenCodeExecutableEnv, "")
	if got, _, _, logs := resolve(t); got != outcomeSkip || !strings.Contains(strings.Join(logs, "\n"), nativetest.OpenCodeExecutableEnv) {
		t.Fatalf("outcome=%s logs=%v", got, logs)
	}
}

func TestNativeOpenCodeExecutableRelativePathFails(t *testing.T) {
	t.Setenv(nativetest.OpenCodeExecutableEnv, "opencode")
	if got, _, _, logs := resolve(t); got != outcomeFatal {
		t.Fatalf("outcome=%s logs=%v", got, logs)
	}
}

// The probe must run in the private discovery environment: no inherited
// provider or user configuration and the OpenCode updater disabled.
func TestNativeOpenCodeExecutableUsesPrivateEnvironment(t *testing.T) {
	t.Setenv("DELIDEV_NATIVE_TEST_LEAK", "1")
	script := `[ -z "$DELIDEV_NATIVE_TEST_LEAK" ] && [ "$OPENCODE_DISABLE_AUTOUPDATE" = true ] && [ "$HOME" != "` + os.Getenv("HOME") + `" ] && printf '1.18.32\n'`
	t.Setenv(nativetest.OpenCodeExecutableEnv, fakeOpenCode(t, script))
	if got, _, version, logs := resolve(t); got != outcomeRun || version != "1.18.32" {
		t.Fatalf("outcome=%s version=%q logs=%v", got, version, logs)
	}
}

func TestPrivateRuntimeEnvironmentMatchesHarness(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	want, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := nativetest.PrivateRuntimeEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("native test environment drifted from harness.PrivateRuntimeEnvironment:\n got %v\nwant %v", got, want)
	}
}

func TestParseVersionMatchesHarnessAdmission(t *testing.T) {
	for _, raw := range []string{"1.18.33-01", "1.18.33-..", "1.18.33-", "1.18.33+", "1.18.33+a..b", "1.18.33-rc.", "01.18.33", "1.018.33", "1.18", "v", "", "vv1.18.33", "1.18.33 rc"} {
		if version, err := nativetest.ParseVersion(raw); err == nil {
			t.Errorf("ParseVersion(%q) = %q, want an error", raw, version)
		}
	}
	for _, raw := range []string{"1.18.32", "v1.18.32\n", " 2.0.0-rc.1 ", "1.18.33+build.5", "1.18.33-rc.1+build.5"} {
		version, err := nativetest.ParseVersion(raw)
		if err != nil || !domain.HarnessVersionAllowed(domain.OpenCode, version) {
			t.Errorf("ParseVersion(%q) = %q, %v; want an admitted version", raw, version, err)
		}
	}
}
