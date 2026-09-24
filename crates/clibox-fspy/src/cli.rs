use std::{
    ffi::OsString,
    fmt::Write as _,
    fs::File,
    io::{BufReader, Write as _},
    path::PathBuf,
    sync::{Arc, atomic::AtomicBool},
    time::Duration,
};

use clap::{Args, Subcommand};

use crate::{
    assetcov, autowatch, fbreak, latencylab, minrepro, output,
    trace::{self, Comparison, EncodedPath},
};

#[derive(Subcommand)]
pub enum Fspy {
    /// Record a newly launched command's file operations as versioned NDJSON.
    #[command(
        after_help = "Example: clibox fspy record --output trace.ndjson -- cargo test\nCovers \
                      synchronous open/close, read/write, metadata, directories, pathname \
                      mutations, and supported descendants.\nmmap, asynchronous I/O, and \
                      interception bypasses are outside this boundary.\nTrace files reveal \
                      accessed paths; remove local artifacts manually when no longer \
                      needed.\nExit codes: 0 success, 1 tracing failure, child failure status, 2 \
                      invalid input, 124 timeout, 130 Ctrl+C, 143 SIGTERM."
    )]
    Record(Record),
    /// Compare complete compatible record files by project-relative path.
    #[command(
        after_help = "Example: clibox fspy compare before.ndjson after.ndjson \
                      --fail-on-change\nCount and timing changes are informational under \
                      --fail-on-change.\nNo file content comparison is performed. Exit codes: 0 \
                      report, 1 failure or gated change, 2 invalid input."
    )]
    Compare(Compare),
    /// Measure which selected resource files a test command actually reads.
    #[command(
        after_help = "Example: clibox fspy assetcov --include 'assets/**' -- cargo test\nOnly a \
                      successful content read covers a file; opening or checking metadata does \
                      not.\nExit codes: 0 success, 1 tracing or coverage failure, 2 invalid \
                      input, 124 timeout, 130 Ctrl+C, 143 SIGTERM."
    )]
    Assetcov(assetcov::Assetcov),
    /// Compare traced baseline runs with runs delayed before selected
    /// operations.
    #[command(
        after_help = "Example: clibox fspy latencylab --include 'assets/**' --delay 5ms -- cargo \
                      test\nRuns alternate baseline and delayed conditions. Results measure this \
                      execution, not a storage-device prediction.\nExit codes: 0 report, 1 \
                      runtime or experiment failure, 2 invalid input, 124 timeout, 130 Ctrl+C, \
                      143 SIGTERM."
    )]
    Latencylab(latencylab::Latencylab),
    /// Pause a selected caller before its file operation and control it from a
    /// TTY.
    #[command(
        after_help = "Example: clibox fspy fbreak --include 'assets/**' --op read -- cargo \
                      test\nControls: n release one match, c continue all callers, q quit. The \
                      child cannot read terminal input.\nExit codes: 0 success, child failure \
                      status, 1 tracing or control failure, 2 invalid input, 124 timeout, 130 \
                      quit/Ctrl+C, 143 SIGTERM."
    )]
    Fbreak(fbreak::Fbreak),
    /// Rerun a command when its traced project inputs change.
    #[command(
        after_help = "Example: clibox fspy autowatch --include 'src/**' -- cargo test\nRuns \
                      immediately, then watches observed inputs, queried directories, and missing \
                      paths. Each run is serial; mmap and asynchronous I/O remain outside tracing \
                      coverage.\nExit codes: 1 tracing/watch failure, 2 invalid input, 124 run \
                      timeout, 130 Ctrl+C, 143 SIGTERM."
    )]
    Autowatch(autowatch::Autowatch),
    /// Collect traced project inputs and publish only a verified reproduction.
    #[command(
        after_help = "Example: clibox fspy min-repro --include 'src/**' --bundle-dir repro \
                      --expect-exit 1 --expect-stderr 'failure' -- cargo test\nThe candidate runs \
                      once from a separate directory; this is not an OS sandbox or a portable \
                      runtime bundle. The command and expected stderr text are never \
                      stored.\nExit codes: 0 verified bundle, 1 failure, 2 invalid input, 124 \
                      timeout, 130 Ctrl+C, 143 SIGTERM."
    )]
    MinRepro(minrepro::MinRepro),
}

#[derive(Args)]
pub struct Record {
    /// Existing root for path classification; does not change the child cwd.
    #[arg(long, value_name = "DIR")]
    root: Option<PathBuf>,
    /// Write NDJSON to a file; omitted or '-' writes to stdout.
    #[arg(long, value_name = "FILE")]
    output: Option<PathBuf>,
    /// Replace an existing regular output file.
    #[arg(long)]
    force: bool,
    /// Optional execution budget in integer ms/s/m/h.
    #[arg(long, value_parser = parse_duration, value_name = "DURATION")]
    timeout: Option<Duration>,
    /// Grace period before forcefully terminating owned descendants.
    #[arg(long, default_value = "5s", value_parser = parse_duration, value_name = "DURATION")]
    kill_after: Duration,
    /// Maximum operation events (default: 1000000).
    #[arg(long, default_value_t = trace::DEFAULT_MAX_EVENTS, value_parser = parse_positive)]
    max_events: usize,
    /// Maximum encoded NDJSON bytes (default: 268435456).
    #[arg(long, default_value_t = trace::DEFAULT_MAX_BYTES, value_parser = parse_positive)]
    max_trace_bytes: usize,
    /// Command and literal arguments; use -- before COMMAND.
    #[arg(last = true, required = true, num_args = 1.., value_name = "COMMAND [ARG...]")]
    command: Vec<OsString>,
}

#[derive(Args)]
pub struct Compare {
    /// Complete before record.
    before: PathBuf,
    /// Complete after record.
    after: PathBuf,
    /// Return 1 for added/removed project paths or changed operation kinds.
    #[arg(long)]
    fail_on_change: bool,
    #[command(flatten)]
    report: ReportOptions,
    /// Maximum operation events read from each trace.
    #[arg(long, default_value_t = trace::DEFAULT_MAX_EVENTS, value_parser = parse_positive)]
    max_events: usize,
    /// Maximum encoded bytes read from each trace.
    #[arg(long, default_value_t = trace::DEFAULT_MAX_BYTES, value_parser = parse_positive)]
    max_trace_bytes: usize,
}

#[derive(Args)]
pub(crate) struct ReportOptions {
    /// Emit a machine-readable JSON report.
    #[arg(long, conflicts_with = "quiet")]
    pub(crate) json: bool,
    /// Suppress the report; failures still print diagnostics.
    #[arg(long)]
    pub(crate) quiet: bool,
    /// Write the report to a file; omitted or '-' writes to stdout.
    #[arg(long, value_name = "FILE")]
    pub(crate) output: Option<PathBuf>,
    /// Replace an existing regular output file.
    #[arg(long)]
    pub(crate) force: bool,
}

pub(crate) fn parse_positive(value: &str) -> Result<usize, &'static str> {
    value
        .parse::<usize>()
        .ok()
        .filter(|value| *value > 0)
        .ok_or("Provide a positive integer.")
}

pub(crate) fn parse_duration(value: &str) -> Result<Duration, &'static str> {
    let (amount, scale) = [("ms", 1u64), ("s", 1000), ("m", 60_000), ("h", 3_600_000)]
        .into_iter()
        .find_map(|(suffix, scale)| value.strip_suffix(suffix).map(|amount| (amount, scale)))
        .ok_or("Use an integer duration with ms, s, m, or h.")?;
    let amount = amount
        .parse::<u64>()
        .map_err(|_| "Use an integer duration.")?;
    let millis = amount
        .checked_mul(scale)
        .filter(|millis| *millis > 0)
        .ok_or("Use a positive duration within range.")?;
    let duration = Duration::from_millis(millis);
    if std::time::Instant::now().checked_add(duration).is_none() {
        return Err("Duration is too large.");
    }
    Ok(duration)
}

#[must_use]
pub fn execute(command: Fspy) -> i32 {
    match command {
        Fspy::Record(options) => record(options),
        Fspy::Compare(options) => compare(&options),
        Fspy::Assetcov(options) => assetcov::execute(options),
        Fspy::Latencylab(options) => latencylab::execute(options),
        Fspy::Fbreak(options) => fbreak::execute(options),
        Fspy::Autowatch(options) => autowatch::execute(options),
        Fspy::MinRepro(options) => minrepro::execute(options),
    }
}

#[expect(
    clippy::too_many_lines,
    reason = "Keep trace execution and result publication in one auditable flow"
)]
fn record(options: Record) -> i32 {
    let destination = match output::destination(options.output.as_ref(), options.force) {
        Ok(path) => path,
        Err(message) => return fail(2, "invalid_output", message),
    };
    let root = match options.root {
        Some(path) => path,
        None => match std::env::current_dir() {
            Ok(path) => path,
            Err(_) => {
                return fail(
                    1,
                    "working_directory",
                    "Cannot resolve the current directory.",
                );
            }
        },
    };
    if !root.is_dir() {
        return fail(2, "invalid_root", "--root must name an existing directory.");
    }
    let Some((program, arguments)) = options.command.split_first() else {
        return fail(2, "missing_command", "Provide a command after --.");
    };
    let cancellation = Arc::new(AtomicBool::new(false));
    #[cfg(unix)]
    let termination = Arc::new(AtomicBool::new(false));
    #[cfg(unix)]
    if signal_hook::flag::register(signal_hook::consts::SIGINT, Arc::clone(&cancellation)).is_err()
        || signal_hook::flag::register(signal_hook::consts::SIGTERM, Arc::clone(&cancellation))
            .is_err()
        || signal_hook::flag::register(signal_hook::consts::SIGTERM, Arc::clone(&termination))
            .is_err()
    {
        return fail(
            1,
            "signal_handler",
            "Cannot supervise cancellation signals.",
        );
    }
    tracing::info!(command = "record", stage = "start", "fspy_execution");
    #[cfg(target_os = "linux")]
    let captured = crate::linux::capture(
        crate::linux::CaptureRequest {
            root: &root,
            program: program.as_os_str(),
            arguments,
            child_io: crate::linux::ChildIo::Report,
            timeout: options.timeout,
            kill_after: options.kill_after,
            max_events: options.max_events,
            max_bytes: options.max_trace_bytes,
            delay_rule: None,
            break_control: None,
            child_cwd: None,
            stderr_match: None,
            deny_rule: None,
        },
        &cancellation,
    );
    #[cfg(not(target_os = "linux"))]
    let captured: Result<(), std::io::Error> = Err(std::io::Error::new(
        std::io::ErrorKind::Unsupported,
        "file operation tracing is unavailable on this platform",
    ));
    #[cfg(target_os = "linux")]
    let captured = match captured {
        Ok(captured) => captured,
        Err(_error) => {
            return fail(
                1,
                "tracing_unavailable",
                "Cannot launch a complete traced execution; check ptrace permissions and the \
                 selected command.",
            );
        }
    };
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (captured, destination, program, arguments, cancellation);
        fail(
            1,
            "tracing_unavailable",
            "This platform has no complete file-operation backend yet.",
        )
    }
    #[cfg(target_os = "linux")]
    {
        let mut bytes = Vec::new();
        for event in &captured.events {
            if trace::write_event(&mut bytes, event).is_err() {
                return fail(1, "trace_encoding", "Cannot encode the trace.");
            }
        }
        if output::write(&bytes, destination, options.force).is_err() {
            return fail(
                1,
                "output_failed",
                "Cannot publish the trace; check output permissions and --force.",
            );
        }
        tracing::info!(
            command = "record",
            stage = "finish",
            complete = captured.complete(),
            events = captured.events.len(),
            "fspy_execution"
        );
        #[cfg(unix)]
        if termination.load(std::sync::atomic::Ordering::Relaxed) {
            return 143;
        }
        match captured.failure {
            Some(crate::linux::LinuxTraceError::Timeout) => 124,
            Some(crate::linux::LinuxTraceError::Cancelled) => 130,
            Some(_) => fail(
                1,
                "tracing_incomplete",
                "The trace is incomplete; inspect its terminal classification.",
            ),
            None => captured.root_status.map_or(1, exit_code),
        }
    }
}

#[cfg(target_os = "linux")]
pub(crate) fn exit_code(status: std::process::ExitStatus) -> i32 {
    use std::os::unix::process::ExitStatusExt as _;
    status
        .code()
        .unwrap_or_else(|| status.signal().map_or(1, |signal| 128 + signal))
}

fn compare(options: &Compare) -> i32 {
    let destination =
        match output::destination(options.report.output.as_ref(), options.report.force) {
            Ok(path) => path,
            Err(message) => return fail(2, "invalid_output", message),
        };
    let before = File::open(&options.before)
        .map(BufReader::new)
        .map_err(|_| trace::TraceError::Input)
        .and_then(|reader| {
            trace::read_complete(reader, options.max_events, options.max_trace_bytes)
        });
    let after = File::open(&options.after)
        .map(BufReader::new)
        .map_err(|_| trace::TraceError::Input)
        .and_then(|reader| {
            trace::read_complete(reader, options.max_events, options.max_trace_bytes)
        });
    let (before, after) = match (before, after) {
        (Ok(before), Ok(after)) => (before, after),
        (Err(error), _) | (_, Err(error)) => {
            return fail(
                1,
                &error.to_string(),
                "Cannot compare incomplete or invalid trace input.",
            );
        }
    };
    let comparison = match trace::compare(&before, &after) {
        Ok(comparison) => comparison,
        Err(error) => return fail(1, &error.to_string(), "The trace records are incompatible."),
    };
    if !options.report.quiet {
        let bytes = if options.report.json {
            serde_json::to_vec(&comparison).map(|mut value| {
                value.push(b'\n');
                value
            })
        } else {
            Ok(render_human(&comparison).into_bytes())
        };
        let Ok(bytes) = bytes else {
            return fail(1, "report_encoding", "Cannot encode the comparison.");
        };
        if output::write(&bytes, destination, options.report.force).is_err() {
            return fail(1, "output_failed", "Cannot publish the comparison report.");
        }
    }
    i32::from(options.fail_on_change && comparison.has_structural_change())
}

fn render_human(comparison: &Comparison) -> String {
    let mut text = String::from("File-access comparison (schema 1)\n");
    for (title, entries) in [
        ("Added", &comparison.added),
        ("Removed", &comparison.removed),
        ("Changed operations", &comparison.changed_kinds),
        ("Count or timing", &comparison.count_or_timing),
        ("External accesses", &comparison.external),
    ] {
        let _ = writeln!(text, "{title}: {}", entries.len());
        for entry in entries {
            let _ = writeln!(
                text,
                "  {}: {:?} -> {:?}; count {} -> {}; failures {} -> {}; operation ns {} -> {}",
                render_path(&entry.path),
                entry.before_kinds,
                entry.after_kinds,
                entry.before_count,
                entry.after_count,
                entry.before_failures,
                entry.after_failures,
                entry.before_operation_ns,
                entry.after_operation_ns
            );
        }
    }
    text
}

pub(crate) fn render_path(path: &EncodedPath) -> String {
    let Ok(bytes) = path.decode() else {
        return "<invalid>".to_owned();
    };
    let mut text = String::new();
    for byte in bytes {
        if (0x20..=0x7e).contains(&byte) && byte != b'\\' {
            text.push(char::from(byte));
        } else {
            let _ = write!(text, "\\x{byte:02X}");
        }
    }
    text
}

pub(crate) fn fail(code: i32, classification: &str, guidance: &str) -> i32 {
    tracing::warn!(command = "fspy", classification, "fspy_failed");
    let _ = writeln!(std::io::stderr(), "error: {classification}: {guidance}");
    code
}
