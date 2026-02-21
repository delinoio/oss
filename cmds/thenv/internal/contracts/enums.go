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
