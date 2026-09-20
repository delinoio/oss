use std::{fmt, future::Future, time::Duration};

use serde::Serialize;
use tokio::time::{sleep, timeout, Instant};

use crate::cli::Options;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Tcp,
    Http,
    File,
}

impl fmt::Display for Kind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::Tcp => "tcp",
            Self::Http => "http",
            Self::File => "file",
        })
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Status {
    Ready,
    Timeout,
    Failed,
    Cancelled,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Code {
    DnsLookup,
    DnsConfiguration,
    ConnectionRefused,
    NetworkUnavailable,
    AttemptTimeout,
    UnexpectedStatus,
    TlsCertificate,
    TlsProtocol,
    TrustStore,
    PermissionDenied,
    NetworkIo,
    HttpProtocol,
    FileMissing,
    NotRegularFile,
    Filesystem,
    RuntimeInitialization,
    SignalHandler,
    Interrupted,
    Terminated,
    OverallTimeout,
}

impl Code {
    pub fn message(self) -> &'static str {
        match self {
            Self::DnsLookup => "DNS lookup did not resolve the target; check DNS availability.",
            Self::DnsConfiguration => {
                "Cannot initialize the system DNS resolver; check resolver configuration and \
                 permissions."
            }
            Self::ConnectionRefused => "Connection refused; check that the service is listening.",
            Self::NetworkUnavailable => {
                "Network temporarily unavailable; check connectivity and service availability."
            }
            Self::AttemptTimeout => {
                "The attempt deadline expired; check target readiness or increase \
                 --attempt-timeout."
            }
            Self::UnexpectedStatus => {
                "HTTP status did not match; check readiness or the requested --status."
            }
            Self::TlsCertificate => {
                "TLS certificate verification failed; repair the certificate, hostname, or OS \
                 trust store."
            }
            Self::TlsProtocol => "TLS negotiation failed; check that the service supports HTTPS.",
            Self::TrustStore => {
                "Cannot initialize OS certificate trust; repair the OS trust store."
            }
            Self::PermissionDenied => "Permission denied; check current-user access permissions.",
            Self::NetworkIo => {
                "A terminal network operation failed; check local resources and service \
                 configuration."
            }
            Self::HttpProtocol => {
                "Invalid HTTP response or request failure; check the service protocol."
            }
            Self::FileMissing => "File is missing; check that its producer creates the target.",
            Self::NotRegularFile => {
                "The target is not a regular file; select a regular file instead."
            }
            Self::Filesystem => {
                "Cannot inspect file metadata; check path components, symbolic links, and \
                 filesystem access."
            }
            Self::RuntimeInitialization => {
                "Cannot initialize the wait runtime; check available system resources."
            }
            Self::SignalHandler => {
                "Cannot install or receive cancellation signals; check process signal support."
            }
            Self::Interrupted => "Wait cancelled by Ctrl+C; the target was left unchanged.",
            Self::Terminated => "Wait cancelled by SIGTERM; the target was left unchanged.",
            Self::OverallTimeout => {
                "Overall deadline expired; check readiness or increase --timeout."
            }
        }
    }

    pub fn retryable(self) -> bool {
        matches!(
            self,
            Self::DnsLookup
                | Self::ConnectionRefused
                | Self::NetworkUnavailable
                | Self::AttemptTimeout
                | Self::UnexpectedStatus
                | Self::FileMissing
        )
    }
}

impl fmt::Display for Code {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        // Serialization and diagnostics share the exact stable classification.
        let value = serde_json::to_value(self).map_err(|_| fmt::Error)?;
        f.write_str(value.as_str().ok_or(fmt::Error)?)
    }
}
impl std::error::Error for Code {}

#[derive(Serialize)]
pub struct Error {
    pub code: Code,
    pub message: String,
}

#[derive(Serialize)]
pub struct Report {
    pub kind: Kind,
    pub status: Status,
    pub elapsed_ms: u128,
    pub attempts: u64,
    pub error: Option<Error>,
    #[serde(skip)]
    pub exit_code: u8,
}

impl Report {
    pub fn failure(kind: Kind, code: Code) -> Self {
        Self::finish(kind, Instant::now(), 0, Status::Failed, Some(code), None)
    }

    fn finish(
        kind: Kind,
        start: Instant,
        attempts: u64,
        status: Status,
        code: Option<Code>,
        last: Option<Code>,
    ) -> Self {
        let error = code.map(|code| {
            let mut message = code.message().to_owned();
            if let Some(last) = last {
                message.push_str(&format!(
                    " Last retryable failure: {last}. {}",
                    last.message()
                ));
            }
            Error { code, message }
        });
        let exit_code = match code {
            None => 0,
            Some(Code::Interrupted) => 130,
            Some(Code::Terminated) => 143,
            _ => 1,
        };
        Self {
            kind,
            status,
            elapsed_ms: start.elapsed().as_millis(),
            attempts,
            error,
            exit_code,
        }
    }
}

/// The probe future owns one attempt. Dropping it cancels network work; no next
/// attempt starts until this future has completed or its budget has expired.
/// Tokio's clock is monotonic and replaceable by paused time in scheduler
/// tests.
pub async fn run<P, F, C>(
    kind: Kind,
    options: &Options,
    attempt_timeout: Option<Duration>,
    mut probe: P,
    cancellation: C,
) -> Report
where
    P: FnMut() -> F,
    F: Future<Output = Result<(), Code>>,
    C: Future<Output = Code>,
{
    let start = Instant::now();
    let limit = options.timeout.filter(|d| !d.is_zero());
    let mut attempts = 0u64;
    let mut last = None;
    tokio::pin!(cancellation);
    let operation = async {
        loop {
            let remaining = limit.map(|limit| limit.saturating_sub(start.elapsed()));
            if remaining == Some(Duration::ZERO) {
                return (Status::Timeout, Some(Code::OverallTimeout), last);
            }
            let budget = match (attempt_timeout, remaining) {
                (Some(a), Some(b)) => Some(a.min(b)),
                (a, b) => a.or(b),
            };
            attempts = attempts.saturating_add(1);
            tracing::debug!(%kind, attempt = attempts, elapsed_ms = start.elapsed().as_millis(), "wait_attempt");
            let result = match budget {
                Some(budget) => timeout(budget, probe())
                    .await
                    .unwrap_or(Err(Code::AttemptTimeout)),
                None => probe().await,
            };
            if let Err(code) = result {
                tracing::debug!(%kind, attempt = attempts, %code, elapsed_ms = start.elapsed().as_millis(), "wait_observation");
                if !code.retryable() {
                    return (Status::Failed, Some(code), None);
                }
                last = Some(code);
            }
            // A result observed after the overall deadline cannot establish
            // readiness within the caller's budget, even if it became ready.
            if limit.is_some_and(|limit| start.elapsed() >= limit) {
                return (Status::Timeout, Some(Code::OverallTimeout), last);
            }
            if result.is_ok() {
                return (Status::Ready, None, None);
            }
            let delay = limit
                .map(|limit| options.interval.min(limit.saturating_sub(start.elapsed())))
                .unwrap_or(options.interval);
            sleep(delay).await;
        }
    };
    let (status, code, last) = tokio::select! {
        biased;
        code = &mut cancellation => (if matches!(code, Code::Interrupted | Code::Terminated) { Status::Cancelled } else { Status::Failed }, Some(code), None),
        result = operation => result,
    };
    Report::finish(kind, start, attempts, status, code, last)
}

pub struct Signals {
    #[cfg(unix)]
    interrupt: tokio::signal::unix::Signal,
    #[cfg(unix)]
    terminate: tokio::signal::unix::Signal,
    #[cfg(windows)]
    interrupt: tokio::signal::windows::CtrlC,
}

impl Signals {
    pub fn install() -> Result<Self, Code> {
        #[cfg(unix)]
        {
            use tokio::signal::unix::{signal, SignalKind};
            Ok(Self {
                interrupt: signal(SignalKind::interrupt()).map_err(|_| Code::SignalHandler)?,
                terminate: signal(SignalKind::terminate()).map_err(|_| Code::SignalHandler)?,
            })
        }
        #[cfg(windows)]
        {
            Ok(Self {
                interrupt: tokio::signal::windows::ctrl_c().map_err(|_| Code::SignalHandler)?,
            })
        }
    }

    pub async fn cancelled(mut self) -> Code {
        tokio::select! {
            result = self.interrupt.recv() => if result.is_some() { Code::Interrupted } else { Code::SignalHandler },
            result = async {
                #[cfg(unix)] { self.terminate.recv().await }
                #[cfg(not(unix))] { std::future::pending::<Option<()>>().await }
            } => if result.is_some() { Code::Terminated } else { Code::SignalHandler },
        }
    }
}

#[cfg(test)]
#[path = "wait_tests.rs"]
mod tests;
