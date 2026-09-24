use std::{
    io::{self, Write},
    num::NonZeroUsize,
};

use clap::{Subcommand, ValueEnum};
use serde::Serialize;

use crate::{
    error::{Code, Failure, Result},
    runtime,
};

#[cfg(target_os = "linux")]
mod linux;
#[cfg(target_os = "macos")]
mod macos;
#[cfg(windows)]
mod windows;

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq, Serialize, ValueEnum)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    #[default]
    Available,
    Logical,
}

#[derive(Subcommand)]
pub enum Action {
    /// Print estimated usable parallelism or online logical CPUs.
    #[command(
        after_help = "Examples:\n  clibox system cpus\n  clibox system cpus --kind logical\n  \
                      clibox system cpus --json\n  clibox system cpus --kind logical \
                      --quiet\n\nAvailable is Rust's estimate of suitable parallelism, not idle \
                      CPUs or guaranteed capacity. Logical counts online CPUs visible to this OS, \
                      without clibox affinity or quota adjustments. Neither mode applies OMP_* \
                      overrides or substitutes another count on failure. Output is one integer \
                      and LF, or one compact JSON object and LF; quiet still queries. No stdin or \
                      external utilities are used.\n\nExit codes: 0 success, 1 query/output \
                      failure, 2 invalid arguments, 130 Ctrl+C/Windows Ctrl+Break, 143 Unix \
                      SIGTERM. A failed stdout write may leave partial output."
    )]
    Cpus {
        /// Count kind: available (default) or logical.
        #[arg(long, value_enum, default_value_t = Kind::Available)]
        kind: Kind,
        /// Print {"kind":"...","count":N} with one trailing LF.
        #[arg(long, conflicts_with = "quiet")]
        json: bool,
        /// Perform the query without printing a result.
        #[arg(long)]
        quiet: bool,
    },
}

trait Backend {
    fn query(&self, kind: Kind) -> Result<usize>;
}

struct Native;
impl Backend for Native {
    fn query(&self, kind: Kind) -> Result<usize> {
        match kind {
            Kind::Available => available_with(std::thread::available_parallelism),
            Kind::Logical => logical(),
        }
    }
}

fn available_with(query: impl FnOnce() -> io::Result<NonZeroUsize>) -> Result<usize> {
    query()
        .map(NonZeroUsize::get)
        .map_err(|error| query_error(&error))
}

fn query_error(error: &io::Error) -> Failure {
    let code = match error.kind() {
        io::ErrorKind::PermissionDenied => Code::PermissionDenied,
        io::ErrorKind::NotFound | io::ErrorKind::Unsupported => Code::BackendUnavailable,
        _ => Code::EnumerationFailed,
    };
    Failure::new(
        code,
        "CPU count query failed; check OS CPU information and current-user access.",
    )
}

fn invalid_count() -> Failure {
    Failure::new(
        Code::EnumerationFailed,
        "The OS reported an invalid online CPU count; check CPU enumeration for this environment.",
    )
}

#[cfg(target_os = "linux")]
fn logical() -> Result<usize> {
    linux::logical()
}
#[cfg(target_os = "macos")]
fn logical() -> Result<usize> {
    macos::logical()
}
#[cfg(windows)]
fn logical() -> Result<usize> {
    windows::logical()
}

fn run(
    backend: impl Backend + Send + 'static,
    kind: Kind,
    json: bool,
    quiet: bool,
    out: &mut impl Write,
) -> Result<()> {
    tracing::debug!(
        operation = "system-cpus",
        ?kind,
        backend = std::env::consts::OS,
        "CPU query started"
    );
    // The worker may finish after cancellation, but it cannot publish or
    // mutate any result on its own.
    let result = runtime::interruptible(move || backend.query(kind));
    let count = match result {
        Ok(count) if count > 0 => count,
        Ok(_) => {
            let error = invalid_count();
            tracing::debug!(operation = "system-cpus", ?kind, backend = std::env::consts::OS, classification = ?error.code, "CPU query failed");
            return Err(error);
        }
        Err(error) => {
            tracing::debug!(operation = "system-cpus", ?kind, backend = std::env::consts::OS, classification = ?error.code, "CPU query failed");
            return Err(error);
        }
    };
    runtime::check_cancelled()?;
    tracing::debug!(
        operation = "system-cpus",
        ?kind,
        backend = std::env::consts::OS,
        classification = "success",
        "CPU query completed"
    );
    if quiet {
        return Ok(());
    }
    let output = if json {
        format!(
            "{{\"kind\":\"{}\",\"count\":{count}}}\n",
            match kind {
                Kind::Available => "available",
                Kind::Logical => "logical",
            }
        )
    } else {
        format!("{count}\n")
    };
    out.write_all(output.as_bytes()).map_err(|error| {
        let failure = Failure::new(
            if error.kind() == io::ErrorKind::PermissionDenied {
                Code::PermissionDenied
            } else {
                Code::IoFailed
            },
            "Could not write CPU count to stdout; check the output destination.",
        );
        tracing::debug!(operation = "system-cpus", ?kind, backend = std::env::consts::OS, classification = ?failure.code, "CPU output failed");
        failure
    })
}

pub fn execute(action: Action) -> Result<()> {
    let Action::Cpus { kind, json, quiet } = action;
    // Parsing rejects conflicting flags before the system command is entered.
    run(Native, kind, json, quiet, &mut io::stdout().lock())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn available_returns_the_standard_api_result_without_adjustment_or_fallback() {
        assert_eq!(
            available_with(|| Ok(NonZeroUsize::new(17).unwrap())).unwrap(),
            17
        );
        assert_eq!(
            available_with(|| Err(io::ErrorKind::PermissionDenied.into()))
                .unwrap_err()
                .code,
            Code::PermissionDenied
        );
    }

    struct Fake(Result<usize>);
    impl Backend for Fake {
        fn query(&self, _kind: Kind) -> Result<usize> {
            self.0.clone()
        }
    }

    #[test]
    fn only_the_selected_kind_is_queried() {
        struct Recording(std::sync::mpsc::Sender<Kind>);
        impl Backend for Recording {
            fn query(&self, kind: Kind) -> Result<usize> {
                self.0.send(kind).unwrap();
                Err(Failure::new(Code::BackendUnavailable, "missing"))
            }
        }
        let (sender, receiver) = std::sync::mpsc::channel();
        let mut out = Vec::new();
        assert_eq!(
            run(Recording(sender), Kind::Logical, true, false, &mut out)
                .unwrap_err()
                .code,
            Code::BackendUnavailable
        );
        assert_eq!(receiver.recv().unwrap(), Kind::Logical);
        assert!(receiver.try_recv().is_err());
        assert!(out.is_empty());
    }

    #[test]
    fn result_bytes_and_failures() {
        let mut out = Vec::new();
        assert!(run(Fake(Ok(8)), Kind::Available, false, false, &mut out).is_ok());
        assert_eq!(out, b"8\n");
        out.clear();
        assert!(run(Fake(Ok(72)), Kind::Logical, true, false, &mut out).is_ok());
        assert_eq!(out, b"{\"kind\":\"logical\",\"count\":72}\n");
        out.clear();
        assert!(run(Fake(Ok(8)), Kind::Available, false, true, &mut out).is_ok());
        assert!(out.is_empty());
        for result in [
            Ok(0),
            Err(Failure::new(Code::PermissionDenied, "denied")),
            Err(Failure::new(Code::BackendUnavailable, "missing")),
        ] {
            assert!(run(Fake(result), Kind::Logical, true, false, &mut out).is_err());
            assert!(out.is_empty());
        }
    }

    struct BrokenWriter;
    impl Write for BrokenWriter {
        fn write(&mut self, _: &[u8]) -> io::Result<usize> {
            Err(io::ErrorKind::BrokenPipe.into())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    #[test]
    fn output_failure_is_classified() {
        assert_eq!(
            run(
                Fake(Ok(4)),
                Kind::Available,
                false,
                false,
                &mut BrokenWriter
            )
            .unwrap_err()
            .code,
            Code::IoFailed
        );
    }
}
