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
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
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

func discoverOpenCode(t *testing.T, binary string) {
	t.Helper()
	input := allMissing(t)
	input.VerifyProtocol = true
	input.Selections.Executables[2].Path = binary
	root, owner := discoveryRoot(t), domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var logs bytes.Buffer
	output, err := Discover(ctx, DiscoveryConfig{Root: root, OwnerID: owner, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.ValidateDiscovery(input); err != nil {
		t.Fatal(err)
	}
	i := output.Installations[2]
	if i.Harness != domain.OpenCode || i.Version != domain.OpenCodeProtocolVersion || !i.ProtocolVerified || i.Protocol == nil || i.Protocol.State != domain.ProtocolVerified || i.Protocol.Problem != nil || len(i.Capabilities) != 0 {
		t.Fatalf("OpenCode discovery: state=%s version=%s protocol=%+v; logs: %s", i.State, i.Version, i.Protocol, logs.String())
	}
	if err := process.ReconcileOwner(filepath.Join(root, "processes"), owner); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "probes"))
	if err != nil || len(entries) != 0 {
		t.Fatal("completed OpenCode probe runtime retained")
	}
}

func TestDiscoveryVerifiesOpenCodeWithoutExecution(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	discoverOpenCode(t, executable)
}
func TestManualNativeOpenCodeDiscovery(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit private OpenCode discovery")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("select an absolute native executable")
	}
	discoverOpenCode(t, executable)
}
