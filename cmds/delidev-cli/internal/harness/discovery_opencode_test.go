package harness

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativetest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

//go:embed opencode/testdata/schema.json
var openCodeSchema []byte

func init() {
	name := filepath.Base(os.Getenv("HOME"))
	if len(os.Args) < 2 || (name != "opencode" && name != "opencode-protocol") {
		return
	}
	if os.Args[1] == "--version" {
		// The pinned binary creates these directories even for version output.
		if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), "config", "opencode"), 0700); err != nil {
			os.Exit(39)
		}
		fmt.Println(domain.OpenCodeProtocolVersion)
		os.Exit(0)
	}
	if os.Args[1] != "serve" {
		return
	}
	if len(os.Args) != 5 || os.Args[1] != "serve" || os.Args[2] != "--hostname=127.0.0.1" || !strings.HasPrefix(os.Args[3], "--port=") || os.Args[4] != "--mdns=false" {
		os.Exit(40)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strings.TrimPrefix(os.Args[3], "--port=")))
	if err != nil {
		os.Exit(41)
	}
	fmt.Println("opencode server listening on http://" + listener.Addr().String())
	_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "delidev" || password != os.Getenv("OPENCODE_SERVER_PASSWORD") {
			w.Header().Set("Www-Authenticate", `Basic realm="Secure Area"`)
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/global/health":
			_, _ = fmt.Fprintf(w, `{"healthy":true,"version":%q}`, domain.OpenCodeProtocolVersion)
		case "/global/config":
			_, _ = io.WriteString(w, `{}`)
		case "/doc":
			_, _ = w.Write(openCodeSchema)
		default:
			os.Exit(42)
		}
	}))
	os.Exit(0)
}

// discoverOpenCode runs discovery against binary and expects the installation
// to report wantVersion, which must itself satisfy the minimum-version
// admission rule. The in-process fake reports the baseline; a real binary
// reports whatever admitted version is installed.
func discoverOpenCode(t *testing.T, binary, wantVersion string) {
	t.Helper()
	input := allMissing(t)
	input.VerifyProtocol = true
	input.Selections.Executables[2].Path = binary
	root, owner := discoveryRoot(t), domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var logs bytes.Buffer
	output, err := Discover(ctx, DiscoveryConfig{Root: root, Profiles: testProfiles(t, root), OwnerID: owner, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.ValidateDiscovery(input); err != nil {
		t.Fatal(err)
	}
	i := output.Installations[2]
	if i.Harness != domain.OpenCode || !domain.HarnessVersionAllowed(domain.OpenCode, i.Version) || i.Version != wantVersion || !i.ProtocolVerified || i.Protocol == nil || i.Protocol.State != domain.ProtocolVerified || i.Protocol.Problem != nil || len(i.Capabilities) != 0 {
		t.Fatalf("OpenCode discovery: state=%s version=%s protocol=%+v; logs: %s", i.State, i.Version, i.Protocol, logs.String())
	}
	if err := process.ReconcileOwner(filepath.Join(root, "processes"), owner); err != nil {
		t.Fatal(err)
	}
	// The accountless probe profile is persistent; no disposable probe directory is allocated.
	if _, err := os.Stat(filepath.Join(root, "probes")); err == nil {
		t.Fatal("a disposable OpenCode probe directory was allocated")
	}
	if _, ok, err := testProfiles(t, root).Lookup(context.Background(), nativeprofile.Key{Harness: domain.OpenCode, Purpose: nativeprofile.PurposeDiscovery}); err != nil || !ok {
		t.Fatal("persistent OpenCode probe profile was not retained")
	}
}

func TestDiscoveryVerifiesOpenCodeWithoutExecution(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	discoverOpenCode(t, executable, domain.OpenCodeProtocolVersion)
}

// openCodeVersionWrapperScript is a fixed script: it contains no dynamic value.
// The reported version and the real executable live in sibling data files that
// the script reads as data, so no path, version or environment value is ever
// interpolated into shell source. The --version branch mirrors the pinned
// binary, which creates its config directory even for version output.
const openCodeVersionWrapperScript = `#!/bin/sh
here=$(dirname "$0")
if [ "$1" = "--version" ]; then
	mkdir -p "$HOME/config/opencode"
	cat "$here/version"
	exit 0
fi
IFS= read -r target < "$here/target"
exec "$target" "$@"
`

// writeOpenCodeVersionWrapper creates an executable "opencode" in dir that
// reports version for --version and otherwise delegates to target.
func writeOpenCodeVersionWrapper(t *testing.T, dir, version, target string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"version": version + "\n", "target": target + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wrapper := filepath.Join(dir, "opencode")
	if err := os.WriteFile(wrapper, []byte(openCodeVersionWrapperScript), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

// TestDiscoveryAdmitsNewerOpenCodeVersion proves that a binary above the
// baseline is admitted and reported with its own observed version. The wrapper
// only rewrites the --version line; the protocol behavior is the in-process fake.
// The hostile case places the wrapper and its target under paths full of shell
// metacharacters; any interpolation of those paths would break or run them.
func TestDiscoveryAdmitsNewerOpenCodeVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper executables are not available on Windows")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const newer = "1.18.33"
	for _, test := range []struct{ name, directory string }{
		{"plain", "wrapper"},
		{"hostile-path", "it's \"quoted\" $(touch pwned) `touch pwned` ; * \\ done"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, test.directory)
			target := executable
			if test.name == "hostile-path" {
				target = filepath.Join(dir, "target's $(touch pwned) `touch pwned`")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(executable, target); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			}
			discoverOpenCode(t, writeOpenCodeVersionWrapper(t, dir, newer, target), newer)
			for _, marker := range []string{filepath.Join(root, "pwned"), filepath.Join(dir, "pwned"), "pwned"} {
				if _, err := os.Stat(marker); err == nil {
					t.Fatalf("shell metacharacters in a path were executed: %s", marker)
				}
			}
		})
	}
}

func TestManualNativeOpenCodeDiscovery(t *testing.T) {
	executable, version := nativetest.NativeOpenCodeExecutable(t)
	discoverOpenCode(t, executable, version)
}
