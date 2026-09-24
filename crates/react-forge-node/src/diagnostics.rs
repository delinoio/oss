//! A bounded operation-local tracing collector. Dependency events and arbitrary
//! fields are never forwarded. No global subscriber or process logger is set.
use std::{
    collections::BTreeMap,
    fmt,
    sync::{Arc, Mutex},
};

use tracing::{
    Event, Subscriber,
    field::{Field, Visit},
};
use tracing_subscriber::{Layer, layer::Context, prelude::*};

#[derive(Clone, Default)]
pub struct Collector(Arc<Mutex<Vec<BTreeMap<String, serde_json::Value>>>>);
struct Fields(BTreeMap<String, serde_json::Value>);
impl Visit for Fields {
    fn record_debug(&mut self, _: &Field, _: &dyn fmt::Debug) {}

    fn record_str(&mut self, field: &Field, value: &str) {
        if matches!(
            field.name(),
            "operation" | "stage" | "format" | "status" | "code"
        ) && value.len() <= 48
            && value.bytes().all(|c| c.is_ascii_lowercase() || c == b'_')
        {
            self.0.insert(field.name().into(), value.into());
        }
    }

    fn record_u64(&mut self, field: &Field, value: u64) {
        if matches!(field.name(), "revision" | "duration_ms") {
            self.0.insert(field.name().into(), value.into());
        }
    }
}
impl<S: Subscriber> Layer<S> for Collector {
    fn on_event(&self, event: &Event<'_>, _: Context<'_, S>) {
        if event.metadata().target() != "react_forge" {
            return;
        }
        let mut fields = Fields(BTreeMap::new());
        event.record(&mut fields);
        if let Ok(mut events) = self.0.lock() {
            if events.len() < 32 {
                events.push(fields.0);
            }
        }
    }
}
impl Collector {
    pub fn scoped<T>(&self, work: impl FnOnce() -> T) -> T {
        tracing::subscriber::with_default(tracing_subscriber::registry().with(self.clone()), work)
    }

    pub fn json(&self) -> String {
        self.0
            .lock()
            .ok()
            .and_then(|events| serde_json::to_string(&*events).ok())
            .unwrap_or_else(|| "[]".into())
    }
}
