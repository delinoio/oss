package contracts

// Log event constants for structured logging.
const (
	EventServerStart   = "server_start"
	EventServerStop    = "server_stop"
	EventDBOpen        = "db_open"
	EventDBMigrate     = "db_migrate"
	EventDBClose       = "db_close"
	EventUpsertMetrics = "upsert_metrics"
	EventListSeries    = "list_series"
	EventGetComparison = "get_comparison"
	EventPublishReport = "publish_report"
	EventAuthFailure   = "auth_failure"
	EventHealthCheck   = "health_check"
)
