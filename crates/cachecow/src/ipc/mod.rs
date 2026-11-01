// SPDX-License-Identifier: Apache-2.0
//! Local, current-user-only IPC used by the process control channel and the
//! nested-session protocol: Unix domain sockets in a private `/tmp` directory,
//! or Windows named pipes restricted to the current user's SID.
//!
//! Messages are newline-delimited JSON, like Go's `json.Encoder`/`Decoder`.

use std::time::{Duration, Instant};

use anyhow::Result;
use serde::{Serialize, de::DeserializeOwned};

/// A bound listener and its endpoint string.
pub struct Listener {
    _private: (),
}

/// Closes a [`Listener`] from another thread; a blocked `accept` then fails.
#[derive(Clone)]
pub struct ListenerCloser {
    _private: (),
}

impl ListenerCloser {
    pub fn close(&self) {
        todo!("ipc::ListenerCloser::close")
    }
}

impl Listener {
    /// Binds a new private endpoint. `prefix` names the temporary directory
    /// (`ccow-p-`, `ccow-s-`) or pipe (`ccow-`, `ccow-session-`).
    pub fn bind(prefix: &str) -> Result<(Self, String)> {
        let _ = prefix;
        todo!("ipc::Listener::bind")
    }

    /// Waits for one client. Fails once the listener is closed.
    pub fn accept(&self) -> Result<Connection> {
        todo!("ipc::Listener::accept")
    }

    pub fn closer(&self) -> ListenerCloser {
        todo!("ipc::Listener::closer")
    }

    /// Closes the listener and removes its private endpoint directory.
    pub fn close(&self) {
        todo!("ipc::Listener::close")
    }
}

/// A connected stream with JSON framing.
pub struct Connection {
    _private: (),
}

/// Closes a [`Connection`] from another thread, interrupting blocked I/O.
#[derive(Clone)]
pub struct ConnectionCloser {
    _private: (),
}

impl ConnectionCloser {
    pub fn close(&self) {
        todo!("ipc::ConnectionCloser::close")
    }
}

impl Connection {
    /// Connects to `endpoint`, waiting at most `timeout`.
    pub fn dial(endpoint: &str, timeout: Duration) -> Result<Self> {
        let _ = (endpoint, timeout);
        todo!("ipc::Connection::dial")
    }

    /// Kernel-authenticated peer process ID.
    pub fn peer_pid(&self) -> Result<i64> {
        todo!("ipc::Connection::peer_pid")
    }

    /// Bounds subsequent reads and writes; `None` removes the bound.
    pub fn set_deadline(&self, deadline: Option<Instant>) -> Result<()> {
        let _ = deadline;
        todo!("ipc::Connection::set_deadline")
    }

    pub fn closer(&self) -> ConnectionCloser {
        todo!("ipc::Connection::closer")
    }

    /// Writes one JSON value followed by a newline.
    pub fn send<T: Serialize>(&mut self, value: &T) -> Result<()> {
        let _ = value;
        todo!("ipc::Connection::send")
    }

    /// Reads one newline-delimited JSON value of at most `limit` bytes.
    /// Unknown fields are rejected when `T` denies them.
    pub fn recv<T: DeserializeOwned>(&mut self, limit: usize) -> Result<T> {
        let _ = limit;
        todo!("ipc::Connection::recv")
    }

    /// Blocks until the peer closes or sends any byte (cancellation signal).
    pub fn wait_closed(&mut self) {
        todo!("ipc::Connection::wait_closed")
    }

    /// Sends the three standard stream descriptors (Unix `SCM_RIGHTS`).
    #[cfg(unix)]
    pub fn send_streams(&self, fds: [std::os::fd::BorrowedFd<'_>; 3]) -> Result<()> {
        let _ = fds;
        todo!("ipc::Connection::send_streams")
    }

    /// Receives exactly three descriptors sent by [`Connection::send_streams`].
    #[cfg(unix)]
    pub fn recv_streams(&self) -> Result<[std::os::fd::OwnedFd; 3]> {
        todo!("ipc::Connection::recv_streams")
    }
}
