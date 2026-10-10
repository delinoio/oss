// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type State string

const (
	Ready            State = "ready"
	Missing          State = "missing"
	Stopped          State = "stopped"
	LoggedOut        State = "logged-out"
	PermissionDenied State = "permission-denied"
	Malformed        State = "malformed"
	Incomplete       State = "incomplete"
)

type Ownership string

const (
	Own     Ownership = "own"
	Shared  Ownership = "shared"
	Tagged  Ownership = "tagged"
	Unknown Ownership = "unknown"
)

type Peer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Origin    string    `json:"origin"`
	Online    bool      `json:"online"`
	Ownership Ownership `json:"ownership"`
}
type Discovery struct {
	State      State  `json:"state"`
	Self       *Peer  `json:"self,omitempty"`
	Peers      []Peer `json:"peers"`
	HTTPSReady bool   `json:"https_ready"`
}

const MaxOutput = 1 << 20
const MaxPeers = 256

// Runner never supplies raw stdout/stderr to callers outside the typed adapter.
// The real implementation runs only read commands during discovery.
type Runner interface {
	Read(context.Context, ...string) ([]byte, error)
}
type CLI struct{ Executable string }
type ReadFailure struct{ State State }

func (e ReadFailure) Error() string { return "Tailscale observation unavailable" }
func (c CLI) Read(ctx context.Context, args ...string) ([]byte, error) {
	if c.Executable == "" {
		return nil, ReadFailure{Missing}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Executable, args...)
	cmd.Stdin = nil
	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, os.ErrPermission) {
		return nil, ReadFailure{PermissionDenied}
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, ReadFailure{Missing}
	}
	if output.over {
		return nil, ReadFailure{Malformed}
	}
	if err != nil {
		return nil, ReadFailure{PermissionDenied}
	}
	return output.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > MaxOutput {
		b.over = true
		return n, nil
	}
	_, err := b.Buffer.Write(p)
	return n, err
}
func Discover(ctx context.Context, r Runner) (Discovery, error) {
	raw, err := r.Read(ctx, "status", "--json")
	if err != nil {
		var failure ReadFailure
		if errors.As(err, &failure) {
			return Discovery{State: failure.State, Peers: []Peer{}}, nil
		}
		return Discovery{}, err
	}
	return DecodeStatus(raw)
}

type peerWire struct {
	ID         string
	HostName   string
	DNSName    string
	UserID     json.Number
	Online     *bool
	Tags       []string
	ShareeNode bool
}

func CanonicalOrigin(name string) (string, error) {
	// MagicDNS status emits one terminal DNS dot. It is observation spelling,
	// never an alias accepted for a saved origin or a caller-supplied target.
	name = strings.TrimSuffix(name, ".")
	if len(name) > 253 || name != strings.ToLower(name) || !strings.HasSuffix(name, ".ts.net") {
		return "", invalid()
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", invalid()
		}
		for _, ch := range label {
			if ch != '-' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
				return "", invalid()
			}
		}
	}
	return "https://" + name + ":8443", nil
}
func ValidateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u == nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return invalid()
	}
	canonical, err := CanonicalOrigin(u.Hostname())
	if err != nil || canonical != origin {
		return invalid()
	}
	return nil
}
func invalid() error {
	return domain.Fail(domain.InvalidArgument, "The Tailscale observation is invalid.", "Refresh the original device observation.")
}
func DecodeStatus(raw []byte) (Discovery, error) {
	bad := func(state State) (Discovery, error) { return Discovery{State: state, Peers: []Peer{}}, nil }
	if len(raw) == 0 || len(raw) > MaxOutput || !json.Valid(raw) {
		return bad(Malformed)
	}
	var wire struct {
		BackendState string
		Self         *peerWire
		Peer         map[string]*peerWire
		CertDomains  []string
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&wire) != nil {
		return bad(Malformed)
	}
	switch wire.BackendState {
	case "NeedsLogin":
		return bad(LoggedOut)
	case "Stopped", "NoState", "Starting":
		return bad(Stopped)
	case "Running":
	default:
		return bad(Malformed)
	}
	if wire.Self == nil || wire.Peer == nil {
		return bad(Incomplete)
	}
	if len(wire.Peer) > MaxPeers {
		return bad(Incomplete)
	}
	normalize := func(p *peerWire) (Peer, error) {
		if p == nil || domain.Text(p.ID, "Tailscale identity", 256, true) != nil || domain.Text(p.HostName, "Tailscale name", 256, true) != nil || p.Online == nil || len(p.Tags) > 128 {
			return Peer{}, invalid()
		}
		origin, err := CanonicalOrigin(p.DNSName)
		if err != nil {
			return Peer{}, err
		}
		owner := Unknown
		if len(p.Tags) > 0 {
			owner = Tagged
		} else if p.ShareeNode {
			owner = Shared
		} else if p.UserID != "" && wire.Self.UserID != "" && p.UserID == wire.Self.UserID {
			owner = Own
		}
		return Peer{ID: p.ID, Name: p.HostName, Origin: origin, Online: *p.Online, Ownership: owner}, nil
	}
	self, err := normalize(wire.Self)
	if err != nil {
		return bad(Malformed)
	}
	result := Discovery{State: Ready, Self: &self, Peers: []Peer{}}
	for _, name := range wire.CertDomains {
		if domain.Text(name, "HTTPS certificate domain", 253, true) != nil {
			return bad(Malformed)
		}
		origin, err := CanonicalOrigin(name)
		if err == nil && origin == self.Origin {
			result.HTTPSReady = true
		}
	}
	seen := map[string]bool{self.ID: true}
	for _, wirePeer := range wire.Peer {
		peer, err := normalize(wirePeer)
		if err != nil || seen[peer.ID] {
			return bad(Malformed)
		}
		seen[peer.ID] = true
		result.Peers = append(result.Peers, peer)
	}
	sort.Slice(result.Peers, func(i, j int) bool { return result.Peers[i].ID < result.Peers[j].ID })
	return result, nil
}

type PeerState string

const (
	NotChecked  PeerState = "not-checked"
	Accepting   PeerState = "accepting"
	Offline     PeerState = "offline"
	TLSFailure  PeerState = "tls-failure"
	Blocked     PeerState = "blocked"
	Unsupported PeerState = "unsupported"
)

type PeerCheck struct {
	State    PeerState
	ServerID domain.ID
	Key      []byte
}

// Check performs exactly one anonymous capability observation. Online membership
// is not DeliDev availability, and the response cannot authorize a pairing.
func Check(ctx context.Context, peer Peer, transport http.RoundTripper) (PeerCheck, error) {
	if ValidateOrigin(peer.Origin) != nil {
		return PeerCheck{}, invalid()
	}
	if !peer.Online {
		return PeerCheck{State: Offline}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if transport == nil {
		transport = &http.Transport{Proxy: nil, TLSHandshakeTimeout: 3 * time.Second}
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.Origin+"/delidev.v1.TailscaleService/GetTailscaleStatus", strings.NewReader("{}"))
	if err != nil {
		return PeerCheck{}, invalid()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	response, err := client.Do(req)
	if err != nil {
		return PeerCheck{State: TLSFailure}, nil
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return PeerCheck{State: Blocked}, nil
		}
		return PeerCheck{State: Unsupported}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(raw) > 8192 {
		return PeerCheck{State: Unsupported}, nil
	}
	var status struct {
		ServerID  domain.ID `json:"serverId"`
		PublicKey []byte    `json:"publicKey"`
		Origin    string    `json:"origin"`
		Accepting bool      `json:"accepting"`
	}
	if domain.Decode(raw, &status) != nil || status.ServerID.Validate() != nil || len(status.PublicKey) != 32 || status.Origin != peer.Origin || !status.Accepting {
		return PeerCheck{State: Unsupported}, nil
	}
	return PeerCheck{State: Accepting, ServerID: status.ServerID, Key: status.PublicKey}, nil
}
