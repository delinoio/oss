// SPDX-License-Identifier: Apache-2.0
use std::{
    sync::{Arc, Condvar, Mutex, atomic::Ordering},
    thread,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use serde::Serialize;

use crate::{
    Connector, LocalServerState, LocalWorkerAction, LocalWorkerState, LocalWorkerStatus,
    NativeFailure, Result, Supervision,
};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum LocalWorkerManagementState {
    Checking,
    Registering,
    Starting,
    Running,
    Retrying,
    Paused,
    Blocked,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
pub struct LocalWorkerManagement {
    pub state: LocalWorkerManagementState,
    pub attempts: u32,
    pub retry_ms: u64,
    pub failure: Option<NativeFailure>,
    pub owned_by_app: bool,
}
impl Default for LocalWorkerManagement {
    fn default() -> Self {
        Self {
            state: LocalWorkerManagementState::Checking,
            attempts: 0,
            retry_ms: 0,
            failure: None,
            owned_by_app: false,
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum WorkerHostMode {
    Launch,
    Retry,
    Ensure,
}
impl WorkerHostMode {
    fn as_str(self) -> &'static str {
        match self {
            Self::Launch => "launch",
            Self::Retry => "retry",
            Self::Ensure => "ensure",
        }
    }
}

pub struct WorkerSupervision {
    wake: Arc<(Mutex<bool>, Condvar)>,
    task: Mutex<Option<thread::JoinHandle<()>>>,
}
impl WorkerSupervision {
    pub fn new(connector: Arc<Connector>, server: Arc<Supervision>) -> Self {
        connector.worker_auto_enabled.store(true, Ordering::Release);
        let wake = Arc::new((Mutex::new(false), Condvar::new()));
        let signal = Arc::clone(&wake);
        let task = thread::spawn(move || {
            loop {
                if *signal.0.lock().unwrap_or_else(|e| e.into_inner()) {
                    return;
                }
                if server.status().state == LocalServerState::Ready {
                    match server.launch_connection() {
                        Ok(Some(connection)) => {
                            let previous = connector
                                .worker_client_id
                                .lock()
                                .unwrap_or_else(|e| e.into_inner())
                                .replace(connection.device_id.clone());
                            if previous.is_some_and(|id| id != connection.device_id) {
                                connector
                                    .publish_worker_management(LocalWorkerManagement::default());
                            }
                            connector.manage_worker();
                        }
                        Ok(None) => {}
                        Err(error) => connector.worker_management_failure(error),
                    }
                }
                let state = *connector
                    .worker_management
                    .lock()
                    .unwrap_or_else(|e| e.into_inner());
                let delay = match state.state {
                    LocalWorkerManagementState::Retrying => state.retry_ms,
                    LocalWorkerManagementState::Blocked => 60000,
                    _ => 5000,
                };
                let stop = signal.0.lock().unwrap_or_else(|e| e.into_inner());
                let (stop, _) = signal
                    .1
                    .wait_timeout_while(stop, Duration::from_millis(delay), |stop| !*stop)
                    .unwrap_or_else(|e| e.into_inner());
                if *stop {
                    return;
                }
            }
        });
        Self {
            wake,
            task: Mutex::new(Some(task)),
        }
    }

    pub fn request_stop(&self) {
        *self.wake.0.lock().unwrap_or_else(|e| e.into_inner()) = true;
        self.wake.1.notify_all();
    }

    pub fn stop(&self) {
        self.request_stop();
        if let Some(task) = self.task.lock().unwrap_or_else(|e| e.into_inner()).take() {
            if task.join().is_err() {
                tracing::error!(
                    operation = "local_worker_supervision",
                    phase = "join-failed"
                );
            }
        }
    }
}
impl Drop for WorkerSupervision {
    fn drop(&mut self) {
        self.stop();
    }
}

impl Connector {
    fn publish_worker_management(&self, next: LocalWorkerManagement) {
        let mut value = self
            .worker_management
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        if value.state != next.state || value.failure != next.failure {
            tracing::info!(operation = "local_worker_supervision", state = ?next.state, failure = ?next.failure, attempts = next.attempts, retry_ms = next.retry_ms, owned_by_app = next.owned_by_app);
        }
        *value = next;
    }

    pub(crate) fn managed_worker_status(&self, mut status: LocalWorkerStatus) -> LocalWorkerStatus {
        let owned = status
            .generation
            .as_deref()
            .is_some_and(|generation| self.owns_worker_generation(generation));
        let mut next = *self
            .worker_management
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        next.owned_by_app = owned;
        let pending_stop = status.generation.is_some()
            && status.generation
                == *self
                    .worker_pause_generation
                    .lock()
                    .unwrap_or_else(|e| e.into_inner());
        if self.worker_admission_unconfirmed()
            || (status.state == LocalWorkerState::Starting && !status.controller_active)
            || (status.desired_stopped && status.state == LocalWorkerState::Uncertain)
        {
            next.state = LocalWorkerManagementState::Blocked;
            next.failure = Some(NativeFailure::InvalidEvidence);
        } else if pending_stop && !status.desired_stopped {
            next.state = LocalWorkerManagementState::Blocked;
            next.failure = Some(NativeFailure::TimedOut);
        } else if next.state == LocalWorkerManagementState::Blocked
            && matches!(
                next.failure,
                Some(
                    NativeFailure::CredentialUnavailable
                        | NativeFailure::PermissionDenied
                        | NativeFailure::Incompatible
                )
            )
        {
            // Process metadata cannot clear a rejected authenticated admission.
            // A new explicit Start or adopted authenticated client rechecks it.
        } else if status.desired_stopped {
            next.state = LocalWorkerManagementState::Paused;
            next.failure = None;
        } else if status.state == LocalWorkerState::Running {
            next = LocalWorkerManagement {
                state: LocalWorkerManagementState::Running,
                owned_by_app: owned,
                ..LocalWorkerManagement::default()
            };
        } else if status.state == LocalWorkerState::Starting {
            next.state = LocalWorkerManagementState::Starting;
        }
        self.publish_worker_management(next);
        status.management = Some(next);
        status
    }

    pub(crate) fn unavailable_worker_status(&self) -> LocalWorkerStatus {
        LocalWorkerStatus {
            state: LocalWorkerState::NotStarted,
            machine_id: String::new(),
            generation: None,
            controller_active: false,
            desired_stopped: false,
            management: Some(
                *self
                    .worker_management
                    .lock()
                    .unwrap_or_else(|e| e.into_inner()),
            ),
        }
    }

    pub(crate) fn start_managed_worker(&self, mode: WorkerHostMode) -> Result<LocalWorkerStatus> {
        let before = if mode == WorkerHostMode::Launch {
            self.local_worker_inner(LocalWorkerAction::Status, None)
                .ok()
                .and_then(|status| status.generation)
        } else {
            None
        };
        let result = self.admit_managed_worker(mode);
        if mode == WorkerHostMode::Launch
            && result.is_err()
            && self
                .worker_pause_generation
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .is_none()
        {
            let after = self
                .local_worker_inner(LocalWorkerAction::Status, None)
                .ok();
            // Retry an intentional launch that failed before opening its Go
            // barrier. A stopped newly published generation consumes that
            // intent and cannot be reopened by this retry.
            let pending =
                after.is_none_or(|status| !status.desired_stopped || status.generation == before);
            self.worker_launch_pending.store(pending, Ordering::Release);
        }
        result
    }

    fn admit_managed_worker(&self, mode: WorkerHostMode) -> Result<LocalWorkerStatus> {
        if self.exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let client_id = self
            .worker_client_id
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
            .ok_or(NativeFailure::CredentialUnavailable)?;
        let attempts = self
            .worker_management
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .attempts;
        self.publish_worker_management(LocalWorkerManagement {
            state: LocalWorkerManagementState::Registering,
            attempts,
            ..LocalWorkerManagement::default()
        });
        let prepared = self.run(&[
            "worker".into(),
            "desktop-prepare".into(),
            "--mode".into(),
            mode.as_str().into(),
            "--client-id".into(),
            client_id.clone().into(),
        ])?;
        let executable = prepared
            .get("executable")
            .and_then(|v| v.as_str())
            .map(std::path::PathBuf::from)
            .filter(|path| path.is_absolute())
            .ok_or(NativeFailure::InvalidEvidence)?;
        let mut args = vec![
            "worker".into(),
            "desktop-host".into(),
            "--mode".into(),
            mode.as_str().into(),
            "--client-id".into(),
            client_id.into(),
        ];
        if mode == WorkerHostMode::Ensure {
            if let Some(generation) = self
                .worker_exited
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .clone()
            {
                args.extend(["--exited-generation".into(), generation.into()]);
            }
        }
        let value = self.run_host_child(&executable, &args, true)?;
        if value.get("state").and_then(|v| v.as_str()) == Some("service-managed") {
            return Err(NativeFailure::ServiceManaged);
        }
        let proof = self.local_worker_proof_inner()?;
        let status = crate::local_worker::worker_status(
            value
                .get("worker")
                .cloned()
                .ok_or(NativeFailure::InvalidEvidence)?,
            &proof.server_id,
            &proof.endpoint,
            &proof.machine_id,
        )?;
        Ok(self.managed_worker_status(status))
    }

    pub(crate) fn manage_worker(&self) {
        let Ok(_gate) = self.gate.try_lock() else {
            return;
        };
        if self.exiting.load(Ordering::Acquire) {
            return;
        }
        self.reap_worker_children();
        if self.worker_admission_unconfirmed() {
            match self.local_worker_inner(LocalWorkerAction::Status, None) {
                Ok(status) => {
                    self.managed_worker_status(status);
                }
                Err(_) => self.worker_management_failure(NativeFailure::InvalidEvidence),
            }
            return;
        }
        let launch = self.worker_launch_pending.swap(false, Ordering::AcqRel);
        let result = if launch {
            self.start_managed_worker(WorkerHostMode::Launch)
        } else {
            match self.local_worker_inner(LocalWorkerAction::Status, None) {
                Ok(status)
                    if (status.generation.is_some()
                        && status.generation
                            == *self
                                .worker_pause_generation
                                .lock()
                                .unwrap_or_else(|e| e.into_inner()))
                        || status.desired_stopped
                        || status.controller_active
                        || (status.state == LocalWorkerState::Starting
                            && status.generation
                                != *self
                                    .worker_exited
                                    .lock()
                                    .unwrap_or_else(|e| e.into_inner())) =>
                {
                    Ok(self.managed_worker_status(status))
                }
                Ok(_) => self.start_managed_worker(WorkerHostMode::Ensure),
                // Retry the original pairing journal after an interrupted first
                // admission. This cannot reopen a retained stopped generation.
                Err(NativeFailure::CredentialUnavailable | NativeFailure::SidecarFailed) => {
                    self.start_managed_worker(WorkerHostMode::Retry)
                }
                Err(error) => Err(error),
            }
        };
        if let Err(error) = result {
            self.worker_management_failure(error);
        }
    }

    pub(crate) fn worker_management_failure(&self, error: NativeFailure) {
        let attempts = self
            .worker_management
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .attempts
            .saturating_add(1);
        let jitter = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .subsec_nanos() as u64;
        self.publish_worker_management(failure(error, attempts, jitter));
    }
}

fn failure(error: NativeFailure, attempts: u32, jitter: u64) -> LocalWorkerManagement {
    let retry = matches!(
        error,
        NativeFailure::Busy
            | NativeFailure::TimedOut
            | NativeFailure::SidecarFailed
            | NativeFailure::StorageUnavailable
    );
    let ceiling = (1000_u64 << attempts.saturating_sub(1).min(5)).min(30000);
    LocalWorkerManagement {
        state: if retry {
            LocalWorkerManagementState::Retrying
        } else {
            LocalWorkerManagementState::Blocked
        },
        attempts,
        retry_ms: if retry {
            ceiling / 2 + jitter % (ceiling / 2 + 1)
        } else {
            0
        },
        failure: Some(error),
        owned_by_app: false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn retries_are_bounded_and_authority_failures_never_repair() {
        for n in 1..=100 {
            let value = failure(NativeFailure::TimedOut, n, u64::MAX);
            assert_eq!(value.state, LocalWorkerManagementState::Retrying);
            assert!((500..=30000).contains(&value.retry_ms));
        }
        for error in [
            NativeFailure::PermissionDenied,
            NativeFailure::InvalidEvidence,
            NativeFailure::ServiceManaged,
            NativeFailure::Incompatible,
        ] {
            assert_eq!(
                failure(error, 1, 0).state,
                LocalWorkerManagementState::Blocked
            );
        }
    }
}
