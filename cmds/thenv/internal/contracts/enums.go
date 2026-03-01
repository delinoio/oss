package contracts

type ThenvOperation string

const (
	ThenvOperationPush   ThenvOperation = "push"
	ThenvOperationPull   ThenvOperation = "pull"
	ThenvOperationList   ThenvOperation = "list"
	ThenvOperationRotate ThenvOperation = "rotate"
)

type ThenvConflictPolicy string

const (
	ThenvConflictPolicyFailClosed     ThenvConflictPolicy = "fail-closed"
	ThenvConflictPolicyForceOverwrite ThenvConflictPolicy = "force-overwrite"
)

type ThenvAuthIdentitySource string

const (
	ThenvAuthIdentitySourceHeader       ThenvAuthIdentitySource = "header"
	ThenvAuthIdentitySourceHashedLegacy ThenvAuthIdentitySource = "hashed-legacy"
	ThenvAuthIdentitySourceUnspecified  ThenvAuthIdentitySource = "unspecified"
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
