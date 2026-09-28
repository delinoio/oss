package opencode

import "encoding/json"

const (
	checkpointWriteTool      checkpointToolName = "write"
	checkpointEditTool       checkpointToolName = "edit"
	checkpointApplyPatchTool checkpointToolName = "apply_patch"
)

func checkpointFileTool(name checkpointToolName) bool {
	return name == checkpointWriteTool || name == checkpointEditTool || name == checkpointApplyPatchTool
}

// These completed native tools retain their results in message history. Their
// paths, diagnostics and diffs are inert original content: replacement neither
// reapplies a change nor reconciles the current workspace to an old result.
// Keep the full raw metadata in the existing history digest, including opaque
// diagnostic fields and number spelling; this check only excludes auxiliary
// output state that the database-only replacement profile cannot restore.
func checkpointFileMetadata(raw json.RawMessage, name checkpointToolName) bool {
	required := []string{"diagnostics", "truncated"}
	switch name {
	case checkpointWriteTool:
		required = append(required, "filepath", "exists")
	case checkpointEditTool:
		required = append(required, "diff", "filediff")
	case checkpointApplyPatchTool:
		required = append(required, "diff", "files")
	default:
		return false
	}
	fields, err := shape(raw, required, nil)
	var truncated *bool
	var diagnostics map[string]json.RawMessage
	if err != nil || json.Unmarshal(fields["truncated"], &truncated) != nil || truncated == nil || *truncated || json.Unmarshal(fields["diagnostics"], &diagnostics) != nil || diagnostics == nil {
		return false
	}
	for _, issues := range diagnostics {
		var values []json.RawMessage
		if json.Unmarshal(issues, &values) != nil || values == nil {
			return false
		}
	}
	if name == checkpointWriteTool {
		var exists *bool
		return checkpointFileString(fields["filepath"]) && json.Unmarshal(fields["exists"], &exists) == nil && exists != nil
	}
	if !checkpointFileString(fields["diff"]) {
		return false
	}
	if name == checkpointEditTool {
		return checkpointFileDiff(fields["filediff"], false)
	}
	var files []json.RawMessage
	if json.Unmarshal(fields["files"], &files) != nil || len(files) == 0 {
		return false
	}
	for _, file := range files {
		if !checkpointFileDiff(file, true) {
			return false
		}
	}
	return true
}

func checkpointFileDiff(raw json.RawMessage, patch bool) bool {
	required, optional := []string{"file", "patch", "additions", "deletions"}, []string(nil)
	if patch {
		required = []string{"filePath", "relativePath", "type", "patch", "additions", "deletions"}
		optional = []string{"movePath"}
	}
	fields, err := shape(raw, required, optional)
	if err != nil {
		return false
	}
	for key, value := range fields {
		switch key {
		case "additions", "deletions":
			if _, valid := nativeCount(value); !valid {
				return false
			}
		case "type":
			var kind string
			if json.Unmarshal(value, &kind) != nil || kind != "add" && kind != "update" && kind != "delete" && kind != "move" {
				return false
			}
		default:
			if !checkpointFileString(value) {
				return false
			}
		}
	}
	return true
}

func checkpointFileString(raw json.RawMessage) bool {
	var value *string
	return json.Unmarshal(raw, &value) == nil && value != nil
}
