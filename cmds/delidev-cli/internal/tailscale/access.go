// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type AccessState string

const (
	AccessOff             AccessState = "off"
	AccessStarting        AccessState = "starting"
	AccessReady           AccessState = "ready"
	AccessUnavailable     AccessState = "unavailable"
	AccessCleanupRequired AccessState = "cleanup-required"
)

type Access struct {
	State   AccessState `json:"state"`
	Origin  string      `json:"origin"`
	Enabled bool        `json:"enabled"`
}
type accessReceipt struct {
	Request domain.ID `json:"request"`
	Enable  bool      `json:"enable"`
	Origin  string    `json:"origin"`
}
type accessIntent struct {
	ServerID domain.ID       `json:"server_id"`
	Enabled  bool            `json:"enabled"`
	Origin   string          `json:"origin"`
	OwnerID  domain.ID       `json:"owner_id,omitempty"`
	Receipts []accessReceipt `json:"receipts"`
}
type Child interface {
	Close() error
	Done() <-chan struct{}
}
type Launcher interface {
	Start(context.Context, domain.ID, string) (Child, error)
}
type ProcessLauncher struct {
	Executable string
	Root       string
	Logger     *slog.Logger
}

func (l ProcessLauncher) Start(ctx context.Context, owner domain.ID, target string) (Child, error) {
	handle, err := process.Start(ctx, process.Config{Directory: filepath.Join(l.Root, "tailscale-processes"), OwnerID: owner, Executable: l.Executable, Args: []string{"serve", "--https=8443", target}, Env: os.Environ(), Cwd: l.Root, Stdout: io.Discard, Stderr: io.Discard, Logger: l.Logger})
	if err != nil {
		return nil, err
	}
	if err := handle.Resume(); err != nil {
		return handle, err
	}
	return handle, nil
}

// AccessManager belongs to one original server process. Its independent backend
// is never the private desktop listener and its child is never a global Serve
// configuration. It cannot run reset/off, enable Funnel or adopt foreign state.
type AccessManager struct {
	Root     string
	ServerID domain.ID
	Parent   context.Context
	Runner   Runner
	Launcher Launcher
	Handler  http.Handler
	Logger   *slog.Logger
	mu       sync.Mutex
	intent   accessIntent
	loaded   bool
	state    AccessState
	child    Child
	server   *http.Server
	listener net.Listener
	done     chan error
}

func (m *AccessManager) load() error {
	if m.loaded {
		return nil
	}
	if m.ServerID.Validate() != nil {
		return invalid()
	}
	raw, err := security.ReadPrivate(filepath.Join(m.Root, "tailscale-access.json"), 256<<10)
	if errors.Is(err, os.ErrNotExist) {
		m.intent = accessIntent{ServerID: m.ServerID, Receipts: []accessReceipt{}}
	} else if err != nil {
		return err
	} else if domain.Decode(raw, &m.intent) != nil || m.intent.ServerID != m.ServerID || len(m.intent.Receipts) > 1024 || m.intent.Origin != "" && ValidateOrigin(m.intent.Origin) != nil {
		return recovery()
	}
	seen := map[domain.ID]bool{}
	for _, r := range m.intent.Receipts {
		if r.Request.Validate() != nil || seen[r.Request] || ValidateOrigin(r.Origin) != nil {
			return recovery()
		}
		seen[r.Request] = true
	}
	if m.intent.OwnerID != "" && m.intent.OwnerID.Validate() != nil {
		return recovery()
	}
	m.loaded = true
	m.state = AccessOff
	return nil
}
func (m *AccessManager) save() error {
	raw, err := json.Marshal(m.intent)
	if err != nil {
		return err
	}
	return security.WriteAtomic(filepath.Join(m.Root, "tailscale-access.json"), raw)
}
func (m *AccessManager) Status() (Access, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.load(); err != nil {
		return Access{}, err
	}
	if m.child != nil {
		select {
		case <-m.child.Done():
			m.state = AccessCleanupRequired
		default:
		}
	}
	return Access{State: m.state, Origin: m.intent.Origin, Enabled: m.intent.Enabled}, nil
}
func (m *AccessManager) Origin() string {
	value, err := m.Status()
	if err != nil || value.State != AccessReady {
		return ""
	}
	return value.Origin
}
func (m *AccessManager) Set(ctx context.Context, id domain.ID, enable bool, server domain.ID, origin string) (Access, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id.Validate() != nil || server != m.ServerID || ValidateOrigin(origin) != nil {
		return Access{}, invalid()
	}
	if err := m.load(); err != nil {
		return Access{}, err
	}
	replay := false
	for _, r := range m.intent.Receipts {
		if r.Request != id {
			continue
		}
		if r.Enable != enable || r.Origin != origin {
			return Access{}, domain.Fail(domain.Conflict, "The original access request changed.", "Reconcile the retained access request.")
		}
		replay = true
	}
	if !replay {
		if len(m.intent.Receipts) >= 1024 {
			return Access{}, domain.Fail(domain.ResourceExhausted, "The access history is full.", "Retain original access ownership.")
		}
		m.intent.Receipts = append(m.intent.Receipts, accessReceipt{Request: id, Enable: enable, Origin: origin})
		m.intent.Enabled = enable
		m.intent.Origin = origin
		if err := m.save(); err != nil {
			return Access{}, err
		}
	}
	// An old exact replay cannot reapply a later superseded opt-in or disable.
	if !replay || len(m.intent.Receipts) > 0 && m.intent.Receipts[len(m.intent.Receipts)-1].Request == id {
		if enable {
			if m.child == nil {
				if err := m.start(ctx); err != nil {
					return Access{}, err
				}
			}
		} else if err := m.close(); err != nil {
			return Access{}, err
		}
	}
	return Access{State: m.state, Origin: m.intent.Origin, Enabled: m.intent.Enabled}, nil
}
func (m *AccessManager) Restore(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.load(); err != nil {
		return err
	}
	if !m.intent.Enabled {
		return nil
	}
	return m.start(ctx)
}
func (m *AccessManager) start(ctx context.Context) error {
	if m.child != nil {
		return nil
	}
	m.state = AccessStarting
	if m.intent.OwnerID != "" {
		if err := process.ReconcileOwnerContext(ctx, filepath.Join(m.Root, "tailscale-processes"), m.intent.OwnerID); err != nil {
			m.state = AccessCleanupRequired
			return recovery()
		}
		m.intent.OwnerID = ""
		if err := m.save(); err != nil {
			return err
		}
	}
	observation, err := Discover(ctx, m.Runner)
	if err != nil || observation.State != Ready || observation.Self == nil || !observation.HTTPSReady || observation.Self.Origin != m.intent.Origin {
		m.state = AccessUnavailable
		return domain.Fail(domain.Unavailable, "Tailscale HTTPS access is unavailable.", "Check installed Tailscale, its original device identity, HTTPS prerequisites and permission. No login or policy change was performed.")
	}
	raw, err := m.Runner.Read(ctx, "serve", "status", "--json")
	if err != nil || !emptyServeConfig(raw) {
		m.state = AccessUnavailable
		return domain.Fail(domain.Conflict, "Tailscale Serve is already configured or unavailable.", "Keep the foreign configuration. DeliDev does not reset, adopt or remap it.")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.state = AccessUnavailable
		return err
	}
	m.listener = listener
	parent := m.Parent
	if parent == nil {
		parent = context.Background()
	}
	m.server = &http.Server{Handler: m.Handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return parent }}
	m.done = make(chan error, 1)
	go func() { m.done <- m.server.Serve(listener) }()
	m.intent.OwnerID = domain.NewID()
	if err := m.save(); err != nil {
		_ = m.close()
		return err
	}
	child, err := m.Launcher.Start(parent, m.intent.OwnerID, "http://"+listener.Addr().String())
	m.child = child
	if err != nil {
		if cleanup := m.close(); cleanup != nil {
			return cleanup
		}
		m.state = AccessUnavailable
		return domain.Fail(domain.Unavailable, "The owned Tailscale endpoint could not start.", "Inspect Tailscale permission and retained original access ownership.")
	}
	m.child = child
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = m.close()
			return ctx.Err()
		case <-child.Done():
			_ = m.close()
			m.state = AccessUnavailable
			return domain.Fail(domain.Unavailable, "The owned Tailscale endpoint stopped before readiness.", "Check original Tailscale access prerequisites.")
		case <-deadline.C:
			_ = m.close()
			m.state = AccessCleanupRequired
			return recovery()
		case <-tick.C:
			raw, err := m.Runner.Read(ctx, "serve", "status", "--json")
			if err == nil && ownedServeConfig(raw, m.intent.Origin, "http://"+listener.Addr().String()) {
				m.state = AccessReady
				return nil
			}
		}
	}
}
func (m *AccessManager) Close() error { m.mu.Lock(); defer m.mu.Unlock(); return m.close() }
func (m *AccessManager) close() error {
	hadChild := m.child != nil
	// Fence the backend first, then join only this retained foreground child.
	var err error
	if m.server != nil {
		err = m.server.Close()
		if m.done != nil {
			<-m.done
		}
		m.server = nil
		m.listener = nil
		m.done = nil
	}
	if m.child != nil {
		err = errors.Join(err, m.child.Close())
		if err == nil {
			m.child = nil
		}
	}
	if err != nil {
		m.state = AccessCleanupRequired
		return recovery()
	}
	if m.loaded && m.intent.OwnerID != "" && !hadChild {
		bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := process.ReconcileOwnerContext(bounded, filepath.Join(m.Root, "tailscale-processes"), m.intent.OwnerID); err != nil {
			m.state = AccessCleanupRequired
			return recovery()
		}
	}
	m.state = AccessOff
	if m.loaded && m.intent.OwnerID != "" {
		m.intent.OwnerID = ""
		if err := m.save(); err != nil {
			m.state = AccessCleanupRequired
			return err
		}
	}
	return nil
}
func emptyServeConfig(raw []byte) bool {
	if len(raw) > MaxOutput {
		return false
	}
	var cfg map[string]json.RawMessage
	if !uniqueJSON(raw) || json.Unmarshal(raw, &cfg) != nil {
		return false
	}
	for key, value := range cfg {
		switch key {
		case "TCP", "Web", "AllowFunnel", "Foreground":
			var entries map[string]json.RawMessage
			if json.Unmarshal(value, &entries) != nil || len(entries) != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func ownedServeConfig(raw []byte, origin, target string) bool {
	if len(raw) > MaxOutput || ValidateOrigin(origin) != nil {
		return false
	}
	var cfg struct {
		TCP map[string]struct{ HTTPS bool }
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
		AllowFunnel map[string]bool
		Foreground  map[string]json.RawMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if !uniqueJSON(raw) || decoder.Decode(&cfg) != nil {
		return false
	}
	for _, enabled := range cfg.AllowFunnel {
		if enabled {
			return false
		}
	}
	if len(cfg.Foreground) > 0 {
		if len(cfg.Foreground) != 1 || len(cfg.TCP) > 0 || len(cfg.Web) > 0 {
			return false
		}
		for _, scope := range cfg.Foreground {
			return ownedServeConfig(scope, origin, target)
		}
	}
	for _, enabled := range cfg.AllowFunnel {
		if enabled {
			return false
		}
	}
	host := origin[len("https://"):]
	return len(cfg.TCP) == 1 && cfg.TCP["8443"].HTTPS && len(cfg.Web) == 1 && len(cfg.Web[host].Handlers) == 1 && cfg.Web[host].Handlers["/"].Proxy == target
}
