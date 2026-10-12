// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type mcpSecretVault interface {
	Put(context.Context, credentials.Ref, []byte) (string, error)
	Get(context.Context, credentials.Ref) ([]byte, error)
	Close() error
}

func (m *Manager) openMCPVault(root string, scope domain.ID) (mcpSecretVault, error) {
	if m.mcpOpenVault != nil {
		return m.mcpOpenVault(root, scope)
	}
	return credentials.Open(root, scope, m.Logger)
}

type mcpReceipt struct {
	Digest             string           `json:"digest"`
	ActorID            domain.ID        `json:"actor_id"`
	DefinitionID       domain.ID        `json:"definition_id"`
	DefinitionRevision uint64           `json:"definition_revision,omitempty"`
	Result             domain.MCPResult `json:"result"`
	OAuthState         string           `json:"oauth_state,omitempty"`
	RedirectURI        string           `json:"redirect_uri,omitempty"`
	Consumed           bool             `json:"consumed,omitempty"`
}
type mcpCatalog struct {
	Version        uint32                             `json:"version"`
	ServerID       domain.ID                          `json:"server_id"`
	WorkerDeviceID domain.ID                          `json:"worker_device_id"`
	Definitions    map[domain.ID]domain.MCPDefinition `json:"definitions"`
	// Immutable safe definition/credential references remain after catalog deletion.
	// A runtime adapter may retire them only with independent original cleanup proof.
	Generations map[string]domain.MCPDefinition `json:"generations"`
	Receipts    map[domain.ID]mcpReceipt        `json:"receipts"`
}

func (c mcpCatalog) valid(server, worker domain.ID) bool {
	if c.Version != 1 || c.ServerID != server || c.WorkerDeviceID != worker || c.Definitions == nil || c.Generations == nil || c.Receipts == nil || len(c.Definitions) > domain.MaxMCPDefinitions || len(c.Generations) > 8192 || len(c.Receipts) > 4096 {
		return false
	}
	for id, d := range c.Definitions {
		if id != d.ID || d.WorkerDeviceID != worker || d.Validate() != nil {
			return false
		}
	}
	for key, d := range c.Generations {
		if key != string(d.ID)+":"+stringRevision(d.Revision) || d.WorkerDeviceID != worker || d.Validate() != nil {
			return false
		}
	}
	for id, r := range c.Receipts {
		if id != r.Result.RequestID || r.ActorID.Validate() != nil || r.Result.WorkerDeviceID != worker || r.Result.Validate() != nil {
			return false
		}
	}
	return true
}

func (m *Manager) ReadMCP(ctx context.Context, request ReadRequest) (domain.MCPResult, error) {
	var result domain.MCPResult
	if request.MCP == nil || request.Skills != nil || request.PRCandidate != nil || time.Now().After(request.Deadline) {
		return result, domain.MCPUnavailable()
	}
	q := request.MCP
	for _, id := range []domain.ID{q.ServerID, q.ActorID, q.MachineID, q.WorkerDeviceID, q.WorkerInstanceID, q.RequestID} {
		if id.Validate() != nil {
			return result, domain.MCPUnavailable()
		}
	}
	if q.MachineID != request.Preparation.MachineID || q.Mutation.Validate() != nil {
		return result, domain.MCPUnavailable()
	}
	root := filepath.Join(m.Root, "mcp", string(q.ServerID), string(q.WorkerDeviceID))
	if err := security.PrivateDir(root); err != nil {
		return result, domain.SafeError(err)
	}
	lock, err := security.TryLock(filepath.Join(root, "catalog.lock"))
	if err != nil {
		return result, domain.MCPUnavailable()
	}
	defer lock.Close()
	path := filepath.Join(root, "catalog.json")
	catalog := mcpCatalog{Version: 1, ServerID: q.ServerID, WorkerDeviceID: q.WorkerDeviceID, Definitions: map[domain.ID]domain.MCPDefinition{}, Generations: map[string]domain.MCPDefinition{}, Receipts: map[domain.ID]mcpReceipt{}}
	raw, err := security.ReadPrivate(path, 32<<20)
	if err == nil {
		if domain.DecodeBounded(raw, &catalog, 32<<20) != nil || !catalog.valid(q.ServerID, q.WorkerDeviceID) {
			return result, ResultUncertain()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, domain.SafeError(err)
	}
	save := func() error {
		raw, e := json.Marshal(catalog)
		if e != nil || len(raw) > 32<<20 {
			return domain.Fail(domain.ResourceExhausted, "MCP retention is full.", "Preserve original generations and arrange offline maintenance.")
		}
		return security.WriteAtomic(path, raw)
	}
	inventory := func() []domain.MCPDefinition {
		entries := make([]domain.MCPDefinition, 0, len(catalog.Definitions))
		for _, d := range catalog.Definitions {
			entries = append(entries, d)
		}
		slices.SortFunc(entries, func(a, b domain.MCPDefinition) int { return strings.Compare(string(a.ID), string(b.ID)) })
		return entries
	}
	result = domain.MCPResult{WorkerDeviceID: q.WorkerDeviceID, RequestID: q.RequestID, State: domain.MCPSucceeded, DefinitionID: q.Mutation.DefinitionID, Definitions: inventory()}
	if q.Mutation.Action == domain.MCPList {
		return result, nil
	}
	if q.Mutation.Action == domain.MCPInspectMutation {
		receipt, ok := catalog.Receipts[q.RequestID]
		if !ok {
			result.State = domain.MCPFailed
			result.Problem = domain.Fail(domain.NotFound, "The original request was not delivered to this Worker.", "The original identity is now reserved without replaying it.")
			receiptResult := result
			receiptResult.Definitions = []domain.MCPDefinition{}
			catalog.Receipts[q.RequestID] = mcpReceipt{ActorID: q.ActorID, Result: receiptResult, Consumed: true}
			if save() != nil {
				return domain.MCPResult{}, ResultUncertain()
			}
			return result, nil
		}
		if receipt.ActorID != q.ActorID {
			return result, domain.Fail(domain.PermissionDenied, "The MCP request belongs to another client.", "Use its original authorized client.")
		}
		result = receipt.Result
		result.Definitions = inventory()
		if result.State == domain.MCPPending && result.ExpiresAt != nil && time.Now().After(*result.ExpiresAt) {
			result.State = domain.MCPCanceled
		}
		if result.State == domain.MCPPending && result.AuthorizationURL == "" {
			result.State = domain.MCPRecoveryRequired
		}
		return result, nil
	}
	document, _ := json.Marshal(q.Mutation)
	digest := sha256.Sum256(document)
	binding := hex.EncodeToString(digest[:])
	if prior, ok := catalog.Receipts[q.RequestID]; ok {
		if prior.Digest != binding || prior.ActorID != q.ActorID {
			return result, domain.Fail(domain.Conflict, "The MCP request identity has different input.", "Inspect the original accepted request.")
		}
		result = prior.Result
		result.Definitions = inventory()
		if result.State == domain.MCPPending && result.AuthorizationURL == "" {
			result.State = domain.MCPRecoveryRequired
		}
		return result, nil
	}
	if len(catalog.Receipts) >= 4096 || len(catalog.Generations) >= 8192 {
		return result, domain.Fail(domain.ResourceExhausted, "MCP retention is full.", "Preserve original receipts and generations.")
	}
	current, exists := catalog.Definitions[q.Mutation.DefinitionID]
	if q.Mutation.Action != domain.MCPOAuthCancel && (exists && current.Revision != q.Mutation.ExpectedRevision || !exists && q.Mutation.ExpectedRevision != 0) {
		return result, domain.Fail(domain.Conflict, "The MCP definition revision changed.", "Read the current definition before another explicit edit.")
	}
	if q.Mutation.Action != domain.MCPSave && q.Mutation.Action != domain.MCPOAuthCancel && !exists {
		return result, domain.Fail(domain.NotFound, "The MCP definition was removed.", "Select a current Worker-owned definition.")
	}
	if secret := q.Mutation.Secrets; secret != nil {
		transport := current.Transport
		if q.Mutation.Definition != nil {
			transport = q.Mutation.Definition.Transport
		}
		if transport == domain.MCPStdio && (len(secret.Headers) > 0 || secret.AccessToken != "" || secret.RefreshToken != "") || transport == domain.MCPStreamableHTTP && len(secret.Environment) > 0 {
			return result, domain.MCPUnavailable()
		}
	}
	priorDefinitions := maps.Clone(catalog.Definitions)
	priorGenerations := maps.Clone(catalog.Generations)
	pending := mcpReceipt{Digest: binding, ActorID: q.ActorID, DefinitionID: q.Mutation.DefinitionID, DefinitionRevision: current.Revision, Result: domain.MCPResult{WorkerDeviceID: q.WorkerDeviceID, RequestID: q.RequestID, State: domain.MCPPending, DefinitionID: q.Mutation.DefinitionID, Definitions: []domain.MCPDefinition{}}}
	catalog.Receipts[q.RequestID] = pending
	if err := save(); err != nil {
		return result, ResultUncertain()
	}
	// Once intent is synchronized, interruptions retain the original receipt.
	// Retrying never redoes a vault write or OAuth exchange with unknown outcome.
	finishFailure := func(err error, state domain.MCPMutationState) (domain.MCPResult, error) {
		catalog.Definitions = priorDefinitions
		catalog.Generations = priorGenerations
		pending.Result.State = state
		pending.Result.Problem = domain.SafeError(err)
		pending.Result.Definitions = []domain.MCPDefinition{}
		catalog.Receipts[q.RequestID] = pending
		if save() != nil {
			return domain.MCPResult{}, ResultUncertain()
		}
		failed := pending.Result
		failed.Definitions = inventory()
		return failed, nil
	}
	fail := func(err error) (domain.MCPResult, error) { return finishFailure(err, domain.MCPRecoveryRequired) }
	reject := func(err error) (domain.MCPResult, error) { return finishFailure(err, domain.MCPFailed) }
	putSecret := func(secret any, refID domain.ID) (domain.ID, error) {
		raw, e := json.Marshal(secret)
		if e != nil || len(raw) > credentials.MaxSecretBytes {
			return "", domain.MCPUnavailable()
		}
		vault, e := m.openMCPVault(filepath.Join(root, "credentials"), q.WorkerDeviceID)
		if e != nil {
			return "", e
		}
		defer vault.Close()
		_, e = vault.Put(ctx, credentials.Ref{Owner: q.Mutation.DefinitionID, ID: refID, Purpose: credentials.WorkerMCP}, raw)
		return refID, e
	}
	switch q.Mutation.Action {
	case domain.MCPSave:
		next := *q.Mutation.Definition
		next.ID, next.WorkerDeviceID, next.Revision = q.Mutation.DefinitionID, q.WorkerDeviceID, current.Revision+1
		// User configuration cannot manufacture native support or credential ownership.
		next.Support = []domain.MCPHarnessSupport{}
		next.AgentReferences = nil
		next.CredentialID = current.CredentialID
		next.AuthenticationExpiresAt = current.AuthenticationExpiresAt
		oauthBefore, _ := json.Marshal(current.OAuth)
		oauthAfter, _ := json.Marshal(next.OAuth)
		stdioTargetChanged := current.Transport == domain.MCPStdio && (next.Executable != current.Executable || !slices.Equal(next.Arguments, current.Arguments) || next.WorkingDirectory != current.WorkingDirectory)
		if exists && (next.Transport != current.Transport || next.Endpoint != current.Endpoint || string(oauthBefore) != string(oauthAfter) || stdioTargetChanged) {
			next.CredentialID = ""
			next.AuthenticationExpiresAt = nil
			next.Authentication = domain.MCPAuthenticationRequired
		} else {
			next.Authentication = current.Authentication
		}
		if next.Authentication == "" {
			next.Authentication = domain.MCPNoAuthentication
		}
		if next.OAuth != nil && next.CredentialID == "" {
			next.Authentication = domain.MCPAuthenticationRequired
		}
		if next.Validate() != nil || !exists && len(catalog.Definitions) >= domain.MaxMCPDefinitions {
			return reject(domain.MCPUnavailable())
		}
		catalog.Definitions[next.ID] = next
	case domain.MCPDelete:
		delete(catalog.Definitions, current.ID)
	case domain.MCPManualAuthentication:
		if q.Mutation.Secrets == nil {
			return reject(domain.MCPUnavailable())
		}
		current.CredentialID, err = putSecret(q.Mutation.Secrets, q.RequestID)
		if err != nil {
			return fail(err)
		}
		current.Authentication = domain.MCPAuthenticated
		current.AuthenticationExpiresAt = q.Mutation.Secrets.ExpiresAt
		current.Revision++
		current.Support = []domain.MCPHarnessSupport{}
		catalog.Definitions[current.ID] = current
	case domain.MCPOAuthStart:
		if current.OAuth == nil {
			return reject(domain.MCPUnavailable())
		}
		state, e := security.RandomToken()
		if e != nil {
			return fail(e)
		}
		verifier, e := security.RandomToken()
		if e != nil {
			return fail(e)
		}
		if _, e = putSecret(struct {
			Verifier string `json:"verifier"`
		}{verifier}, q.RequestID); e != nil {
			return fail(e)
		}
		u, _ := url.Parse(current.OAuth.AuthorizationURL)
		values := u.Query()
		challenge := sha256.Sum256([]byte(verifier))
		values.Set("response_type", "code")
		values.Set("client_id", current.OAuth.ClientID)
		values.Set("redirect_uri", current.OAuth.RedirectURI)
		values.Set("state", state)
		values.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
		values.Set("code_challenge_method", "S256")
		values.Set("scope", strings.Join(current.OAuth.Scopes, " "))
		u.RawQuery = values.Encode()
		expires := time.Now().UTC().Add(10 * time.Minute)
		result.State, result.AuthorizationURL, result.ExpiresAt = domain.MCPPending, u.String(), &expires
		pending.OAuthState, pending.RedirectURI = state, current.OAuth.RedirectURI
	case domain.MCPOAuthCallback, domain.MCPOAuthCancel:
		original, ok := catalog.Receipts[q.Mutation.OriginalRequestID]
		if !ok || original.ActorID != q.ActorID || original.DefinitionID != q.Mutation.DefinitionID || original.Consumed || original.Result.State != domain.MCPPending || original.Result.ExpiresAt == nil {
			return reject(ResultUncertain())
		}
		original.Consumed = true
		original.Result.State = domain.MCPCanceled
		if q.Mutation.Action == domain.MCPOAuthCallback {
			// The original revision also pins the complete OAuth configuration.
			// A later edit cannot redirect an accepted code to another token endpoint.
			if original.DefinitionRevision == 0 || original.DefinitionRevision != current.Revision || time.Now().After(*original.Result.ExpiresAt) || current.OAuth == nil || current.OAuth.RedirectURI != original.RedirectURI {
				return reject(ResultUncertain())
			}
			callback, e := url.Parse(q.Mutation.CallbackURI)
			expected, e2 := url.Parse(original.RedirectURI)
			if e != nil || e2 != nil || callback.Scheme != expected.Scheme || callback.Host != expected.Host || callback.Path != expected.Path || callback.User != nil || callback.Fragment != "" || callback.Query().Get("state") != original.OAuthState || callback.Query().Get("code") == "" || callback.Query().Get("error") != "" || len(callback.Query()["code"]) != 1 || len(callback.Query()["state"]) != 1 {
				return reject(domain.MCPUnavailable())
			}
			for key, values := range expected.Query() {
				if !slices.Equal(callback.Query()[key], values) {
					return reject(domain.MCPUnavailable())
				}
			}
			original.Result.State = domain.MCPRecoveryRequired
			catalog.Receipts[q.Mutation.OriginalRequestID] = original
			if e := save(); e != nil {
				return fail(ResultUncertain())
			}
			vault, e := m.openMCPVault(filepath.Join(root, "credentials"), q.WorkerDeviceID)
			if e != nil {
				return fail(e)
			}
			secret, e := vault.Get(ctx, credentials.Ref{Owner: current.ID, ID: q.Mutation.OriginalRequestID, Purpose: credentials.WorkerMCP})
			vault.Close()
			if e != nil {
				return fail(e)
			}
			var proof struct {
				Verifier string `json:"verifier"`
			}
			if domain.Decode(secret, &proof) != nil || proof.Verifier == "" {
				return fail(ResultUncertain())
			}
			exchange := exchangeMCPOAuth
			if m.mcpExchange != nil {
				exchange = m.mcpExchange
			}
			tokens, e := exchange(ctx, *current.OAuth, callback.Query().Get("code"), proof.Verifier)
			if e != nil {
				return fail(e)
			}
			current.CredentialID, e = putSecret(tokens, q.RequestID)
			if e != nil {
				return fail(e)
			}
			current.Authentication = domain.MCPAuthenticated
			current.AuthenticationExpiresAt = tokens.ExpiresAt
			current.Revision++
			current.Support = []domain.MCPHarnessSupport{}
			catalog.Definitions[current.ID] = current
			original.Result.State = domain.MCPSucceeded
		} else {
			result.State = domain.MCPCanceled
		}
		catalog.Receipts[q.Mutation.OriginalRequestID] = original
	default:
		return fail(domain.MCPUnavailable())
	}
	inventoryJSON, _ := json.Marshal(inventory())
	if len(inventoryJSON) > 900<<10 {
		return fail(domain.Fail(domain.ResourceExhausted, "The MCP catalog exceeds its projection bound.", "Reduce safe definition metadata without discarding retained generations."))
	}
	for _, d := range catalog.Definitions {
		catalog.Generations[string(d.ID)+":"+stringRevision(d.Revision)] = d
	}
	if len(catalog.Generations) > 8192 {
		return fail(domain.MCPUnavailable())
	}
	result.Definitions = inventory()
	pending.Result = result
	pending.Result.Definitions = []domain.MCPDefinition{}
	catalog.Receipts[q.RequestID] = pending
	if err := save(); err != nil {
		return fail(ResultUncertain())
	}
	if m.Logger != nil {
		m.Logger.InfoContext(ctx, "mcp_management_finished", "request_id", q.RequestID, "worker_device_id", q.WorkerDeviceID, "action", q.Mutation.Action, "state", result.State)
	}
	return result, nil
}
func stringRevision(revision uint64) string { raw, _ := json.Marshal(revision); return string(raw) }
func exchangeMCPOAuth(ctx context.Context, config domain.MCPOAuthConfiguration, code, verifier string) (domain.MCPSecrets, error) {
	var secret domain.MCPSecrets
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {config.ClientID}, "code": {code}, "redirect_uri": {config.RedirectURI}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return secret, ResultUncertain()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return secret, ResultUncertain()
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 || response.StatusCode != http.StatusOK {
		return secret, ResultUncertain()
	}
	defer clear(raw)
	secret, ok := decodeMCPOAuthToken(raw)
	if !ok {
		return domain.MCPSecrets{}, ResultUncertain()
	}
	return secret, nil
}

func decodeMCPOAuthToken(raw []byte) (domain.MCPSecrets, bool) {
	var secret domain.MCPSecrets
	fields := map[string]json.RawMessage{}
	if domain.DecodeBounded(raw, &fields, 64<<10) != nil || fields == nil {
		return secret, false
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	for name, target := range map[string]any{
		"access_token":  &token.AccessToken,
		"refresh_token": &token.RefreshToken,
		"token_type":    &token.TokenType,
		"expires_in":    &token.ExpiresIn,
	} {
		value, present := fields[name]
		if present && domain.Decode(value, target) != nil {
			return secret, false
		}
	}
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || token.ExpiresIn < 0 || token.ExpiresIn > 365*24*3600 {
		return secret, false
	}
	secret.AccessToken, secret.RefreshToken = token.AccessToken, token.RefreshToken
	if token.ExpiresIn > 0 {
		expires := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
		secret.ExpiresAt = &expires
	}
	return secret, true
}

// MCPGenerationSecrets is private Worker adapter authority. It resolves only an
// exact retained definition copy; current catalog edits/deletion cannot redirect
// an accepted execution to replacement commands or credentials. Callers still
// own native inventory/configuration and independent process cleanup proof.
func (m *Manager) MCPGenerationSecrets(ctx context.Context, server, worker domain.ID, definition domain.MCPDefinition) (domain.MCPSecrets, error) {
	var secrets domain.MCPSecrets
	if server.Validate() != nil || worker.Validate() != nil || definition.WorkerDeviceID != worker || definition.Validate() != nil {
		return secrets, ResultUncertain()
	}
	root := filepath.Join(m.Root, "mcp", string(server), string(worker))
	lock, err := security.TryLock(filepath.Join(root, "catalog.lock"))
	if err != nil {
		return secrets, ResultUncertain()
	}
	defer lock.Close()
	raw, err := security.ReadPrivate(filepath.Join(root, "catalog.json"), 32<<20)
	if err != nil {
		return secrets, ResultUncertain()
	}
	var catalog mcpCatalog
	if domain.DecodeBounded(raw, &catalog, 32<<20) != nil || !catalog.valid(server, worker) {
		return secrets, ResultUncertain()
	}
	retained, ok := catalog.Generations[string(definition.ID)+":"+stringRevision(definition.Revision)]
	if !ok {
		return secrets, ResultUncertain()
	}
	// Support is independently observed adapter evidence, not editable definition
	// bytes. Public Agent references likewise do not belong to a native generation.
	retained.Support, retained.AgentReferences = nil, nil
	definition.Support, definition.AgentReferences = nil, nil
	want, _ := json.Marshal(definition)
	have, _ := json.Marshal(retained)
	if !slices.Equal(want, have) {
		return secrets, ResultUncertain()
	}
	if definition.CredentialID == "" {
		return secrets, nil
	}
	vault, err := m.openMCPVault(filepath.Join(root, "credentials"), worker)
	if err != nil {
		return secrets, err
	}
	defer vault.Close()
	raw, err = vault.Get(ctx, credentials.Ref{Owner: definition.ID, ID: definition.CredentialID, Purpose: credentials.WorkerMCP})
	if err != nil {
		return secrets, err
	}
	if domain.DecodeWithLimit(raw, &secrets, credentials.MaxSecretBytes) != nil {
		return secrets, ResultUncertain()
	}
	return secrets, nil
}
