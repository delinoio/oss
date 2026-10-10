// SPDX-License-Identifier: Apache-2.0
// Package managedmcp owns only explicitly managed Worker definitions. It never
// adopts native host configuration or launches a configured MCP executable.
package managedmcp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Secrets is injected for offline fixtures. Production uses the selected
// Worker's native credential vault, never the product server's credential store.
type Secrets interface {
	Put(context.Context, credentials.Ref, []byte) (string, error)
	Get(context.Context, credentials.Ref) ([]byte, error)
	Delete(context.Context, credentials.Ref) error
}
type Manager struct {
	Root           string
	ServerID       domain.ID
	MachineID      domain.ID
	WorkerDeviceID domain.ID
	Logger         *slog.Logger
	Secrets        Secrets
	HTTP           *http.Client
	Now            func() time.Time
}
type entry struct {
	Definition domain.ManagedMCPDefinition `json:"definition"`
	Deleted    bool                        `json:"deleted,omitempty"`
}
type receipt struct {
	ActorID domain.ID               `json:"actor_id"`
	Digest  string                  `json:"digest"`
	Result  domain.ManagedMCPResult `json:"result"`
}
type oauthAttempt struct {
	ActorID          domain.ID                `json:"actor_id"`
	DefinitionID     domain.ID                `json:"definition_id"`
	Revision         uint64                   `json:"revision"`
	State            domain.MCPOperationState `json:"state"`
	ExpiresAt        time.Time                `json:"expires_at"`
	AuthorizationURL string                   `json:"authorization_url"`
	StateDigest      string                   `json:"state_digest"`
}
type catalog struct {
	Version        uint32                                 `json:"version"`
	ServerID       domain.ID                              `json:"server_id"`
	MachineID      domain.ID                              `json:"machine_id"`
	WorkerDeviceID domain.ID                              `json:"worker_device_id"`
	ReceiptKey     string                                 `json:"receipt_key"`
	Entries        map[domain.ID]entry                    `json:"entries"`
	Generations    map[string]domain.ManagedMCPDefinition `json:"generations"`
	Receipts       map[domain.ID]receipt                  `json:"receipts"`
	Attempts       map[domain.ID]oauthAttempt             `json:"attempts"`
}

func unavailable() error {
	return domain.Fail(domain.RecoveryRequired, "The original MCP operation requires recovery.", "Inspect its original request; do not repeat an uncertain native exchange.")
}
func (m Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}
func (m Manager) Execute(ctx context.Context, q domain.ManagedMCPRequest) (domain.ManagedMCPResult, error) {
	empty := domain.ManagedMCPResult{Definitions: []domain.ManagedMCPDefinition{}}
	if q.ServerID != m.ServerID || q.MachineID != m.MachineID || q.WorkerDeviceID != m.WorkerDeviceID || q.ActorID.Validate() != nil || q.RequestID.Validate() != nil || q.WorkerInstanceID.Validate() != nil {
		return empty, domain.Fail(domain.PermissionDenied, "MCP ownership changed.", "Select the original authenticated Runner Device.")
	}
	root := filepath.Join(m.Root, "managed-mcp", string(m.ServerID), string(m.WorkerDeviceID))
	if err := security.PrivateDir(root); err != nil {
		return empty, err
	}
	lock, err := security.TryLock(filepath.Join(root, "catalog.lock"))
	if err != nil {
		return empty, domain.Fail(domain.Conflict, "MCP management is busy.", "Wait for the original Worker operation.")
	}
	defer lock.Close()
	path := filepath.Join(root, "catalog.json")
	c := catalog{Version: 1, ServerID: m.ServerID, MachineID: m.MachineID, WorkerDeviceID: m.WorkerDeviceID, Entries: map[domain.ID]entry{}, Generations: map[string]domain.ManagedMCPDefinition{}, Receipts: map[domain.ID]receipt{}, Attempts: map[domain.ID]oauthAttempt{}}
	raw, err := security.ReadPrivate(path, 16<<20)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return empty, err
		}
		c.ReceiptKey = base64.RawURLEncoding.EncodeToString(key)
	} else if err != nil {
		return empty, err
	} else if domain.DecodeWithLimit(raw, &c, 16<<20) != nil || c.Version != 1 || c.ServerID != m.ServerID || c.MachineID != m.MachineID || c.WorkerDeviceID != m.WorkerDeviceID || c.Entries == nil || c.Generations == nil || c.Receipts == nil || c.Attempts == nil {
		return empty, unavailable()
	}
	key, err := base64.RawURLEncoding.DecodeString(c.ReceiptKey)
	if err != nil || len(key) != 32 {
		return empty, unavailable()
	}
	for _, v := range c.Entries {
		if v.Definition.Validate() != nil {
			return empty, unavailable()
		}
	}
	// Safe receipts contain only a keyed input digest and metadata. Neither
	// credential bytes nor authorization codes are written to catalog history.
	digestInput := q
	digestInput.WorkerInstanceID = ""
	encoded, _ := json.Marshal(digestInput)
	mac := hmac.New(sha256.New, key)
	mac.Write(encoded)
	digest := hex.EncodeToString(mac.Sum(nil))
	clear(encoded)
	if q.Action == domain.MCPList {
		for _, v := range c.Entries {
			if !v.Deleted {
				empty.Definitions = append(empty.Definitions, v.Definition)
			}
		}
		slices.SortFunc(empty.Definitions, func(a, b domain.ManagedMCPDefinition) int {
			if a.Name < b.Name {
				return -1
			}
			if a.Name > b.Name {
				return 1
			}
			return 0
		})
		return empty, nil
	}
	if q.Action == domain.MCPOperationRead {
		r, ok := c.Receipts[q.AttemptID]
		if !ok && q.DefinitionID.Validate() == nil {
			empty.Operation = &domain.ManagedMCPOperation{ID: q.AttemptID, DefinitionID: q.DefinitionID, State: domain.MCPOperationRejected}
			return empty, nil
		}
		if !ok || r.ActorID != q.ActorID {
			return empty, domain.Fail(domain.NotFound, "The original MCP operation was not found.", "Inspect the original request on its original Worker.")
		}
		return r.Result, nil
	}
	if prior, ok := c.Receipts[q.RequestID]; ok {
		if prior.ActorID != q.ActorID || prior.Digest != digest {
			return empty, domain.Fail(domain.Conflict, "The MCP request identity was reused.", "Preserve the original request and input.")
		}
		if prior.Result.Operation == nil || prior.Result.Operation.State == domain.MCPOperationStarted || prior.Result.Operation.State == domain.MCPOperationRecovery {
			return prior.Result, unavailable()
		}
		prior.Result.Replayed = true
		return prior.Result, nil
	}
	if len(c.Receipts) >= 4096 || len(c.Entries) >= 256 && q.Action == domain.MCPSave && q.ExpectedRevision == 0 {
		return empty, domain.Fail(domain.ResourceExhausted, "The Worker MCP catalog is full.", "Retain original operations and contact the administrator.")
	}
	save := func() error {
		b, e := json.Marshal(c)
		if e != nil || len(b) > 16<<20 {
			return unavailable()
		}
		return security.WriteAtomicOwned(path, b)
	}
	id := q.DefinitionID
	if q.Definition != nil {
		id = q.Definition.ID
	}
	old, exists := c.Entries[id]
	if q.Action != domain.MCPSave || q.ExpectedRevision > 0 {
		if !exists || old.Deleted {
			return empty, domain.Fail(domain.NotFound, "The MCP definition was not found.", "Read the selected Worker catalog.")
		}
		if old.Definition.Revision != q.ExpectedRevision {
			return empty, domain.Fail(domain.Conflict, "The MCP definition revision changed.", "Read the current definition and preserve your draft.")
		}
	} else if exists {
		return empty, domain.Fail(domain.Conflict, "The MCP definition already exists.", "Use its current revision.")
	}
	if err = validateRequest(q, c, old, exists); err != nil {
		return empty, err
	}
	if q.Action == domain.MCPOAuthBegin {
		if err = m.verifyOAuthMetadata(ctx, old.Definition); err != nil {
			return empty, err
		}
		for _, a := range c.Attempts {
			if a.DefinitionID == id && (a.State == domain.MCPOperationStarted || a.State == domain.MCPOperationRecovery || a.State == domain.MCPOperationAwaiting && m.now().Before(a.ExpiresAt)) {
				return empty, domain.Fail(domain.Conflict, "MCP authentication is already pending.", "Inspect or cancel the original attempt.")
			}
		}
	}
	if q.Action == domain.MCPOAuthComplete {
		a := c.Attempts[q.AttemptID]
		if a.State != domain.MCPOperationAwaiting || !m.now().Before(a.ExpiresAt) {
			return empty, unavailable()
		}
		if err = validateCallback(q.CallbackURL, old.Definition, a); err != nil {
			return empty, err
		}
	}
	if q.Action == domain.MCPOAuthCancel && c.Attempts[q.AttemptID].State != domain.MCPOperationAwaiting {
		return empty, unavailable()
	}
	result := empty
	result.Operation = &domain.ManagedMCPOperation{ID: q.RequestID, DefinitionID: id, State: domain.MCPOperationStarted}
	c.Receipts[q.RequestID] = receipt{ActorID: q.ActorID, Digest: digest, Result: result}
	if err = save(); err != nil {
		return empty, err
	}
	switch q.Action {
	case domain.MCPSave:
		if q.Definition == nil {
			return result, domain.Fail(domain.InvalidArgument, "MCP definition is missing.", "Keep the complete original definition.")
		}
		d := *q.Definition
		// Native adapter support is never accepted from the user or fabricated by
		// catalog configuration. Runtime adapters independently verify support.
		if len(d.SupportedHarnesses) > 0 || d.CredentialID != "" || d.MachineID != m.MachineID || d.WorkerDeviceID != m.WorkerDeviceID || d.Revision != q.ExpectedRevision+1 || d.Validate() != nil {
			return result, domain.Fail(domain.InvalidArgument, "MCP definition is invalid.", "Save only metadata for the selected Worker.")
		}
		if exists && old.Definition.Transport == d.Transport && old.Definition.Endpoint == d.Endpoint && old.Definition.Command == d.Command && old.Definition.Cwd == d.Cwd && slices.Equal(old.Definition.Arguments, d.Arguments) && old.Definition.Authentication == d.Authentication && equalProfile(old.Definition.OAuth, d.OAuth) {
			d.CredentialID = old.Definition.CredentialID
			d.CredentialExpiresAt = old.Definition.CredentialExpiresAt
		}
		c.Entries[id] = entry{Definition: d}
		c.Generations[generationKey(d)] = d
		result.Definitions = []domain.ManagedMCPDefinition{d}
	case domain.MCPDelete:
		if !q.Confirmed || q.Referenced {
			return result, domain.Fail(domain.Conflict, "MCP definition is still referenced or deletion is unconfirmed.", "Unselect it from every Agent Worker, then confirm deletion.")
		}
		old.Deleted = true
		c.Entries[id] = old
		// Historical generations and credentials remain owned until their separate
		// execution cleanup proves they are unused. Deletion never erases history.
	case domain.MCPAuthenticate:
		if old.Definition.Authentication != domain.MCPManualAuthentication || q.Secrets == nil || q.Secrets.Validate(old.Definition.Transport) != nil {
			return result, domain.Fail(domain.InvalidArgument, "MCP manual credentials are invalid.", "Provide explicit credentials for the original definition.")
		}
		b, _ := json.Marshal(q.Secrets)
		defer clear(b)
		if err = m.putSecret(ctx, id, q.RequestID, b); err != nil {
			return result, err
		}
		old.Definition.CredentialID = q.RequestID
		old.Definition.Revision++
		c.Entries[id] = old
		c.Generations[generationKey(old.Definition)] = old.Definition
		result.Definitions = []domain.ManagedMCPDefinition{old.Definition}
	case domain.MCPOAuthBegin, domain.MCPOAuthComplete, domain.MCPOAuthCancel:
		result, err = m.oauth(ctx, &c, q, old.Definition, result, save)
		if err != nil {
			return result, err
		}
	default:
		return result, domain.Fail(domain.InvalidArgument, "Unknown MCP operation.", "Use an explicit supported management action.")
	}
	if result.Operation.State == domain.MCPOperationStarted {
		result.Operation.State = domain.MCPOperationCompleted
	}
	c.Receipts[q.RequestID] = receipt{ActorID: q.ActorID, Digest: digest, Result: result}
	if err = save(); err != nil {
		return result, unavailable()
	}
	if m.Logger != nil {
		m.Logger.InfoContext(ctx, "managed_mcp_operation_finished", "machine_id", m.MachineID, "definition_id", id, "action", q.Action, "state", result.Operation.State)
	}
	return result, nil
}
func generationKey(d domain.ManagedMCPDefinition) string {
	return string(d.ID) + "/" + fmtRevision(d.Revision)
}
func equalProfile(a, b *domain.MCPOAuthProfile) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (m Manager) putSecret(ctx context.Context, id, request domain.ID, b []byte) error {
	if m.Secrets == nil {
		return domain.Fail(domain.Unavailable, "Worker credential storage is unavailable.", "Unlock the selected Worker's native credential store.")
	}
	_, err := m.Secrets.Put(ctx, credentials.Ref{Owner: id, ID: request, Purpose: credentials.ManagedMCP}, b)
	return err
}

func validateRequest(q domain.ManagedMCPRequest, c catalog, old entry, exists bool) error {
	switch q.Action {
	case domain.MCPSave:
		if q.Definition == nil || q.Definition.Validate() != nil || len(q.Definition.SupportedHarnesses) > 0 || q.Definition.CredentialID != "" || q.Definition.Revision != q.ExpectedRevision+1 {
			return domain.Fail(domain.InvalidArgument, "MCP definition is invalid.", "Preserve and correct the draft before saving.")
		}
	case domain.MCPDelete:
		if !q.Confirmed || q.Referenced {
			return domain.Fail(domain.Conflict, "MCP deletion is blocked.", "Unselect the definition and confirm deletion.")
		}
	case domain.MCPAuthenticate:
		if !exists || old.Definition.Authentication != domain.MCPManualAuthentication || q.Secrets == nil || q.Secrets.Validate(old.Definition.Transport) != nil {
			return domain.Fail(domain.InvalidArgument, "MCP credentials are invalid.", "Provide explicit credentials for the original transport.")
		}
	case domain.MCPOAuthBegin:
		if old.Definition.OAuth == nil || old.Definition.Authentication != domain.MCPOAuthAuthentication {
			return domain.Fail(domain.Unsupported, "MCP OAuth is unavailable.", "Configure an explicit public-client OAuth profile.")
		}
	case domain.MCPOAuthComplete, domain.MCPOAuthCancel:
		a, ok := c.Attempts[q.AttemptID]
		if !ok || a.ActorID != q.ActorID || a.DefinitionID != old.Definition.ID || a.Revision != old.Definition.Revision {
			return domain.Fail(domain.PermissionDenied, "MCP OAuth belongs to another scope.", "Use the original request and definition generation.")
		}
		if q.Action == domain.MCPOAuthCancel && !q.Confirmed {
			return domain.Fail(domain.InvalidArgument, "MCP authentication cancellation is unconfirmed.", "Confirm cancellation of the original attempt.")
		}
	default:
		return domain.Fail(domain.InvalidArgument, "Unknown MCP action.", "Use the original supported management operation.")
	}
	return nil
}
