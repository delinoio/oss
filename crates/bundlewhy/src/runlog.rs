//! Structured JSON-lines run log (`bundlewhy.log`).
//!
//! Records run ID, stage, duration, engine versions and error codes. Callers
//! must never pass source content, environment values, credentials or tokens.

use std::{fs::File, io::Write, path::Path, sync::Mutex};

use serde_json::{Map, Value, json};

pub const LOG_FILE: &str = "bundlewhy.log";

pub struct RunLog {
    run_id: String,
    file: Mutex<Option<File>>,
}

impl RunLog {
    pub fn create(run_dir: &Path, run_id: &str) -> Self {
        let file = File::options()
            .create(true)
            .append(true)
            .open(run_dir.join(LOG_FILE));
        if let Err(error) = &file {
            tracing::warn!(%error, "run log could not be opened");
        }
        Self {
            run_id: run_id.to_owned(),
            file: Mutex::new(file.ok()),
        }
    }

    pub fn event(&self, event: &str, fields: Value) {
        let mut record = Map::new();
        record.insert("ts".to_owned(), json!(chrono::Utc::now().to_rfc3339()));
        record.insert("runId".to_owned(), json!(self.run_id));
        record.insert("event".to_owned(), json!(event));
        if let Value::Object(extra) = fields {
            record.extend(extra);
        }
        let line = Value::Object(record).to_string();
        if let Ok(mut guard) = self.file.lock()
            && let Some(file) = guard.as_mut()
        {
            let _ = writeln!(file, "{line}");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn writes_json_lines_with_run_context() {
        let dir = tempfile::tempdir().unwrap();
        let log = RunLog::create(dir.path(), "run-1");
        log.event("stage", json!({ "stage": "building", "durationMs": 12 }));
        log.event("error", json!({ "code": "BW_BUILD_FAILED" }));
        let text = std::fs::read_to_string(dir.path().join(LOG_FILE)).unwrap();
        let lines: Vec<Value> = text
            .lines()
            .map(|line| serde_json::from_str(line).unwrap())
            .collect();
        assert_eq!(lines.len(), 2);
        assert_eq!(lines[0]["runId"], "run-1");
        assert_eq!(lines[0]["stage"], "building");
        assert_eq!(lines[1]["code"], "BW_BUILD_FAILED");
    }
}
