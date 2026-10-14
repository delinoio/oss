// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type AccessState string

const (
	Off             AccessState = "off"
	Ready           AccessState = "ready"
	SetupRequired   AccessState = "setup-required"
	CleanupRequired AccessState = "cleanup-required"
)

type Access struct {
	Enabled  bool        `json:"enabled"`
	State    AccessState `json:"state"`
	Origin   string      `json:"origin,omitempty"`
	ServerID domain.ID   `json:"server_id"`
}
type accessReceipt struct {
	ID      domain.ID `json:"id"`
	Enabled bool      `json:"enabled"`
}
type accessIntent struct {
	Receipts    []accessReceipt `json:"receipts"`
	Version     int             `json:"version"`
	ServerID    domain.ID       `json:"server_id"`
	Enabled     bool            `json:"enabled"`
	Origin      string          `json:"origin"`
	Owner       domain.ID       `json:"owner,omitempty"`
	Session     string          `json:"session,omitempty"`
	Request     domain.ID       `json:"request"`
	LastEnabled bool            `json:"last_enabled"`
}
type Manager struct {
	mu          sync.Mutex
	adapter     Adapter
	server      domain.ID
	handler     func(string) http.Handler
	parent      context.Context
	value       accessIntent
	state       AccessState
	child       *process.Handle
	http        *http.Server
	listener    net.Listener
	ingressDone chan struct{}
	cancel      context.CancelFunc
}

func Open(ctx context.Context, root string, server domain.ID, logger *slog.Logger, handler func(string) http.Handler) (*Manager, error) {
	if err := security.PrivateDir(root); err != nil {
		return nil, err
	}
	m := &Manager{adapter: Adapter{Root: root, Logger: logger}, server: server, handler: handler, parent: ctx, value: accessIntent{Version: 1, ServerID: server}, state: Off}
	raw, err := security.ReadPrivate(filepath.Join(root, "access.json"), 128<<10)
	if err == nil {
		defer clear(raw)
		if domain.Decode(raw, &m.value) != nil || m.value.Version != 1 || m.value.ServerID != server || (m.value.Origin != "" && ValidateOrigin(m.value.Origin) != nil) || (m.value.Owner != "" && m.value.Owner.Validate() != nil) || m.value.Request.Validate() != nil {
			return nil, fail(domain.RecoveryRequired)
		}
		if len(m.value.Receipts) > 1024 {
			return nil, fail(domain.RecoveryRequired)
		}
		seen := map[domain.ID]bool{}
		for _, r := range m.value.Receipts {
			if r.ID.Validate() != nil || seen[r.ID] {
				return nil, fail(domain.RecoveryRequired)
			}
			seen[r.ID] = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Reconcile the retained original owner even after an accepted Disable. The
	// intent is fenced before native cleanup and must survive a failed join.
	if m.value.Owner != "" {
		if err := process.ReconcileOwnerContext(ctx, filepath.Join(root, "serve"), m.value.Owner); err != nil {
			m.state = CleanupRequired
			return m, nil
		}
		m.value.Owner = ""
		m.value.Session = ""
		if err := m.save(); err != nil {
			return nil, err
		}
	}
	if m.value.Enabled {
		if err := m.enable(ctx); err != nil {
			if m.value.Owner != "" {
				m.state = CleanupRequired
			} else {
				m.state = SetupRequired
			}
			logger.WarnContext(ctx, "tailscale_access", "phase", "restart", "code", domain.SafeError(err).Code)
		}
	}
	return m, nil
}
func (m *Manager) save() error {
	raw, err := json.Marshal(m.value)
	if err != nil {
		return err
	}
	return security.WriteAtomic(filepath.Join(m.adapter.Root, "access.json"), raw)
}
func (m *Manager) Status() Access {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.child != nil {
		select {
		case <-m.child.Done():
			m.state = CleanupRequired
		default:
		}
	}
	return Access{m.value.Enabled, m.state, m.value.Origin, m.server}
}
func (m *Manager) Set(ctx context.Context, id domain.ID, enabled bool) (Access, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := id.Validate(); err != nil {
		return Access{}, err
	}
	if err := m.parent.Err(); err != nil {
		return Access{}, err
	}
	replayed := false
	for _, receipt := range m.value.Receipts {
		if receipt.ID == id {
			if receipt.Enabled != enabled {
				return Access{}, fail(domain.Conflict)
			}
			replayed = true
		}
	}
	if replayed && id != m.value.Request {
		return Access{m.value.Enabled, m.state, m.value.Origin, m.server}, nil
	}
	if replayed && enabled && m.state == Ready {
		return Access{m.value.Enabled, m.state, m.value.Origin, m.server}, nil
	}
	if !replayed {
		if len(m.value.Receipts) >= 1024 {
			return Access{}, fail(domain.ResourceExhausted)
		}
		m.value.Receipts = append(m.value.Receipts, accessReceipt{id, enabled})
	}
	if m.value.Request == id && m.value.LastEnabled != enabled {
		return Access{}, fail(domain.Conflict)
	}
	m.value.Request = id
	m.value.Enabled = enabled
	m.value.LastEnabled = enabled
	if err := m.save(); err != nil {
		return Access{}, err
	}
	// Always fence the ingress before joining its original child. Enabled=false
	// does not prove that a previous child's cleanup completed.
	if err := m.cleanup(); err != nil {
		return Access{}, err
	}
	if enabled {
		if err := m.enable(ctx); err != nil {
			if m.value.Owner != "" {
				m.state = CleanupRequired
			} else {
				m.state = SetupRequired
			}
			return Access{enabled, m.state, m.value.Origin, m.server}, err
		}
	}
	return Access{enabled, m.state, m.value.Origin, m.server}, nil
}
func (m *Manager) cleanup() error {
	if m.http != nil {
		_ = m.http.Close()
		<-m.ingressDone
		m.http = nil
		m.listener = nil
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.child != nil {
		err := m.child.Close()
		if err != nil {
			m.state = CleanupRequired
			return err
		}
		_ = m.child.Wait()
		m.child = nil
	}
	if m.value.Owner != "" {
		if err := process.ReconcileOwnerContext(context.Background(), filepath.Join(m.adapter.Root, "serve"), m.value.Owner); err != nil {
			m.state = CleanupRequired
			return err
		}
	}
	m.value.Owner = ""
	m.value.Session = ""
	m.state = Off
	if m.value.Request != "" {
		return m.save()
	}
	return nil
}
func (m *Manager) Close() error { m.mu.Lock(); defer m.mu.Unlock(); return m.cleanup() }
func (m *Manager) enable(ctx context.Context) error {
	executable, err := Resolve()
	if err != nil {
		return err
	}
	m.adapter.Executable = executable
	inventory, err := m.adapter.Status(ctx)
	if err != nil {
		return err
	}
	// Only the explicit NeedsLogin observation is relabelled. Every other
	// non-Running state keeps the generic unavailable message below.
	if inventory.State == LoggedOut {
		m.adapter.logLoginRequired(ctx)
		return LoginRequired()
	}
	if inventory.State != Running || !inventory.HTTPS || inventory.Self == nil {
		return fail(domain.Unavailable)
	}
	origin, err := Origin(inventory.Self.Host)
	if err != nil {
		return err
	}
	if m.value.Origin != "" && m.value.Origin != origin {
		return fail(domain.Conflict)
	}
	raw, err := m.adapter.Read(ctx, "serve", "status", "--json")
	if err != nil {
		return err
	}
	if !EmptyServe(raw) {
		return fail(domain.Conflict)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail(domain.Unavailable)
	}
	m.listener = listener
	m.http = &http.Server{Handler: m.handler(origin), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return m.parent }}
	m.ingressDone = make(chan struct{})
	ingress, done := m.http, m.ingressDone
	go func() { defer close(done); _ = ingress.Serve(listener) }()
	target := "http://" + listener.Addr().String()
	m.value.Origin = origin
	m.value.Owner = domain.NewID()
	// An empty private owner index is retained before process startup. Recovery
	// can prove nonlaunch without interpreting a missing journal as completion.
	if err := security.PrivateDir(filepath.Join(m.adapter.Root, "serve", string(m.value.Owner))); err != nil {
		return errors.Join(err, m.cleanup())
	}
	if err := m.save(); err != nil {
		return errors.Join(err, m.cleanup())
	}
	child, cancel := context.WithCancel(m.parent)
	m.cancel = cancel
	config := m.adapter.config(m.value.Owner, []string{"serve", "--https=8443", target}, io.Discard)
	config.Directory = filepath.Join(m.adapter.Root, "serve")
	handle, err := process.Start(child, config)
	if err != nil {
		return errors.Join(err, m.cleanup())
	}
	m.child = handle
	// The installed CLI creates its own IPN watcher SessionID and performs the
	// parent ETag/If-Match CAS. No --bg, --yes, service, reset, off or Funnel path
	// is admitted. Backend CAS/port rejection protects the race after our read.
	if err := handle.Resume(); err != nil {
		return errors.Join(err, m.cleanup())
	}
	_ = handle.CloseInput()
	deadline, cancelRead := context.WithTimeout(ctx, 15*time.Second)
	defer cancelRead()
	for {
		select {
		case <-handle.Done():
			return errors.Join(fail(domain.Unavailable), m.cleanup())
		default:
		}
		raw, err = m.adapter.Read(deadline, "serve", "status", "--json")
		session := ForegroundSession(raw, target, inventory.Self.Host+":8443")
		if err == nil && session != "" {
			m.value.Session = session
			if err = m.save(); err != nil {
				return errors.Join(err, m.cleanup())
			}
			var identity Identity
			if err = Probe(deadline, origin, &identity); err == nil && identity.Version == 1 && identity.ServerID == m.server && identity.Origin == origin && identity.ProtocolVersion == rpc.ProtocolVersion && identity.Approval {
				m.state = Ready
				go func(original *process.Handle) {
					<-original.Done()
					m.mu.Lock()
					defer m.mu.Unlock()
					if m.child == original {
						if err := m.cleanup(); err != nil {
							m.state = CleanupRequired
						} else {
							m.state = SetupRequired
						}
					}
				}(handle)
				m.adapter.Logger.InfoContext(ctx, "tailscale_access", "phase", "ready")
				return nil
			}
		}
		select {
		case <-deadline.Done():
			return errors.Join(fail(domain.Unavailable), m.cleanup())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Only one newly observed foreground configuration with exactly the requested
// HTTPS port/root proxy can prove readiness. Matching background/foreign scopes
// cannot be adopted as the child started from an empty parent configuration.
type foregroundConfig struct {
	TCP map[string]struct {
		HTTPS         bool
		HTTP          bool
		TCPForward    string
		TerminateTLS  string
		ProxyProtocol int
	}
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy         string
			Path          string
			Text          string
			AcceptAppCaps []string
			Redirect      string
		}
	}
	AllowFunnel map[string]bool
	Services    map[string]json.RawMessage
	Foreground  map[string]*foregroundConfig
}

func ForegroundSession(raw []byte, target, hostPort string) string {
	var config foregroundConfig
	if len(raw) > 1<<20 || domain.Decode(raw, &config) != nil || len(config.TCP) != 0 || len(config.Web) != 0 || len(config.AllowFunnel) != 0 || len(config.Services) != 0 || len(config.Foreground) != 1 {
		return ""
	}
	for session, value := range config.Foreground {
		if value == nil || session == "" || len(session) > 256 || len(value.TCP) != 1 || len(value.AllowFunnel) != 0 || len(value.Services) != 0 || len(value.Foreground) != 0 || len(value.Web) != 1 {
			return ""
		}
		tcp := value.TCP["8443"]
		if !tcp.HTTPS || tcp.HTTP || tcp.TCPForward != "" || tcp.TerminateTLS != "" || tcp.ProxyProtocol != 0 {
			return ""
		}
		web, ok := value.Web[hostPort]
		if !ok || len(web.Handlers) != 1 {
			return ""
		}
		handler, ok := web.Handlers["/"]
		if !ok || handler.Proxy != target || handler.Path != "" || handler.Text != "" || len(handler.AcceptAppCaps) != 0 || handler.Redirect != "" {
			return ""
		}
		return session
	}
	return ""
}

type Identity struct {
	Version         int       `json:"version"`
	ServerID        domain.ID `json:"server_id"`
	Origin          string    `json:"origin"`
	ProtocolVersion int       `json:"protocol_version"`
	Approval        bool      `json:"approval"`
}

// Probe sends only one normal-TLS request to the exact admitted origin. There
// are no redirects, ambient proxies, alternative ports or connectivity scans.
func Probe(ctx context.Context, origin string, out any) error {
	if err := ValidateOrigin(origin); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/delidev/tailscale/v1/identity", nil)
	if err != nil {
		return fail(domain.InvalidArgument)
	}
	res, err := client.Do(req)
	if err != nil {
		return fail(domain.Unavailable)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8193))
	if err != nil || len(raw) > 8192 || res.StatusCode != http.StatusOK {
		return fail(domain.Unavailable)
	}
	if out != nil && domain.Decode(raw, out) != nil {
		return fail(domain.Unsupported)
	}
	return nil
}
