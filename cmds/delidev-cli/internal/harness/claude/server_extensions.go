package claude

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	CodeExecutionResultBlock   ContentBlockKind = "code_execution_tool_result"
	BashExecutionResultBlock   ContentBlockKind = "bash_code_execution_tool_result"
	EditorExecutionResultBlock ContentBlockKind = "text_editor_code_execution_tool_result"
	AdvisorResultBlock         ContentBlockKind = "advisor_tool_result"
	ToolSearchResultBlock      ContentBlockKind = "tool_search_tool_result"
	ServerCodeExecution        ServerToolName   = "code_execution"
	ServerBashExecution        ServerToolName   = "bash_code_execution"
	ServerEditorExecution      ServerToolName   = "text_editor_code_execution"
	ServerAdvisor              ServerToolName   = "advisor"
	ServerToolSearchRegex      ServerToolName   = "tool_search_tool_regex"
	ServerToolSearchBM25       ServerToolName   = "tool_search_tool_bm25"
)

type ServerResultKind string
type ServerFileKind string

const (
	ServerCodeResult            ServerResultKind = "code_execution_result"
	ServerEncryptedCodeResult   ServerResultKind = "encrypted_code_execution_result"
	ServerBashResult            ServerResultKind = "bash_code_execution_result"
	ServerEditorCreateResult    ServerResultKind = "text_editor_code_execution_create_result"
	ServerEditorReplaceResult   ServerResultKind = "text_editor_code_execution_str_replace_result"
	ServerEditorViewResult      ServerResultKind = "text_editor_code_execution_view_result"
	ServerAdvisorResult         ServerResultKind = "advisor_result"
	ServerAdvisorRedactedResult ServerResultKind = "advisor_redacted_result"
	ServerToolSearchResult      ServerResultKind = "tool_search_tool_search_result"
	ServerTextFile              ServerFileKind   = "text"
	ServerImageFile             ServerFileKind   = "image"
	ServerPDFFile               ServerFileKind   = "pdf"
)

// These are observations of a provider-owned runtime, never local terminals,
// patches, filesystem ownership, downloadable attachments or installed tools.
type NativeServerExecution struct {
	Kind       ServerResultKind
	ReturnCode int64
	Stdout     *string  `json:"-"`
	Stderr     string   `json:"-"`
	Files      []string `json:"-"`
}

type NativeServerEdit struct {
	Kind                                   ServerResultKind
	Updated                                *bool
	FileType                               ServerFileKind
	Content                                *string  `json:"-"`
	Lines                                  []string `json:"-"`
	NewLines, NewStart, OldLines, OldStart *uint64
	LineCount, StartLine, TotalLines       *uint64
}

type NativeServerAdvice struct {
	Kind ServerResultKind
	Text *string `json:"-"`
	Stop *NativeStopReason
}

func serverResultKind(kind ContentBlockKind) bool {
	return slices.Contains([]ContentBlockKind{WebSearchResultBlock, WebFetchResultBlock, CodeExecutionResultBlock, BashExecutionResultBlock, EditorExecutionResultBlock, AdvisorResultBlock, ToolSearchResultBlock}, kind)
}

func knownServerTool(name ServerToolName) bool {
	return slices.Contains([]ServerToolName{ServerWebSearch, ServerWebFetch, ServerCodeExecution, ServerBashExecution, ServerEditorExecution, ServerAdvisor, ServerToolSearchRegex, ServerToolSearchBM25}, name)
}

func decodeServerExtension(raw json.RawMessage, kind ContentBlockKind, result *NativeServerResult) error {
	var fields map[string]json.RawMessage
	var shape ServerResultKind
	if domain.Decode(raw, &fields) != nil || json.Unmarshal(fields["type"], &shape) != nil {
		return lifecycleUncertain()
	}
	errorType := ""
	errors := []ServerToolProblem{"invalid_tool_input", "unavailable", "too_many_requests", "execution_time_exceeded"}
	switch kind {
	case CodeExecutionResultBlock:
		result.Name, errorType = ServerCodeExecution, "code_execution_tool_result_error"
	case BashExecutionResultBlock:
		result.Name, errorType = ServerBashExecution, "bash_code_execution_tool_result_error"
		errors = append(errors, "output_file_too_large")
	case EditorExecutionResultBlock:
		result.Name, errorType = ServerEditorExecution, "text_editor_code_execution_tool_result_error"
		errors = append(errors, "file_not_found")
	case AdvisorResultBlock:
		result.Name, errorType = ServerAdvisor, "advisor_tool_result_error"
		errors = []ServerToolProblem{"max_uses_exceeded", "prompt_too_long", "too_many_requests", "overloaded", "unavailable", "execution_time_exceeded", "model_not_found"}
	case ToolSearchResultBlock:
		errorType = "tool_search_tool_result_error"
	default:
		return lifecycleUncertain()
	}
	if string(shape) == errorType {
		var problem struct {
			Type    string            `json:"type"`
			Code    ServerToolProblem `json:"error_code"`
			Message *string           `json:"error_message"`
		}
		if decodeNativeObject(raw, &problem) != nil || !slices.Contains(errors, problem.Code) || (problem.Message != nil && domain.Text(*problem.Message, "native server tool diagnostic", domain.MaxMessageText, false) != nil) {
			return lifecycleUncertain()
		}
		if kind != EditorExecutionResultBlock && kind != ToolSearchResultBlock && fields["error_message"] != nil {
			return lifecycleUncertain()
		}
		result.Problem, result.Detail = problem.Code, problem.Message
		return nil
	}
	switch kind {
	case CodeExecutionResultBlock, BashExecutionResultBlock:
		return decodeServerExecution(raw, kind, shape, result)
	case EditorExecutionResultBlock:
		return decodeServerEdit(raw, shape, result)
	case AdvisorResultBlock:
		var advice struct {
			Type      ServerResultKind  `json:"type"`
			Text      *string           `json:"text"`
			Encrypted *string           `json:"encrypted_content"`
			Stop      *NativeStopReason `json:"stop_reason"`
		}
		if decodeNativeObject(raw, &advice) != nil {
			return lifecycleUncertain()
		}
		switch shape {
		case ServerAdvisorResult:
			if fields["encrypted_content"] != nil || advice.Text == nil || domain.Text(*advice.Text, "native advisor output", domain.MaxMessageText, false) != nil {
				return lifecycleUncertain()
			}
		case ServerAdvisorRedactedResult:
			if fields["text"] != nil || advice.Encrypted == nil || domain.Text(*advice.Encrypted, "native encrypted advice", domain.MaxMessageText, true) != nil {
				return lifecycleUncertain()
			}
		default:
			return lifecycleUncertain()
		}
		result.Advice = &NativeServerAdvice{Kind: shape, Text: advice.Text, Stop: advice.Stop}
	case ToolSearchResultBlock:
		var found struct {
			Type  ServerResultKind  `json:"type"`
			Tools []json.RawMessage `json:"tool_references"`
		}
		if decodeNativeObject(raw, &found) != nil || shape != ServerToolSearchResult || found.Tools == nil || len(found.Tools) > 1024 {
			return lifecycleUncertain()
		}
		result.ToolReferences = make([]string, 0, len(found.Tools))
		for _, reference := range found.Tools {
			var tool struct {
				Type string `json:"type"`
				Name string `json:"tool_name"`
			}
			if decodeNativeObject(reference, &tool) != nil || tool.Type != "tool_reference" || domain.Text(tool.Name, "native discovered tool reference", 256, true) != nil {
				return lifecycleUncertain()
			}
			result.ToolReferences = append(result.ToolReferences, tool.Name)
		}
	default:
		return lifecycleUncertain()
	}
	return nil
}

func decodeServerExecution(raw json.RawMessage, kind ContentBlockKind, shape ServerResultKind, result *NativeServerResult) error {
	var value struct {
		Type      ServerResultKind  `json:"type"`
		Code      *int64            `json:"return_code"`
		Stdout    *string           `json:"stdout"`
		Stderr    *string           `json:"stderr"`
		Encrypted *string           `json:"encrypted_stdout"`
		Files     []json.RawMessage `json:"content"`
	}
	var fields map[string]json.RawMessage
	if decodeNativeObject(raw, &value) != nil || json.Unmarshal(raw, &fields) != nil || value.Code == nil || value.Stderr == nil || domain.Text(*value.Stderr, "native server stderr", domain.MaxMessageText, false) != nil || value.Files == nil || len(value.Files) > 1024 {
		return lifecycleUncertain()
	}
	outputType := "code_execution_output"
	if kind == BashExecutionResultBlock {
		if shape != ServerBashResult {
			return lifecycleUncertain()
		}
		outputType = "bash_code_execution_output"
	} else if shape != ServerCodeResult && shape != ServerEncryptedCodeResult {
		return lifecycleUncertain()
	}
	if shape == ServerEncryptedCodeResult {
		if fields["stdout"] != nil || value.Encrypted == nil || domain.Text(*value.Encrypted, "native encrypted stdout", domain.MaxMessageText, true) != nil {
			return lifecycleUncertain()
		}
	} else if fields["encrypted_stdout"] != nil || value.Stdout == nil || domain.Text(*value.Stdout, "native server stdout", domain.MaxMessageText, false) != nil {
		return lifecycleUncertain()
	}
	execution := &NativeServerExecution{Kind: shape, ReturnCode: *value.Code, Stdout: value.Stdout, Stderr: *value.Stderr, Files: make([]string, 0, len(value.Files))}
	for _, rawFile := range value.Files {
		var file struct {
			Type string `json:"type"`
			ID   string `json:"file_id"`
		}
		if decodeNativeObject(rawFile, &file) != nil || file.Type != outputType || domain.Text(file.ID, "native server file identity", 1024, true) != nil {
			return lifecycleUncertain()
		}
		execution.Files = append(execution.Files, file.ID)
	}
	result.Execution = execution
	return nil
}

func decodeServerEdit(raw json.RawMessage, shape ServerResultKind, result *NativeServerResult) error {
	edit := &NativeServerEdit{Kind: shape}
	switch shape {
	case ServerEditorCreateResult:
		var value struct {
			Type    ServerResultKind `json:"type"`
			Updated *bool            `json:"is_file_update"`
		}
		if decodeNativeObject(raw, &value) != nil || value.Updated == nil {
			return lifecycleUncertain()
		}
		edit.Updated = value.Updated
	case ServerEditorReplaceResult:
		var value struct {
			Type     ServerResultKind  `json:"type"`
			Lines    []json.RawMessage `json:"lines"`
			NewLines *uint64           `json:"new_lines"`
			NewStart *uint64           `json:"new_start"`
			OldLines *uint64           `json:"old_lines"`
			OldStart *uint64           `json:"old_start"`
		}
		if decodeNativeObject(raw, &value) != nil || len(value.Lines) > 4096 {
			return lifecycleUncertain()
		}
		size := 0
		var lines []string
		if value.Lines != nil {
			lines = make([]string, 0, len(value.Lines))
		}
		for _, rawLine := range value.Lines {
			var line *string
			if json.Unmarshal(rawLine, &line) != nil || line == nil || domain.Text(*line, "native server edit line", domain.MaxMessageText, false) != nil {
				return lifecycleUncertain()
			}
			size += len(*line)
			lines = append(lines, *line)
		}
		if size > domain.MaxMessageText {
			return lifecycleUncertain()
		}
		edit.Lines, edit.NewLines, edit.NewStart, edit.OldLines, edit.OldStart = lines, value.NewLines, value.NewStart, value.OldLines, value.OldStart
	case ServerEditorViewResult:
		var value struct {
			Type     ServerResultKind `json:"type"`
			Content  *string          `json:"content"`
			FileType ServerFileKind   `json:"file_type"`
			Count    *uint64          `json:"num_lines"`
			Start    *uint64          `json:"start_line"`
			Total    *uint64          `json:"total_lines"`
		}
		if decodeNativeObject(raw, &value) != nil || value.Content == nil || domain.Text(*value.Content, "native server file content", domain.MaxMessageText, false) != nil || !slices.Contains([]ServerFileKind{ServerTextFile, ServerImageFile, ServerPDFFile}, value.FileType) {
			return lifecycleUncertain()
		}
		edit.Content, edit.FileType, edit.LineCount, edit.StartLine, edit.TotalLines = value.Content, value.FileType, value.Count, value.Start, value.Total
	default:
		return lifecycleUncertain()
	}
	result.Edit = edit
	return nil
}
