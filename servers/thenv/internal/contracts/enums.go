package contracts

type ThenvOperation string

const (
	ThenvOperationPush     ThenvOperation = "push"
	ThenvOperationPull     ThenvOperation = "pull"
	ThenvOperationList     ThenvOperation = "list"
	ThenvOperationRotate   ThenvOperation = "rotate"
	ThenvOperationActivate ThenvOperation = "activate"
	ThenvOperationPolicy   ThenvOperation = "policy-update"
)

type RoleDecision string

const (
	RoleDecisionAllow RoleDecision = "allow"
	RoleDecisionDeny  RoleDecision = "deny"
)

type OperationResult string

const (
	OperationResultSuccess OperationResult = "success"
	OperationResultFailure OperationResult = "failure"
	OperationResultDenied  OperationResult = "denied"
)
