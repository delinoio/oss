// SPDX-License-Identifier: Apache-2.0
//! Termination signal handling. On Unix this covers SIGINT, SIGTERM and
//! (for the supervisor) SIGHUP; on Windows CTRL_C and CTRL_BREAK events.

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
    _private: (),
}

/// Cancels `handle` on the first observed termination signal.
pub fn notify(set: SignalSet, handle: CancelHandle) -> anyhow::Result<Registration> {
    let _ = (set, handle);
    todo!("signals::notify")
}
