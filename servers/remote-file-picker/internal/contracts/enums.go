package contracts

// LogEvent identifies structured log event types for the remote-file-picker server.
type LogEvent string

const (
	LogEventServerStart       LogEvent = "server.start"
	LogEventServerStop        LogEvent = "server.stop"
	LogEventUploadCreate      LogEvent = "upload.create"
	LogEventUploadConfirm     LogEvent = "upload.confirm"
	LogEventUploadStatusQuery LogEvent = "upload.status_query"
	LogEventAuthFailure       LogEvent = "auth.failure"
)

// OperationResult describes the outcome of a service operation.
type OperationResult string

const (
	OperationResultSuccess OperationResult = "success"
	OperationResultFailure OperationResult = "failure"
	OperationResultDenied  OperationResult = "denied"
)
