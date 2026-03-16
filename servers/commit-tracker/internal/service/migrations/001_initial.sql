CREATE TABLE IF NOT EXISTS metric_definitions (
  metric_key TEXT PRIMARY KEY,
  display_name TEXT NOT NULL DEFAULT '',
  unit TEXT NOT NULL DEFAULT '',
  value_kind INTEGER NOT NULL DEFAULT 0,
  direction INTEGER NOT NULL DEFAULT 0,
  warning_threshold_percent REAL DEFAULT 0,
  fail_threshold_percent REAL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS commit_measurements (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  provider INTEGER NOT NULL,
  repository TEXT NOT NULL,
  branch TEXT NOT NULL,
  commit_sha TEXT NOT NULL,
  run_id TEXT NOT NULL DEFAULT '',
  environment TEXT NOT NULL DEFAULT '',
  metric_key TEXT NOT NULL REFERENCES metric_definitions(metric_key),
  metric_value REAL NOT NULL,
  measured_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  UNIQUE(provider, repository, commit_sha, run_id, environment, metric_key)
);

CREATE INDEX IF NOT EXISTS idx_commit_measurements_series ON commit_measurements(provider, repository, branch, environment, metric_key, measured_at DESC);
CREATE INDEX IF NOT EXISTS idx_commit_measurements_commit ON commit_measurements(provider, repository, commit_sha, environment, metric_key);
