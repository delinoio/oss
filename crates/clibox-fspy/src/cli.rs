//! CLI surface for local file-access workflows.

use std::{
    ffi::OsString,
    fs,
    io::{self, BufReader, Write},
    path::{Path, PathBuf},
    time::Duration,
};

use clap::{Args, Subcommand};

#[cfg(target_os = "linux")]
use crate::coverage;
use crate::record::{self, CompleteRecord, DEFAULT_BYTE_LIMIT, DEFAULT_EVENT_LIMIT};

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
            let result = serde_json::json!({
                "added": difference.added.iter().map(|key| &key.0).collect::<Vec<_>>(),
                "removed": difference.removed.iter().map(|key| &key.0).collect::<Vec<_>>(),
                "changed_operations": difference.changed_operations.iter().map(|key| &key.0).collect::<Vec<_>>(),
                "changed_counts": difference.changed_counts.iter().map(|key| &key.0).collect::<Vec<_>>(),
                "changed_failures": difference.changed_failures.iter().map(|key| &key.0).collect::<Vec<_>>(),
                "changed_timing": difference.changed_timing.iter().map(|key| &key.0).collect::<Vec<_>>(),
                "external_added": difference.external_added.iter().map(|key| &key.0).collect::<Vec<_>>(),
                "external_removed": difference.external_removed.iter().map(|key| &key.0).collect::<Vec<_>>(),
            });
            serde_json::to_vec_pretty(&result).unwrap_or_default()
        } else {
            format!(
                "Added project files: {}\nRemoved project files: {}\nChanged operation kinds: \
                 {}\nChanged counts: {}\nChanged failures: {}\nChanged timings: {}\nExternal \
                 added: {}\nExternal removed: {}\n",
                difference.added.len(),
                difference.removed.len(),
                difference.changed_operations.len(),
                difference.changed_counts.len(),
                difference.changed_failures.len(),
                difference.changed_timing.len(),
                difference.external_added.len(),
                difference.external_removed.len()
            )
            .into_bytes()
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
        |_| Duration::ZERO,
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
            format!(
                "Covered: {}/{} ({:.2}%)\n",
                report.covered.len(),
                denominator.files.len(),
                report.percentage
            )
            .into_bytes()
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
    }
}
