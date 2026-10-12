// SPDX-License-Identifier: Apache-2.0
// Package tailscale owns bounded installed-client observations and one original
// foreground Serve child. Tailnet visibility never grants DeliDev authority.
package tailscale

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type State string

const (
	Running         State = "running"
	Missing         State = "missing"
	LoggedOut       State = "logged-out"
	MachineApproval State = "machine-approval"
	Stopped         State = "stopped"
	Starting        State = "starting"
	Unknown         State = "unknown"
)

type Ownership string

const (
	Own              Ownership = "own"
	Shared           Ownership = "shared"
	Tagged           Ownership = "tagged"
	UnknownOwnership Ownership = "unknown"
)

type Peer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Ownership Ownership `json:"ownership"`
	Online    bool      `json:"online"`
	SharedTo  bool      `json:"shared_to"`
}
type Inventory struct {
	State      State     `json:"state"`
	Self       *Peer     `json:"self,omitempty"`
	Peers      []Peer    `json:"peers"`
	HTTPS      bool      `json:"https"`
	ObservedAt time.Time `json:"observed_at"`
}
type nativePeer struct {
	ID              string
	HostName        string
	DNSName         string
	UserID          *uint64
	AltSharerUserID *uint64
	Tags            []string
	Online          *bool
	ShareeNode      *bool
	CapMap          map[string]json.RawMessage
}
type nativeStatus struct {
	Version      string
	BackendState string
	Self         *nativePeer
	Peer         map[string]*nativePeer
	CertDomains  []string
}

func fail(code domain.Code) error {
	return domain.Fail(code, "Tailscale is unavailable or its observation is unsupported.", "Inspect the installed Tailscale client and its permissions. Use manual connection or SSH setup when unavailable.")
}
func CanonicalHost(name string) (string, error) {
	host := strings.TrimSuffix(name, ".")
	if host == "" || len(host) > 253 || host != strings.ToLower(host) || !strings.Contains(host, ".") {
		return "", fail(domain.InvalidArgument)
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fail(domain.InvalidArgument)
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return "", fail(domain.InvalidArgument)
			}
		}
	}
	return host, nil
}
func Origin(host string) (string, error) {
	h, err := CanonicalHost(host)
	if err != nil || h != host {
		return "", fail(domain.InvalidArgument)
	}
	return "https://" + h + ":8443", nil
}
func ValidateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return fail(domain.InvalidArgument)
	}
	exact, err := Origin(u.Hostname())
	if err != nil || exact != origin {
		return fail(domain.InvalidArgument)
	}
	return nil
}
func Resolve() (string, error) {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return "", fail(domain.NotFound)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fail(domain.PermissionDenied)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fail(domain.PermissionDenied)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fail(domain.PermissionDenied)
	}
	return path, nil
}

// A writer error cancels the owned process through Run's original context. Raw
// stdout/stderr never enters logs or caller-facing native error messages.
type boundedOutput struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
	cancel   context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data)+len(p) > 1<<20 {
		b.overflow = true
		b.cancel()
		return 0, io.ErrShortWrite
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

type Adapter struct {
	Executable string
	Root       string
	Logger     *slog.Logger
}

func (a Adapter) config(owner domain.ID, args []string, out io.Writer) process.Config {
	return process.Config{Directory: filepath.Join(a.Root, "reads"), OwnerID: owner, Executable: a.Executable, Args: args, Env: os.Environ(), Cwd: a.Root, Stdout: out, Stderr: io.Discard, Logger: a.Logger}
}
func (a Adapter) Read(ctx context.Context, args ...string) ([]byte, error) {
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b := &boundedOutput{cancel: cancel}
	parent := filepath.Join(a.Root, "reads")
	if err := security.PrivateDir(parent); err != nil {
		return nil, err
	}
	lock, err := security.TryLock(filepath.Join(a.Root, "read.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		return nil, fail(domain.RecoveryRequired)
	}
	root, err := os.MkdirTemp(parent, "command-")
	if err != nil {
		return nil, fail(domain.Unavailable)
	}
	clean := false
	defer func() {
		if clean {
			_ = os.RemoveAll(root)
		}
	}()
	config := a.config(domain.NewID(), args, b)
	config.Directory = root
	err = process.Run(child, config)
	clean = err == nil || domain.SafeError(err).Code != domain.RecoveryRequired
	if b.overflow {
		return nil, fail(domain.ResourceExhausted)
	}
	if err != nil {
		return nil, fail(domain.SafeError(err).Code)
	}
	return b.data, nil
}
func ParseStatus(raw []byte) (Inventory, error) {
	var n nativeStatus
	if len(raw) > 1<<20 || json.Unmarshal(raw, &n) != nil || n.Version == "" {
		return Inventory{}, fail(domain.Unsupported)
	}
	result := Inventory{Peers: []Peer{}, ObservedAt: time.Now().UTC()}
	switch n.BackendState {
	case "Running":
		result.State = Running
	case "NeedsLogin":
		result.State = LoggedOut
	case "NeedsMachineAuth":
		result.State = MachineApproval
	case "Stopped":
		result.State = Stopped
	case "Starting":
		result.State = Starting
	case "NoState":
		result.State = Unknown
	default:
		return Inventory{}, fail(domain.Unsupported)
	}
	if result.State != Running {
		return result, nil
	}
	if n.Self == nil || n.Peer == nil || len(n.Peer) > 512 {
		return Inventory{}, fail(domain.Unsupported)
	}
	convert := func(p *nativePeer) (Peer, error) {
		if p == nil || p.ID == "" || len(p.ID) > 256 || len(p.HostName) > 256 || p.Online == nil {
			return Peer{}, fail(domain.Unsupported)
		}
		host, err := CanonicalHost(p.DNSName)
		if err != nil {
			return Peer{}, fail(domain.Unsupported)
		}
		ownership := UnknownOwnership
		if len(p.Tags) > 0 {
			ownership = Tagged
		} else if p.AltSharerUserID != nil && *p.AltSharerUserID != 0 {
			ownership = Shared
		} else if p.ShareeNode != nil && *p.ShareeNode {
			ownership = UnknownOwnership
		} else if p.UserID != nil && n.Self.UserID != nil && *p.UserID != 0 && *p.UserID == *n.Self.UserID {
			ownership = Own
		}
		return Peer{ID: p.ID, Name: p.HostName, Host: host, Ownership: ownership, Online: *p.Online, SharedTo: p.ShareeNode != nil && *p.ShareeNode}, nil
	}
	self, err := convert(n.Self)
	if err != nil {
		return Inventory{}, err
	}
	result.Self = &self
	_, capable := n.Self.CapMap["https"]
	for _, cert := range n.CertDomains {
		if cert == self.Host {
			result.HTTPS = capable
		}
	}
	ids := map[string]bool{self.ID: true}
	for _, p := range n.Peer {
		v, err := convert(p)
		if err != nil || ids[v.ID] {
			return Inventory{}, fail(domain.Unsupported)
		}
		ids[v.ID] = true
		result.Peers = append(result.Peers, v)
	}
	sort.Slice(result.Peers, func(i, j int) bool { return result.Peers[i].ID < result.Peers[j].ID })
	return result, nil
}
func (a Adapter) Status(ctx context.Context) (Inventory, error) {
	raw, err := a.Read(ctx, "status", "--json")
	if err != nil {
		return Inventory{}, err
	}
	defer clear(raw)
	return ParseStatus(raw)
}
func EmptyServe(raw []byte) bool {
	if strings.TrimSpace(string(raw)) == "null" {
		return true
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return false
	}
	for key, raw := range value {
		switch key {
		case "TCP", "Web", "AllowFunnel", "Foreground", "Services":
			var entries map[string]json.RawMessage
			if string(raw) != "null" && (json.Unmarshal(raw, &entries) != nil || len(entries) != 0) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
