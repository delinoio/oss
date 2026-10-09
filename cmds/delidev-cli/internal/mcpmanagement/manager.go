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
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
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

type OAuthToken struct {
	Refresh   string    `json:"refresh,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Secrets struct {
	OAuth       *OAuthToken       `json:"oauth,omitempty"`
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
	ExpiresAt      time.Time            `json:"expires_at,omitempty"`
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
	Owner    domain.ID `json:"owner"`
	Server   domain.ID `json:"server"`
	Revision uint64    `json:"revision,string"`
}
type preparation struct {
	Actor     domain.ID       `json:"actor"`
	Digest    string          `json:"digest"`
	Reference credentials.Ref `json:"reference"`
}
type catalog struct {
	MachineID domain.ID                  `json:"machine_id,omitempty"`
	Deleted   map[domain.ID]bool         `json:"deleted"`
	OAuth     map[domain.ID]oauthAttempt `json:"oauth,omitempty"`
	Prepared  map[domain.ID]preparation  `json:"prepared"`
	Version   uint32                     `json:"version"`
	ServerID  domain.ID                  `json:"server_id"`
	DeviceID  domain.ID                  `json:"device_id"`
	Servers   map[domain.ID]server       `json:"servers"`
	Receipts  map[domain.ID]receipt      `json:"receipts"`
	Pins      map[string]snapshot        `json:"pins"`
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
func Open(root string, serverID, deviceID, machineID domain.ID, logger *slog.Logger) (*Manager, error) {
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
	if machineID.Validate() != nil || m.state.MachineID != "" && m.state.MachineID != machineID {
		vault.Close()
		lock.Close()
		return nil, unavailable()
	}
	m.state.MachineID = machineID
	if e := m.persist(); e != nil {
		vault.Close()
		lock.Close()
		return nil, e
	}
	m.lock, m.ownedVault, m.logger = lock, vault, logger
	return m, nil
}
func open(root string, serverID, deviceID domain.ID, vault SecretStore) (*Manager, error) {
	m := &Manager{root: root, vault: vault, state: catalog{Prepared: map[domain.ID]preparation{}, Deleted: map[domain.ID]bool{}, Version: 1, ServerID: serverID, DeviceID: deviceID, Servers: map[domain.ID]server{}, Receipts: map[domain.ID]receipt{}, Pins: map[string]snapshot{}}}
	raw, e := security.ReadPrivate(filepath.Join(root, "catalog.json"), 4<<20)
	if errors.Is(e, os.ErrNotExist) {
		return m, m.persist()
	}
	if e != nil || domain.Decode(raw, &m.state) != nil || m.state.Version != 1 || m.state.ServerID != serverID || m.state.DeviceID != deviceID || m.state.Servers == nil || m.state.Receipts == nil || m.state.Pins == nil || m.state.Deleted == nil || m.state.Prepared == nil || len(m.state.Servers) > 128 || len(m.state.Receipts) > 4096 {
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
	if e := m.validateClaims(); e != nil {
		return nil, e
	}
	return m, nil
}
func (m *Manager) validateClaims() error {
	if len(m.state.Prepared) > 4352 || len(m.state.Pins) > 4096 || len(m.state.OAuth) > 256 || len(m.state.Deleted) > 4096 {
		return unavailable()
	}
	validRef := func(ref credentials.Ref, owner domain.ID) bool {
		return ref.Owner == owner && ref.ID.Validate() == nil && ref.Purpose == credentials.MCPServer
	}
	for id, source := range m.state.Servers {
		if source.Current == 0 || len(source.Generations) > 4096 {
			return unavailable()
		}
		for revision, g := range source.Generations {
			if revision == 0 || g.Secret != nil && !validRef(*g.Secret, id) {
				return unavailable()
			}
		}
		if source.Deleting != nil && (source.Deleting.ServerID != id || source.Deleting.Operation != Delete || !source.Deleting.Confirmed || source.Deleting.Secrets != nil || source.Deleting.ID.Validate() != nil || source.Deleting.Actor.Validate() != nil) {
			return unavailable()
		}
	}
	for id, receipt := range m.state.Receipts {
		if id.Validate() != nil || receipt.Actor.Validate() != nil || len(receipt.Digest) != 64 {
			return unavailable()
		}
	}
	for id, claim := range m.state.Prepared {
		if id.Validate() != nil || claim.Actor.Validate() != nil || claim.Reference.Owner.Validate() != nil || !validRef(claim.Reference, claim.Reference.Owner) || claim.Reference.ID != id || len(claim.Digest) != 64 {
			return unavailable()
		}
	}
	for key, pin := range m.state.Pins {
		source, ok := m.state.Servers[pin.Server]
		if !ok || pin.Owner.Validate() != nil || key != string(pin.Owner)+"/"+string(pin.Server) || source.Generations[pin.Revision].Entry.Definition.Revision != pin.Revision {
			return unavailable()
		}
	}
	for id, a := range m.state.OAuth {
		if id != a.ID || id.Validate() != nil || a.Actor.Validate() != nil || a.Server.Validate() != nil || a.Revision == 0 || !callbackURL(a.Callback) || !validRef(a.Secret, a.Server) || a.Secret.ID != id || len(a.StartDigest) != 64 {
			return unavailable()
		}
		switch a.Phase {
		case "registration-started", "awaiting-callback", "exchange-started", "cancel-started", "canceled", "complete", "catalog-deleted":
		default:
			return unavailable()
		}
		if a.CompletionID != "" && (a.CompletionID.Validate() != nil || len(a.CompletionDigest) != 64) {
			return unavailable()
		}
	}
	for id := range m.state.Deleted {
		if id.Validate() != nil {
			return unavailable()
		}
	}
	return nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = true
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
		m.failed = true
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
			entry := s.Generations[s.Current].Entry
			if !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt) {
				entry.Authentication = Required
			}
			rows = append(rows, entry)
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
	defer clear(raw)
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
	if m.state.Deleted[r.ServerID] {
		return Result{}, conflict()
	}
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
		if r.Definition == nil || r.Definition.ID != r.ServerID || r.Definition.Revision != r.Revision || r.Enabled != nil || r.Definition.Validate() != nil {
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
		if !m.inventoryFits(r.ServerID, def) {
			return result, domain.Fail(domain.ResourceExhausted, "The MCP inventory exceeds its response bound.", "Reduce non-secret arguments and metadata before saving another definition.")
		}
		g := generation{Entry: Entry{Definition: def, Authentication: Required}}
		if exists {
			g.Secret = s.Generations[s.Current].Secret
			g.Entry.Authentication = s.Generations[s.Current].Entry.Authentication
			g.Entry.ExpiresAt = s.Generations[s.Current].Entry.ExpiresAt
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
			defer clear(secret)
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
			g.Entry.ExpiresAt = time.Time{}
		} else if exists {
			old := s.Generations[s.Current].Entry.Definition
			if old.Transport != def.Transport || old.Endpoint != def.Endpoint || old.Authentication != def.Authentication || !sameNames(old, def) {
				g.Secret = nil
				g.Entry.Authentication = Required
				g.Entry.ExpiresAt = time.Time{}
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
			m.state.Deleted[r.ServerID] = true
			result.Deleted = true
			break
		}
		if e := m.cleanupSecrets(ctx, r.ServerID, s); e != nil {
			return result, e
		}
		delete(m.state.Servers, r.ServerID)
		m.state.Deleted[r.ServerID] = true
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

// Bound current public metadata before admitting a save. Otherwise several
// individually valid definitions could make the entire catalog unreadable.
func (m *Manager) inventoryFits(id domain.ID, definition domain.MCPDefinition) bool {
	result := &pb.ListMcpServersResponse{}
	for key, source := range m.state.Servers {
		if key == id || source.Retired || source.Deleting != nil {
			continue
		}
		result.Servers = append(result.Servers, EntryToWire(source.Generations[source.Current].Entry, m.state.ServerID, m.state.DeviceID))
	}
	result.Servers = append(result.Servers, EntryToWire(Entry{Definition: definition, Authentication: Required}, m.state.ServerID, m.state.DeviceID))
	return proto.Size(result) <= 192<<10
}

func sameNames(a, b domain.MCPDefinition) bool {
	aa, _ := json.Marshal([]any{a.EnvironmentNames, a.HeaderNames})
	bb, _ := json.Marshal([]any{b.EnvironmentNames, b.HeaderNames})
	return string(aa) == string(bb)
}
func validateSecrets(d domain.MCPDefinition, s Secrets) error {
	if s.OAuth != nil || d.Authentication != domain.MCPManual || len(s.Environment) != len(d.EnvironmentNames) || len(s.Headers) != len(d.HeaderNames) {
		return invalid()
	}
	for _, n := range d.EnvironmentNames {
		if value, ok := s.Environment[n]; !ok || strings.ContainsRune(value, 0) || domain.Text(value, "protected MCP value", 16<<10, true) != nil {
			return invalid()
		}
	}
	for _, n := range d.HeaderNames {
		if value, ok := s.Headers[n]; !ok || strings.ContainsAny(value, "\r\n\x00") || domain.Text(value, "protected MCP value", 16<<10, true) != nil {
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
	return m.pinLocked(ctx, owner, selection)
}

// PinCurrent freezes the current generation for a new accepted dependent owner.
// Retries keep the previously accepted generation, even after catalog edits.
func (m *Manager) PinCurrent(ctx context.Context, owner domain.ID, selection domain.MCPSelection) (Entry, *Secrets, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if source, ok := m.state.Servers[selection.ServerID]; ok {
		selection.Revision = source.Current
	}
	return m.pinLocked(ctx, owner, selection)
}
func (m *Manager) pinLocked(ctx context.Context, owner domain.ID, selection domain.MCPSelection) (Entry, *Secrets, error) {
	if m.failed || owner.Validate() != nil || selection.DeviceID != m.state.DeviceID || m.state.MachineID != "" && selection.MachineID != m.state.MachineID {
		return Entry{}, nil, unavailable()
	}
	key := string(owner) + "/" + string(selection.ServerID)
	source, ok := m.state.Servers[selection.ServerID]
	if !ok {
		return Entry{}, nil, conflict()
	}
	pin, accepted := m.state.Pins[key]
	revision := selection.Revision
	if accepted {
		revision = pin.Revision
	} else {
		if source.Retired || source.Deleting != nil || selection.Revision != source.Current {
			return Entry{}, nil, conflict()
		}
	}
	g, ok := source.Generations[revision]
	if !ok {
		return Entry{}, nil, unavailable()
	}
	if !accepted {
		if (!g.Entry.ExpiresAt.IsZero() && time.Now().After(g.Entry.ExpiresAt)) || !g.Entry.Definition.Enabled || (g.Entry.Authentication != Ready && g.Entry.Authentication != NotRequired) {
			return Entry{}, nil, domain.Fail(domain.Unavailable, "The selected MCP definition is not ready.", "Enable and authenticate it explicitly before a new execution.")
		}
		m.state.Pins[key] = snapshot{Owner: owner, Server: selection.ServerID, Revision: revision}
		if e := m.persist(); e != nil {
			return Entry{}, nil, e
		}
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
	if m.failed || owner.Validate() != nil {
		return unavailable()
	}
	for key, pin := range m.state.Pins {
		if pin.Owner != owner {
			continue
		}
		source, ok := m.state.Servers[pin.Server]
		other := false
		for otherKey, p := range m.state.Pins {
			if otherKey != key && p.Server == pin.Server {
				other = true
			}
		}
		// Keep the original pin durable until the last retired catalog cleanup is
		// confirmed. A failed deletion can therefore resume only this original owner.
		if ok && source.Retired && !other {
			if e := m.cleanupSecrets(ctx, pin.Server, source); e != nil {
				return e
			}
			delete(m.state.Servers, pin.Server)
		}
		delete(m.state.Pins, key)
		if e := m.persist(); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) cleanupSecrets(ctx context.Context, id domain.ID, source server) error {
	seen := map[credentials.Ref]bool{}
	refs := []credentials.Ref{}
	for _, g := range source.Generations {
		if g.Secret != nil {
			refs = append(refs, *g.Secret)
		}
	}
	for _, p := range m.state.Prepared {
		if p.Reference.Owner == id {
			refs = append(refs, p.Reference)
		}
	}
	for _, a := range m.state.OAuth {
		if a.Server == id {
			refs = append(refs, a.Secret)
		}
	}
	for _, ref := range refs {
		if !seen[ref] {
			if e := m.vault.Delete(ctx, ref); e != nil {
				return e
			}
			seen[ref] = true
		}
	}
	for key, p := range m.state.Prepared {
		if p.Reference.Owner == id {
			delete(m.state.Prepared, key)
		}
	}
	for key, a := range m.state.OAuth {
		if a.Server == id {
			a.Authorization = ""
			a.State = Canceled
			a.Phase = "catalog-deleted"
			m.state.OAuth[key] = a
		}
	}
	return nil
}

// RejectedWithoutEffect is positive original-journal evidence. A failed or
// poisoned owner cannot establish it. It is never inferred from an HTTP error.
func (m *Manager) RejectedWithoutEffect(request domain.ID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed || request.Validate() != nil {
		return false
	}
	if _, ok := m.state.Prepared[request]; ok {
		return false
	}
	if _, ok := m.state.Receipts[request]; ok {
		return false
	}
	for _, s := range m.state.Servers {
		if s.Deleting != nil && s.Deleting.ID == request {
			return false
		}
	}
	for _, a := range m.state.OAuth {
		if a.ID == request || a.CompletionID == request {
			return false
		}
	}
	return true
}
