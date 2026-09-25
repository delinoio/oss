package domain

// Claude permission modes describe native tool approval behavior. They are not
// Codex filesystem sandbox policies or evidence of OS-enforced isolation.
type ClaudePermissionMode string

const (
	ClaudePermissionDefault     ClaudePermissionMode = "default"
	ClaudePermissionPlan        ClaudePermissionMode = "plan"
	ClaudePermissionAcceptEdits ClaudePermissionMode = "acceptEdits"
	ClaudePermissionDontAsk     ClaudePermissionMode = "dontAsk"
	ClaudePermissionBypass      ClaudePermissionMode = "bypassPermissions"
)

func (p ClaudePermissionMode) Valid() bool {
	switch p {
	case ClaudePermissionDefault, ClaudePermissionPlan, ClaudePermissionAcceptEdits, ClaudePermissionDontAsk, ClaudePermissionBypass:
		return true
	default:
		return false
	}
}

func (o AgentOptions) validateClaudePermission(harness Harness) error {
	if (o.ClaudePermission != "" && (harness != ClaudeCode || !o.ClaudePermission.Valid())) || (harness == ClaudeCode && (o.Permission != PermissionDefault || o.ApprovalPolicy != "")) {
		return Fail(Unsupported, "The permission selection does not belong to this native harness.", "Use Claude tool permission modes separately from Codex sandbox and approval policies; retained selections are not translated.")
	}
	return nil
}

// ClaudePermissionForInput derives the explicit native launch mode from the
// immutable selection and input. Plan uses Claude's native plan behavior, never
// a synthetic read-only sandbox. Leaving the selection absent retains the
// pinned default behavior without adding a field to older stored snapshots.
func (o AgentOptions) ClaudePermissionForInput(mode SessionMode) (ClaudePermissionMode, error) {
	if err := o.validateClaudePermission(ClaudeCode); err != nil {
		return "", err
	}
	if !mode.Valid() {
		return "", Fail(InvalidArgument, "Invalid input mode.", "Select Execute or Plan.")
	}
	if mode == PlanMode {
		return ClaudePermissionPlan, nil
	}
	if o.ClaudePermission == "" {
		return ClaudePermissionDefault, nil
	}
	return o.ClaudePermission, nil
}
