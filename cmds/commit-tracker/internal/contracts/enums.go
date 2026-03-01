package contracts

type CommitTrackerOperation string

const (
	CommitTrackerOperationIngest CommitTrackerOperation = "ingest"
	CommitTrackerOperationReport CommitTrackerOperation = "report"
)
