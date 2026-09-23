pub mod handler;
mod listener;

use std::{
    io::{self},
    os::{
        fd::{FromRawFd, OwnedFd},
        unix::ffi::OsStrExt,
    },
};

pub use handler::SeccompNotifyHandler;
use listener::NotifyListener;
use passfd::tokio::FdPassingExt;
use seccompiler::{BpfProgram, SeccompAction, SeccompFilter};
use tokio::{
    net::UnixListener,
    task::{JoinHandle, JoinSet},
};
use tokio_util::sync::CancellationToken;
use tracing::{Level, span};

use crate::{
    bindings::alloc::alloc_seccomp_notif_resp,
    payload::{Filter, SeccompPayload},
};

pub struct Supervisor<H> {
    payload: SeccompPayload,
    cancellation: CancellationToken,
    handling_loop_task: JoinHandle<io::Result<Vec<H>>>,
}

impl<H> Supervisor<H> {
    #[must_use]
    pub const fn payload(&self) -> &SeccompPayload {
        &self.payload
    }

    /// Stops accepting notifications and returns the recorded handler state
    /// without waiting for inherited listeners in surviving descendants.
    ///
    /// # Panics
    /// Panics if the handling loop task has panicked.
    ///
    /// # Errors
    /// Returns an error if any of the spawned handler tasks failed with an I/O
    /// error.
    pub async fn stop(self) -> io::Result<Vec<H>> {
        self.cancellation.cancel();
        self.handling_loop_task
            .await
            .expect("handling loop task panicked")
    }
}

/// Creates a new supervisor that listens for seccomp user notifications.
///
/// # Panics
/// Panics if the seccomp filter cannot be compiled or the target architecture
/// is unsupported.
///
/// # Errors
/// Returns an error if the temporary IPC socket cannot be created.
pub fn supervise<H: SeccompNotifyHandler + Default + Send + 'static>() -> io::Result<Supervisor<H>>
{
    let notify_listener = tempfile::Builder::new()
        .prefix("fspy_seccomp_notify")
        .make(|path| UnixListener::bind(path))?;

    let seccomp_filter = SeccompFilter::new(
        H::syscalls()
            .iter()
            .map(|sysno| (sysno.id().into(), vec![]))
            .collect(),
        SeccompAction::Allow,
        SeccompAction::UserNotif,
        std::env::consts::ARCH.try_into().unwrap(),
    )
    .unwrap();

    let bpf_filter = Filter(
        BpfProgram::try_from(seccomp_filter)
            .unwrap()
            .into_iter()
            .map(Into::into)
            .collect(),
    );

    let payload = SeccompPayload {
        ipc_path: notify_listener.path().as_os_str().as_bytes().to_vec(),
        filter: bpf_filter,
    };

    let cancellation = CancellationToken::new();
    let loop_cancellation = cancellation.clone();

    let handling_loop = async move {
        let mut join_set: JoinSet<io::Result<H>> = JoinSet::new();

        loop {
            let (incoming_stream, _) = tokio::select! {
                biased;
                () = loop_cancellation.cancelled() => break,
                incoming = notify_listener.as_file().accept() => incoming?,
            };
            let notify_fd = incoming_stream.recv_fd().await?;
            // SAFETY: `recv_fd` returns a valid file descriptor received via
            // Unix domain socket fd passing
            let notify_fd = unsafe { OwnedFd::from_raw_fd(notify_fd) };
            let mut listener = NotifyListener::try_from(notify_fd)?;

            let mut handler = H::default();
            let mut resp_buf = alloc_seccomp_notif_resp();
            let handler_cancellation = loop_cancellation.clone();

            join_set.spawn(async move {
                let mut first_tracking_error = None;
                loop {
                    let notify = tokio::select! {
                        biased;
                        () = handler_cancellation.cancelled() => break,
                        notify = listener.next() => notify?,
                    };
                    let Some(notify) = notify else { break };
                    let _span = span!(Level::TRACE, "notify loop tick");
                    // Errors on the supervisor side could be caused by a target process aborting.
                    // Continue the syscall even when recording fails, but preserve that
                    // failure so the caller cannot use an incomplete trace as complete.
                    let handle_result = handler.handle_notify(notify);
                    let req_id = notify.id;
                    listener.send_continue(req_id, &mut resp_buf)?;
                    if let Err(error) = handle_result {
                        first_tracking_error.get_or_insert(error);
                    }
                }
                first_tracking_error.map_or(Ok(handler), Err)
            });
        }
        let mut handlers = Vec::<H>::new();
        while let Some(handler) = join_set.join_next().await.transpose()? {
            handlers.push(handler?);
        }
        Ok(handlers)
    };
    Ok(Supervisor {
        payload,
        cancellation,
        handling_loop_task: tokio::spawn(handling_loop),
    })
}
