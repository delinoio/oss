// SPDX-License-Identifier: Apache-2.0
// Package mcpmanagement owns the selected Worker's private MCP catalog. Catalog
// operations do not start an MCP executable or establish native runtime support.
package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type Operation string

const (
	Save   Operation = "save"
	Enable Operation = "enable"
	Delete Operation = "delete"
)

type AuthState string

const (
	NotRequired AuthState = "not-required"
	Required    AuthState = "required"
	Pending     AuthState = "pending"
	Ready       AuthState = "ready"
	Canceled    AuthState = "canceled"
	Uncertain   AuthState = "uncertain"
)

type Secrets struct {
	Environment map[string]string `json:"environment"`
	Headers     map[string]string `json:"headers"`
}
type Request struct {
	ID         domain.ID             `json:"id"`
	Actor      domain.ID             `json:"actor"`
	ServerID   domain.ID             `json:"server_id"`
	Revision   uint64                `json:"revision,string"`
	Operation  Operation             `json:"operation"`
	Definition *domain.MCPDefinition `json:"definition,omitempty"`
	Enabled    *bool                 `json:"enabled,omitempty"`
	Secrets    *Secrets              `json:"secrets,omitempty"`
	Confirmed  bool                  `json:"confirmed"`
}
type Entry struct {
	Definition     domain.MCPDefinition `json:"definition"`
	Authentication AuthState            `json:"authentication"`
}
type Result struct {
	Entry    *Entry `json:"entry,omitempty"`
	Deleted  bool   `json:"deleted"`
	Replayed bool   `json:"replayed"`
}
type receipt struct {
	Actor  domain.ID `json:"actor"`
	Digest string    `json:"digest"`
	Result Result    `json:"result"`
}
type generation struct {
	Entry  Entry            `json:"entry"`
	Secret *credentials.Ref `json:"secret,omitempty"`
}
type server struct {
	Retired     bool                  `json:"retired"`
	Current     uint64                `json:"current,string"`
	Generations map[uint64]generation `json:"generations"`
	Deleting    *Request              `json:"deleting,omitempty"`
}
type snapshot struct {
	Server   domain.ID `json:"server"`
	Revision uint64    `json:"revision,string"`
}
type preparation struct {
	Actor     domain.ID       `json:"actor"`
	Digest    string          `json:"digest"`
	Reference credentials.Ref `json:"reference"`
}
type catalog struct {
	Prepared map[domain.ID]preparation `json:"prepared"`
	Version  uint32                    `json:"version"`
	ServerID domain.ID                 `json:"server_id"`
	DeviceID domain.ID                 `json:"device_id"`
	Servers  map[domain.ID]server      `json:"servers"`
	Receipts map[domain.ID]receipt     `json:"receipts"`
	Pins     map[domain.ID]snapshot    `json:"pins"`
}
type SecretStore interface {
	Put(context.Context, credentials.Ref, []byte) (string, error)
	Get(context.Context, credentials.Ref) ([]byte, error)
	Delete(context.Context, credentials.Ref) error
}
type Manager struct {
	failed     bool
	mu         sync.Mutex
	root       string
	state      catalog
	vault      SecretStore
	ownedVault *credentials.Vault
	lock       *security.Lock
	logger     *slog.Logger
}

func unavailable() error {
	return domain.Fail(domain.RecoveryRequired, "The MCP catalog requires original recovery.", "Retain the original Worker and retry the exact request after checking its protected storage.")
}
func conflict() error {
	return domain.Fail(domain.Conflict, "The MCP definition changed.", "Refresh the selected Runner's catalog and confirm a new change.")
}
func invalid() error {
	return domain.Fail(domain.InvalidArgument, "Invalid MCP management request.", "Use the selected Runner and the original definition revision.")
}
func Open(root string, serverID, deviceID domain.ID, logger *slog.Logger) (*Manager, error) {
	root = filepath.Join(root, "mcp-management")
	if serverID.Validate() != nil || deviceID.Validate() != nil {
		return nil, invalid()
	}
	if e := security.PrivateDir(root); e != nil {
		return nil, e
	}
	lock, e := security.TryLock(filepath.Join(root, "catalog.lock"))
	if e != nil {
		return nil, e
	}
	vault, e := credentials.Open(filepath.Join(root, "vault"), serverID, logger)
	if e != nil {
		lock.Close()
		return nil, e
	}
	m, e := open(root, serverID, deviceID, vault)
	if e != nil {
		vault.Close()
		lock.Close()
		return nil, e
	}
	m.lock, m.ownedVault, m.logger = lock, vault, logger
	return m, nil
}
func open(root string, serverID, deviceID domain.ID, vault SecretStore) (*Manager, error) {
	m := &Manager{root: root, vault: vault, state: catalog{Prepared: map[domain.ID]preparation{}, Version: 1, ServerID: serverID, DeviceID: deviceID, Servers: map[domain.ID]server{}, Receipts: map[domain.ID]receipt{}, Pins: map[domain.ID]snapshot{}}}
	raw, e := security.ReadPrivate(filepath.Join(root, "catalog.json"), 4<<20)
	if errors.Is(e, os.ErrNotExist) {
		return m, m.persist()
	}
	if e != nil || domain.Decode(raw, &m.state) != nil || m.state.Version != 1 || m.state.ServerID != serverID || m.state.DeviceID != deviceID || m.state.Servers == nil || m.state.Receipts == nil || m.state.Pins == nil || m.state.Prepared == nil || len(m.state.Servers) > 128 || len(m.state.Receipts) > 4096 {
		return nil, unavailable()
	}
	for id, s := range m.state.Servers {
		if id.Validate() != nil || s.Generations == nil || s.Generations[s.Current].Entry.Definition.ID != id {
			return nil, unavailable()
		}
		for r, g := range s.Generations {
			if g.Entry.Definition.Validate() != nil || g.Entry.Definition.Revision != r {
				return nil, unavailable()
			}
		}
	}
	return m, nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ownedVault != nil {
		m.ownedVault.Close()
		m.ownedVault = nil
	}
	if m.lock != nil {
		m.lock.Close()
		m.lock = nil
	}
}
func (m *Manager) persist() error {
	raw, e := json.Marshal(m.state)
	if e != nil || len(raw) > 4<<20 {
		return unavailable()
	}
	if e = security.WriteAtomicOwned(filepath.Join(m.root, "catalog.json"), raw); e != nil {
		m.failed = true
	}
	return e
}
func (m *Manager) List() ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed {
		return nil, unavailable()
	}
	rows := []Entry{}
	for _, s := range m.state.Servers {
		if s.Deleting == nil && !s.Retired {
			rows = append(rows, s.Generations[s.Current].Entry)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Definition.Name < rows[j].Definition.Name })
	return rows, nil
}
func (m *Manager) Mutate(ctx context.Context, r Request) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed {
		return Result{}, unavailable()
	}
	if r.ID.Validate() != nil || r.Actor.Validate() != nil || r.ServerID.Validate() != nil {
		return Result{}, invalid()
	}
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if old, ok := m.state.Receipts[r.ID]; ok {
		if old.Actor != r.Actor || old.Digest != digest {
			return Result{}, conflict()
		}
		result := old.Result
		result.Replayed = true
		return result, nil
	}
	if len(m.state.Receipts) >= 4096 {
		return Result{}, domain.Fail(domain.ResourceExhausted, "The MCP receipt ledger is full.", "Retain it for recovery; no original receipt is discarded automatically.")
	}
	s, exists := m.state.Servers[r.ServerID]
	if exists && (s.Retired || s.Current == math.MaxUint64) {
		return Result{}, conflict()
	}
	if !exists && r.Revision != 0 || exists && s.Current != r.Revision {
		return Result{}, conflict()
	}
	if exists && s.Deleting != nil {
		prior, _ := json.Marshal(*s.Deleting)
		if string(prior) != string(raw) {
			return Result{}, unavailable()
		}
	}
	if s.Generations == nil {
		s.Generations = map[uint64]generation{}
	}
	result := Result{}
	switch r.Operation {
	case Save:
		if r.Definition == nil || r.Definition.ID != r.ServerID || r.Enabled != nil || r.Definition.Validate() != nil {
			return result, invalid()
		}
		if !exists && len(m.state.Servers) >= 128 {
			return result, domain.Fail(domain.ResourceExhausted, "The MCP catalog is full.", "Remove an unreferenced definition explicitly.")
		}
		for id, other := range m.state.Servers {
			if id != r.ServerID && other.Generations[other.Current].Entry.Definition.Name == r.Definition.Name {
				return result, conflict()
			}
		}
		def := *r.Definition
		def.Revision = r.Revision + 1
		g := generation{Entry: Entry{Definition: def, Authentication: Required}}
		if exists {
			g.Secret = s.Generations[s.Current].Secret
			g.Entry.Authentication = s.Generations[s.Current].Entry.Authentication
		}
		if def.Authentication == domain.MCPAnonymous {
			g.Secret = nil
			g.Entry.Authentication = NotRequired
		}
		if r.Secrets != nil {
			if validateSecrets(def, *r.Secrets) != nil {
				return result, invalid()
			}
			ref := credentials.Ref{Owner: r.ServerID, ID: r.ID, Purpose: credentials.MCPServer}
			secret, _ := json.Marshal(r.Secrets)
			if prior, ok := m.state.Prepared[r.ID]; ok {
				if prior.Actor != r.Actor || prior.Digest != digest || prior.Reference != ref {
					return result, conflict()
				}
			} else {
				m.state.Prepared[r.ID] = preparation{Actor: r.Actor, Digest: digest, Reference: ref}
				if e := m.persist(); e != nil {
					return result, e
				}
			}
			if _, e := m.vault.Put(ctx, ref, secret); e != nil {
				return result, e
			}
			g.Secret = &ref
			g.Entry.Authentication = Ready
		} else if exists {
			old := s.Generations[s.Current].Entry.Definition
			if old.Transport != def.Transport || old.Endpoint != def.Endpoint || old.Authentication != def.Authentication || !sameNames(old, def) {
				g.Secret = nil
				g.Entry.Authentication = Required
			}
		}
		if def.Authentication == domain.MCPAnonymous {
			g.Entry.Authentication = NotRequired
		}
		s.Current = def.Revision
		s.Generations[s.Current] = g
		m.state.Servers[r.ServerID] = s
		entry := g.Entry
		result.Entry = &entry
	case Enable:
		if !exists || r.Enabled == nil || r.Definition != nil || r.Secrets != nil {
			return result, invalid()
		}
		g := s.Generations[s.Current]
		g.Entry.Definition.Revision++
		g.Entry.Definition.Enabled = *r.Enabled
		s.Current++
		s.Generations[s.Current] = g
		m.state.Servers[r.ServerID] = s
		entry := g.Entry
		result.Entry = &entry
	case Delete:
		if !exists || !r.Confirmed || r.Definition != nil || r.Secrets != nil || r.Enabled != nil {
			return result, invalid()
		}
		hasPins := false
		for _, pin := range m.state.Pins {
			if pin.Server == r.ServerID {
				hasPins = true
			}
		}
		// Persist removal intent before touching protected references. Restart can
		// continue only this original confirmed request; no replacement may adopt it.
		copy := r
		s.Deleting = &copy
		m.state.Servers[r.ServerID] = s
		if e := m.persist(); e != nil {
			return result, e
		}
		if hasPins {
			s.Retired = true
			m.state.Servers[r.ServerID] = s
			result.Deleted = true
			break
		}
		seen := map[credentials.Ref]bool{}
		for _, g := range s.Generations {
			if g.Secret != nil && !seen[*g.Secret] {
				if e := m.vault.Delete(ctx, *g.Secret); e != nil {
					return result, e
				}
				seen[*g.Secret] = true
			}
		}
		for id, p := range m.state.Prepared {
			if p.Reference.Owner == r.ServerID && !seen[p.Reference] {
				if e := m.vault.Delete(ctx, p.Reference); e != nil {
					return result, e
				}
				delete(m.state.Prepared, id)
			}
		}
		delete(m.state.Servers, r.ServerID)
		result.Deleted = true
	default:
		return result, invalid()
	}
	m.state.Receipts[r.ID] = receipt{Actor: r.Actor, Digest: digest, Result: result}
	if e := m.persist(); e != nil {
		return Result{}, e
	}
	if m.logger != nil {
		m.logger.InfoContext(ctx, "mcp_catalog_mutation", "operation_id", r.ID, "server_id", r.ServerID, "operation", r.Operation, "revision", r.Revision, "outcome", "confirmed")
	}
	return result, nil
}
func sameNames(a, b domain.MCPDefinition) bool {
	aa, _ := json.Marshal([]any{a.EnvironmentNames, a.HeaderNames})
	bb, _ := json.Marshal([]any{b.EnvironmentNames, b.HeaderNames})
	return string(aa) == string(bb)
}
func validateSecrets(d domain.MCPDefinition, s Secrets) error {
	if d.Authentication != domain.MCPManual || len(s.Environment) != len(d.EnvironmentNames) || len(s.Headers) != len(d.HeaderNames) {
		return invalid()
	}
	for _, n := range d.EnvironmentNames {
		if value, ok := s.Environment[n]; !ok || domain.Text(value, "protected MCP value", 16<<10, true) != nil {
			return invalid()
		}
	}
	for _, n := range d.HeaderNames {
		if value, ok := s.Headers[n]; !ok || domain.Text(value, "protected MCP value", 16<<10, true) != nil {
			return invalid()
		}
	}
	raw, _ := json.Marshal(s)
	if len(raw) > credentials.MaxSecretBytes {
		return invalid()
	}
	return nil
}

// Pin is called only by an accepted execution's original Worker owner. It does
// not expose protected values to the server or public management inventory.
func (m *Manager) Pin(ctx context.Context, owner domain.ID, selection domain.MCPSelection) (Entry, *Secrets, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed || owner.Validate() != nil || selection.DeviceID != m.state.DeviceID {
		return Entry{}, nil, unavailable()
	}
	s, ok := m.state.Servers[selection.ServerID]
	if !ok || s.Retired || s.Deleting != nil || selection.Revision != s.Current {
		return Entry{}, nil, conflict()
	}
	g := s.Generations[s.Current]
	if !g.Entry.Definition.Enabled || (g.Entry.Authentication != Ready && g.Entry.Authentication != NotRequired) {
		return Entry{}, nil, domain.Fail(domain.Unavailable, "The selected MCP definition is not ready.", "Enable and authenticate it explicitly before a new execution.")
	}
	pin := snapshot{Server: selection.ServerID, Revision: selection.Revision}
	if prior, ok := m.state.Pins[owner]; ok && prior != pin {
		return Entry{}, nil, conflict()
	}
	m.state.Pins[owner] = pin
	if e := m.persist(); e != nil {
		return Entry{}, nil, e
	}
	if g.Secret == nil {
		return g.Entry, nil, nil
	}
	raw, e := m.vault.Get(ctx, *g.Secret)
	if e != nil {
		return Entry{}, nil, e
	}
	var secret Secrets
	if domain.Decode(raw, &secret) != nil {
		return Entry{}, nil, unavailable()
	}
	return g.Entry, &secret, nil
}

// Release joins the dependent owner's confirmed cleanup before catalog-owned
// credentials can disappear. Deletion never removes another owner's snapshot.
func (m *Manager) Release(ctx context.Context, owner domain.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed {
		return unavailable()
	}
	pin, ok := m.state.Pins[owner]
	if !ok {
		return nil
	}
	delete(m.state.Pins, owner)
	if e := m.persist(); e != nil {
		return e
	}
	s, ok := m.state.Servers[pin.Server]
	if !ok || !s.Retired {
		return nil
	}
	for _, p := range m.state.Pins {
		if p.Server == pin.Server {
			return nil
		}
	}
	for id, p := range m.state.Prepared {
		if p.Reference.Owner == pin.Server {
			if e := m.vault.Delete(ctx, p.Reference); e != nil {
				return e
			}
			delete(m.state.Prepared, id)
		}
	}
	delete(m.state.Servers, pin.Server)
	return m.persist()
}
