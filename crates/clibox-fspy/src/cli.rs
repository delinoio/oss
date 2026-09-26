//! CLI surface for local file-access workflows.

use std::{
    ffi::OsString,
    fs,
    io::{self, BufReader, Write},
    path::{Path, PathBuf},
    time::Duration,
};

use clap::{Args, Subcommand, ValueEnum};

#[cfg(target_os = "linux")]
use crate::coverage;
use crate::record::{self, CompleteRecord, NativePath, DEFAULT_BYTE_LIMIT, DEFAULT_EVENT_LIMIT};

#[derive(Debug, Subcommand)]
pub enum Command {
    /// Capture paired synchronous file operations as versioned NDJSON.
    #[command(
        after_help = "Example: clibox fspy record --output trace.ndjson -- cargo test\nThe \
                      declared boundary excludes mmap and asynchronous I/O. Records contain \
                      file-access paths."
    )]
    Record(RecordArgs),
    /// Compare two complete, compatible execution records.
    #[command(
        after_help = "Example: clibox fspy compare before.ndjson after.ndjson --fail-on-change"
    )]
    Compare(CompareArgs),
    /// Report selected existing resources actually read by a command.
    #[command(after_help = "Example: clibox fspy assetcov --include 'assets/**' -- cargo test")]
    Assetcov(AssetcovArgs),
    /// Alternate baseline and delayed runs of matching file operations.
    #[command(
        after_help = "Example: clibox fspy latencylab --include 'src/**' --delay 10ms -- cargo \
                      test\nTiming is an observation under current conditions, not a \
                      storage-device prediction."
    )]
    Latencylab(LatencyArgs),
    /// Collect and verify observed project inputs for a failing command.
    #[command(
        after_help = "Example: clibox fspy min-repro --include 'src/**' --bundle-dir repro \
                      --expect-exit 1 --expect-stderr 'failed' -- cargo test\nThe bundle is \
                      verified in a separate cwd; it is not an OS sandbox or a cross-machine \
                      guarantee."
    )]
    MinRepro(MinReproArgs),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum OperationKind {
    Read,
    Write,
    Open,
    Close,
    Metadata,
    Directory,
    Mutation,
    Exec,
}

#[cfg(target_os = "linux")]
impl OperationKind {
    fn matches(self, operation: record::Operation) -> bool {
        use record::Operation;
        match self {
            Self::Read => matches!(operation, Operation::Read | Operation::PositionalRead),
            Self::Write => matches!(operation, Operation::Write | Operation::PositionalWrite),
            Self::Open => operation == Operation::Open,
            Self::Close => operation == Operation::Close,
            Self::Metadata => operation == Operation::Metadata,
            Self::Directory => operation == Operation::Directory,
            Self::Mutation => operation == Operation::Mutation,
            Self::Exec => operation == Operation::Exec,
        }
    }
}

#[derive(Debug, Clone, Args)]
pub struct ExecutionArgs {
    /// Classify paths relative to this existing directory; child cwd is
    /// unchanged.
    #[arg(long)]
    root: Option<PathBuf>,
    /// Maximum events in one execution (default: 1000000).
    #[arg(long, default_value_t = DEFAULT_EVENT_LIMIT, value_parser = parse_positive_usize)]
    max_events: usize,
    /// Maximum encoded trace bytes (default: 268435456).
    #[arg(long, default_value_t = DEFAULT_BYTE_LIMIT, value_parser = parse_positive_u64)]
    max_bytes: u64,
    /// Optional execution deadline, e.g. 500ms, 2s, 1m.
    #[arg(long, value_parser = parse_positive_duration)]
    timeout: Option<Duration>,
    /// Graceful cleanup interval before force termination (default: 5s).
    #[arg(long, default_value = "5s", value_parser = parse_positive_duration)]
    kill_after: Duration,
}

#[derive(Debug, Clone, Args)]
pub struct OutputArgs {
    /// Write to FILE atomically; omit or use - for stdout (./- is a file).
    #[arg(long)]
    output: Option<PathBuf>,
    /// Replace an existing regular output file.
    #[arg(long, requires = "output")]
    force: bool,
}

#[derive(Debug, Args)]
pub struct RecordArgs {
    #[command(flatten)]
    execution: ExecutionArgs,
    #[command(flatten)]
    output: OutputArgs,
    /// Child program and tokenized arguments.
    #[arg(last = true, required = true, num_args = 1..)]
    command: Vec<OsString>,
}

#[derive(Debug, Args)]
pub struct CompareArgs {
    /// Earlier complete record.
    before: PathBuf,
    /// Later complete record.
    after: PathBuf,
    /// Fail on added/removed project files or changed operation kinds.
    #[arg(long)]
    fail_on_change: bool,
    /// Emit machine-readable JSON.
    #[arg(long, conflicts_with = "quiet")]
    json: bool,
    /// Suppress report output (failures still have diagnostics).
    #[arg(long)]
    quiet: bool,
    #[command(flatten)]
    output: OutputArgs,
}

#[derive(Debug, Args)]
pub struct AssetcovArgs {
    #[command(flatten)]
    execution: ExecutionArgs,
    /// Select existing project files; repeat for a union.
    #[arg(long, required = true)]
    include: Vec<String>,
    /// Exclude selected project files; repeat for a union.
    #[arg(long)]
    exclude: Vec<String>,
    /// Fail when actual-read coverage is below 0..100 percent.
    #[arg(long, value_parser = parse_percent)]
    fail_under: Option<f64>,
    /// Emit machine-readable JSON.
    #[arg(long, conflicts_with = "quiet")]
    json: bool,
    /// Suppress report output (failures still have diagnostics).
    #[arg(long)]
    quiet: bool,
    #[command(flatten)]
    output: OutputArgs,
    /// Child program and tokenized arguments.
    #[arg(last = true, required = true, num_args = 1..)]
    command: Vec<OsString>,
}

#[derive(Debug, Args)]
pub struct LatencyArgs {
    #[command(flatten)]
    execution: ExecutionArgs,
    /// Select matching project paths; repeat for a union.
    #[arg(long, required = true)]
    include: Vec<String>,
    /// Exclude matching project paths.
    #[arg(long)]
    exclude: Vec<String>,
    /// Delay before every matching operation.
    #[arg(long, value_parser = parse_positive_duration)]
    delay: Duration,
    /// Match operation kinds; default is read and positional read.
    #[arg(long = "op", value_enum)]
    operations: Vec<OperationKind>,
    /// Number of baseline/delayed pairs (default: 3).
    #[arg(long, default_value_t = 3, value_parser = parse_positive_usize)]
    runs: usize,
    /// Emit machine-readable JSON.
    #[arg(long, conflicts_with = "quiet")]
    json: bool,
    /// Suppress report output (failures still have diagnostics).
    #[arg(long)]
    quiet: bool,
    #[command(flatten)]
    output: OutputArgs,
    /// Child program and tokenized arguments.
    #[arg(last = true, required = true, num_args = 1..)]
    command: Vec<OsString>,
}

#[derive(Debug, Args)]
pub struct MinReproArgs {
    #[command(flatten)]
    execution: ExecutionArgs,
    /// Select project inputs eligible for collection.
    #[arg(long, required = true)]
    include: Vec<String>,
    /// Exclude selected project inputs.
    #[arg(long)]
    exclude: Vec<String>,
    /// New bundle directory; existing paths are never overwritten.
    #[arg(long)]
    bundle_dir: PathBuf,
    /// Required nonzero numeric child exit status.
    #[arg(long, value_parser = parse_nonzero_exit)]
    expect_exit: i64,
    /// Required fixed substring of child stderr.
    #[arg(long, value_parser = parse_nonempty_text)]
    expect_stderr: String,
    /// Maximum snapshot bytes (default: 1 GiB).
    #[arg(long, default_value_t = 1_073_741_824_u64, value_parser = parse_positive_u64)]
    max_snapshot_bytes: u64,
    /// Maximum snapshot files and links (default: 100000).
    #[arg(long, default_value_t = 100_000, value_parser = parse_positive_usize)]
    max_snapshot_files: usize,
    /// Maximum candidate result bytes (default: 1 GiB).
    #[arg(long, default_value_t = 1_073_741_824_u64, value_parser = parse_positive_u64)]
    max_result_bytes: u64,
    /// Maximum candidate result files and links (default: 100000).
    #[arg(long, default_value_t = 100_000, value_parser = parse_positive_usize)]
    max_result_files: usize,
    /// Emit machine-readable JSON.
    #[arg(long, conflicts_with = "quiet")]
    json: bool,
    /// Suppress report output (failures still have diagnostics).
    #[arg(long)]
    quiet: bool,
    #[command(flatten)]
    output: OutputArgs,
    /// Child program and tokenized arguments.
    #[arg(last = true, required = true, num_args = 1..)]
    command: Vec<OsString>,
}

fn parse_positive_usize(value: &str) -> Result<usize, &'static str> {
    value
        .parse()
        .ok()
        .filter(|number| *number > 0)
        .ok_or("Use a positive integer.")
}

fn parse_positive_u64(value: &str) -> Result<u64, &'static str> {
    value
        .parse()
        .ok()
        .filter(|number| *number > 0)
        .ok_or("Use a positive integer.")
}

fn parse_nonzero_exit(value: &str) -> Result<i64, &'static str> {
    value
        .parse()
        .ok()
        .filter(|code| *code != 0)
        .ok_or("Use a nonzero numeric exit code.")
}

fn parse_nonempty_text(value: &str) -> Result<String, &'static str> {
    if value.is_empty() {
        Err("Use a nonempty stderr substring.")
    } else {
        Ok(value.to_owned())
    }
}

fn parse_percent(value: &str) -> Result<f64, &'static str> {
    value
        .parse()
        .ok()
        .filter(|number: &f64| number.is_finite() && (0.0..=100.0).contains(number))
        .ok_or("Use a finite percentage from 0 through 100.")
}

fn parse_positive_duration(value: &str) -> Result<Duration, &'static str> {
    let (digits, multiplier) = [("ms", 1_u64), ("s", 1_000), ("m", 60_000), ("h", 3_600_000)]
        .into_iter()
        .find_map(|(suffix, multiplier)| {
            value
                .strip_suffix(suffix)
                .map(|digits| (digits, multiplier))
        })
        .ok_or("Use a positive integer duration with ms, s, m, or h.")?;
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err("Use a positive integer duration.");
    }
    let millis = digits
        .parse::<u64>()
        .ok()
        .and_then(|number| number.checked_mul(multiplier))
        .filter(|number| *number > 0)
        .ok_or("Duration is zero or too large.")?;
    let duration = Duration::from_millis(millis);
    if std::time::Instant::now().checked_add(duration).is_none() {
        return Err("Duration is too large.");
    }
    Ok(duration)
}

fn diagnostic(classification: &str, action: &'static str) -> i32 {
    tracing::error!(
        command = action,
        classification,
        "file-access workflow failed"
    );
    let _ = writeln!(io::stderr(), "error: fspy {action}: {classification}");
    1
}

fn display_path(path: &NativePath) -> String {
    match path {
        NativePath::UnixBytes(bytes) => bytes
            .iter()
            .flat_map(|byte| std::ascii::escape_default(*byte))
            .map(char::from)
            .collect(),
        NativePath::WindowsUtf16(units) => char::decode_utf16(units.iter().copied())
            .map(|character| match character {
                Ok(character) if !character.is_control() => character.to_string(),
                Ok(character) => character.escape_default().to_string(),
                Err(error) => format!("\\u{:04x}", error.unpaired_surrogate()),
            })
            .collect(),
    }
}

fn display_key(platform: record::Platform, key: &record::ProjectKey) -> String {
    let native = match platform {
        record::Platform::Linux | record::Platform::Macos => NativePath::UnixBytes(key.0.clone()),
        record::Platform::Windows => NativePath::WindowsUtf16(
            key.0
                .chunks_exact(2)
                .map(|pair| u16::from_le_bytes([pair[0], pair[1]]))
                .collect(),
        ),
    };
    display_path(&native)
}

fn destination(output: &OutputArgs) -> Result<Option<&Path>, &'static str> {
    let Some(path) = output.output.as_deref() else {
        return Ok(None);
    };
    if path.as_os_str() == "-" {
        if output.force {
            return Err("invalid_force_stdout");
        }
        return Ok(None);
    }
    Ok(Some(path))
}

fn publish(output: &OutputArgs, bytes: &[u8]) -> Result<(), &'static str> {
    let Some(path) = destination(output)? else {
        return io::stdout().write_all(bytes).map_err(|_| "output_write");
    };
    if let Ok(metadata) = fs::symlink_metadata(path) {
        if !output.force {
            return Err("output_exists");
        }
        if !metadata.file_type().is_file() {
            return Err("output_not_regular");
        }
    }
    let parent = path
        .parent()
        .filter(|parent| !parent.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let mut temporary = tempfile::Builder::new()
        .prefix(".clibox-fspy-")
        .tempfile_in(parent)
        .map_err(|_| "output_prepare")?;
    temporary.write_all(bytes).map_err(|_| "output_write")?;
    temporary.as_file().sync_all().map_err(|_| "output_sync")?;
    if let Ok(metadata) = fs::symlink_metadata(path) {
        if !output.force || !metadata.file_type().is_file() {
            return Err("output_exists");
        }
        temporary
            .as_file()
            .set_permissions(metadata.permissions())
            .map_err(|_| "output_permissions")?;
    }
    if output.force {
        temporary.persist(path).map_err(|_| "output_publish")?;
    } else {
        temporary
            .persist_noclobber(path)
            .map_err(|_| "output_publish")?;
    }
    Ok(())
}

fn load(path: &Path) -> Result<CompleteRecord, record::ParseFailure> {
    let file = fs::File::open(path).map_err(|_| record::ParseFailure::Io)?;
    record::parse(
        BufReader::new(file),
        DEFAULT_EVENT_LIMIT,
        DEFAULT_BYTE_LIMIT,
    )
}

fn aggregate_details(
    platform: record::Platform,
    before: &std::collections::BTreeMap<record::ProjectKey, record::PathAggregate>,
    after: &std::collections::BTreeMap<record::ProjectKey, record::PathAggregate>,
) -> Vec<serde_json::Value> {
    let keys = before
        .keys()
        .chain(after.keys())
        .collect::<std::collections::BTreeSet<_>>();
    keys.into_iter()
        .map(|key| {
            let describe = |aggregate: Option<&record::PathAggregate>| {
                aggregate.map(|value| {
                    serde_json::json!({
                        "operations": value.operations,
                        "count": value.count,
                        "failures": value.failures,
                        "total_duration_ns": value.total_duration_ns,
                    })
                })
            };
            serde_json::json!({
                "path": display_key(platform, key),
                "before": describe(before.get(key)),
                "after": describe(after.get(key)),
            })
        })
        .collect()
}

fn compare(args: CompareArgs) -> i32 {
    let before = match load(&args.before) {
        Ok(record) => record,
        Err(error) => return diagnostic(&error.to_string(), "compare"),
    };
    let after = match load(&args.after) {
        Ok(record) => record,
        Err(error) => return diagnostic(&error.to_string(), "compare"),
    };
    let difference = match record::compare(&before, &after) {
        Ok(difference) => difference,
        Err(_) => return diagnostic("incompatible_record", "compare"),
    };
    if !args.quiet {
        let report = if args.json {
            let path_list = |keys: &[record::ProjectKey]| {
                keys.iter()
                    .map(|key| display_key(before.header.platform, key))
                    .collect::<Vec<_>>()
            };
            let result = serde_json::json!({
                "added": path_list(&difference.added),
                "removed": path_list(&difference.removed),
                "changed_operations": path_list(&difference.changed_operations),
                "changed_counts": path_list(&difference.changed_counts),
                "changed_failures": path_list(&difference.changed_failures),
                "changed_timing": path_list(&difference.changed_timing),
                "external_added": path_list(&difference.external_added),
                "external_removed": path_list(&difference.external_removed),
                "external_changed_operations": path_list(&difference.external_changed_operations),
                "external_changed_counts": path_list(&difference.external_changed_counts),
                "external_changed_failures": path_list(&difference.external_changed_failures),
                "external_changed_timing": path_list(&difference.external_changed_timing),
                "project_details": aggregate_details(before.header.platform, &record::aggregate_project(&before), &record::aggregate_project(&after)),
                "external_details": aggregate_details(before.header.platform, &record::aggregate_external(&before), &record::aggregate_external(&after)),
            });
            serde_json::to_vec_pretty(&result).unwrap_or_default()
        } else {
            let mut report = String::new();
            for (label, keys) in [
                ("Added project files", &difference.added),
                ("Removed project files", &difference.removed),
                ("Changed operation kinds", &difference.changed_operations),
                ("Changed operation counts", &difference.changed_counts),
                ("Changed failures", &difference.changed_failures),
                ("Changed operation timings", &difference.changed_timing),
                ("Added external accesses", &difference.external_added),
                ("Removed external accesses", &difference.external_removed),
                (
                    "Changed external operation kinds",
                    &difference.external_changed_operations,
                ),
                (
                    "Changed external counts",
                    &difference.external_changed_counts,
                ),
                (
                    "Changed external failures",
                    &difference.external_changed_failures,
                ),
                (
                    "Changed external timings",
                    &difference.external_changed_timing,
                ),
            ] {
                report.push_str(label);
                report.push_str(":\n");
                for key in keys {
                    report.push_str("  ");
                    report.push_str(&display_key(before.header.platform, key));
                    report.push('\n');
                }
            }
            for (label, old, new) in [
                (
                    "Project",
                    record::aggregate_project(&before),
                    record::aggregate_project(&after),
                ),
                (
                    "External",
                    record::aggregate_external(&before),
                    record::aggregate_external(&after),
                ),
            ] {
                report.push_str(label);
                report.push_str(" operation totals:\n");
                for value in aggregate_details(before.header.platform, &old, &new) {
                    report.push_str("  ");
                    report.push_str(value["path"].as_str().unwrap_or("?"));
                    report.push_str(": before ");
                    report.push_str(&value["before"].to_string());
                    report.push_str(", after ");
                    report.push_str(&value["after"].to_string());
                    report.push('\n');
                }
            }
            report.into_bytes()
        };
        let mut report = report;
        if !report.ends_with(b"\n") {
            report.push(b'\n');
        }
        if let Err(error) = publish(&args.output, &report) {
            return diagnostic(error, "compare");
        }
    }
    if args.fail_on_change && difference.gated_change() {
        1
    } else {
        0
    }
}

#[cfg(target_os = "linux")]
fn execution_root(args: &ExecutionArgs) -> Result<PathBuf, &'static str> {
    let root = match &args.root {
        Some(root) => root.clone(),
        None => std::env::current_dir().map_err(|_| "working_directory")?,
    };
    let root = fs::canonicalize(root).map_err(|_| "root_unavailable")?;
    if !root.is_dir() {
        return Err("root_not_directory");
    }
    Ok(root)
}

#[cfg(target_os = "linux")]
struct SignalHandlers {
    cancelled: std::sync::Arc<std::sync::atomic::AtomicBool>,
    signal: std::sync::Arc<std::sync::atomic::AtomicUsize>,
    registrations: Vec<signal_hook::SigId>,
}

#[cfg(target_os = "linux")]
impl SignalHandlers {
    fn new() -> Result<Self, crate::linux::TraceFailure> {
        use std::sync::{
            atomic::{AtomicBool, AtomicUsize},
            Arc,
        };

        let mut handlers = Self {
            cancelled: Arc::new(AtomicBool::new(false)),
            signal: Arc::new(AtomicUsize::new(0)),
            registrations: Vec::new(),
        };
        for signal in [signal_hook::consts::SIGINT, signal_hook::consts::SIGTERM] {
            handlers.registrations.push(
                signal_hook::flag::register(signal, handlers.cancelled.clone())
                    .map_err(|_| crate::linux::TraceFailure::Spawn)?,
            );
            handlers.registrations.push(
                signal_hook::flag::register_usize(signal, handlers.signal.clone(), signal as usize)
                    .map_err(|_| crate::linux::TraceFailure::Spawn)?,
            );
        }
        Ok(handlers)
    }
}

#[cfg(target_os = "linux")]
impl Drop for SignalHandlers {
    fn drop(&mut self) {
        for registration in self.registrations.drain(..) {
            signal_hook::low_level::unregister(registration);
        }
    }
}

#[cfg(target_os = "linux")]
struct CaptureFailure {
    error: crate::linux::TraceFailure,
    signal: usize,
}

#[cfg(target_os = "linux")]
fn execute_capture(
    command: &[OsString],
    root: &Path,
    args: &ExecutionArgs,
) -> Result<CompleteRecord, CaptureFailure> {
    execute_capture_with(command, root, args, |_| Duration::ZERO)
}

#[cfg(target_os = "linux")]
fn execute_capture_with<F>(
    command: &[OsString],
    root: &Path,
    args: &ExecutionArgs,
    delay_for: F,
) -> Result<CompleteRecord, CaptureFailure>
where
    F: FnMut(&crate::linux::paths::DecodedOperation) -> Duration,
{
    use std::{
        os::fd::AsFd,
        process::{Command as ProcessCommand, Stdio},
        sync::atomic::Ordering,
    };

    if command.is_empty() {
        return Err(CaptureFailure {
            error: crate::linux::TraceFailure::Spawn,
            signal: 0,
        });
    }
    let mut child = ProcessCommand::new(&command[0]);
    child.args(&command[1..]);
    let stderr = io::stderr()
        .as_fd()
        .try_clone_to_owned()
        .map_err(|_| CaptureFailure {
            error: crate::linux::TraceFailure::Spawn,
            signal: 0,
        })?;
    child.stdout(Stdio::from(stderr)).stderr(Stdio::inherit());
    let signals = SignalHandlers::new().map_err(|error| CaptureFailure { error, signal: 0 })?;
    let result = crate::linux::capture::capture(
        &mut child,
        root,
        crate::linux::Limits {
            max_events: args.max_events,
            max_bytes: args.max_bytes,
            timeout: args.timeout,
            kill_after: args.kill_after,
        },
        &signals.cancelled,
        delay_for,
    );
    result.map_err(|error| CaptureFailure {
        error,
        signal: signals.signal.load(Ordering::SeqCst),
    })
}

#[cfg(target_os = "linux")]
fn execute_repro_capture(
    command: &[OsString],
    root: &Path,
    cwd: Option<&Path>,
    args: &ExecutionArgs,
    expected_stderr: &str,
) -> Result<(CompleteRecord, bool), CaptureFailure> {
    use std::{
        os::{
            fd::{AsFd, OwnedFd},
            unix::net::UnixStream,
        },
        process::{Command as ProcessCommand, Stdio},
        sync::{
            atomic::{AtomicBool, Ordering},
            Arc,
        },
        thread,
    };

    let mut child = ProcessCommand::new(&command[0]);
    child.args(&command[1..]);
    if let Some(cwd) = cwd {
        child.current_dir(cwd);
    }
    let stdout_stderr = io::stderr()
        .as_fd()
        .try_clone_to_owned()
        .map_err(|_| CaptureFailure {
            error: crate::linux::TraceFailure::Spawn,
            signal: 0,
        })?;
    child.stdout(Stdio::from(stdout_stderr));
    let (mut reader, writer) = UnixStream::pair().map_err(|_| CaptureFailure {
        error: crate::linux::TraceFailure::Spawn,
        signal: 0,
    })?;
    reader
        .set_read_timeout(Some(Duration::from_millis(100)))
        .map_err(|_| CaptureFailure {
            error: crate::linux::TraceFailure::Spawn,
            signal: 0,
        })?;
    child.stderr(Stdio::from(OwnedFd::from(writer)));
    let signals = SignalHandlers::new().map_err(|error| CaptureFailure { error, signal: 0 })?;
    let finished = Arc::new(AtomicBool::new(false));
    let reader_finished = finished.clone();
    let needle = expected_stderr.as_bytes().to_vec();
    let forwarder = thread::Builder::new()
        .name("clibox-fspy-stderr".into())
        .spawn(move || {
            let mut matched = false;
            let mut tail = Vec::new();
            let mut buffer = [0_u8; 8192];
            loop {
                match std::io::Read::read(&mut reader, &mut buffer) {
                    Ok(0) => return Ok(matched),
                    Ok(count) => {
                        io::stderr()
                            .write_all(&buffer[..count])
                            .map_err(|_| "output_forward")?;
                        let mut combined = tail;
                        combined.extend_from_slice(&buffer[..count]);
                        matched |= combined
                            .windows(needle.len())
                            .any(|window| window == needle);
                        let keep = needle.len().saturating_sub(1).min(combined.len());
                        tail = combined[combined.len() - keep..].to_vec();
                    }
                    Err(error)
                        if matches!(
                            error.kind(),
                            io::ErrorKind::WouldBlock | io::ErrorKind::TimedOut
                        ) =>
                    {
                        if reader_finished.load(Ordering::SeqCst) {
                            return Err("output_incomplete");
                        }
                    }
                    Err(_) => return Err("output_forward"),
                }
            }
        })
        .map_err(|_| CaptureFailure {
            error: crate::linux::TraceFailure::Spawn,
            signal: 0,
        })?;
    let result = crate::linux::capture::capture(
        &mut child,
        root,
        crate::linux::Limits {
            max_events: args.max_events,
            max_bytes: args.max_bytes,
            timeout: args.timeout,
            kill_after: args.kill_after,
        },
        &signals.cancelled,
        |_| Duration::ZERO,
    );
    // Command keeps its configured descriptor after spawn. Closing it lets
    // the forwarding thread observe EOF once all owned tracees have exited.
    child.stderr(Stdio::null());
    finished.store(true, Ordering::SeqCst);
    let forwarded = forwarder.join().map_err(|_| CaptureFailure {
        error: crate::linux::TraceFailure::Supervision("output_thread"),
        signal: 0,
    })?;
    let matched = forwarded.map_err(|_| CaptureFailure {
        error: crate::linux::TraceFailure::Supervision("output_forward"),
        signal: 0,
    })?;
    let record = result.map_err(|error| CaptureFailure {
        error,
        signal: signals.signal.load(Ordering::SeqCst),
    })?;
    Ok((record, matched))
}

#[cfg(target_os = "linux")]
fn capture_status(failure: CaptureFailure, action: &'static str) -> i32 {
    match failure.error {
        crate::linux::TraceFailure::Timeout => {
            diagnostic("timeout", action);
            124
        }
        crate::linux::TraceFailure::Cancellation => {
            diagnostic("cancellation", action);
            if failure.signal == signal_hook::consts::SIGTERM as usize {
                143
            } else {
                130
            }
        }
        other => diagnostic(
            match other {
                crate::linux::TraceFailure::Permission => "trace_permission",
                crate::linux::TraceFailure::UnsupportedKernel => "unsupported_trace_kernel",
                crate::linux::TraceFailure::EventLimit => "event_limit",
                crate::linux::TraceFailure::ByteLimit => "byte_limit",
                crate::linux::TraceFailure::Cleanup => "cleanup_failure",
                crate::linux::TraceFailure::Spawn => "spawn_failure",
                crate::linux::TraceFailure::Supervision(_) => "trace_supervision",
                _ => "trace_failure",
            },
            action,
        ),
    }
}

#[cfg(target_os = "linux")]
fn child_status(record: &CompleteRecord) -> i32 {
    if let Some(signal) = record.summary.child_signal {
        return 128_i32.saturating_add(signal);
    }
    record
        .summary
        .child_exit_code
        .and_then(|code| i32::try_from(code).ok())
        .unwrap_or(1)
}

#[cfg(target_os = "linux")]
fn record(args: RecordArgs) -> i32 {
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "record"),
    };
    let record = match execute_capture(&args.command, &root, &args.execution) {
        Ok(record) => record,
        Err(error) => return capture_status(error, "record"),
    };
    let mut encoded = Vec::new();
    if let Err(error) = record::serialize(
        &record,
        &mut encoded,
        args.execution.max_events,
        args.execution.max_bytes,
    ) {
        return diagnostic(&error.to_string(), "record");
    }
    if let Err(error) = publish(&args.output, &encoded) {
        return diagnostic(error, "record");
    }
    child_status(&record)
}

#[cfg(target_os = "linux")]
fn assetcov(args: AssetcovArgs) -> i32 {
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "assetcov"),
    };
    let denominator = match coverage::select_existing(&root, &args.include, &args.exclude) {
        Ok(value) => value,
        Err(error) => return diagnostic(&error.to_string(), "assetcov"),
    };
    let record = match execute_capture(&args.command, &root, &args.execution) {
        Ok(record) => record,
        Err(error) => return capture_status(error, "assetcov"),
    };
    let report = match coverage::analyze(&denominator, &record) {
        Ok(report) => report,
        Err(error) => return diagnostic(&error.to_string(), "assetcov"),
    };
    if !args.quiet {
        let mut encoded = if args.json {
            serde_json::to_vec_pretty(&serde_json::json!({
                "covered": report.covered.iter().map(|file| &file.logical).collect::<Vec<_>>(),
                "uncovered": report.uncovered.iter().map(|file| &file.logical).collect::<Vec<_>>(),
                "covered_count": report.covered.len(),
                "total_count": denominator.files.len(),
                "percentage": report.percentage,
                "child_exit_code": record.summary.child_exit_code,
                "child_signal": record.summary.child_signal,
            }))
            .unwrap_or_default()
        } else {
            let mut text = format!(
                "Covered: {}/{} ({:.2}%)\n",
                report.covered.len(),
                denominator.files.len(),
                report.percentage
            );
            text.push_str("Covered files:\n");
            for file in &report.covered {
                text.push_str("  ");
                text.push_str(&display_path(&file.logical));
                text.push('\n');
            }
            text.push_str("Uncovered files:\n");
            for file in &report.uncovered {
                text.push_str("  ");
                text.push_str(&display_path(&file.logical));
                text.push('\n');
            }
            text.into_bytes()
        };
        if !encoded.ends_with(b"\n") {
            encoded.push(b'\n');
        }
        if let Err(error) = publish(&args.output, &encoded) {
            return diagnostic(error, "assetcov");
        }
    }
    let status = child_status(&record);
    if status != 0 {
        return status;
    }
    if args
        .fail_under
        .is_some_and(|threshold| report.fails_threshold(threshold))
    {
        1
    } else {
        0
    }
}

#[cfg(target_os = "linux")]
fn matches_selected(
    operation: record::Operation,
    paths: &[record::AccessPath],
    selector: &coverage::Selector,
    kinds: &[OperationKind],
) -> bool {
    let operation_matches = if kinds.is_empty() {
        OperationKind::Read.matches(operation)
    } else {
        kinds.iter().any(|kind| kind.matches(operation))
    };
    operation_matches
        && paths.iter().any(|path| {
            path.project_relative
                .as_ref()
                .is_some_and(|relative| selector.matches(relative))
        })
}

#[cfg(target_os = "linux")]
fn median(values: &[u128]) -> u128 {
    let mut sorted = values.to_vec();
    sorted.sort_unstable();
    let middle = sorted.len() / 2;
    if sorted.len().is_multiple_of(2) {
        (sorted[middle - 1] + sorted[middle]) / 2
    } else {
        sorted[middle]
    }
}

#[cfg(target_os = "linux")]
fn latencylab(args: LatencyArgs) -> i32 {
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "latencylab"),
    };
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => selector,
        Err(error) => return diagnostic(&error.to_string(), "latencylab"),
    };
    let mut runs = Vec::with_capacity(args.runs.saturating_mul(2));
    let mut baseline = Vec::with_capacity(args.runs);
    let mut delayed = Vec::with_capacity(args.runs);
    let mut any_match = false;
    for pair in 0..args.runs {
        for injected in [false, true] {
            let delay = if injected { args.delay } else { Duration::ZERO };
            let began = std::time::Instant::now();
            let record =
                match execute_capture_with(&args.command, &root, &args.execution, |operation| {
                    if matches_selected(
                        operation.operation,
                        &operation.paths,
                        &selector,
                        &args.operations,
                    ) {
                        delay
                    } else {
                        Duration::ZERO
                    }
                }) {
                    Ok(record) => record,
                    Err(error) => return capture_status(error, "latencylab"),
                };
            let execution_ns = began.elapsed().as_nanos();
            if child_status(&record) != 0 {
                return diagnostic("child_failure", "latencylab");
            }
            let matching = record
                .operations
                .iter()
                .filter(|pair| {
                    matches_selected(
                        pair.start.operation,
                        &pair.start.paths,
                        &selector,
                        &args.operations,
                    )
                })
                .collect::<Vec<_>>();
            any_match |= !matching.is_empty();
            let operation_ns = matching
                .iter()
                .map(|pair| u128::from(pair.completion.monotonic_ns - pair.start.monotonic_ns))
                .sum::<u128>();
            let observed_delay_ns = matching
                .iter()
                .map(|pair| u128::from(pair.completion.observed_delay_ns))
                .sum::<u128>();
            let requested_delay_ns = matching.len() as u128 * delay.as_nanos();
            runs.push(serde_json::json!({
                "pair": pair + 1,
                "condition": if injected { "delayed" } else { "baseline" },
                "matching_operations": matching.len(),
                "requested_delay_ns": requested_delay_ns,
                "observed_delay_ns": observed_delay_ns,
                "operation_ns": operation_ns,
                "execution_ns": execution_ns,
            }));
            if injected {
                delayed.push(execution_ns);
            } else {
                baseline.push(execution_ns);
            }
        }
    }
    if !any_match {
        return diagnostic("no_matching_operations", "latencylab");
    }
    let baseline_median_ns = median(&baseline);
    let delayed_median_ns = median(&delayed);
    let slowdown_ratio = delayed_median_ns as f64 / baseline_median_ns.max(1) as f64;
    if !args.quiet {
        let mut report = if args.json {
            serde_json::to_vec_pretty(&serde_json::json!({
                "runs": runs,
                "baseline_median_ns": baseline_median_ns,
                "delayed_median_ns": delayed_median_ns,
                "slowdown_ratio": slowdown_ratio,
                "requested_delay_ns": args.delay.as_nanos(),
            }))
            .unwrap_or_default()
        } else {
            let mut text = format!(
                "Requested per-operation delay: {} ns\nBaseline median: {baseline_median_ns} \
                 ns\nDelayed median: {delayed_median_ns} ns\nSlowdown: \
                 {slowdown_ratio:.3}x\nRuns:\n",
                args.delay.as_nanos()
            );
            for run in &runs {
                text.push_str(&format!(
                    "  pair {} {}: matches={}, requested={} ns, observed={} ns, operation={} ns, \
                     execution={} ns\n",
                    run["pair"],
                    run["condition"].as_str().unwrap_or("?"),
                    run["matching_operations"],
                    run["requested_delay_ns"],
                    run["observed_delay_ns"],
                    run["operation_ns"],
                    run["execution_ns"]
                ));
            }
            text.into_bytes()
        };
        if !report.ends_with(b"\n") {
            report.push(b'\n');
        }
        if let Err(error) = publish(&args.output, &report) {
            return diagnostic(error, "latencylab");
        }
    }
    0
}

#[cfg(target_os = "linux")]
fn logical_relative(root: &Path, path: &record::AccessPath) -> Option<PathBuf> {
    use std::os::unix::ffi::OsStrExt;
    let NativePath::UnixBytes(bytes) = &path.logical else {
        return None;
    };
    let absolute = Path::new(std::ffi::OsStr::from_bytes(bytes));
    let relative = absolute.strip_prefix(root).ok()?;
    if relative.as_os_str().is_empty()
        || !relative
            .components()
            .all(|component| matches!(component, std::path::Component::Normal(_)))
    {
        return None;
    }
    Some(relative.to_path_buf())
}

#[cfg(target_os = "linux")]
fn unix_native(path: &Path) -> NativePath {
    use std::os::unix::ffi::OsStrExt;
    NativePath::UnixBytes(path.as_os_str().as_bytes().to_vec())
}

#[cfg(target_os = "linux")]
fn collect_required(
    record: &CompleteRecord,
    root: &Path,
    selector: &coverage::Selector,
    snapshot: &crate::repro::Snapshot,
) -> Result<std::collections::BTreeSet<PathBuf>, crate::repro::ReproFailure> {
    use crate::repro::ReproFailure;
    let mut required = std::collections::BTreeSet::new();
    for pair in &record.operations {
        let input_operation = matches!(
            pair.start.operation,
            record::Operation::Read
                | record::Operation::PositionalRead
                | record::Operation::Open
                | record::Operation::Metadata
                | record::Operation::Directory
                | record::Operation::Exec
        );
        if !input_operation {
            continue;
        }
        for path in &pair.start.paths {
            if path.class != record::PathClass::Project {
                continue;
            }
            let alias = path
                .identity
                .and_then(|identity| snapshot.selected_alias_for_identity(identity))
                .map(Path::to_path_buf)
                .or_else(|| logical_relative(root, path));
            let Some(alias) = alias else {
                if pair.start.operation.is_content_read() && pair.completion.native_error.is_none()
                {
                    return Err(ReproFailure::UncollectedInput);
                }
                continue;
            };
            if crate::repro::Snapshot::is_blocked(&alias) {
                return Err(ReproFailure::BlockedInput);
            }
            let selected = selector.matches(&unix_native(&alias));
            if !selected || !snapshot.contains_selected(&alias) {
                if pair.start.operation.is_content_read() && pair.completion.native_error.is_none()
                {
                    return Err(ReproFailure::UncollectedInput);
                }
                continue;
            }
            required.insert(alias);
        }
    }
    snapshot.verify_required(&required)?;
    Ok(required)
}

#[cfg(target_os = "linux")]
fn tree_limits(root: &Path, max_bytes: u64, max_files: usize) -> Result<(), &'static str> {
    let mut bytes = 0_u64;
    let mut files = 0_usize;
    for entry in walkdir::WalkDir::new(root)
        .follow_links(false)
        .into_iter()
        .skip(1)
    {
        let entry = entry.map_err(|_| "result_unavailable")?;
        if entry.file_type().is_file() || entry.path_is_symlink() {
            files = files.checked_add(1).ok_or("result_file_limit")?;
            if files > max_files {
                return Err("result_file_limit");
            }
        }
        if entry.path_is_symlink() {
            let resolved = fs::canonicalize(entry.path()).map_err(|_| "result_external_link")?;
            if !resolved.starts_with(root) {
                return Err("result_external_link");
            }
        }
        if entry.file_type().is_file() {
            bytes = bytes
                .checked_add(entry.metadata().map_err(|_| "result_unavailable")?.len())
                .ok_or("result_byte_limit")?;
            if bytes > max_bytes {
                return Err("result_byte_limit");
            }
        }
    }
    Ok(())
}

#[cfg(target_os = "linux")]
fn publish_new_directory(temporary: &Path, destination: &Path) -> Result<(), &'static str> {
    use std::{ffi::CString, os::unix::ffi::OsStrExt};
    let source = CString::new(temporary.as_os_str().as_bytes()).map_err(|_| "bundle_path")?;
    let destination =
        CString::new(destination.as_os_str().as_bytes()).map_err(|_| "bundle_path")?;
    // SAFETY: both paths are NUL-terminated, the temp directory is owned by
    // this process, and RENAME_NOREPLACE prevents a concurrent replacement.
    let result = unsafe {
        libc::syscall(
            libc::SYS_renameat2,
            libc::AT_FDCWD,
            source.as_ptr(),
            libc::AT_FDCWD,
            destination.as_ptr(),
            libc::RENAME_NOREPLACE,
        )
    };
    if result == 0 {
        Ok(())
    } else {
        Err("bundle_publish")
    }
}

#[cfg(target_os = "linux")]
fn min_repro(args: MinReproArgs) -> i32 {
    use std::collections::BTreeSet;
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "min-repro"),
    };
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => selector,
        Err(error) => return diagnostic(&error.to_string(), "min-repro"),
    };
    if fs::symlink_metadata(&args.bundle_dir).is_ok() {
        return diagnostic("bundle_exists", "min-repro");
    }
    let parent = args
        .bundle_dir
        .parent()
        .filter(|path| !path.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let parent = match fs::canonicalize(parent) {
        Ok(path) if path.is_dir() => path,
        _ => return diagnostic("bundle_parent", "min-repro"),
    };
    let bundle_name = match args.bundle_dir.file_name() {
        Some(name) if name != "." && name != ".." => name,
        _ => return diagnostic("bundle_path", "min-repro"),
    };
    let bundle_path = parent.join(bundle_name);
    if fs::symlink_metadata(&bundle_path).is_ok() {
        return diagnostic("bundle_exists", "min-repro");
    }
    let snapshot = match crate::repro::Snapshot::take(
        &root,
        &selector,
        args.max_snapshot_bytes,
        args.max_snapshot_files,
    ) {
        Ok(snapshot) => snapshot,
        Err(error) => return diagnostic(&error.to_string(), "min-repro"),
    };
    let (original, original_matches) = match execute_repro_capture(
        &args.command,
        &root,
        None,
        &args.execution,
        &args.expect_stderr,
    ) {
        Ok(result) => result,
        Err(error) => return capture_status(error, "min-repro"),
    };
    if original.summary.child_exit_code != Some(args.expect_exit) || !original_matches {
        return diagnostic("original_mismatch", "min-repro");
    }
    let required = match collect_required(&original, &root, &selector, &snapshot) {
        Ok(required) => required,
        Err(error) => return diagnostic(&error.to_string(), "min-repro"),
    };
    let candidate = match tempfile::Builder::new()
        .prefix(".clibox-fspy-candidate-")
        .tempdir_in(&parent)
    {
        Ok(candidate) => candidate,
        Err(_) => return diagnostic("candidate_prepare", "min-repro"),
    };
    let staged = match snapshot.stage_required(&required, candidate.path()) {
        Ok(staged) => staged,
        Err(error) => return diagnostic(&error.to_string(), "min-repro"),
    };
    let staged_paths = staged
        .iter()
        .map(|file| file.relative.clone())
        .collect::<BTreeSet<_>>();
    let (rerun, rerun_matches) = match execute_repro_capture(
        &args.command,
        candidate.path(),
        Some(candidate.path()),
        &args.execution,
        &args.expect_stderr,
    ) {
        Ok(result) => result,
        Err(error) => return capture_status(error, "min-repro"),
    };
    if rerun.summary.child_exit_code != Some(args.expect_exit) || !rerun_matches {
        return diagnostic("reproduction_mismatch", "min-repro");
    }
    for pair in &rerun.operations {
        if !matches!(
            pair.start.operation,
            record::Operation::Open
                | record::Operation::Read
                | record::Operation::PositionalRead
                | record::Operation::Metadata
                | record::Operation::Directory
                | record::Operation::Exec
        ) {
            continue;
        }
        for path in &pair.start.paths {
            if path.class == record::PathClass::Project
                && pair.start.operation.is_content_read()
                && pair.completion.native_error.is_none()
            {
                let Some(relative) = path.project_relative.as_ref().and_then(|path| {
                    use std::os::unix::ffi::OsStringExt;
                    match path {
                        NativePath::UnixBytes(bytes) => {
                            Some(PathBuf::from(OsString::from_vec(bytes.clone())))
                        }
                        _ => None,
                    }
                }) else {
                    return diagnostic("reproduction_uncollected_input", "min-repro");
                };
                if !staged_paths.contains(&relative) {
                    let generated = rerun.operations.iter().any(|earlier| {
                        earlier.start.monotonic_ns < pair.start.monotonic_ns
                            && matches!(
                                earlier.start.operation,
                                record::Operation::Write | record::Operation::PositionalWrite
                            )
                            && earlier.completion.native_error.is_none()
                            && earlier.start.paths.iter().any(|write_path| {
                                write_path.project_relative.as_ref()
                                    == path.project_relative.as_ref()
                            })
                    });
                    if !generated {
                        return diagnostic("reproduction_uncollected_input", "min-repro");
                    }
                }
            } else if path.class == record::PathClass::External {
                let original_access = [
                    &path.logical,
                    path.resolved.as_ref().unwrap_or(&path.logical),
                ]
                .into_iter()
                .any(|native| {
                    use std::os::unix::ffi::OsStrExt;
                    match native {
                        NativePath::UnixBytes(bytes) => {
                            let path = Path::new(std::ffi::OsStr::from_bytes(bytes));
                            path.starts_with(&root) && !path.starts_with(candidate.path())
                        }
                        _ => false,
                    }
                });
                if original_access {
                    return diagnostic("reproduction_original_dependency", "min-repro");
                }
            }
        }
    }
    if let Err(error) = tree_limits(
        candidate.path(),
        args.max_result_bytes,
        args.max_result_files,
    ) {
        return diagnostic(error, "min-repro");
    }
    let bundle = match tempfile::Builder::new()
        .prefix(".clibox-fspy-bundle-")
        .tempdir_in(&parent)
    {
        Ok(bundle) => bundle,
        Err(_) => return diagnostic("bundle_prepare", "min-repro"),
    };
    let staged = match snapshot.stage_required(&required, bundle.path()) {
        Ok(staged) => staged,
        Err(error) => return diagnostic(&error.to_string(), "min-repro"),
    };
    let metadata_dir = bundle.path().join(".clibox-fspy-repro");
    if fs::symlink_metadata(&metadata_dir).is_ok() {
        return diagnostic("bundle_reserved_path", "min-repro");
    }
    if fs::create_dir(&metadata_dir).is_err() {
        return diagnostic("bundle_prepare", "min-repro");
    }
    let external = original
        .operations
        .iter()
        .filter(|pair| {
            matches!(
                pair.start.operation,
                record::Operation::Open
                    | record::Operation::Read
                    | record::Operation::PositionalRead
                    | record::Operation::Metadata
                    | record::Operation::Directory
                    | record::Operation::Exec
            )
        })
        .flat_map(|pair| pair.start.paths.iter())
        .filter(|path| path.class == record::PathClass::External)
        .filter_map(|path| match &path.logical {
            NativePath::UnixBytes(bytes) => Some(bytes.clone()),
            _ => None,
        })
        .collect::<BTreeSet<_>>()
        .into_iter()
        .map(NativePath::UnixBytes)
        .collect::<Vec<_>>();
    let staged_links = snapshot
        .links()
        .iter()
        .filter(|(path, _)| bundle.path().join(path).is_symlink())
        .collect::<Vec<_>>();
    let manifest = serde_json::json!({
        "schema_version": 1,
        "files": staged.iter().map(|file| serde_json::json!({
            "path": unix_native(&file.relative),
            "sha256": file.sha256,
            "size": file.size,
        })).collect::<Vec<_>>(),
        "internal_links": staged_links.iter().map(|(path, target)| serde_json::json!({
            "path": unix_native(path),
            "target": unix_native(target),
        })).collect::<Vec<_>>(),
        "external_dependencies": external,
    });
    if fs::write(metadata_dir.join("manifest.json"), serde_json::to_vec_pretty(&manifest).unwrap_or_default()).is_err()
        || fs::write(metadata_dir.join("README.md"), b"Run the original command from this bundle directory and check its expected exit status and stderr substring. The command and environment were intentionally not saved. External runtime and system dependencies are listed in manifest.json and were not bundled. This reproduction is verified only on the originating machine under the current environment. Delete the bundle directory manually when finished.\n").is_err()
    { return diagnostic("bundle_write", "min-repro"); }
    if let Err(error) = tree_limits(bundle.path(), args.max_result_bytes, args.max_result_files) {
        return diagnostic(error, "min-repro");
    }
    if let Err(error) = publish_new_directory(bundle.path(), &bundle_path) {
        return diagnostic(error, "min-repro");
    }
    if !args.quiet {
        let mut report = if args.json {
            serde_json::to_vec_pretty(&serde_json::json!({
                "verified": true,
                "bundle_dir": bundle_path,
                "collected_files": staged.len(),
                "external_accesses": external.len(),
            }))
            .unwrap_or_default()
        } else {
            format!(
                "Verified reproduction: {}\nCollected files: {}\nExternal accesses: {}\n",
                bundle_path.display(),
                staged.len(),
                external.len()
            )
            .into_bytes()
        };
        if !report.ends_with(b"\n") {
            report.push(b'\n');
        }
        if let Err(error) = publish(&args.output, &report) {
            return diagnostic(error, "min-repro");
        }
    }
    0
}

pub fn execute(command: Command) -> i32 {
    match command {
        Command::Compare(args) => compare(args),
        Command::Record(args) => {
            #[cfg(target_os = "linux")]
            {
                record(args)
            }
            #[cfg(not(target_os = "linux"))]
            {
                let _ = args;
                diagnostic("unsupported_target", "record")
            }
        }
        Command::Assetcov(args) => {
            #[cfg(target_os = "linux")]
            {
                assetcov(args)
            }
            #[cfg(not(target_os = "linux"))]
            {
                let _ = args;
                diagnostic("unsupported_target", "assetcov")
            }
        }
        Command::Latencylab(args) => {
            #[cfg(target_os = "linux")]
            {
                latencylab(args)
            }
            #[cfg(not(target_os = "linux"))]
            {
                let _ = args;
                diagnostic("unsupported_target", "latencylab")
            }
        }
        Command::MinRepro(args) => {
            #[cfg(target_os = "linux")]
            {
                min_repro(args)
            }
            #[cfg(not(target_os = "linux"))]
            {
                let _ = args;
                diagnostic("unsupported_target", "min-repro")
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use clap::Parser;

    use super::*;

    #[derive(Parser)]
    struct TestCli {
        #[command(subcommand)]
        command: Command,
    }

    #[test]
    fn parser_rejects_conflicting_or_invalid_report_options() {
        assert!(
            TestCli::try_parse_from(["fspy", "compare", "a", "b", "--json", "--quiet"]).is_err()
        );
        assert!(TestCli::try_parse_from([
            "fspy",
            "assetcov",
            "--include",
            "*",
            "--fail-under",
            "101",
            "--",
            "true"
        ])
        .is_err());
        assert!(
            TestCli::try_parse_from(["fspy", "record", "--timeout", "0s", "--", "true"]).is_err()
        );
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn private_record_compare_and_coverage_handlers_run_a_real_child() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let trace_path = directory.path().join("trace.ndjson");
        let record = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("record"),
            OsString::from("--root"),
            directory.path().as_os_str().to_os_string(),
            OsString::from("--output"),
            trace_path.as_os_str().to_os_string(),
            OsString::from("--"),
            OsString::from("/bin/cat"),
            input.as_os_str().to_os_string(),
        ])
        .unwrap();
        assert_eq!(execute(record.command), 0);
        let restored = load(&trace_path).unwrap();
        assert!(restored.summary.operation_count > 0);
        let compare = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("compare"),
            trace_path.as_os_str().to_os_string(),
            trace_path.as_os_str().to_os_string(),
            OsString::from("--quiet"),
        ])
        .unwrap();
        assert_eq!(execute(compare.command), 0);
        let coverage = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("assetcov"),
            OsString::from("--root"),
            directory.path().as_os_str().to_os_string(),
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--quiet"),
            OsString::from("--fail-under"),
            OsString::from("100"),
            OsString::from("--"),
            OsString::from("/bin/cat"),
            input.as_os_str().to_os_string(),
        ])
        .unwrap();
        assert_eq!(execute(coverage.command), 0);
        let latency_report = directory.path().join("latency.json");
        let latency = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("latencylab"),
            OsString::from("--root"),
            directory.path().as_os_str().to_os_string(),
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--delay"),
            OsString::from("1ms"),
            OsString::from("--runs"),
            OsString::from("1"),
            OsString::from("--json"),
            OsString::from("--output"),
            latency_report.as_os_str().to_os_string(),
            OsString::from("--"),
            OsString::from("/bin/cat"),
            input.as_os_str().to_os_string(),
        ])
        .unwrap();
        assert_eq!(execute(latency.command), 0);
        let report: serde_json::Value =
            serde_json::from_slice(&fs::read(latency_report).unwrap()).unwrap();
        assert_eq!(report["runs"].as_array().unwrap().len(), 2);
        assert!(report["runs"][1]["matching_operations"].as_u64().unwrap() > 0);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn verified_reproduction_uses_a_separate_working_directory() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().join("project");
        fs::create_dir(&root).unwrap();
        fs::write(root.join("input.txt"), b"fixture").unwrap();
        fs::write(root.join(".env"), b"secret").unwrap();
        for case in ["verified", "blocked", "original", "metadata"] {
            let bundle = directory.path().join(format!("{case}-bundle"));
            let status = std::process::Command::new(std::env::current_exe().unwrap())
                .arg("--exact")
                .arg("cli::tests::reproduction_child_process")
                .env("CLIBOX_FSPY_REPRO_CHILD", case)
                .env("CLIBOX_FSPY_REPRO_BUNDLE", &bundle)
                .current_dir(&root)
                .output()
                .unwrap();
            assert!(
                status.status.success(),
                "{case}: {}",
                String::from_utf8_lossy(&status.stderr)
            );
            if case == "verified" {
                assert_eq!(fs::read(bundle.join("input.txt")).unwrap(), b"fixture");
                assert!(bundle.join(".clibox-fspy-repro/manifest.json").exists());
                assert!(!bundle.join(".env").exists());
            } else {
                assert!(!bundle.exists());
            }
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn reproduction_child_process() {
        let Some(case) = std::env::var_os("CLIBOX_FSPY_REPRO_CHILD") else {
            return;
        };
        let bundle = std::env::var_os("CLIBOX_FSPY_REPRO_BUNDLE").unwrap();
        let root = std::env::current_dir().unwrap();
        let include = if case == "blocked" { "*" } else { "input.txt" };
        let mut arguments = vec![
            OsString::from("fspy"),
            OsString::from("min-repro"),
            OsString::from("--include"),
            OsString::from(include),
            OsString::from("--bundle-dir"),
            bundle,
            OsString::from("--expect-exit"),
            OsString::from("42"),
            OsString::from("--expect-stderr"),
            OsString::from("EXPECTED"),
            OsString::from("--quiet"),
            OsString::from("--"),
            OsString::from("/bin/sh"),
            OsString::from("-c"),
        ];
        if case == "original" || case == "metadata" {
            arguments.push(OsString::from(if case == "metadata" {
                "test -f \"$1\"; echo EXPECTED >&2; exit 42"
            } else {
                "cat \"$1\" >/dev/null; echo EXPECTED >&2; exit 42"
            }));
            arguments.push(OsString::from("sh"));
            arguments.push(root.join("input.txt").into_os_string());
        } else {
            arguments.push(OsString::from(if case == "blocked" {
                "cat .env >/dev/null; echo EXPECTED >&2; exit 42"
            } else {
                "cat input.txt >/dev/null; echo EXPECTED >&2; exit 42"
            }));
        }
        let cli = TestCli::try_parse_from(arguments).unwrap();
        assert_eq!(execute(cli.command), if case == "verified" { 0 } else { 1 });
    }
}
