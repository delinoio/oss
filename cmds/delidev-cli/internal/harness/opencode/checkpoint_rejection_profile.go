package opencode

// An omitted stored name preserves version 9's original Read/read meaning.
// New permission names bind only rejection evidence; they grant no allowance.
type checkpointRejectionPermission string

const (
	checkpointRejectRead     checkpointRejectionPermission = "read"
	checkpointRejectExternal checkpointRejectionPermission = "external_directory"
	checkpointRejectShell    checkpointRejectionPermission = "bash"
	checkpointRejectEdit     checkpointRejectionPermission = "edit"
	checkpointRejectGlob     checkpointRejectionPermission = "glob"
	checkpointRejectGrep     checkpointRejectionPermission = "grep"
)

func checkpointRejectableTool(name checkpointToolName) bool {
	switch name {
	case checkpointReadTool, checkpointShellTool, checkpointGlobTool, checkpointGrepTool, checkpointWriteTool, checkpointEditTool, checkpointApplyPatchTool:
		return true
	default:
		return false
	}
}

func (p checkpointRejectionPermission) supports(name checkpointToolName) bool {
	switch p {
	case checkpointRejectRead:
		return name == checkpointReadTool
	case checkpointRejectExternal:
		return checkpointRejectableTool(name)
	case checkpointRejectShell:
		return name == checkpointShellTool
	case checkpointRejectEdit:
		return checkpointFileTool(name)
	case checkpointRejectGlob:
		return name == checkpointGlobTool
	case checkpointRejectGrep:
		return name == checkpointGrepTool
	default:
		return false
	}
}

func (p checkpointRejectionPermission) stored() checkpointRejectionPermission {
	if p == checkpointRejectRead {
		return ""
	}
	return p
}

func (p checkpointRejectionPermission) validStored(name checkpointToolName) bool {
	if p == "" {
		return name == checkpointReadTool
	}
	return p != checkpointRejectRead && p.supports(name)
}

func checkpointHasNamedRejection(proof *checkpointToolHistory) bool {
	for _, rejection := range proof.Rejections {
		if rejection.Permission != "" {
			return true
		}
	}
	for _, rejection := range proof.RejectionPolicy {
		if rejection.Permission != "" {
			return true
		}
	}
	return false
}
