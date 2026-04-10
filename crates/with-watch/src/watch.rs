use std::{
    sync::mpsc::{self, Receiver},
    time::Duration,
};

use notify::{Event, RecommendedWatcher, RecursiveMode, Watcher};

use crate::{
    error::{Result, WithWatchError},
    snapshot::WatchInput,
};

#[derive(Debug, Default, Clone, Copy)]
pub struct CollectedEvents {
    pub event_count: usize,
    pub path_count: usize,
    pub error_count: usize,
}

pub struct WatchLoop {
    _watcher: RecommendedWatcher,
    rx: Receiver<notify::Result<Event>>,
}

impl WatchLoop {
    pub fn new(inputs: &[WatchInput]) -> Result<Self> {
        let (tx, rx) = mpsc::channel();
        let mut watcher = notify::recommended_watcher(move |event| {
            let _ = tx.send(event);
        })
        .map_err(WithWatchError::WatcherCreate)?;

        let mut watched_anchors = Vec::new();
        for input in inputs {
            let anchor = input.watch_anchor().to_path_buf();
            if watched_anchors.contains(&anchor) {
                continue;
            }
            watcher
                .watch(&anchor, RecursiveMode::Recursive)
                .map_err(|source| WithWatchError::WatchPath {
                    path: anchor.clone(),
                    source,
                })?;
            watched_anchors.push(anchor);
        }

        Ok(Self {
            _watcher: watcher,
            rx,
        })
    }

    pub fn collect_events(
        &mut self,
        timeout: Duration,
        debounce_window: Duration,
    ) -> Option<CollectedEvents> {
        let first = self.rx.recv_timeout(timeout).ok()?;
        let mut collected = CollectedEvents::default();
        accumulate_event(first, &mut collected);

        while let Ok(event) = self.rx.recv_timeout(debounce_window) {
            accumulate_event(event, &mut collected);
        }

        Some(collected)
    }
}

fn accumulate_event(event: notify::Result<Event>, collected: &mut CollectedEvents) {
    collected.event_count += 1;
    match event {
        Ok(event) => {
            collected.path_count += event.paths.len();
        }
        Err(_) => {
            collected.error_count += 1;
        }
    }
}
