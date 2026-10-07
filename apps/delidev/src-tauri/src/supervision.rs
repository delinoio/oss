use std::{
    sync::{Arc, Condvar, Mutex, atomic::Ordering},
    thread,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use serde::Serialize;

use crate::{Connection, Connector, NativeFailure};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum LocalServerState {
    Checking,
    Ready,
    Retrying,
    Stopped,
    Blocked,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
pub struct LocalServerStatus {
    pub state: LocalServerState,
    pub attempts: u32,
    pub retry_ms: u64,
    pub failure: Option<NativeFailure>,
}
impl Default for LocalServerStatus {
    fn default() -> Self {
        Self {
            state: LocalServerState::Checking,
            attempts: 0,
            retry_ms: 0,
            failure: None,
        }
    }
}

struct Shared {
    launch: Option<crate::Result<Connection>>,
    status: LocalServerStatus,
    generation: u64,
    exit: bool,
}
struct State {
    value: Mutex<Shared>,
    wake: Condvar,
}

// One loop per host; Go serializes all cross-process controllers and validates
// durable intent/configuration again at the child server's native start
// barrier.
pub struct Supervision {
    connector: Arc<Connector>,
    shared: Arc<State>,
    task: Mutex<Option<thread::JoinHandle<()>>>,
}
impl Supervision {
    pub fn new(connector: Arc<Connector>) -> Self {
        let shared = Arc::new(State {
            value: Mutex::new(Shared {
                status: LocalServerStatus::default(),
                launch: None,
                generation: 0,
                exit: false,
            }),
            wake: Condvar::new(),
        });
        let worker = Arc::clone(&connector);
        let state = Arc::clone(&shared);
        let task = thread::spawn(move || {
            // The fresh main host owns this attempt. Helpers never enter run(),
            // and saved windows can only observe their own pinned connection.
            let launched = worker.launch();
            {
                let mut value = state.value.lock().unwrap_or_else(|e| e.into_inner());
                value.launch = Some(launched);
                state.wake.notify_all();
            }
            let mut attempts = 0_u32;
            let mut previous = LocalServerStatus::default();
            loop {
                let generation = {
                    let value = state.value.lock().unwrap_or_else(|e| e.into_inner());
                    if value.exit {
                        return;
                    }
                    value.generation
                };
                let result = worker.ensure();
                let jitter = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .unwrap_or_default()
                    .subsec_nanos() as u64;
                let status = observation(result, &mut attempts, jitter);
                let mut value = state.value.lock().unwrap_or_else(|e| e.into_inner());
                if value.exit {
                    return;
                }
                if value.generation != generation {
                    attempts = 0;
                    continue;
                }
                if previous.state != status.state || previous.failure != status.failure {
                    tracing::info!(operation = "local_server_supervision", state = ?status.state, failure = ?status.failure, attempts = status.attempts, retry_ms = status.retry_ms);
                }
                previous = status;
                value.status = status;
                let delay = match status.state {
                    LocalServerState::Retrying => status.retry_ms,
                    LocalServerState::Blocked => 60000,
                    _ => 5000,
                };
                let (value, _) = state
                    .wake
                    .wait_timeout_while(value, Duration::from_millis(delay), |value| {
                        !value.exit && value.generation == generation
                    })
                    .unwrap_or_else(|e| e.into_inner());
                if value.exit {
                    return;
                }
            }
        });
        Self {
            connector,
            shared,
            task: Mutex::new(Some(task)),
        }
    }

    pub fn launch_connection(&self) -> crate::Result<Option<Connection>> {
        let original = {
            let value = self.shared.value.lock().unwrap_or_else(|e| e.into_inner());
            if value.exit {
                return Err(NativeFailure::Stopped);
            }
            match &value.launch {
                None => return Ok(None),
                Some(Err(failure)) => return Err(*failure),
                Some(Ok(connection)) => connection.clone(),
            }
        };
        self.connector.observe_launch(&original).map(Some)
    }

    pub fn retry_launch(&self) -> crate::Result<Connection> {
        {
            let mut value = self.shared.value.lock().unwrap_or_else(|e| e.into_inner());
            if value.exit {
                return Err(NativeFailure::Stopped);
            }
            match &value.launch {
                None => return Err(NativeFailure::Busy),
                Some(Ok(original)) => {
                    let original = original.clone();
                    drop(value);
                    return self.connector.observe_launch(&original);
                }
                Some(Err(NativeFailure::Stopped)) => return Err(NativeFailure::Stopped),
                Some(Err(_)) => value.launch = None,
            }
        }
        let result = self.connector.retry_launch();
        let mut value = self.shared.value.lock().unwrap_or_else(|e| e.into_inner());
        value.launch = Some(result.clone());
        value.generation = value.generation.wrapping_add(1);
        self.shared.wake.notify_all();
        result
    }

    pub fn adopt(&self, connection: &Connection) {
        let mut value = self.shared.value.lock().unwrap_or_else(|e| e.into_inner());
        if value.exit {
            return;
        }
        value.launch = Some(Ok(connection.clone()));
        value.generation = value.generation.wrapping_add(1);
        self.shared.wake.notify_all();
    }

    pub fn request_stop(&self) {
        self.connector.exiting.store(true, Ordering::Release);
        self.connector.session.fence();
        let mut state = self.shared.value.lock().unwrap_or_else(|e| e.into_inner());
        state.exit = true;
        self.shared.wake.notify_all();
    }

    pub fn stop(&self) {
        self.request_stop();
        if let Some(task) = self.task.lock().unwrap_or_else(|e| e.into_inner()).take() {
            let _ = task.join();
        }
    }

    pub fn status(&self) -> LocalServerStatus {
        self.shared
            .value
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .status
    }

    pub fn refresh(&self) {
        let mut state = self.shared.value.lock().unwrap_or_else(|e| e.into_inner());
        state.generation = state.generation.wrapping_add(1);
        self.shared.wake.notify_all();
    }
}
impl Drop for Supervision {
    fn drop(&mut self) {
        self.stop();
    }
}

fn observation(
    result: crate::Result<LocalServerState>,
    attempts: &mut u32,
    jitter: u64,
) -> LocalServerStatus {
    match result {
        Ok(state) => {
            *attempts = 0;
            LocalServerStatus {
                state,
                ..LocalServerStatus::default()
            }
        }
        Err(failure) => {
            *attempts = attempts.saturating_add(1);
            let retry = matches!(
                failure,
                NativeFailure::Busy | NativeFailure::SidecarFailed | NativeFailure::TimedOut
            );
            LocalServerStatus {
                state: if retry {
                    LocalServerState::Retrying
                } else {
                    LocalServerState::Blocked
                },
                attempts: *attempts,
                retry_ms: if retry { backoff(*attempts, jitter) } else { 0 },
                failure: Some(failure),
            }
        }
    }
}
fn backoff(attempts: u32, jitter: u64) -> u64 {
    let ceiling = (1000_u64 << attempts.saturating_sub(1).min(5)).min(30000);
    // Equal jitter stays nonzero and never exceeds the 30-second ceiling.
    ceiling / 2 + jitter % (ceiling / 2 + 1)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn failures_back_off_without_restarting_stopped_or_incompatible_servers() {
        let mut attempts = 0;
        for n in 1..=100 {
            let status = observation(Err(NativeFailure::SidecarFailed), &mut attempts, u64::MAX);
            assert_eq!(status.state, LocalServerState::Retrying);
            assert!((500..=30000).contains(&status.retry_ms));
            assert_eq!(attempts, n);
        }
        let stopped = observation(Ok(LocalServerState::Stopped), &mut attempts, 0);
        assert_eq!(stopped.attempts, 0);
        assert_eq!(stopped.retry_ms, 0);
        let incompatible = observation(Err(NativeFailure::Incompatible), &mut attempts, 0);
        assert_eq!(incompatible.state, LocalServerState::Blocked);
        assert_eq!(incompatible.retry_ms, 0);
        assert_eq!(
            observation(Ok(LocalServerState::Ready), &mut attempts, 0).attempts,
            0
        );
        assert_ne!(backoff(5, 1), backoff(5, 100));
    }
}
