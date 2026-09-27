package grok

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type CreationPhase string
type creationStage string

const (
	ClaimCreation        CreationPhase = "claim-creation"
	BindCreation         CreationPhase = "bind-creation"
	inspectCreationStage creationStage = "inspect"
	claimCreationStage   creationStage = "claim"
	submitCreationStage  creationStage = "submit"
	bindCreationStage    creationStage = "bind"
	setupCreationStage   creationStage = "setup"
)

// CreationClaim is metadata only. The caller must durably synchronize each
// original phase before returning success and reject existing uncertain work.
type CreationClaim struct {
	Phase               CreationPhase `json:"phase"`
	RequestID           domain.ID     `json:"request_id"`
	ProductSessionID    domain.ID     `json:"product_session_id"`
	NativeSessionID     domain.ID     `json:"native_session_id,omitempty"`
	BodyDigest          string        `json:"body_digest"`
	ConfigurationDigest string        `json:"configuration_digest"`
}

func (c CreationClaim) Validate() error {
	if c.RequestID.Validate() != nil || c.ProductSessionID.Validate() != nil || c.RequestID == c.ProductSessionID {
		return apiConfigurationError()
	}
	for _, value := range []string{c.BodyDigest, c.ConfigurationDigest} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value {
			return apiConfigurationError()
		}
	}
	if c.Phase == ClaimCreation && c.NativeSessionID == "" {
		return nil
	}
	if c.Phase == BindCreation && c.NativeSessionID.Validate() == nil {
		return nil
	}
	return apiConfigurationError()
}

type newSessionParams struct {
	Cwd        string     `json:"cwd"`
	MCPServers []struct{} `json:"mcpServers"`
}

type newSessionResult struct {
	SessionID domain.ID `json:"sessionId"`
	Models    struct {
		Current   string          `json:"currentModelId"`
		Available json.RawMessage `json:"availableModels"`
	} `json:"models"`
	Options []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Category string `json:"category"`
		Type     string `json:"type"`
		Current  string `json:"currentValue"`
		Options  []struct {
			Value string `json:"value"`
			Name  string `json:"name"`
		} `json:"options"`
	} `json:"configOptions"`
	Meta struct {
		Cwd      string          `json:"currentWorkingDirectory"`
		Indexed  json.RawMessage `json:"codebaseIndexed"`
		Git      bool            `json:"isGitRepo"`
		GitRoot  json.RawMessage `json:"gitRoot"`
		Warning  bool            `json:"showNonGitWarning"`
		Feedback bool            `json:"feedbackEnabled"`
		Config   struct {
			Options []struct {
				ID       string `json:"id"`
				Category string `json:"category"`
				Label    string `json:"label"`
				Selected bool   `json:"selected"`
			} `json:"options"`
		} `json:"x.ai/sessionConfig"`
		Detail struct {
			ID    domain.ID `json:"sessionId"`
			Kind  string    `json:"kind"`
			Cwd   string    `json:"cwd"`
			Model string    `json:"currentModelId"`
		} `json:"x.ai/sessionDetail"`
		Memory string `json:"x.ai/memoryMode"`
	} `json:"_meta"`
}

func validateNewSession(raw []byte, workspace string, profile apiProfile) (domain.ID, error) {
	var value newSessionResult
	if decode(raw, &value) != nil || value.SessionID.Validate() != nil || value.Models.Current != selectedModel || validateModels(value.Models.Available, profile) != nil || len(value.Options) != 1 {
		return "", incompatible()
	}
	option := value.Options[0]
	if option.ID != "model" || option.Name != "Model" || option.Category != "model" || option.Type != "select" || option.Current != selectedModel || len(option.Options) != 1 || option.Options[0].Value != selectedModel || option.Options[0].Name != selectedModelName {
		return "", incompatible()
	}
	meta := value.Meta
	if meta.Cwd != workspace || !emptyArray(meta.Indexed) || meta.Git || !isNull(meta.GitRoot) || meta.Warning || len(meta.Config.Options) != 1 || meta.Memory != "legacy" || meta.Detail.ID != value.SessionID || meta.Detail.Kind != "build" || meta.Detail.Cwd != workspace || meta.Detail.Model != selectedModel {
		return "", incompatible()
	}
	selected := meta.Config.Options[0]
	if selected.ID != selectedModel || selected.Category != "model" || selected.Label != selectedModelName || !selected.Selected {
		return "", incompatible()
	}
	return value.SessionID, nil
}

func sessionUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original Grok Build session operation requires reconciliation.", "Retain its native runtime and original claim; never repeat uncertain creation or input.")
}

// Create binds exactly one new native session to its original product session.
// The native acknowledgement is recorded before startup observations can fail.
// Any failure after the first claim permanently closes this creation boundary.
func (a *apiConnection) Create(ctx context.Context, request, product domain.ID, record func(context.Context, CreationClaim) error) (session domain.ID, returned error) {
	if request.Validate() != nil || product.Validate() != nil || request == product || record == nil {
		return "", apiConfigurationError()
	}
	stage := inspectCreationStage
	defer func() {
		if logger := a.inspection.Logger; logger != nil {
			if returned != nil {
				logger.WarnContext(ctx, "Grok Build native session creation failed", "owner_id", a.inspection.OwnerID, "request_id", request, "session_id", product, "stage", stage, "code", domain.SafeError(returned).Code)
			} else {
				logger.InfoContext(ctx, "Grok Build native session bound", "owner_id", a.inspection.OwnerID, "request_id", request, "session_id", product)
			}
		}
	}()
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return "", domain.SafeError(ctx.Err())
	}
	if a.creationStarted {
		return "", sessionUncertain()
	}
	ready, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := a.profile.checkInitialized(); err != nil {
		return "", err
	}
	if err := inspectConfiguration(ready, a.inspection, a.profile.path); err != nil {
		return "", err
	}
	if err := a.profile.checkInitialized(); err != nil {
		return "", err
	}
	params := newSessionParams{Cwd: a.workspace, MCPServers: []struct{}{}}
	body, _ := json.Marshal(params)
	digest, configuration := sha256.Sum256(body), sha256.Sum256(a.profile.configuration)
	claim := CreationClaim{Phase: ClaimCreation, RequestID: request, ProductSessionID: product, BodyDigest: hex.EncodeToString(digest[:]), ConfigurationDigest: hex.EncodeToString(configuration[:])}
	a.creationStarted = true
	stage = claimCreationStage
	if err := record(ready, claim); err != nil {
		return "", sessionUncertain()
	}
	stage = submitCreationStage
	response, err := a.wire.Call(ready, request, "session/new", params)
	if err != nil {
		return "", sessionUncertain()
	}
	if response.ErrorCode != nil {
		return "", sessionUncertain()
	}
	native, err := validateNewSession(response.Result, a.workspace, a.profile)
	if err != nil {
		return "", err
	}
	a.session = native
	claim.Phase, claim.NativeSessionID = BindCreation, native
	stage = bindCreationStage
	if err := record(ready, claim); err != nil {
		return "", sessionUncertain()
	}
	stage = setupCreationStage
	if err := a.observeSetup(ready, native); err != nil {
		return "", err
	}
	if err := a.profile.checkInitialized(); err != nil {
		return "", err
	}
	return native, nil
}

func (a *apiConnection) observeSetup(ctx context.Context, session domain.ID) error {
	phases := []string{"auth", "resolve_workspace", "folder_trust", "plugin_registry", "mcp_merge", "persistence_init", "spawn_session_actor", "git_discovery", "finalize_response", "tool_overrides", "response_ready"}
	phase, inventories := 0, 0
	initialized := false
	for phase < len(phases) || !initialized {
		event, err := a.wire.Next(ctx)
		if err != nil {
			return err
		}
		if event.Kind != nativewire.Notification || event.EmittedAtMS != nil {
			return incompatible()
		}
		switch event.Method {
		case "_x.ai/session/setup":
			var progress struct {
				Method  string          `json:"method"`
				Phase   string          `json:"phase"`
				Session json.RawMessage `json:"sessionId"`
			}
			if decode(event.Params, &progress) != nil || progress.Method != "session/new" || phase >= len(phases) || progress.Phase != phases[phase] {
				return incompatible()
			}
			if phase < 5 {
				if !isNull(progress.Session) {
					return incompatible()
				}
			} else {
				var id domain.ID
				if decode(progress.Session, &id) != nil || id != session {
					return incompatible()
				}
			}
			phase++
		case "_x.ai/mcp/servers_updated":
			var inventory struct {
				Servers json.RawMessage `json:"mcpServers"`
			}
			if decode(event.Params, &inventory) != nil || !emptyArray(inventory.Servers) || inventories >= 2 {
				return incompatible()
			}
			inventories++
		case "_x.ai/mcp_initialized":
			var inventory struct {
				Session domain.ID `json:"sessionId"`
				Count   uint32    `json:"mcpToolCount"`
				Elapsed uint64    `json:"elapsedMs"`
			}
			if decode(event.Params, &inventory) != nil || inventory.Session != session || inventory.Count != 0 || initialized || inventories == 0 || phase != len(phases) {
				return incompatible()
			}
			initialized = true
		default:
			return incompatible()
		}
	}
	return nil
}
