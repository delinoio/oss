// SPDX-License-Identifier: Apache-2.0
//! Local, current-user-only IPC used by the process control channel and the
//! nested-session protocol: Unix domain sockets in a private `/tmp` directory,
//! or Windows named pipes restricted to the current user's SID.
//!
//! Messages are newline-delimited JSON, like Go's `json.Encoder`/`Decoder`.
//! A [`Connection`] can be shared between threads through
//! [`Connection::try_clone`]; closing any clone (or its [`ConnectionCloser`])
//! interrupts blocked reads and writes on every clone.

use std::{
    io,
    sync::Arc,
    time::{Duration, Instant},
};

use anyhow::{Result, bail};
use serde::{Serialize, de::DeserializeOwned};

#[cfg(unix)]
mod unix;
#[cfg(unix)]
use unix as platform;
#[cfg(windows)]
mod windows;
#[cfg(windows)]
use windows as platform;

/// Default bound for one received JSON frame.
pub const DEFAULT_LIMIT: usize = 1024 * 1024;

/// A bound listener and its endpoint string.
pub struct Listener {
    inner: Arc<platform::Listener>,
}

/// Closes a [`Listener`] from another thread; a blocked `accept` then fails.
#[derive(Clone)]
pub struct ListenerCloser {
    inner: Arc<platform::Listener>,
}

impl ListenerCloser {
    pub fn close(&self) {
        self.inner.close();
    }
}

impl Listener {
    /// Binds a new private endpoint. `prefix` names the temporary directory
    /// (`ccow-p-`, `ccow-s-`) or pipe (`ccow-`, `ccow-session-`).
    pub fn bind(prefix: &str) -> Result<(Self, String)> {
        let (inner, endpoint) = platform::Listener::bind(prefix)?;
        tracing::debug!(endpoint = %endpoint, "ipc listener bound");
        Ok((
            Self {
                inner: Arc::new(inner),
            },
            endpoint,
        ))
    }

    /// Waits for one client. Fails once the listener is closed.
    pub fn accept(&self) -> Result<Connection> {
        let stream = self.inner.accept()?;
        Ok(Connection {
            inner: Arc::new(stream),
            buffer: Vec::new(),
        })
    }

    pub fn closer(&self) -> ListenerCloser {
        ListenerCloser {
            inner: self.inner.clone(),
        }
    }

    /// Closes the listener and removes its private endpoint directory.
    pub fn close(&self) {
        self.inner.close();
    }
}

impl Drop for Listener {
    fn drop(&mut self) {
        self.inner.close();
    }
}

/// A connected stream with JSON framing.
pub struct Connection {
    inner: Arc<platform::Stream>,
    buffer: Vec<u8>,
}

/// Closes a [`Connection`] from another thread, interrupting blocked I/O.
#[derive(Clone)]
pub struct ConnectionCloser {
    inner: Arc<platform::Stream>,
}

impl ConnectionCloser {
    pub fn close(&self) {
        self.inner.close();
    }
}

impl Connection {
    /// Connects to `endpoint`, waiting at most `timeout`.
    pub fn dial(endpoint: &str, timeout: Duration) -> Result<Self> {
        let stream = platform::Stream::dial(endpoint, timeout)?;
        Ok(Self {
            inner: Arc::new(stream),
            buffer: Vec::new(),
        })
    }

    /// Kernel-authenticated peer process ID.
    pub fn peer_pid(&self) -> Result<i64> {
        Ok(self.inner.peer_pid()?)
    }

    /// Bounds subsequent reads and writes; `None` removes the bound.
    pub fn set_deadline(&self, deadline: Option<Instant>) -> Result<()> {
        Ok(self.inner.set_deadline(deadline)?)
    }

    pub fn closer(&self) -> ConnectionCloser {
        ConnectionCloser {
            inner: self.inner.clone(),
        }
    }

    /// Closes the connection; the peer observes end of stream.
    pub fn close(&self) {
        self.inner.close();
    }

    /// Shares the underlying stream. Buffered unread data stays with `self`,
    /// so clone only between complete frames.
    pub fn try_clone(&self) -> Result<Self> {
        Ok(Self {
            inner: self.inner.clone(),
            buffer: Vec::new(),
        })
    }

    /// Writes one JSON value followed by a newline.
    pub fn send<T: Serialize>(&mut self, value: &T) -> Result<()> {
        let mut bytes = serde_json::to_vec(value)?;
        bytes.push(b'\n');
        self.inner.write_all(&bytes)?;
        Ok(())
    }

    /// Reads one newline-delimited JSON value of at most `limit` bytes.
    /// Unknown fields are rejected when `T` denies them.
    pub fn recv<T: DeserializeOwned>(&mut self, limit: usize) -> Result<T> {
        loop {
            if let Some(end) = self.buffer.iter().position(|b| *b == b'\n') {
                let line: Vec<u8> = self.buffer.drain(..=end).collect();
                if line.len() > limit {
                    bail!("IPC message exceeds {limit} bytes");
                }
                return Ok(serde_json::from_slice(&line)?);
            }
            if self.buffer.len() > limit {
                bail!("IPC message exceeds {limit} bytes");
            }
            let mut chunk = [0u8; 8192];
            let n = self.inner.read(&mut chunk)?;
            if n == 0 {
                if self.buffer.iter().all(u8::is_ascii_whitespace) {
                    return Err(
                        anyhow::Error::new(io::Error::from(io::ErrorKind::UnexpectedEof))
                            .context("IPC peer closed the connection"),
                    );
                }
                // Go's decoder accepts a final value without a newline.
                let line = std::mem::take(&mut self.buffer);
                return Ok(serde_json::from_slice(&line)?);
            }
            self.buffer.extend_from_slice(&chunk[..n]);
        }
    }

    /// Blocks until the peer closes or sends any byte (cancellation signal).
    pub fn wait_closed(&mut self) {
        if !self.buffer.is_empty() {
            return;
        }
        let mut byte = [0u8; 1];
        let _ = self.inner.read(&mut byte);
    }

    /// Sends the three standard stream descriptors (Unix `SCM_RIGHTS`).
    #[cfg(unix)]
    pub fn send_streams(&self, fds: [std::os::fd::BorrowedFd<'_>; 3]) -> Result<()> {
        Ok(self.inner.send_fds(fds)?)
    }

    /// Receives exactly three descriptors sent by [`Connection::send_streams`].
    #[cfg(unix)]
    pub fn recv_streams(&self) -> Result<[std::os::fd::OwnedFd; 3]> {
        if !self.buffer.is_empty() {
            return Err(anyhow::anyhow!(
                "stream descriptors must precede framed IPC data"
            ));
        }
        self.inner.recv_fds()
    }
}

/// Error for operations on a closed endpoint.
fn closed_error() -> io::Error {
    io::Error::new(io::ErrorKind::ConnectionAborted, "IPC endpoint closed")
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    #[derive(serde::Serialize, serde::Deserialize, Debug, PartialEq)]
    #[serde(deny_unknown_fields)]
    struct Message {
        value: String,
    }

    #[test]
    fn round_trip_peer_and_close() {
        let (listener, endpoint) = Listener::bind("ccow-t-").unwrap();
        let client = std::thread::spawn(move || {
            let mut c = Connection::dial(&endpoint, Duration::from_secs(5)).unwrap();
            c.send(&Message {
                value: "hello".into(),
            })
            .unwrap();
            let reply: Message = c.recv(DEFAULT_LIMIT).unwrap();
            assert_eq!(reply.value, "world");
        });
        let mut server = listener.accept().unwrap();
        assert_eq!(server.peer_pid().unwrap(), i64::from(std::process::id()));
        let m: Message = server.recv(DEFAULT_LIMIT).unwrap();
        assert_eq!(m.value, "hello");
        server
            .send(&Message {
                value: "world".into(),
            })
            .unwrap();
        client.join().unwrap();
        assert!(server.recv::<Message>(DEFAULT_LIMIT).is_err());
    }

    #[test]
    fn closer_interrupts_accept_and_read() {
        let (listener, endpoint) = Listener::bind("ccow-t-").unwrap();
        let closer = listener.closer();
        let t = std::thread::spawn(move || listener.accept().is_err());
        std::thread::sleep(Duration::from_millis(50));
        closer.close();
        assert!(t.join().unwrap());

        let (listener, endpoint2) = Listener::bind("ccow-t-").unwrap();
        let _ = endpoint;
        let client = std::thread::spawn(move || {
            Connection::dial(&endpoint2, Duration::from_secs(5)).unwrap()
        });
        let mut server = listener.accept().unwrap();
        let _keep = client.join().unwrap();
        let closer = server.closer();
        let reader = std::thread::spawn(move || server.recv::<Message>(DEFAULT_LIMIT).is_err());
        std::thread::sleep(Duration::from_millis(50));
        closer.close();
        assert!(reader.join().unwrap());
    }

    #[test]
    fn deadline_and_limit() {
        let (listener, endpoint) = Listener::bind("ccow-t-").unwrap();
        let client = std::thread::spawn(move || {
            let mut c = Connection::dial(&endpoint, Duration::from_secs(5)).unwrap();
            c.send(&"x".repeat(100)).unwrap();
            std::thread::sleep(Duration::from_millis(500));
        });
        let mut server = listener.accept().unwrap();
        assert!(server.recv::<String>(10).is_err());
        server
            .set_deadline(Some(Instant::now() + Duration::from_millis(50)))
            .unwrap();
        let mut fresh = server.try_clone().unwrap();
        assert!(fresh.recv::<String>(DEFAULT_LIMIT).is_err());
        client.join().unwrap();
    }
}
