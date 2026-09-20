use std::{
    collections::{BTreeMap, BTreeSet},
    io::{self, Write},
    time::{Duration, Instant},
};

use clap::{Args, Subcommand, ValueEnum};
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
#[cfg(target_os = "linux")]
use linux::Native;
#[cfg(target_os = "macos")]
use macos::Native;
#[cfg(windows)]
use windows::Native;

#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, ValueEnum, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Protocol {
    Tcp,
    Udp,
    All,
}
impl Protocol {
    pub fn accepts(self, other: Self) -> bool {
        self == Self::All || self == other
    }
}

#[derive(Subcommand)]
pub enum Action {
    /// List TCP LISTEN or UDP owners, including IPv4 and IPv6.
    #[command(
        after_help = "Examples:\n  clibox port which 3000 8080\n  clibox port which 5353 \
                      --protocol udp --json\n  clibox port which 3000 --quiet"
    )]
    Which {
        #[command(flatten)]
        query: Query,
        #[arg(long, conflicts_with = "json")]
        quiet: bool,
    },
    /// Forcibly terminate verified owners without elevation or descendant
    /// killing.
    #[command(
        after_help = "Example:\n  clibox port kill 3000 8080 --json\n\nRechecks owner identity, \
                      then verifies termination for at most five seconds total. Ports are not \
                      reserved."
    )]
    Kill {
        #[command(flatten)]
        query: Query,
    },
}

#[derive(Args)]
pub struct Query {
    #[arg(required = true, num_args = 1.., value_parser = parse_port)]
    ports: Vec<u16>,
    #[arg(long, value_enum, default_value = "tcp")]
    protocol: Protocol,
    #[arg(long)]
    json: bool,
}
fn parse_port(raw: &str) -> std::result::Result<u16, &'static str> {
    if raw.is_empty() || !raw.bytes().all(|b| b.is_ascii_digit()) {
        return Err("Expected a decimal port from 1 through 65535");
    }
    raw.parse::<u16>()
        .ok()
        .filter(|p| *p != 0)
        .ok_or("Expected a decimal port from 1 through 65535")
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum Status {
    Killed,
    AlreadyExited,
    Skipped,
    Failed,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Identity {
    pub birth: u128,
    pub socket: u64,
}

#[derive(Clone, Debug, Serialize)]
pub struct Entry {
    pub pid: Option<u32>,
    pub name: Option<String>,
    pub protocol: Protocol,
    pub address: String,
    pub port: u16,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub status: Option<Status>,
    #[serde(skip)]
    pub identity: Option<Identity>,
}

#[derive(Default, Serialize)]
pub struct Report {
    pub results: Vec<Entry>,
    pub errors: Vec<Failure>,
}
impl Report {
    fn normalize(&mut self) {
        self.results.sort_by(|a, b| {
            (a.port, a.protocol, &a.address, a.pid).cmp(&(b.port, b.protocol, &b.address, b.pid))
        });
        self.results.dedup_by(|a, b| {
            a.pid == b.pid && a.protocol == b.protocol && a.address == b.address && a.port == b.port
        });
    }
}

pub trait Backend {
    fn snapshot(&mut self, ports: &BTreeSet<u16>, protocol: Protocol) -> Report;
    /// Must recheck birth identity and one of the originally observed sockets
    /// immediately before signaling. A replacement owner is never added to
    /// the initial target set.
    fn terminate(&mut self, pid: u32, expected: &[Entry]) -> Result<bool>;
    fn alive(&mut self, pid: u32, birth: u128) -> Result<bool>;
}
trait Clock {
    fn elapsed(&self) -> Duration;
    fn pause(&mut self);
}
struct RealClock(Instant);
impl Clock for RealClock {
    fn elapsed(&self) -> Duration {
        self.0.elapsed()
    }

    fn pause(&mut self) {
        std::thread::sleep(runtime::POLL);
    }
}

fn kill(report: &mut Report, backend: &mut impl Backend, clock: &mut impl Clock) {
    let mut targets: BTreeMap<u32, Vec<Entry>> = BTreeMap::new();
    for row in &mut report.results {
        row.status = Some(Status::Skipped);
        if let Some(pid) = row.pid {
            targets.entry(pid).or_default().push(row.clone());
        } else {
            report.errors.push(
                Failure::new(
                    Code::IdentityUnverifiable,
                    "Socket owner could not be verified; no signal was sent.",
                )
                .port(row.port),
            );
        }
    }
    let mut pending = BTreeMap::new();
    let set_status = |report: &mut Report, pid, status| {
        for row in &mut report.results {
            if row.pid == Some(pid) {
                row.status = Some(status);
            }
        }
    };
    for (pid, rows) in targets {
        if runtime::cancelled() {
            report.errors.push(Failure::new(
                Code::Cancelled,
                "Termination interrupted; completed terminations are not undone.",
            ));
            break;
        }
        let birth = rows.first().and_then(|e| e.identity).map(|i| i.birth);
        if birth.is_none() || rows.iter().any(|r| r.identity.map(|i| i.birth) != birth) {
            report.errors.push(
                Failure::new(
                    Code::IdentityUnverifiable,
                    "Process identity could not be verified; no signal was sent.",
                )
                .pid(pid),
            );
            continue;
        }
        match backend.terminate(pid, &rows) {
            Ok(false) => set_status(report, pid, Status::AlreadyExited),
            Ok(true) => {
                tracing::debug!(
                    operation = "port-kill",
                    pid,
                    "Verified owner termination requested"
                );
                pending.insert(pid, birth.unwrap());
            }
            Err(error) => {
                set_status(
                    report,
                    pid,
                    if matches!(
                        error.code,
                        Code::IdentityChanged | Code::IdentityUnverifiable | Code::OwnershipChanged
                    ) {
                        Status::Skipped
                    } else {
                        Status::Failed
                    },
                );
                report.errors.push(error.pid(pid));
            }
        }
    }
    // One deadline for the entire pending set, not five seconds for each PID.
    let deadline = clock.elapsed() + Duration::from_secs(5);
    while !pending.is_empty() {
        let mut completed = Vec::new();
        for (&pid, &birth) in &pending {
            match backend.alive(pid, birth) {
                Ok(false) => {
                    set_status(report, pid, Status::Killed);
                    completed.push(pid);
                }
                Err(error) => {
                    set_status(report, pid, Status::Failed);
                    report.errors.push(error.pid(pid));
                    completed.push(pid);
                }
                Ok(true) => (),
            }
        }
        for pid in completed {
            pending.remove(&pid);
        }
        if pending.is_empty() {
            break;
        }
        if runtime::cancelled() || clock.elapsed() >= deadline {
            for pid in pending.keys() {
                set_status(report, *pid, Status::Failed);
                report.errors.push(
                    Failure::new(
                        if runtime::cancelled() {
                            Code::Cancelled
                        } else {
                            Code::TerminationTimeout
                        },
                        "Termination was requested but could not be confirmed within the shared \
                         wait.",
                    )
                    .pid(*pid),
                );
            }
            break;
        }
        clock.pause();
    }
}

pub fn execute(action: Action) -> Result<i32> {
    let (query, quiet, terminate) = match action {
        Action::Which { query, quiet } => (query, quiet, false),
        Action::Kill { query } => (query, false, true),
    };
    let ports = query.ports.into_iter().collect();
    let mut backend = Native::default();
    let mut report = backend.snapshot(&ports, query.protocol);
    report.normalize();
    tracing::debug!(
        operation = "port-enumeration",
        backend = std::env::consts::OS,
        results = report.results.len(),
        errors = report.errors.len(),
        "Port snapshot completed"
    );
    if terminate {
        kill(&mut report, &mut backend, &mut RealClock(Instant::now()));
    }
    write_report(
        &report,
        query.json,
        quiet,
        terminate,
        &mut io::stdout().lock(),
    )?;
    for error in &report.errors {
        error.report(if terminate { "port-kill" } else { "port-which" });
    }
    Ok(i32::from(!report.errors.is_empty()))
}

fn write_report(
    report: &Report,
    json: bool,
    quiet: bool,
    terminate: bool,
    out: &mut impl Write,
) -> Result<()> {
    if json {
        serde_json::to_writer(&mut *out, report)
            .map_err(|_| Failure::new(Code::IoFailed, "Could not write JSON output."))?;
        writeln!(out).map_err(|e| Failure::io(&e))?;
    } else if quiet {
        for pid in report
            .results
            .iter()
            .filter_map(|e| e.pid)
            .collect::<BTreeSet<_>>()
        {
            writeln!(out, "{pid}").map_err(|e| Failure::io(&e))?;
        }
    } else {
        writeln!(
            out,
            "PID\tNAME\tPROTOCOL\tADDRESS\tPORT{}",
            if terminate { "\tSTATUS" } else { "" }
        )
        .map_err(|e| Failure::io(&e))?;
        for row in &report.results {
            let name: String = row
                .name
                .as_deref()
                .unwrap_or("-")
                .chars()
                .map(|c| if c.is_control() { '�' } else { c })
                .collect();
            write!(
                out,
                "{}\t{}\t{}\t{}\t{}",
                row.pid.map_or_else(|| "-".into(), |p| p.to_string()),
                name,
                if row.protocol == Protocol::Tcp {
                    "tcp"
                } else {
                    "udp"
                },
                row.address,
                row.port
            )
            .map_err(|e| Failure::io(&e))?;
            if let Some(status) = row.status {
                write!(
                    out,
                    "\t{}",
                    match status {
                        Status::Killed => "killed",
                        Status::AlreadyExited => "already-exited",
                        Status::Skipped => "skipped",
                        Status::Failed => "failed",
                    }
                )
                .map_err(|e| Failure::io(&e))?;
            }
            writeln!(out).map_err(|e| Failure::io(&e))?;
        }
    }
    Ok(())
}

pub fn enumeration_error(pid: Option<u32>, error: std::io::Error) -> Failure {
    let mut failure = Failure::new(
        if error.kind() == io::ErrorKind::PermissionDenied {
            Code::PermissionDenied
        } else {
            Code::EnumerationFailed
        },
        "Port enumeration is incomplete; some socket or process information is inaccessible to \
         the current user.",
    );
    failure.pid = pid;
    failure
}

#[cfg(test)]
mod tests;
