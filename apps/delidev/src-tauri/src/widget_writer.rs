// SPDX-License-Identifier: Apache-2.0
//! Ordered presentation persistence. Storage never owns an admission or UI
//! lock.
use std::{
    sync::{
        Mutex,
        atomic::{AtomicBool, Ordering},
        mpsc::{self, SyncSender, TrySendError},
    },
    thread::{self, JoinHandle},
};

use crate::{NativeFailure, Result, presentation::TraySummary};

// Bound pending projections independently of a stalled filesystem. Admission
// never waits for queue capacity; a rejected refresh retains the old snapshot.
const QUEUE_LIMIT: usize = 64;

#[derive(serde::Serialize)]
#[serde(tag = "action", rename_all = "kebab-case")]
pub enum Publication {
    Publish {
        id: String,
        name: String,
        summary: Box<TraySummary>,
    },
    Remove {
        id: String,
    },
    Stop,
}

type Recheck = Box<dyn FnOnce(&mut Publication) -> bool + Send>;
struct Pending {
    publication: Publication,
    recheck: Recheck,
}

pub struct WidgetWriter {
    stopping: AtomicBool,
    sender: Mutex<Option<SyncSender<Pending>>>,
    task: Mutex<Option<JoinHandle<Result<()>>>>,
}

impl WidgetWriter {
    pub fn new(mut persist: impl FnMut(Publication) -> Result<()> + Send + 'static) -> Self {
        let (sender, receiver) = mpsc::sync_channel::<Pending>(QUEUE_LIMIT);
        let task = thread::spawn(move || {
            for mut pending in receiver {
                // The host rechecks the captured scope, revision, original
                // window and oldest-ready owner here, releasing all locks
                // before entering the synchronous storage adapter.
                if (pending.recheck)(&mut pending.publication)
                    && let Err(code) = persist(pending.publication)
                {
                    tracing::warn!(operation = "widget_snapshot", phase = "write-failed", ?code);
                }
            }
            // Disconnect is the ordered terminal marker. Only this worker can
            // publish it, after every previously admitted write has completed.
            let result = persist(Publication::Stop);
            match &result {
                Ok(()) => tracing::info!(
                    operation = "widget_snapshot",
                    phase = "final-write-completed"
                ),
                Err(code) => {
                    tracing::warn!(operation = "widget_snapshot", phase = "stale-failed", ?code)
                }
            }
            result
        });
        Self {
            stopping: AtomicBool::new(false),
            sender: Mutex::new(Some(sender)),
            task: Mutex::new(Some(task)),
        }
    }

    pub fn submit(
        &self,
        publication: Publication,
        recheck: impl FnOnce(&mut Publication) -> bool + Send + 'static,
    ) -> Result<()> {
        // Stop belongs to the joined worker, never to an ordinary caller.
        if matches!(publication, Publication::Stop) {
            return Err(NativeFailure::InvalidInput);
        }
        let sender = self.sender.try_lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        sender
            .as_ref()
            .ok_or(NativeFailure::Stopped)?
            .try_send(Pending {
                publication,
                recheck: Box::new(recheck),
            })
            .map_err(|error| match error {
                TrySendError::Full(_) => NativeFailure::Busy,
                TrySendError::Disconnected(_) => NativeFailure::StorageUnavailable,
            })
    }

    // Safe on the native UI loop: no persistence, queue wait or task join.
    pub fn request_stop(&self) {
        self.stopping.store(true, Ordering::Release);
    }

    // Call only off the native UI loop. Serialize concurrent joins so none can
    // report completion while the original worker still owns a pending write.
    pub fn stop(&self) -> Result<()> {
        self.request_stop();
        let mut task = self.task.lock().map_err(|_| NativeFailure::Busy)?;
        self.sender.lock().map_err(|_| NativeFailure::Busy)?.take();
        if let Some(task) = task.take() {
            task.join().map_err(|_| NativeFailure::StorageUnavailable)?
        } else {
            Ok(())
        }
    }
}

impl Drop for WidgetWriter {
    fn drop(&mut self) {
        // Normal setup/return cleanup also retains and joins this worker.
        // Process crashes/forced termination still rely on snapshot expiry.
        let _ = self.stop();
    }
}

#[cfg(test)]
mod tests {
    use std::{sync::Arc, time::Duration};

    use super::*;

    const WAIT: Duration = Duration::from_secs(5);

    fn removal(id: &str) -> Publication {
        Publication::Remove { id: id.into() }
    }

    fn snapshot(id: &str) -> Publication {
        Publication::Publish {
            id: id.into(),
            name: "Fixture".into(),
            summary: Box::new(TraySummary {
                overview: None,
                usage: None,
                accounts: None,
            }),
        }
    }

    #[test]
    fn stalled_write_leaves_admission_and_ui_work_live_and_quit_pending() {
        let (entered, entering) = mpsc::channel();
        let (release, releasing) = mpsc::channel();
        let (written, writes) = mpsc::channel();
        let writer = Arc::new(WidgetWriter::new(move |publication| {
            if matches!(&publication, Publication::Publish { id, .. } if id == "blocked") {
                entered.send(()).unwrap();
                releasing.recv_timeout(WAIT).unwrap();
            }
            written
                .send(serde_json::to_value(publication).unwrap())
                .unwrap();
            Ok(())
        }));
        writer.submit(snapshot("blocked"), |_| true).unwrap();
        entering.recv_timeout(WAIT).unwrap();

        // A UI-like event queue continues to process ordinary callbacks and
        // close admission while the independent persistence worker is blocked.
        let (events, event_queue) = mpsc::channel::<Box<dyn FnOnce() + Send>>();
        let ui = thread::spawn(move || {
            for event in event_queue {
                event();
            }
        });
        let (observed, observations) = mpsc::channel();
        let for_ui = Arc::clone(&writer);
        events
            .send(Box::new(move || {
                for_ui.submit(snapshot("second"), |_| true).unwrap();
                for_ui.request_stop();
                observed.send(()).unwrap();
            }))
            .unwrap();
        observations.recv_timeout(WAIT).unwrap();
        drop(events);
        ui.join().unwrap();
        assert_eq!(
            writer.submit(removal("late"), |_| true),
            Err(NativeFailure::Stopped)
        );

        let (joined, joining) = mpsc::channel();
        let stopping = Arc::clone(&writer);
        let quit = thread::spawn(move || {
            joined.send(stopping.stop()).unwrap();
        });
        assert!(joining.recv_timeout(Duration::from_millis(50)).is_err());
        assert!(writes.try_recv().is_err());
        release.send(()).unwrap();
        assert_eq!(joining.recv_timeout(WAIT).unwrap(), Ok(()));
        quit.join().unwrap();
        assert_eq!(
            writes.recv_timeout(WAIT).unwrap(),
            serde_json::to_value(snapshot("blocked")).unwrap()
        );
        assert_eq!(
            writes.recv_timeout(WAIT).unwrap(),
            serde_json::to_value(snapshot("second")).unwrap()
        );
        assert_eq!(
            writes.recv_timeout(WAIT).unwrap(),
            serde_json::json!({"action":"stop"})
        );
        writer.stop().unwrap();
        assert!(writes.try_recv().is_err());
    }

    #[test]
    fn failed_and_rejected_writes_do_not_prevent_ordered_final_stale_publication() {
        let (written, writes) = mpsc::channel();
        let writer = WidgetWriter::new(move |publication| {
            written
                .send(serde_json::to_value(&publication).unwrap())
                .unwrap();
            if matches!(publication, Publication::Remove { .. }) {
                Err(NativeFailure::StorageUnavailable)
            } else {
                Ok(())
            }
        });
        writer.submit(removal("failed"), |_| true).unwrap();
        writer.submit(removal("superseded"), |_| false).unwrap();
        writer.stop().unwrap();
        assert_eq!(writes.recv_timeout(WAIT).unwrap()["id"], "failed");
        assert_eq!(writes.recv_timeout(WAIT).unwrap()["action"], "stop");
        assert!(writes.try_recv().is_err());
    }

    #[test]
    fn full_queue_rejects_refresh_without_waiting_and_still_admits_terminal_marker() {
        let (entered, entering) = mpsc::channel();
        let (release, releasing) = mpsc::channel();
        let (finished, finishing) = mpsc::channel();
        let writer = WidgetWriter::new(move |publication| {
            if matches!(&publication, Publication::Remove { id } if id == "blocked") {
                entered.send(()).unwrap();
                releasing.recv_timeout(WAIT).unwrap();
            }
            if matches!(publication, Publication::Stop) {
                finished.send(()).unwrap();
            }
            Ok(())
        });
        writer.submit(removal("blocked"), |_| true).unwrap();
        entering.recv_timeout(WAIT).unwrap();
        for _ in 0..QUEUE_LIMIT {
            writer.submit(removal("queued"), |_| true).unwrap();
        }
        assert_eq!(
            writer.submit(removal("overflow"), |_| true),
            Err(NativeFailure::Busy)
        );
        assert_eq!(
            writer.submit(Publication::Stop, |_| true),
            Err(NativeFailure::InvalidInput)
        );
        writer.request_stop();
        release.send(()).unwrap();
        writer.stop().unwrap();
        finishing.recv_timeout(WAIT).unwrap();
    }

    #[test]
    fn final_storage_failure_and_worker_panic_report_uncertainty() {
        let writer = WidgetWriter::new(|_| Err(NativeFailure::StorageUnavailable));
        assert_eq!(writer.stop(), Err(NativeFailure::StorageUnavailable));
        let writer = WidgetWriter::new(|_| panic!("injected writer failure"));
        assert_eq!(writer.stop(), Err(NativeFailure::StorageUnavailable));
    }
}
