// SPDX-License-Identifier: Apache-2.0
//! Termination signal handling. On Unix this covers SIGINT, SIGTERM and
//! (for the supervisor) SIGHUP; on Windows CTRL_C and CTRL_BREAK events plus
//! console close, logoff and shutdown (Go maps those to SIGTERM).
//!
//! While a registration is alive, observed signals cancel its context instead
//! of terminating the process, like Go's `signal.Notify`.

use crate::context::CancelHandle;

/// Which signal set to observe.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SignalSet {
    /// CLI: interrupt and terminate.
    Cli,
    /// Native supervisor: interrupt, terminate and hangup.
    Supervisor,
}

/// Registration that stops observing when dropped.
pub struct Registration {
    #[cfg(unix)]
    handle: signal_hook::iterator::Handle,
    #[cfg(windows)]
    id: u64,
}

/// Cancels `handle` on the first observed termination signal.
pub fn notify(set: SignalSet, handle: CancelHandle) -> anyhow::Result<Registration> {
    platform::notify(set, handle)
}

#[cfg(unix)]
mod platform {
    use signal_hook::{
        consts::{SIGHUP, SIGINT, SIGTERM},
        iterator::Signals,
    };

    use super::{Registration, SignalSet};
    use crate::context::CancelHandle;

    pub fn notify(set: SignalSet, cancel: CancelHandle) -> anyhow::Result<Registration> {
        let mut signals = match set {
            SignalSet::Cli => Signals::new([SIGINT, SIGTERM])?,
            SignalSet::Supervisor => Signals::new([SIGINT, SIGTERM, SIGHUP])?,
        };
        let handle = signals.handle();
        std::thread::Builder::new()
            .name("ccow-signals".into())
            .spawn(move || {
                for signal in signals.forever() {
                    tracing::debug!(signal, "termination signal received");
                    cancel.cancel();
                }
            })?;
        Ok(Registration { handle })
    }

    impl Drop for Registration {
        fn drop(&mut self) {
            self.handle.close();
        }
    }
}

#[cfg(windows)]
mod platform {
    use std::sync::{Mutex, Once};

    use windows_sys::{
        Win32::System::Console::{
            CTRL_BREAK_EVENT, CTRL_C_EVENT, CTRL_CLOSE_EVENT, CTRL_LOGOFF_EVENT,
            CTRL_SHUTDOWN_EVENT, SetConsoleCtrlHandler,
        },
        core::BOOL,
    };

    use super::{Registration, SignalSet};
    use crate::context::CancelHandle;

    static HANDLERS: Mutex<(u64, Vec<(u64, CancelHandle)>)> = Mutex::new((0, Vec::new()));
    static INSTALL: Once = Once::new();

    unsafe extern "system" fn handler(event: u32) -> BOOL {
        match event {
            CTRL_C_EVENT | CTRL_BREAK_EVENT | CTRL_CLOSE_EVENT | CTRL_LOGOFF_EVENT
            | CTRL_SHUTDOWN_EVENT => {
                let handlers = HANDLERS
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner);
                if handlers.1.is_empty() {
                    return 0;
                }
                for (_, h) in &handlers.1 {
                    h.cancel();
                }
                1
            }
            _ => 0,
        }
    }

    pub fn notify(_set: SignalSet, cancel: CancelHandle) -> anyhow::Result<Registration> {
        let mut result = Ok(());
        INSTALL.call_once(|| {
            // SAFETY: `handler` is a valid console control routine for the
            // lifetime of the process.
            if unsafe { SetConsoleCtrlHandler(Some(handler), 1) } == 0 {
                result = Err(std::io::Error::last_os_error());
            }
        });
        result?;
        let mut handlers = HANDLERS
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        handlers.0 += 1;
        let id = handlers.0;
        handlers.1.push((id, cancel));
        Ok(Registration { id })
    }

    impl Drop for Registration {
        fn drop(&mut self) {
            let mut handlers = HANDLERS
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            handlers.1.retain(|(id, _)| *id != self.id);
        }
    }
}

#[cfg(all(test, unix))]
mod tests {
    use std::time::Duration;

    use super::*;
    use crate::Context;

    #[test]
    fn hangup_cancels_supervisor_context() {
        let (ctx, cancel) = Context::background().with_cancel();
        let registration = notify(SignalSet::Supervisor, cancel).unwrap();
        // SAFETY: raise has no memory-safety preconditions; SIGHUP is handled.
        unsafe { libc::raise(libc::SIGHUP) };
        let (deadline, _keep) = ctx.with_timeout(Duration::from_secs(5));
        assert_eq!(deadline.wait(), crate::context::CancelError::Canceled);
        drop(registration);
    }
}
