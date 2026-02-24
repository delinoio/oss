package contracts

type Operation string

const (
	OperationUpsertCommitMetrics    Operation = "upsert-commit-metrics"
	OperationListMetricSeries       Operation = "list-metric-series"
	OperationGetPullRequestCompare  Operation = "get-pull-request-comparison"
	OperationPublishPullRequestInfo Operation = "publish-pull-request-report"
)

type OperationResult string

const (
	OperationResultSuccess OperationResult = "success"
	OperationResultFailure OperationResult = "failure"
	OperationResultDenied  OperationResult = "denied"
)
