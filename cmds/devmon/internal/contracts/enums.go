package contracts

type DevmonCommand string

const (
	DevmonCommandDaemon   DevmonCommand = "daemon"
	DevmonCommandValidate DevmonCommand = "validate"
	DevmonCommandService  DevmonCommand = "service"
	DevmonCommandMenubar  DevmonCommand = "menubar"
)

type DevmonServiceAction string

const (
	DevmonServiceActionInstall   DevmonServiceAction = "install"
	DevmonServiceActionUninstall DevmonServiceAction = "uninstall"
	DevmonServiceActionStart     DevmonServiceAction = "start"
	DevmonServiceActionStop      DevmonServiceAction = "stop"
	DevmonServiceActionStatus    DevmonServiceAction = "status"
)

type DevmonJobType string

const (
	DevmonJobTypeShellCommand DevmonJobType = "shell-command"
)

type DevmonRunOutcome string

const (
	DevmonRunOutcomeSuccess         DevmonRunOutcome = "success"
	DevmonRunOutcomeFailed          DevmonRunOutcome = "failed"
	DevmonRunOutcomeTimeout         DevmonRunOutcome = "timeout"
	DevmonRunOutcomeSkippedOverlap  DevmonRunOutcome = "skipped-overlap"
	DevmonRunOutcomeSkippedCapacity DevmonRunOutcome = "skipped-capacity"
	DevmonRunOutcomeSkippedDisabled DevmonRunOutcome = "skipped-disabled"
)
