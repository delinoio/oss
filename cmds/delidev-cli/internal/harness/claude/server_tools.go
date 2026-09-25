package claude

import (
	"bytes"
	"encoding/json"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ServerToolName string
type ServerToolProblem string

const (
	ServerToolUseBlock   ContentBlockKind = "server_tool_use"
	WebSearchResultBlock ContentBlockKind = "web_search_tool_result"
	WebFetchResultBlock  ContentBlockKind = "web_fetch_tool_result"
	ServerWebSearch      ServerToolName   = "web_search"
	ServerWebFetch       ServerToolName   = "web_fetch"
)

type NativeServerTool struct {
	ID     string
	Name   ServerToolName
	Input  json.RawMessage `json:"-"`
	Caller json.RawMessage `json:"-"`
	Cache  json.RawMessage `json:"-"`
	Native json.RawMessage `json:"-"`
}

type NativeWebSearchResult struct {
	URL     string
	Title   string
	PageAge *string
	Native  json.RawMessage `json:"-"`
}

type NativeServerResult struct {
	Kind           ContentBlockKind
	ID             string
	Name           ServerToolName
	Problem        ServerToolProblem
	Search         []NativeWebSearchResult
	URL            *string
	RetrievedAt    *string
	Document       *NativeMediaBlock      `json:"-"`
	Caller         json.RawMessage        `json:"-"`
	Native         json.RawMessage        `json:"-"`
	Detail         *string                `json:"-"`
	Execution      *NativeServerExecution `json:"-"`
	Edit           *NativeServerEdit      `json:"-"`
	Advice         *NativeServerAdvice    `json:"-"`
	ToolReferences []string               `json:"-"`
}

type serverToolState struct {
	name            ServerToolName
	parent, message string
	finished        bool
}

func decodeDirectServerCaller(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var caller struct {
		Type string `json:"type"`
	}
	if decodeNativeObject(raw, &caller) != nil || caller.Type != "direct" {
		return domain.Fail(domain.Unsupported, "The Claude Code server tool caller needs its native ownership adapter.", "Retain the original provider operation without granting local tool authority.")
	}
	return nil
}

func decodeServerBlock(raw []byte, kind ContentBlockKind) (NativeContentBlock, error) {
	block := NativeContentBlock{Kind: kind}
	if kind == ServerToolUseBlock {
		var value struct {
			Type   ContentBlockKind `json:"type"`
			ID     string           `json:"id"`
			Name   ServerToolName   `json:"name"`
			Input  json.RawMessage  `json:"input"`
			Caller json.RawMessage  `json:"caller"`
			Cache  json.RawMessage  `json:"cache_control"`
		}
		var input map[string]json.RawMessage
		if decodeNativeObject(raw, &value) != nil || domain.Text(value.ID, "native server tool identity", 1024, true) != nil || len(value.Input) > domain.MaxMessageText || domain.Decode(value.Input, &input) != nil || input == nil || validateNativeCache(value.Cache) != nil {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		if !knownServerTool(value.Name) {
			return NativeContentBlock{}, domain.Fail(domain.Unsupported, "The Claude Code server tool needs its native result adapter.", "Retain the original provider operation without substituting a local tool.")
		}
		if err := decodeDirectServerCaller(value.Caller); err != nil {
			return NativeContentBlock{}, err
		}
		block.ServerTool = &NativeServerTool{ID: value.ID, Name: value.Name, Input: bytes.Clone(value.Input), Caller: bytes.Clone(value.Caller), Cache: bytes.Clone(value.Cache), Native: bytes.Clone(raw)}
		return block, nil
	}
	var value struct {
		Type    ContentBlockKind `json:"type"`
		ID      string           `json:"tool_use_id"`
		Content json.RawMessage  `json:"content"`
		Caller  json.RawMessage  `json:"caller"`
		Cache   json.RawMessage  `json:"cache_control"`
	}
	if decodeNativeObject(raw, &value) != nil || domain.Text(value.ID, "native server result identity", 1024, true) != nil || validateNativeCache(value.Cache) != nil {
		return NativeContentBlock{}, lifecycleUncertain()
	}
	if err := decodeDirectServerCaller(value.Caller); err != nil {
		return NativeContentBlock{}, err
	}
	result := &NativeServerResult{Kind: kind, ID: value.ID, Caller: bytes.Clone(value.Caller), Native: bytes.Clone(raw)}
	if kind != WebSearchResultBlock && kind != WebFetchResultBlock {
		if len(value.Caller) != 0 {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		if err := decodeServerExtension(value.Content, kind, result); err != nil {
			return NativeContentBlock{}, err
		}
		block.ServerResult = result
		return block, nil
	}
	var problem struct {
		Type string            `json:"type"`
		Code ServerToolProblem `json:"error_code"`
	}
	if decodeNativeObject(value.Content, &problem) == nil {
		allowed := []ServerToolProblem{}
		switch kind {
		case WebSearchResultBlock:
			if problem.Type != "web_search_tool_result_error" {
				return NativeContentBlock{}, lifecycleUncertain()
			}
			result.Name = ServerWebSearch
			allowed = []ServerToolProblem{"invalid_tool_input", "unavailable", "max_uses_exceeded", "too_many_requests", "query_too_long", "request_too_large"}
		case WebFetchResultBlock:
			if problem.Type != "web_fetch_tool_result_error" {
				return NativeContentBlock{}, lifecycleUncertain()
			}
			result.Name = ServerWebFetch
			allowed = []ServerToolProblem{"invalid_tool_input", "url_too_long", "url_not_allowed", "url_not_in_prior_context", "url_not_accessible", "unsupported_content_type", "too_many_requests", "max_uses_exceeded", "unavailable", "content_too_large"}
		default:
			return NativeContentBlock{}, lifecycleUncertain()
		}
		if !slices.Contains(allowed, problem.Code) {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		result.Problem = problem.Code
	} else if kind == WebSearchResultBlock {
		result.Name = ServerWebSearch
		var items []json.RawMessage
		if domain.Decode(value.Content, &items) != nil || items == nil || len(items) > 1024 {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		result.Search = make([]NativeWebSearchResult, 0, len(items))
		for _, rawItem := range items {
			var item struct {
				Type      string  `json:"type"`
				URL       string  `json:"url"`
				Title     *string `json:"title"`
				Age       *string `json:"page_age"`
				Encrypted string  `json:"encrypted_content"`
			}
			if decodeNativeObject(rawItem, &item) != nil || item.Type != "web_search_result" || domain.Text(item.URL, "native search URL", 16<<10, true) != nil || item.Title == nil || domain.Text(*item.Title, "native search title", 16<<10, false) != nil || domain.Text(item.Encrypted, "native encrypted search content", domain.MaxMessageText, true) != nil || (item.Age != nil && domain.Text(*item.Age, "native page age", 1024, false) != nil) {
				return NativeContentBlock{}, lifecycleUncertain()
			}
			result.Search = append(result.Search, NativeWebSearchResult{URL: item.URL, Title: *item.Title, PageAge: item.Age, Native: bytes.Clone(rawItem)})
		}
	} else if kind == WebFetchResultBlock {
		result.Name = ServerWebFetch
		var item struct {
			Type      string          `json:"type"`
			URL       string          `json:"url"`
			Retrieved *string         `json:"retrieved_at"`
			Content   json.RawMessage `json:"content"`
		}
		if decodeNativeObject(value.Content, &item) != nil || item.Type != "web_fetch_result" || domain.Text(item.URL, "native fetched URL", 16<<10, true) != nil {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		if item.Retrieved != nil {
			if _, err := time.Parse(time.RFC3339Nano, *item.Retrieved); err != nil {
				return NativeContentBlock{}, lifecycleUncertain()
			}
		}
		document, err := decodeContentBlock(item.Content)
		if err != nil || document.Kind != DocumentBlock || document.Media == nil {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		result.URL, result.RetrievedAt, result.Document = &item.URL, item.Retrieved, document.Media
	} else {
		return NativeContentBlock{}, lifecycleUncertain()
	}
	block.ServerResult = result
	return block, nil
}

// Validate a full snapshot before publishing any native ownership. Server
// operations share identity/count bounds with local tools, but never enter
// their callback, task, OS process or user tool-result ownership namespace.
func (b *ExecutionBinding) stageServerBlocks(blocks []NativeContentBlock, parent, message string, local map[string]nativeToolState) (map[string]serverToolState, int, error) {
	changes := map[string]serverToolState{}
	delta, newIDs := 0, 0
	for _, block := range blocks {
		if tool := block.ServerTool; tool != nil {
			if b.content.tools[tool.ID].name != "" || local[tool.ID].name != "" || b.content.serverTools[tool.ID].name != "" || changes[tool.ID].name != "" {
				return nil, 0, lifecycleUncertain()
			}
			changes[tool.ID] = serverToolState{name: tool.Name, parent: parent, message: message}
			delta++
			newIDs++
			if b.content.openTools+len(local)+delta > 128 {
				return nil, 0, lifecycleUncertain()
			}
		}
		if result := block.ServerResult; result != nil {
			tool, ok := changes[result.ID]
			if !ok {
				tool, ok = b.content.serverTools[result.ID]
			}
			matches := tool.name == result.Name
			if result.Kind == ToolSearchResultBlock {
				matches = tool.name == ServerToolSearchRegex || tool.name == ServerToolSearchBM25
			}
			if !ok || tool.finished || !matches || tool.parent != parent || tool.message != message || local[result.ID].name != "" {
				return nil, 0, lifecycleUncertain()
			}
			tool.finished = true
			changes[result.ID] = tool
			delta--
		}
	}
	if len(b.content.tools)+len(b.content.serverTools)+len(local)+newIDs > 4096 || b.content.openTools+len(local)+delta < 0 {
		return nil, 0, lifecycleUncertain()
	}
	// The tool-search result family serves both original search algorithms.
	// Resolve its display name only from the fully validated original call.
	for _, block := range blocks {
		if block.ServerResult != nil {
			block.ServerResult.Name = changes[block.ServerResult.ID].name
		}
	}
	return changes, delta, nil
}

func (b *ExecutionBinding) commitServerBlocks(changes map[string]serverToolState, delta int) {
	if len(changes) == 0 {
		return
	}
	if b.content.serverTools == nil {
		b.content.serverTools = map[string]serverToolState{}
	}
	for id, tool := range changes {
		b.content.serverTools[id] = tool
	}
	b.content.openTools += delta
	if b.logger != nil {
		b.logger.Debug("Claude Code server tool ownership observed", "owner_id", b.owner, "observations", len(changes), "open_delta", delta)
	}
}

func (b *ExecutionBinding) hasOpenServerChild(parent string) bool {
	for _, tool := range b.content.serverTools {
		if tool.parent == parent && !tool.finished {
			return true
		}
	}
	return false
}
