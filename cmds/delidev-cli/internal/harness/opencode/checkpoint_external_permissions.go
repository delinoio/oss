package opencode

// The external-directory allowance does not alter any tool's own permission.
// Both independently pinned native Build/Plan policies keep that permission
// separate from read, edit and bash; original rule ordering remains native.
type checkpointAllowancePermission string

const (
	checkpointAllowRead     checkpointAllowancePermission = "read"
	checkpointAllowExternal checkpointAllowancePermission = "external_directory"
)

func (p checkpointAllowancePermission) supports(name checkpointToolName) bool {
	switch p {
	case checkpointAllowRead:
		return name == checkpointReadTool
	case checkpointAllowExternal:
		return checkpointRejectableTool(name)
	default:
		return false
	}
}

func (p checkpointAllowancePermission) stored() checkpointAllowancePermission {
	if p == checkpointAllowRead {
		return ""
	}
	return p
}

func (p checkpointAllowancePermission) validStored(name checkpointToolName) bool {
	return p == "" && name == checkpointReadTool || p == checkpointAllowExternal && p.supports(name)
}

func checkpointHasExternalAllowance(proof *checkpointToolHistory) bool {
	for _, approval := range proof.Always {
		for _, rule := range approval.Rules {
			if rule.Permission == string(checkpointAllowExternal) {
				return true
			}
		}
	}
	for _, policy := range proof.Policy {
		if policy.Permission == checkpointAllowExternal {
			return true
		}
	}
	return false
}
