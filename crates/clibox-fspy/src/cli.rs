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
fn execute_capture(
    command: &[OsString],
    root: &Path,
    args: &ExecutionArgs,
) -> Result<CompleteRecord, crate::linux::TraceFailure> {
    execute_capture_with(command, root, args, |_| Duration::ZERO)
}

#[cfg(target_os = "linux")]
fn execute_capture_with<F>(
    command: &[OsString],
    root: &Path,
    args: &ExecutionArgs,
    delay_for: F,
) -> Result<CompleteRecord, crate::linux::TraceFailure>
where
    F: FnMut(&crate::linux::paths::DecodedOperation) -> Duration,
{
    use std::{
        os::fd::AsFd,
        process::{Command as ProcessCommand, Stdio},
        sync::{atomic::AtomicBool, Arc},
    };

    if command.is_empty() {
        return Err(crate::linux::TraceFailure::Spawn);
    }
    let mut child = ProcessCommand::new(&command[0]);
    child.args(&command[1..]);
    let stderr = io::stderr()
        .as_fd()
        .try_clone_to_owned()
        .map_err(|_| crate::linux::TraceFailure::Spawn)?;
    child.stdout(Stdio::from(stderr)).stderr(Stdio::inherit());
    let cancelled = Arc::new(AtomicBool::new(false));
    let signal = signal_hook::flag::register(signal_hook::consts::SIGINT, cancelled.clone())
        .map_err(|_| crate::linux::TraceFailure::Spawn)?;
    #[cfg(unix)]
    let term = signal_hook::flag::register(signal_hook::consts::SIGTERM, cancelled.clone())
        .map_err(|_| crate::linux::TraceFailure::Spawn)?;
    let result = crate::linux::capture::capture(
        &mut child,
        root,
        crate::linux::Limits {
            max_events: args.max_events,
            timeout: args.timeout,
            kill_after: args.kill_after,
        },
        &cancelled,
        delay_for,
    );
    signal_hook::low_level::unregister(signal);
    #[cfg(unix)]
    signal_hook::low_level::unregister(term);
    result
}

#[cfg(target_os = "linux")]
fn capture_status(error: crate::linux::TraceFailure, action: &'static str) -> i32 {
    match error {
        crate::linux::TraceFailure::Timeout => {
            diagnostic("timeout", action);
            124
        }
        crate::linux::TraceFailure::Cancellation => {
            diagnostic("cancellation", action);
            130
        }
        other => diagnostic(
            match other {
                crate::linux::TraceFailure::Permission => "trace_permission",
                crate::linux::TraceFailure::UnsupportedKernel => "unsupported_trace_kernel",
                crate::linux::TraceFailure::EventLimit => "event_limit",
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
}
