package grok

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/google/uuid"
)

type initializeParams struct {
	ProtocolVersion    uint32     `json:"protocolVersion"`
	ClientCapabilities struct{}   `json:"clientCapabilities"`
	ClientInfo         clientInfo `json:"clientInfo"`
}
type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}
type initializeResult struct {
	ProtocolVersion uint32 `json:"protocolVersion"`
	Capabilities    struct {
		LoadSession bool `json:"loadSession"`
		Prompt      struct {
			Image           bool `json:"image"`
			Audio           bool `json:"audio"`
			EmbeddedContext bool `json:"embeddedContext"`
		} `json:"promptCapabilities"`
		MCP struct {
			HTTP bool `json:"http"`
			SSE  bool `json:"sse"`
		} `json:"mcpCapabilities"`
		Session struct {
			List   struct{} `json:"list"`
			Resume struct{} `json:"resume"`
			Close  struct{} `json:"close"`
		} `json:"sessionCapabilities"`
		Auth struct{} `json:"auth"`
		Meta struct {
			FSNotify bool `json:"x.ai/fs_notify"`
			Hooks    struct {
				BlockingEvents []string `json:"blockingEvents"`
				Decisions      []string `json:"decisions"`
				StopSignals    []string `json:"stopSignals"`
			} `json:"x.ai/hooks"`
			Capabilities struct {
				ToolOverrides struct {
					KeywordSearch  bool `json:"x_keyword_search"`
					SemanticSearch bool `json:"x_semantic_search"`
					UserSearch     bool `json:"x_user_search"`
					ThreadFetch    bool `json:"x_thread_fetch"`
				} `json:"toolOverrides"`
			} `json:"x.ai/capabilities"`
		} `json:"_meta"`
	} `json:"agentCapabilities"`
	AuthMethods []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"authMethods"`
	Meta struct {
		Shell       bool            `json:"grokShell"`
		DefaultAuth json.RawMessage `json:"defaultAuthMethodId"`
		MCPSDK      bool            `json:"x.ai/mcp/sdk"`
		PluginDirs  bool            `json:"x.ai/pluginDirs"`
		Cwd         string          `json:"currentWorkingDirectory"`
		Version     string          `json:"agentVersion"`
		AgentID     string          `json:"agentId"`
		InstanceID  string          `json:"agentInstanceId"`
		Hostname    string          `json:"hostname"`
		ModelState  struct {
			Current string          `json:"currentModelId"`
			Models  json.RawMessage `json:"availableModels"`
		} `json:"modelState"`
		MCPServers json.RawMessage `json:"mcpServers"`
		MCPApps    bool            `json:"mcpApps"`
		Metadata   json.RawMessage `json:"metadata"`
		Commands   []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Input       json.RawMessage `json:"input"`
		} `json:"availableCommands"`
		CancelRewind       bool `json:"cancelRewind"`
		SessionRecap       bool `json:"sessionRecap"`
		FeedbackTraceOffer bool `json:"feedbackTraceOffer"`
		VoiceMode          bool `json:"voiceMode"`
	} `json:"_meta"`
}

func validateInitialize(raw []byte, id domain.ID, cwd string) error {
	var message struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      domain.ID       `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   json.RawMessage `json:"error,omitempty"`
	}
	if decode(raw, &message) != nil || message.JSONRPC != "2.0" || message.ID != id {
		return incompatible()
	}
	if len(message.Error) != 0 {
		var nativeError struct {
			Code    int64           `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data,omitempty"`
		}
		if len(message.Result) != 0 || decode(message.Error, &nativeError) != nil || !text(nativeError.Message, 16<<10) {
			return incompatible()
		}
		// Native diagnostic text/data can contain paths or secrets. Retain only
		// the failed handshake classification, never the provider's explanation.
		return probeUnavailable()
	}
	return validateInitializeResult(message.Result, cwd, nil)
}

func validateInitializeResult(raw []byte, cwd string, profile *apiProfile) error {
	var result initializeResult
	if decode(raw, &result) != nil || result.ProtocolVersion != 1 || result.Meta.Version != SupportedVersion || result.Meta.Cwd != cwd ||
		!isNull(result.Meta.Metadata) || !emptyArray(result.Meta.MCPServers) || result.Meta.MCPApps ||
		!text(result.Meta.ModelState.Current, 256) || !text(result.Meta.Hostname, 1024) ||
		!nativeUUID(result.Meta.AgentID, 5) || !nativeUUID(result.Meta.InstanceID, 4) || result.Meta.FeedbackTraceOffer {
		return incompatible()
	}
	authMethods := []string{"grok.com"}
	if profile == nil {
		if !isNull(result.Meta.DefaultAuth) || !emptyArray(result.Meta.ModelState.Models) {
			return incompatible()
		}
	} else {
		authMethods = []string{"xai.api_key", "grok.com"}
		method := "xai.api_key"
		if profile.authentication == managedAuthentication {
			authMethods = []string{"cached_token", "grok.com"}
			method = "cached_token"
		}
		var auth string
		if decode(result.Meta.DefaultAuth, &auth) != nil || auth != method || result.Meta.ModelState.Current != profile.selector() || validateModels(result.Meta.ModelState.Models, *profile) != nil {
			return incompatible()
		}
	}
	if len(result.AuthMethods) != len(authMethods) {
		return incompatible()
	}
	for i, method := range result.AuthMethods {
		if method.ID != authMethods[i] || !text(method.Name, 256) || !text(method.Description, 4096) {
			return incompatible()
		}
	}
	// These are advertisements only. No advertised tool, hook, session or
	// authentication method grants an executable product capability.
	hooks := result.Capabilities.Meta.Hooks
	if !slices.Equal(hooks.BlockingEvents, []string{"pre_tool_use", "stop", "subagent_stop"}) || !slices.Equal(hooks.Decisions, []string{"deny", "block"}) || !slices.Equal(hooks.StopSignals, []string{"continue", "stopReason", "additionalContext"}) {
		return incompatible()
	}
	commands := []string{"compact", "always-approve", "context", "session-info", "deep-research", "workflow", "goal"}
	if len(result.Meta.Commands) != len(commands) {
		return incompatible()
	}
	seen := map[string]bool{}
	for _, command := range result.Meta.Commands {
		if !slices.Contains(commands, command.Name) || seen[command.Name] || !text(command.Description, 16<<10) {
			return incompatible()
		}
		seen[command.Name] = true
		if !isNull(command.Input) {
			var input struct {
				Hint string `json:"hint"`
			}
			if decode(command.Input, &input) != nil || !text(input.Hint, 4096) {
				return incompatible()
			}
		}
	}
	return nil
}

func nativeUUID(value string, version uuid.Version) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value && id.Version() == version && id.Variant() == uuid.RFC4122
}

// The pinned native process emits its startup MCP inventory after initialize.
// Require this independent empty observation before closing discovery. It is
// not a product event and cannot authorize an MCP connection or tool callback.
func validateMCPInventory(raw []byte) error {
	var message struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Servers json.RawMessage `json:"mcpServers"`
		} `json:"params"`
	}
	if decode(raw, &message) != nil || message.JSONRPC != "2.0" || message.Method != "_x.ai/mcp/servers_updated" || !emptyArray(message.Params.Servers) {
		return incompatible()
	}
	return nil
}
