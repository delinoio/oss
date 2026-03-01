package contracts

type DevmonCommand string

const (
	DevmonCommandDaemon   DevmonCommand = "daemon"
	DevmonCommandValidate DevmonCommand = "validate"
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
