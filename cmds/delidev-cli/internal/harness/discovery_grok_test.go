package harness

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

//go:embed grok/testdata/initialize.json
var grokInitialization []byte

//go:embed grok/testdata/inspect.json
var grokInspection []byte

func init() {
	if len(os.Args) < 3 || filepath.Base(os.Getenv("HOME")) != "grok-build" || os.Args[1] != "--no-auto-update" {
		return
	}
	if os.Args[2] == "--version" {
		fmt.Println("grok " + domain.GrokProtocolVersion + " (4220f3b224a6)")
		os.Exit(0)
	}
	if os.Args[2] == "inspect" {
		var report map[string]any
		_ = json.Unmarshal(grokInspection, &report)
		report["cwd"] = os.Getenv("HOME")
		// Tests add native inspection members through a wrapper script because
		// discovery gives the fake a sanitized environment.
		if extra := os.Getenv("DELIDEV_FAKE_GROK_INSPECT_EXTRA"); extra != "" {
			var members map[string]any
			_ = json.Unmarshal([]byte(extra), &members)
			for key, value := range members {
				report[key] = value
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(0)
	}
	if os.Args[2] != "agent" {
		os.Exit(40)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(41)
	}
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      domain.ID       `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if domain.Decode(scanner.Bytes(), &request) != nil || request.JSONRPC != "2.0" || request.ID.Validate() != nil || request.Method != "initialize" {
		os.Exit(42)
	}
	var result map[string]any
	_ = json.Unmarshal(grokInitialization, &result)
	result["_meta"].(map[string]any)["currentWorkingDirectory"] = os.Getenv("HOME")
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	fmt.Println(`{"jsonrpc":"2.0","method":"_x.ai/mcp/servers_updated","params":{"mcpServers":[]}}`)
	if scanner.Scan() {
		os.Exit(43)
	}
	os.Exit(0)
}

func discoverGrok(t *testing.T, binary string) {
	t.Helper()
	i := discoverGrokInstallation(t, binary)
	if i.Harness != domain.GrokBuild || i.Version != domain.GrokProtocolVersion || !i.ProtocolVerified || i.Protocol == nil || i.Protocol.State != domain.ProtocolVerified || i.Protocol.Problem != nil || len(i.Capabilities) != 0 {
		t.Fatalf("Grok discovery did not verify the isolated profile: state=%s version=%s protocol=%+v", i.State, i.Version, i.Protocol)
	}
}

func discoverGrokInstallation(t *testing.T, binary string) domain.Installation {
	t.Helper()
	input := allMissing(t)
	input.VerifyProtocol = true
	input.Selections.Executables[3].Path = binary
	root, owner := discoveryRoot(t), domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := Discover(ctx, DiscoveryConfig{Root: root, Profiles: testProfiles(t, root), OwnerID: owner}, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.ValidateDiscovery(input); err != nil {
		t.Fatal(err)
	}
	i := output.Installations[3]
	if err := process.ReconcileOwner(filepath.Join(root, "processes"), owner); err != nil {
		t.Fatal(err)
	}
	// The accountless probe profile is persistent; no disposable probe directory is allocated.
	if _, err := os.Stat(filepath.Join(root, "probes")); err == nil {
		t.Fatal("a disposable Grok probe directory was allocated")
	}
	if _, ok, err := testProfiles(t, root).Lookup(context.Background(), nativeprofile.Key{Harness: domain.GrokBuild, Purpose: nativeprofile.PurposeDiscovery}); err != nil || !ok {
		t.Fatal("persistent Grok probe profile was not retained")
	}
	return i
}

func TestDiscoveryVerifiesGrokWithoutExecution(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	discoverGrok(t, binary)
}

// grokWithInspectionMembers wraps the fake native so its inspection output
// carries extra top-level members, as a newer Grok Build release would.
func grokWithInspectionMembers(t *testing.T, members string) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return fixture(t, fmt.Sprintf("DELIDEV_FAKE_GROK_INSPECT_EXTRA='%s' exec '%s' \"$@\"\n", members, binary))
}

func TestDiscoveryGrokInspectionExperiments(t *testing.T) {
	for _, test := range []struct {
		name     string
		members  string
		verified bool
	}{
		{"empty experiments", `{"experiments":[]}`, true},
		{"populated experiments", `{"experiments":["foreign"]}`, false},
		{"unknown member", `{"futureThing":[]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			i := discoverGrokInstallation(t, grokWithInspectionMembers(t, test.members))
			if i.State != domain.InstallationDetected || i.Protocol == nil || i.ProtocolVerified != test.verified {
				t.Fatalf("unexpected Grok discovery: state=%s protocol=%+v", i.State, i.Protocol)
			}
			want := domain.ProtocolUnsupported
			if test.verified {
				want = domain.ProtocolVerified
			}
			if i.Protocol.State != want {
				t.Fatalf("unexpected protocol state: %+v", i.Protocol)
			}
		})
	}
}

func TestManualNativeGrokDiscovery(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private Grok Build discovery")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("select an absolute native executable")
	}
	discoverGrok(t, binary)
}

func TestGrokVersionSuffixIsBoundedSourceHash(t *testing.T) {
	for _, value := range []string{"grok 1.0.41", "grok 1.0.41 (4220f3b224a6)", "v1.0.41"} {
		if version := parseVersion(domain.GrokBuild, value); version != "1.0.41" {
			t.Fatalf("valid version rejected: %q", value)
		}
	}
	for _, value := range []string{"grok 1.0.41 (short)", "grok 1.0.41 (4220f3b224a6) diagnostic", "grok 1.0.41 (4220F3B224A6)", "grok 1.0.41 (4220f3b224a6", "grok 1.0.41 (4220f3b224a6\n)", "grok 1.0.41 (01234567890123456789012345678901234567890)", "grok 1.0.41 (4220f3b224a6) (4220f3b224a6)"} {
		if version := parseVersion(domain.GrokBuild, value); version != "" {
			t.Fatalf("malformed version accepted: %q", value)
		}
	}
}
