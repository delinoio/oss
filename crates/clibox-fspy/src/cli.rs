//! CLI surface for local file-access workflows.

use std::{
    ffi::OsString,
    fs,
    io::{self, BufReader, Write},
    path::{Path, PathBuf},
    time::Duration,
};

use clap::{Args, Subcommand, ValueEnum};

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
use crate::coverage;
use crate::record::{self, CompleteRecord, NativePath, DEFAULT_BYTE_LIMIT, DEFAULT_EVENT_LIMIT};

#[derive(Debug, Subcommand)]
pub enum Command {
    /// Capture paired synchronous file operations as versioned NDJSON.
    #[command(
        after_help = "Example: clibox fspy record --output trace.ndjson -- cargo test\nThe \
                      declared boundary excludes mmap and asynchronous I/O. Records contain \
                      file-access paths.\nExit codes: 0 success, 1 failure, 2 invalid input, 124 \
                      timeout, 130 interrupt, 143 Unix SIGTERM."
    )]
    Record(RecordArgs),
    /// Rerun when inputs discovered from a traced execution change.
    #[command(
        after_help = "Example: clibox fspy autowatch --include 'src/**' -- cargo test\nExit \
                      codes: 0 success, 1 failure, 2 invalid input, 124 timeout, 130 interrupt, \
                      143 Unix SIGTERM."
    )]
    Autowatch(AutowatchArgs),
    /// Compare two complete, compatible execution records.
    #[command(
        after_help = "Example: clibox fspy compare before.ndjson after.ndjson \
                      --fail-on-change\nExit codes: 0 success, 1 failure, 2 invalid input."
    )]
    Compare(CompareArgs),
    /// Report selected existing resources actually read by a command.
    #[command(
        after_help = "Example: clibox fspy assetcov --include 'assets/**' -- cargo test\nExit \
                      codes: 0 success, 1 failure, 2 invalid input, 124 timeout, 130 interrupt, \
                      143 Unix SIGTERM."
    )]
    Assetcov(AssetcovArgs),
    /// Alternate baseline and delayed runs of matching file operations.
    #[command(
        after_help = "Example: clibox fspy latencylab --include 'src/**' --delay 10ms -- cargo \
                      test\nTiming is an observation under current conditions, not a \
                      storage-device prediction.\nExit codes: 0 success, 1 failure, 2 invalid \
                      input, 124 timeout, 130 interrupt, 143 Unix SIGTERM."
    )]
    Latencylab(LatencyArgs),
    /// Collect and verify observed project inputs for a failing command.
    #[command(
        after_help = "Example: clibox fspy min-repro --include 'src/**' --bundle-dir repro \
                      --expect-exit 1 --expect-stderr 'failed' -- cargo test\nThe bundle is \
                      verified in a separate cwd; it is not an OS sandbox or a cross-machine \
                      guarantee.\nExit codes: 0 success, 1 failure, 2 invalid input, 124 timeout, \
                      130 interrupt, 143 Unix SIGTERM."
    )]
    MinRepro(MinReproArgs),
    /// Pause matching calling threads before a file operation.
    #[command(
        after_help = "Example: clibox fspy fbreak --include 'config/**' --op read -- \
                      ./app\nControls on the required terminal: n next, c continue all, q quit. \
                      Child stdin is closed.\nExit codes: 0 success, 1 failure, 2 invalid input, \
                      124 timeout, 130 interrupt, 143 Unix SIGTERM."
    )]
    Fbreak(BreakArgs),
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

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
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
pub struct AutowatchArgs {
    #[command(flatten)]
    execution: ExecutionArgs,
    /// Select project inputs; repeat for a union.
    #[arg(long, required = true)]
    include: Vec<String>,
    /// Exclude project inputs.
    #[arg(long)]
    exclude: Vec<String>,
    /// Coalesce changes before a rerun (default: 200ms).
    #[arg(long, default_value = "200ms", value_parser = parse_positive_duration)]
    debounce: Duration,
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

#[derive(Debug, Args)]
pub struct BreakArgs {
    #[command(flatten)]
    execution: ExecutionArgs,
    /// Select matching project paths.
    #[arg(long, required = true)]
    include: Vec<String>,
    /// Exclude matching project paths.
    #[arg(long)]
    exclude: Vec<String>,
    /// Match operation kinds; default is read and positional read.
    #[arg(long = "op", value_enum)]
    operations: Vec<OperationKind>,
    /// Child program and tokenized arguments; its stdin is closed.
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
            match serde_json::to_vec_pretty(&result) {
                Ok(bytes) => bytes,
                Err(_) => return diagnostic("report_encode", "compare"),
            }
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

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
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
struct ControlTerminal {
    file: fs::File,
    original: libc::termios,
    flags: i32,
    displayed: Option<u64>,
}

#[cfg(target_os = "linux")]
impl ControlTerminal {
    fn new() -> Result<Self, crate::linux::TraceFailure> {
        use std::os::fd::AsRawFd;
        let file = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open("/dev/tty")
            .map_err(|_| crate::linux::TraceFailure::ControlUnavailable)?;
        let fd = file.as_raw_fd();
        // SAFETY: fd is the owned control terminal descriptor and original is
        // writable storage for tcgetattr.
        let mut original: libc::termios = unsafe { std::mem::zeroed() };
        if unsafe { libc::isatty(fd) } != 1
            || unsafe { libc::tcgetattr(fd, &raw mut original) } != 0
        {
            return Err(crate::linux::TraceFailure::ControlUnavailable);
        }
        // SAFETY: F_GETFL reads status flags from the valid terminal fd.
        let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
        if flags < 0 {
            return Err(crate::linux::TraceFailure::ControlUnavailable);
        }
        let terminal = Self {
            file,
            original,
            flags,
            displayed: None,
        };
        let mut immediate = terminal.original;
        immediate.c_lflag &= !(libc::ICANON | libc::ECHO);
        immediate.c_cc[libc::VMIN] = 1;
        immediate.c_cc[libc::VTIME] = 0;
        // SAFETY: tcsetattr/fcntl update only the owned terminal descriptor;
        // Drop restores both settings on success and every later failure.
        if unsafe { libc::tcsetattr(fd, libc::TCSANOW, &raw const immediate) } != 0
            || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0
        {
            return Err(crate::linux::TraceFailure::ControlUnavailable);
        }
        Ok(terminal)
    }

    fn poll(
        &mut self,
        entry: &crate::linux::RawEntry,
        root: &Path,
        selector: &coverage::Selector,
    ) -> Result<crate::linux::ControlDirective, crate::linux::TraceFailure> {
        use std::io::Read;
        if self.displayed != Some(entry.ordinal) {
            let decoded = crate::linux::paths::decode(entry, root)?
                .ok_or(crate::linux::TraceFailure::ControlLoss)?;
            let matching = decoded
                .paths
                .iter()
                .find(|path| {
                    path.project_relative
                        .as_ref()
                        .is_some_and(|relative| selector.matches(relative))
                })
                .ok_or(crate::linux::TraceFailure::ControlLoss)?;
            writeln!(
                self.file,
                "break: {} {:?} pid={} tid={} [n/c/q]",
                display_path(&matching.logical),
                decoded.operation,
                entry.pid,
                entry.tid
            )
            .map_err(|_| crate::linux::TraceFailure::ControlLoss)?;
            self.file
                .flush()
                .map_err(|_| crate::linux::TraceFailure::ControlLoss)?;
            self.displayed = Some(entry.ordinal);
        }
        let mut key = [0_u8; 1];
        match self.file.read(&mut key) {
            Ok(1) => Ok(match key[0] {
                b'n' => crate::linux::ControlDirective::ReleaseOne,
                b'c' => crate::linux::ControlDirective::ContinueAll,
                b'q' => crate::linux::ControlDirective::Quit,
                _ => crate::linux::ControlDirective::Wait,
            }),
            Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                Ok(crate::linux::ControlDirective::Wait)
            }
            _ => Err(crate::linux::TraceFailure::ControlLoss),
        }
    }
}

#[cfg(target_os = "linux")]
impl Drop for ControlTerminal {
    fn drop(&mut self) {
        use std::os::fd::AsRawFd;
        let fd = self.file.as_raw_fd();
        // SAFETY: this descriptor still belongs to self until Drop completes.
        unsafe {
            libc::tcsetattr(fd, libc::TCSANOW, &raw const self.original);
            libc::fcntl(fd, libc::F_SETFL, self.flags);
        }
    }
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
fn repro_capture(
    command: &[OsString],
    root: &Path,
    cwd: Option<&Path>,
    args: &ExecutionArgs,
    expected_stderr: &str,
) -> Result<(CompleteRecord, bool), i32> {
    execute_repro_capture(command, root, cwd, args, expected_stderr)
        .map_err(|failure| capture_status(failure, "min-repro"))
}

#[cfg(target_os = "macos")]
fn repro_capture(
    command: &[OsString],
    root: &Path,
    cwd: Option<&Path>,
    args: &ExecutionArgs,
    expected_stderr: &str,
) -> Result<(CompleteRecord, bool), i32> {
    use std::{
        os::{fd::OwnedFd, unix::net::UnixStream},
        process::Stdio,
        sync::{
            atomic::{AtomicBool, Ordering},
            Arc,
        },
        thread,
    };

    use crate::macos::supervise::{CaptureFailure, Limits};

    let mut child = fspy::Command::new(&command[0]);
    child.args(&command[1..]).envs(std::env::vars_os());
    if let Some(cwd) = cwd {
        child.current_dir(cwd);
    }
    child.stdout(Stdio::inherit());
    let (mut reader, writer) = UnixStream::pair().map_err(|_| {
        eprintln!("clibox fspy reproduction: stage=stderr_channel");
        macos_capture_status((CaptureFailure::Spawn, 0), "min-repro")
    })?;
    reader
        .set_read_timeout(Some(Duration::from_millis(100)))
        .map_err(|_| {
            eprintln!("clibox fspy reproduction: stage=stderr_timeout");
            macos_capture_status((CaptureFailure::Spawn, 0), "min-repro")
        })?;
    child.stderr(Stdio::from(OwnedFd::from(writer)));
    let signals =
        MacSignals::new().map_err(|error| macos_capture_status((error, 0), "min-repro"))?;
    let finished = Arc::new(AtomicBool::new(false));
    let reader_finished = Arc::clone(&finished);
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
        .map_err(|_| {
            eprintln!("clibox fspy reproduction: stage=stderr_thread");
            macos_capture_status((CaptureFailure::Spawn, 0), "min-repro")
        })?;
    let result = crate::macos::supervise::capture(
        child,
        root,
        Limits {
            max_events: args.max_events,
            max_bytes: args.max_bytes,
            timeout: args.timeout,
            kill_after: args.kill_after,
        },
        &signals.cancelled,
    );
    finished.store(true, Ordering::SeqCst);
    let matched = forwarder
        .join()
        .map_err(|_| macos_capture_status((CaptureFailure::TraceLoss, 0), "min-repro"))?
        .map_err(|_| macos_capture_status((CaptureFailure::TraceLoss, 0), "min-repro"))?;
    let record = result.map_err(|error| {
        macos_capture_status((error, signals.signal.load(Ordering::SeqCst)), "min-repro")
    })?;
    Ok((record, matched))
}

#[cfg(target_os = "windows")]
fn repro_capture(
    command: &[OsString],
    root: &Path,
    cwd: Option<&Path>,
    args: &ExecutionArgs,
    expected_stderr: &str,
) -> Result<(CompleteRecord, bool), i32> {
    use std::{
        io::Read,
        os::windows::io::{FromRawHandle, OwnedHandle},
        process::Stdio,
        thread,
    };

    use winapi::um::namedpipeapi::CreatePipe;

    use crate::windows::supervise::{CaptureFailure, Limits};

    let mut read_handle = std::ptr::null_mut();
    let mut write_handle = std::ptr::null_mut();
    // SAFETY: CreatePipe initializes both handles on success. OwnedHandle
    // takes each exactly once and closes it on every subsequent exit path.
    if unsafe {
        CreatePipe(
            &raw mut read_handle,
            &raw mut write_handle,
            std::ptr::null_mut(),
            0,
        )
    } == 0
    {
        return Err(windows_capture_status(CaptureFailure::Spawn, "min-repro"));
    }
    let mut reader = unsafe { fs::File::from_raw_handle(read_handle.cast()) };
    let writer = unsafe { OwnedHandle::from_raw_handle(write_handle.cast()) };
    let mut child = fspy::Command::new(&command[0]);
    child.args(&command[1..]).envs(std::env::vars_os());
    if let Some(cwd) = cwd {
        child.current_dir(cwd);
    }
    child.stdout(Stdio::inherit()).stderr(Stdio::from(writer));
    let _signals =
        WindowsSignals::new().map_err(|failure| windows_capture_status(failure, "min-repro"))?;
    let needle = expected_stderr.as_bytes().to_vec();
    let forwarder = thread::Builder::new()
        .name("clibox-fspy-stderr".into())
        .spawn(move || {
            let mut matched = false;
            let mut tail = Vec::new();
            let mut buffer = [0_u8; 8192];
            loop {
                match reader.read(&mut buffer) {
                    Ok(0) => return Ok(matched),
                    Ok(count) => {
                        io::stderr()
                            .write_all(&buffer[..count])
                            .map_err(|_| "output_forward")?;
                        let mut combined = tail;
                        combined.extend_from_slice(&buffer[..count]);
                        matched |= combined.windows(needle.len()).any(|part| part == needle);
                        let keep = needle.len().saturating_sub(1).min(combined.len());
                        tail = combined[combined.len() - keep..].to_vec();
                    }
                    Err(_) => return Err("output_forward"),
                }
            }
        })
        .map_err(|_| windows_capture_status(CaptureFailure::Spawn, "min-repro"))?;
    let record = crate::windows::supervise::capture(
        child,
        root,
        Limits {
            max_events: args.max_events,
            max_bytes: args.max_bytes,
            timeout: args.timeout,
            kill_after: args.kill_after,
        },
        &WINDOWS_CANCELLED,
    );
    let matched = forwarder
        .join()
        .map_err(|_| windows_capture_status(CaptureFailure::TraceLoss, "min-repro"))?
        .map_err(|_| windows_capture_status(CaptureFailure::TraceLoss, "min-repro"))?;
    let record = record.map_err(|failure| windows_capture_status(failure, "min-repro"))?;
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
                crate::linux::TraceFailure::ControlUnavailable => "control_terminal_unavailable",
                crate::linux::TraceFailure::ControlLoss => "control_channel_loss",
                crate::linux::TraceFailure::Spawn => "spawn_failure",
                crate::linux::TraceFailure::Supervision(_) => "trace_supervision",
                _ => "trace_failure",
            },
            action,
        ),
    }
}

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
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
fn incomplete_record(root: &Path, failure: crate::linux::TraceFailure) -> CompleteRecord {
    use std::os::unix::ffi::OsStrExt;

    let classification = match failure {
        crate::linux::TraceFailure::Permission => record::FailureClass::Permission,
        crate::linux::TraceFailure::UnsupportedKernel => record::FailureClass::UnsupportedTarget,
        crate::linux::TraceFailure::EventLimit => record::FailureClass::EventLimit,
        crate::linux::TraceFailure::ByteLimit => record::FailureClass::ByteLimit,
        crate::linux::TraceFailure::Timeout => record::FailureClass::Timeout,
        crate::linux::TraceFailure::Cancellation => record::FailureClass::Cancellation,
        crate::linux::TraceFailure::Cleanup => record::FailureClass::Cleanup,
        crate::linux::TraceFailure::Spawn | crate::linux::TraceFailure::ControlUnavailable => {
            record::FailureClass::TraceInitialization
        }
        crate::linux::TraceFailure::Supervision(_) | crate::linux::TraceFailure::ControlLoss => {
            record::FailureClass::TraceLoss
        }
    };
    CompleteRecord {
        header: record::Header {
            schema_version: record::SCHEMA_VERSION,
            execution_id: uuid::Uuid::now_v7(),
            platform: record::Platform::Linux,
            backend: record::Backend::Ptrace,
            root: NativePath::UnixBytes(root.as_os_str().as_bytes().to_vec()),
            coverage: record::CoverageBoundary::SynchronousFileOperationsV1,
        },
        operations: Vec::new(),
        summary: record::Summary {
            complete: false,
            child_exit_code: None,
            child_signal: None,
            operation_count: 0,
            failure_count: 0,
            failure: Some(classification),
        },
    }
}

#[cfg(target_os = "linux")]
fn record(args: RecordArgs) -> i32 {
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "record"),
    };
    let record = match execute_capture(&args.command, &root, &args.execution) {
        Ok(record) => record,
        Err(error) => {
            let incomplete = incomplete_record(&root, error.error);
            let mut encoded = Vec::new();
            if record::serialize(
                &incomplete,
                &mut encoded,
                args.execution.max_events,
                args.execution.max_bytes,
            )
            .is_ok()
            {
                if let Err(publication) = publish(&args.output, &encoded) {
                    diagnostic(publication, "record");
                }
            }
            return capture_status(error, "record");
        }
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
            match serde_json::to_vec_pretty(&serde_json::json!({
                "covered": report.covered.iter().map(|file| &file.logical).collect::<Vec<_>>(),
                "uncovered": report.uncovered.iter().map(|file| &file.logical).collect::<Vec<_>>(),
                "covered_count": report.covered.len(),
                "total_count": denominator.files.len(),
                "percentage": report.percentage,
                "child_exit_code": record.summary.child_exit_code,
                "child_signal": record.summary.child_signal,
            })) {
                Ok(bytes) => bytes,
                Err(_) => return diagnostic("report_encode", "assetcov"),
            }
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

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
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

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
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
    latencylab_with(args, |command, root, execution, selector, kinds, delay| {
        execute_capture_with(command, root, execution, |operation| {
            if matches_selected(operation.operation, &operation.paths, selector, kinds) {
                delay
            } else {
                Duration::ZERO
            }
        })
        .map_err(|failure| capture_status(failure, "latencylab"))
    })
}

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
fn latencylab_with<F>(args: LatencyArgs, mut capture: F) -> i32
where
    F: FnMut(
        &[OsString],
        &Path,
        &ExecutionArgs,
        &coverage::Selector,
        &[OperationKind],
        Duration,
    ) -> Result<CompleteRecord, i32>,
{
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
            let record = match capture(
                &args.command,
                &root,
                &args.execution,
                &selector,
                &args.operations,
                delay,
            ) {
                Ok(record) => record,
                Err(status) => return status,
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
            match serde_json::to_vec_pretty(&serde_json::json!({
                "runs": runs,
                "baseline_median_ns": baseline_median_ns,
                "delayed_median_ns": delayed_median_ns,
                "slowdown_ratio": slowdown_ratio,
                "requested_delay_ns": args.delay.as_nanos(),
            })) {
                Ok(bytes) => bytes,
                Err(_) => return diagnostic("report_encode", "latencylab"),
            }
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

#[cfg(any(target_os = "linux", target_os = "macos"))]
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

#[cfg(target_os = "windows")]
fn logical_relative(_root: &Path, path: &record::AccessPath) -> Option<PathBuf> {
    use std::os::windows::ffi::OsStringExt;

    let NativePath::WindowsUtf16(units) = path.project_relative.as_ref()? else {
        return None;
    };
    let relative = PathBuf::from(OsString::from_wide(units));
    if relative.as_os_str().is_empty()
        || !relative
            .components()
            .all(|component| matches!(component, std::path::Component::Normal(_)))
    {
        return None;
    }
    Some(relative)
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn selection_native(path: &Path) -> NativePath {
    use std::os::unix::ffi::OsStrExt;
    NativePath::UnixBytes(path.as_os_str().as_bytes().to_vec())
}

#[cfg(target_os = "windows")]
fn selection_native(path: &Path) -> NativePath {
    use std::os::windows::ffi::OsStrExt;
    NativePath::WindowsUtf16(path.as_os_str().encode_wide().collect())
}

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
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
            let selected = selector.matches(&selection_native(&alias));
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

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
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

#[cfg(target_os = "macos")]
fn publish_new_directory(temporary: &Path, destination: &Path) -> Result<(), &'static str> {
    use std::{ffi::CString, os::unix::ffi::OsStrExt};
    let source = CString::new(temporary.as_os_str().as_bytes()).map_err(|_| "bundle_path")?;
    let destination =
        CString::new(destination.as_os_str().as_bytes()).map_err(|_| "bundle_path")?;
    // SAFETY: RENAME_EXCL atomically publishes this private directory only
    // when no destination exists, including under a concurrent creator.
    let result =
        unsafe { libc::renamex_np(source.as_ptr(), destination.as_ptr(), libc::RENAME_EXCL) };
    if result == 0 {
        Ok(())
    } else {
        Err("bundle_publish")
    }
}

#[cfg(target_os = "windows")]
fn publish_new_directory(temporary: &Path, destination: &Path) -> Result<(), &'static str> {
    use std::os::windows::ffi::OsStrExt;

    use winapi::um::{
        winbase::{MoveFileExW, MOVEFILE_WRITE_THROUGH},
        winnt::LPCWSTR,
    };

    let source = temporary
        .as_os_str()
        .encode_wide()
        .chain([0])
        .collect::<Vec<_>>();
    let target = destination
        .as_os_str()
        .encode_wide()
        .chain([0])
        .collect::<Vec<_>>();
    // SAFETY: both buffers are terminated wide paths on the same volume.
    // Without REPLACE_EXISTING, MoveFileExW rejects a concurrent destination.
    let moved = unsafe {
        MoveFileExW(
            source.as_ptr() as LPCWSTR,
            target.as_ptr() as LPCWSTR,
            MOVEFILE_WRITE_THROUGH,
        )
    };
    if moved != 0 {
        Ok(())
    } else {
        Err("bundle_publish")
    }
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn repro_relative_native(path: &NativePath) -> Option<PathBuf> {
    use std::os::unix::ffi::OsStringExt;
    match path {
        NativePath::UnixBytes(bytes) => Some(PathBuf::from(OsString::from_vec(bytes.clone()))),
        _ => None,
    }
}

#[cfg(target_os = "windows")]
fn repro_relative_native(path: &NativePath) -> Option<PathBuf> {
    use std::os::windows::ffi::OsStringExt;
    match path {
        NativePath::WindowsUtf16(units) => Some(PathBuf::from(OsString::from_wide(units))),
        _ => None,
    }
}

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
fn repro_native_under(path: &NativePath, root: &Path) -> bool {
    repro_relative_native(path).is_some_and(|path| path.starts_with(root))
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn external_native_key(path: &NativePath) -> Option<Vec<u8>> {
    match path {
        NativePath::UnixBytes(bytes) => Some(bytes.clone()),
        _ => None,
    }
}

#[cfg(target_os = "windows")]
fn external_native_key(path: &NativePath) -> Option<Vec<u8>> {
    match path {
        NativePath::WindowsUtf16(units) => {
            Some(units.iter().flat_map(|unit| unit.to_le_bytes()).collect())
        }
        _ => None,
    }
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn external_native_from_key(bytes: Vec<u8>) -> NativePath {
    NativePath::UnixBytes(bytes)
}

#[cfg(target_os = "windows")]
fn external_native_from_key(bytes: Vec<u8>) -> NativePath {
    NativePath::WindowsUtf16(
        bytes
            .chunks_exact(2)
            .map(|pair| u16::from_le_bytes([pair[0], pair[1]]))
            .collect(),
    )
}

#[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
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
    let (original, original_matches) = match repro_capture(
        &args.command,
        &root,
        None,
        &args.execution,
        &args.expect_stderr,
    ) {
        Ok(result) => result,
        Err(status) => return status,
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
    let (rerun, rerun_matches) = match repro_capture(
        &args.command,
        candidate.path(),
        Some(candidate.path()),
        &args.execution,
        &args.expect_stderr,
    ) {
        Ok(result) => result,
        Err(status) => return status,
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
                let Some(relative) = path
                    .project_relative
                    .as_ref()
                    .and_then(repro_relative_native)
                else {
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
                    repro_native_under(native, &root)
                        && !repro_native_under(native, candidate.path())
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
        .filter_map(|path| external_native_key(&path.logical))
        .collect::<BTreeSet<_>>()
        .into_iter()
        .map(external_native_from_key)
        .collect::<Vec<_>>();
    let staged_links = snapshot
        .links()
        .iter()
        .filter(|(path, _)| bundle.path().join(path).is_symlink())
        .collect::<Vec<_>>();
    let manifest = serde_json::json!({
        "schema_version": 1,
        "files": staged.iter().map(|file| serde_json::json!({
            "path": selection_native(&file.relative),
            "sha256": file.sha256,
            "size": file.size,
        })).collect::<Vec<_>>(),
        "internal_links": staged_links.iter().map(|(path, target)| serde_json::json!({
            "path": selection_native(path),
            "target": selection_native(target),
        })).collect::<Vec<_>>(),
        "external_dependencies": external,
    });
    let manifest_bytes = match serde_json::to_vec_pretty(&manifest) {
        Ok(bytes) => bytes,
        Err(_) => return diagnostic("bundle_encode", "min-repro"),
    };
    if fs::write(metadata_dir.join("manifest.json"), manifest_bytes).is_err()
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
            match serde_json::to_vec_pretty(&serde_json::json!({
                "verified": true,
                "bundle_dir": bundle_path,
                "collected_files": staged.len(),
                "external_accesses": external.len(),
            })) {
                Ok(bytes) => bytes,
                Err(_) => return diagnostic("report_encode", "min-repro"),
            }
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

#[cfg(target_os = "linux")]
fn fbreak(args: BreakArgs) -> i32 {
    use std::{
        process::{Command as ProcessCommand, Stdio},
        sync::atomic::Ordering,
    };
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "fbreak"),
    };
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => selector,
        Err(error) => return diagnostic(&error.to_string(), "fbreak"),
    };
    // Admission precedes launch. A missing or unusable control terminal must
    // never leave the child blocked at an operation entry.
    let mut terminal = match ControlTerminal::new() {
        Ok(terminal) => terminal,
        Err(error) => return capture_status(CaptureFailure { error, signal: 0 }, "fbreak"),
    };
    let signals = match SignalHandlers::new() {
        Ok(signals) => signals,
        Err(error) => return capture_status(CaptureFailure { error, signal: 0 }, "fbreak"),
    };
    let mut child = ProcessCommand::new(&args.command[0]);
    child
        .args(&args.command[1..])
        .stdin(Stdio::null())
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit());
    let result = crate::linux::capture::capture_controlled(
        &mut child,
        &root,
        crate::linux::Limits {
            max_events: args.execution.max_events,
            max_bytes: args.execution.max_bytes,
            timeout: args.execution.timeout,
            kill_after: args.execution.kill_after,
        },
        &signals.cancelled,
        |operation| {
            if matches_selected(
                operation.operation,
                &operation.paths,
                &selector,
                &args.operations,
            ) {
                crate::linux::capture::CaptureAction::Hold
            } else {
                crate::linux::capture::CaptureAction::Proceed(Duration::ZERO)
            }
        },
        |entry| terminal.poll(entry, &root, &selector),
    );
    match result {
        Ok(record) => child_status(&record),
        Err(error) => capture_status(
            CaptureFailure {
                error,
                signal: signals.signal.load(Ordering::SeqCst),
            },
            "fbreak",
        ),
    }
}

#[cfg(target_os = "linux")]
fn autowatch(args: AutowatchArgs) -> i32 {
    use std::{
        process::{Command as ProcessCommand, Stdio},
        sync::atomic::Ordering,
    };

    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "autowatch"),
    };
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => selector,
        Err(error) => return diagnostic(&error.to_string(), "autowatch"),
    };
    let signals = match SignalHandlers::new() {
        Ok(signals) => signals,
        Err(error) => {
            return capture_status(CaptureFailure { error, signal: 0 }, "autowatch");
        }
    };
    let mut watcher = crate::watch::WatchSession::new(root.clone());
    let mut previous = crate::watch::Dependencies::default();
    loop {
        if let Err(error) = watcher.start_discovery() {
            return diagnostic(&error.to_string(), "autowatch");
        }
        let mut child = ProcessCommand::new(&args.command[0]);
        child
            .args(&args.command[1..])
            .stdin(Stdio::inherit())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());
        let record = crate::linux::capture::capture(
            &mut child,
            &root,
            crate::linux::Limits {
                max_events: args.execution.max_events,
                max_bytes: args.execution.max_bytes,
                timeout: args.execution.timeout,
                kill_after: args.execution.kill_after,
            },
            &signals.cancelled,
            |_| Duration::ZERO,
        );
        let record = match record {
            Ok(record) => record,
            Err(error) => {
                return capture_status(
                    CaptureFailure {
                        error,
                        signal: signals.signal.load(Ordering::SeqCst),
                    },
                    "autowatch",
                );
            }
        };
        let mut dependencies = crate::watch::Dependencies::from_record(&record, &selector);
        if child_status(&record) != 0 {
            dependencies.merge(previous);
        }
        if let Err(error) = watcher.install(&dependencies) {
            return diagnostic(&error.to_string(), "autowatch");
        }
        previous = dependencies;
        loop {
            if signals.cancelled.load(Ordering::SeqCst) {
                return if signals.signal.load(Ordering::SeqCst) == libc::SIGTERM as usize {
                    143
                } else {
                    130
                };
            }
            match watcher.collect(&previous, args.debounce, Duration::from_millis(100)) {
                Ok(true) => break,
                Ok(false) => {}
                Err(error) => return diagnostic(&error.to_string(), "autowatch"),
            }
        }
    }
}

#[cfg(target_os = "macos")]
struct MacSignals {
    cancelled: std::sync::Arc<std::sync::atomic::AtomicBool>,
    signal: std::sync::Arc<std::sync::atomic::AtomicUsize>,
    registrations: Vec<signal_hook::SigId>,
}

#[cfg(target_os = "macos")]
impl MacSignals {
    fn new() -> Result<Self, crate::macos::supervise::CaptureFailure> {
        use std::sync::{
            atomic::{AtomicBool, AtomicUsize},
            Arc,
        };

        let mut signals = Self {
            cancelled: Arc::new(AtomicBool::new(false)),
            signal: Arc::new(AtomicUsize::new(0)),
            registrations: Vec::new(),
        };
        for number in [signal_hook::consts::SIGINT, signal_hook::consts::SIGTERM] {
            signals.registrations.push(
                signal_hook::flag::register(number, Arc::clone(&signals.cancelled))
                    .map_err(|_| crate::macos::supervise::CaptureFailure::Initialization)?,
            );
            signals.registrations.push(
                signal_hook::flag::register_usize(
                    number,
                    Arc::clone(&signals.signal),
                    number as usize,
                )
                .map_err(|_| crate::macos::supervise::CaptureFailure::Initialization)?,
            );
        }
        Ok(signals)
    }
}

#[cfg(target_os = "macos")]
impl Drop for MacSignals {
    fn drop(&mut self) {
        for registration in self.registrations.drain(..) {
            signal_hook::low_level::unregister(registration);
        }
    }
}

#[cfg(target_os = "macos")]
fn macos_capture(
    command: &[OsString],
    root: &Path,
    args: &ExecutionArgs,
) -> Result<CompleteRecord, (crate::macos::supervise::CaptureFailure, usize)> {
    macos_capture_with(command, root, args, |_| Duration::ZERO)
}

#[cfg(target_os = "macos")]
fn macos_capture_with<F>(
    command: &[OsString],
    root: &Path,
    args: &ExecutionArgs,
    delay_for: F,
) -> Result<CompleteRecord, (crate::macos::supervise::CaptureFailure, usize)>
where
    F: Fn(&crate::macos::Frame) -> Duration + Send + Sync + 'static,
{
    use std::{os::fd::AsFd, process::Stdio, sync::atomic::Ordering};

    let Some(program) = command.first() else {
        return Err((crate::macos::supervise::CaptureFailure::Spawn, 0));
    };
    let mut child = fspy::Command::new(program);
    child.args(&command[1..]).envs(std::env::vars_os());
    let stderr = io::stderr()
        .as_fd()
        .try_clone_to_owned()
        .map_err(|_| (crate::macos::supervise::CaptureFailure::Spawn, 0))?;
    child.stdout(Stdio::from(stderr)).stderr(Stdio::inherit());
    let signals = MacSignals::new().map_err(|error| (error, 0))?;
    crate::macos::supervise::capture_with_delay(
        child,
        root,
        crate::macos::supervise::Limits {
            max_events: args.max_events,
            max_bytes: args.max_bytes,
            timeout: args.timeout,
            kill_after: args.kill_after,
        },
        &signals.cancelled,
        delay_for,
    )
    .map_err(|error| (error, signals.signal.load(Ordering::SeqCst)))
}

#[cfg(target_os = "macos")]
fn macos_latencylab(args: LatencyArgs) -> i32 {
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => std::sync::Arc::new(selector),
        Err(error) => return diagnostic(&error.to_string(), "latencylab"),
    };
    latencylab_with(args, move |command, root, execution, _, kinds, delay| {
        let selector = std::sync::Arc::clone(&selector);
        let kinds = kinds.to_vec();
        macos_capture_with(command, root, execution, move |frame| {
            let Some(operation) = crate::macos::operation(frame.operation) else {
                return Duration::ZERO;
            };
            let Some(path) = frame.access_path.as_ref() else {
                return Duration::ZERO;
            };
            if matches_selected(operation, std::slice::from_ref(path), &selector, &kinds) {
                delay
            } else {
                Duration::ZERO
            }
        })
        .map_err(|failure| macos_capture_status(failure, "latencylab"))
    })
}

#[cfg(target_os = "macos")]
fn macos_autowatch(args: AutowatchArgs) -> i32 {
    use std::{process::Stdio, sync::atomic::Ordering};

    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "autowatch"),
    };
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => selector,
        Err(error) => return diagnostic(&error.to_string(), "autowatch"),
    };
    let signals = match MacSignals::new() {
        Ok(signals) => signals,
        Err(error) => return macos_capture_status((error, 0), "autowatch"),
    };
    let mut watcher = crate::watch::WatchSession::new(root.clone());
    let mut previous = crate::watch::Dependencies::default();
    loop {
        if let Err(error) = watcher.start_discovery() {
            return diagnostic(&error.to_string(), "autowatch");
        }
        let mut child = fspy::Command::new(&args.command[0]);
        child
            .args(&args.command[1..])
            .envs(std::env::vars_os())
            .stdin(Stdio::inherit())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());
        let record = crate::macos::supervise::capture(
            child,
            &root,
            crate::macos::supervise::Limits {
                max_events: args.execution.max_events,
                max_bytes: args.execution.max_bytes,
                timeout: args.execution.timeout,
                kill_after: args.execution.kill_after,
            },
            &signals.cancelled,
        );
        let record = match record {
            Ok(record) => record,
            Err(error) => {
                return macos_capture_status(
                    (error, signals.signal.load(Ordering::SeqCst)),
                    "autowatch",
                );
            }
        };
        let mut dependencies = crate::watch::Dependencies::from_record(&record, &selector);
        if child_status(&record) != 0 {
            dependencies.merge(previous);
        }
        if let Err(error) = watcher.install(&dependencies) {
            return diagnostic(&error.to_string(), "autowatch");
        }
        previous = dependencies;
        loop {
            if signals.cancelled.load(Ordering::SeqCst) {
                return if signals.signal.load(Ordering::SeqCst) == libc::SIGTERM as usize {
                    143
                } else {
                    130
                };
            }
            match watcher.collect(&previous, args.debounce, Duration::from_millis(100)) {
                Ok(true) => break,
                Ok(false) => {}
                Err(error) => return diagnostic(&error.to_string(), "autowatch"),
            }
        }
    }
}

#[cfg(target_os = "windows")]
struct WindowsBreakTerminal {
    input: fs::File,
    output: fs::File,
    original_mode: u32,
}

#[cfg(target_os = "windows")]
impl WindowsBreakTerminal {
    fn new() -> Result<Self, &'static str> {
        use std::os::windows::io::AsRawHandle;

        use winapi::{
            shared::minwindef::TRUE,
            um::{
                consoleapi::{GetConsoleMode, SetConsoleMode},
                wincon::{ENABLE_ECHO_INPUT, ENABLE_LINE_INPUT, ENABLE_PROCESSED_INPUT},
            },
        };

        let input = fs::OpenOptions::new()
            .read(true)
            .open("CONIN$")
            .map_err(|_| "control_terminal_unavailable")?;
        let output = fs::OpenOptions::new()
            .write(true)
            .open("CONOUT$")
            .map_err(|_| "control_terminal_unavailable")?;
        let handle = input.as_raw_handle().cast();
        let mut original_mode = 0;
        // SAFETY: this live console handle and writable mode field belong to
        // the owned CONIN$ descriptor, independent of redirected child I/O.
        if unsafe { GetConsoleMode(handle, &raw mut original_mode) } != TRUE {
            return Err("control_terminal_unavailable");
        }
        let immediate =
            original_mode & !(ENABLE_ECHO_INPUT | ENABLE_LINE_INPUT | ENABLE_PROCESSED_INPUT);
        if unsafe { SetConsoleMode(handle, immediate) } != TRUE {
            return Err("control_terminal_unavailable");
        }
        Ok(Self {
            input,
            output,
            original_mode,
        })
    }

    fn decision(
        &mut self,
        frame: &crate::windows::Frame,
        cancelled: &std::sync::atomic::AtomicBool,
    ) -> Result<u8, &'static str> {
        use std::{io::Read, os::windows::io::AsRawHandle, sync::atomic::Ordering};

        use winapi::um::{synchapi::WaitForSingleObject, winbase::WAIT_OBJECT_0};

        let path = frame.access_path.as_ref().ok_or("control_channel_loss")?;
        let operation =
            crate::windows::operation_id(frame.operation).ok_or("control_channel_loss")?;
        writeln!(
            self.output,
            "break: {} {:?} pid={} tid={} [n/c/q]",
            display_path(&path.logical),
            operation,
            frame.pid,
            frame.tid
        )
        .map_err(|_| "control_channel_loss")?;
        self.output.flush().map_err(|_| "control_channel_loss")?;
        loop {
            if cancelled.load(Ordering::SeqCst) {
                return Ok(0);
            }
            // SAFETY: the owned console input handle remains open for this
            // bounded wait; timeout lets supervision cancel waiting callers.
            let ready = unsafe { WaitForSingleObject(self.input.as_raw_handle().cast(), 100) };
            if ready == winapi::shared::winerror::WAIT_TIMEOUT {
                continue;
            }
            if ready != WAIT_OBJECT_0 {
                return Err("control_channel_loss");
            }
            let mut key = [0_u8; 1];
            match self.input.read(&mut key) {
                Ok(1) if matches!(key[0], b'n' | b'c' | b'q') => return Ok(key[0]),
                Ok(1) => {}
                _ => return Err("control_channel_loss"),
            }
        }
    }
}

#[cfg(target_os = "windows")]
impl Drop for WindowsBreakTerminal {
    fn drop(&mut self) {
        use std::os::windows::io::AsRawHandle;
        // SAFETY: the owned input handle is valid until Drop completes.
        unsafe {
            winapi::um::consoleapi::SetConsoleMode(
                self.input.as_raw_handle().cast(),
                self.original_mode,
            );
        }
    }
}

#[cfg(target_os = "windows")]
fn windows_fbreak(args: BreakArgs) -> i32 {
    use std::{
        process::Stdio,
        sync::{
            atomic::{AtomicBool, Ordering},
            Arc, Mutex,
        },
    };

    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "fbreak"),
    };
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => selector,
        Err(error) => return diagnostic(&error.to_string(), "fbreak"),
    };
    let terminal = match WindowsBreakTerminal::new() {
        Ok(terminal) => Arc::new(Mutex::new(terminal)),
        Err(error) => return diagnostic(error, "fbreak"),
    };
    let _signals = match WindowsSignals::new() {
        Ok(signals) => signals,
        Err(error) => return windows_capture_status(error, "fbreak"),
    };
    let continue_all = Arc::new(AtomicBool::new(false));
    let user_quit = Arc::new(AtomicBool::new(false));
    let control_loss = Arc::new(AtomicBool::new(false));
    let admission = {
        let terminal = Arc::clone(&terminal);
        let continue_all = Arc::clone(&continue_all);
        let user_quit = Arc::clone(&user_quit);
        let control_loss = Arc::clone(&control_loss);
        move |frame: &crate::windows::Frame| {
            use crate::windows::Admission;
            let Some(operation) = crate::windows::operation_id(frame.operation) else {
                return Admission::Proceed(Duration::ZERO);
            };
            if continue_all.load(Ordering::SeqCst)
                || !frame.access_path.as_ref().is_some_and(|path| {
                    matches_selected(
                        operation,
                        std::slice::from_ref(path),
                        &selector,
                        &args.operations,
                    )
                })
            {
                return Admission::Proceed(Duration::ZERO);
            }
            let decision = terminal
                .lock()
                .map_err(|_| "control_channel_loss")
                .and_then(|mut terminal| {
                    if continue_all.load(Ordering::SeqCst) {
                        Ok(b'c')
                    } else if WINDOWS_CANCELLED.load(Ordering::SeqCst) {
                        Ok(0)
                    } else {
                        terminal.decision(frame, &WINDOWS_CANCELLED)
                    }
                });
            match decision {
                Ok(0) => Admission::Quit,
                Ok(b'n') => Admission::Proceed(Duration::ZERO),
                Ok(b'c') => {
                    continue_all.store(true, Ordering::SeqCst);
                    Admission::Proceed(Duration::ZERO)
                }
                Ok(b'q') => {
                    user_quit.store(true, Ordering::SeqCst);
                    WINDOWS_CANCELLED.store(true, Ordering::SeqCst);
                    Admission::Quit
                }
                _ => {
                    control_loss.store(true, Ordering::SeqCst);
                    WINDOWS_CANCELLED.store(true, Ordering::SeqCst);
                    Admission::Quit
                }
            }
        }
    };
    let mut child = fspy::Command::new(&args.command[0]);
    child
        .args(&args.command[1..])
        .envs(std::env::vars_os())
        .stdin(Stdio::null())
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit());
    let result = crate::windows::supervise::capture_with_admission(
        child,
        &root,
        crate::windows::supervise::Limits {
            max_events: args.execution.max_events,
            max_bytes: args.execution.max_bytes,
            timeout: args.execution.timeout,
            kill_after: args.execution.kill_after,
        },
        &WINDOWS_CANCELLED,
        admission,
    );
    if control_loss.load(Ordering::SeqCst) {
        return diagnostic("control_channel_loss", "fbreak");
    }
    if user_quit.load(Ordering::SeqCst) {
        return 130;
    }
    match result {
        Ok(record) => child_status(&record),
        Err(error) => windows_capture_status(error, "fbreak"),
    }
}

#[cfg(target_os = "macos")]
struct MacBreakTerminal {
    file: fs::File,
    original: libc::termios,
    flags: i32,
}

#[cfg(target_os = "macos")]
impl MacBreakTerminal {
    fn new() -> Result<Self, &'static str> {
        use std::os::fd::AsRawFd;

        let file = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open("/dev/tty")
            .map_err(|_| "control_terminal_unavailable")?;
        let fd = file.as_raw_fd();
        // SAFETY: the descriptor belongs to this terminal and the struct is
        // writable for the exact native terminal configuration size.
        let mut original: libc::termios = unsafe { std::mem::zeroed() };
        if unsafe { libc::isatty(fd) } != 1
            || unsafe { libc::tcgetattr(fd, &raw mut original) } != 0
        {
            return Err("control_terminal_unavailable");
        }
        // SAFETY: F_GETFL reads flags from the live descriptor.
        let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
        if flags < 0 {
            return Err("control_terminal_unavailable");
        }
        let terminal = Self {
            file,
            original,
            flags,
        };
        let mut immediate = original;
        immediate.c_lflag &= !(libc::ICANON | libc::ECHO);
        immediate.c_cc[libc::VMIN] = 1;
        immediate.c_cc[libc::VTIME] = 0;
        // SAFETY: Drop restores both settings if either mutation fails.
        if unsafe { libc::tcsetattr(fd, libc::TCSANOW, &raw const immediate) } != 0
            || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0
        {
            return Err("control_terminal_unavailable");
        }
        Ok(terminal)
    }

    fn decision(
        &mut self,
        frame: &crate::macos::Frame,
        cancelled: &std::sync::atomic::AtomicBool,
    ) -> Result<u8, &'static str> {
        use std::{io::Read, sync::atomic::Ordering};

        let path = frame.access_path.as_ref().ok_or("control_channel_loss")?;
        let operation = crate::macos::operation(frame.operation).ok_or("control_channel_loss")?;
        writeln!(
            self.file,
            "break: {} {:?} pid={} tid={} [n/c/q]",
            display_path(&path.logical),
            operation,
            frame.pid,
            frame.tid
        )
        .map_err(|_| "control_channel_loss")?;
        self.file.flush().map_err(|_| "control_channel_loss")?;
        loop {
            if cancelled.load(Ordering::SeqCst) {
                return Ok(0);
            }
            let mut key = [0_u8; 1];
            match self.file.read(&mut key) {
                Ok(1) if matches!(key[0], b'n' | b'c' | b'q') => return Ok(key[0]),
                Ok(1) => {}
                Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                    std::thread::sleep(Duration::from_millis(10));
                }
                Err(error) if error.kind() == io::ErrorKind::Interrupted => {}
                _ => return Err("control_channel_loss"),
            }
        }
    }
}

#[cfg(target_os = "macos")]
impl Drop for MacBreakTerminal {
    fn drop(&mut self) {
        use std::os::fd::AsRawFd;
        let fd = self.file.as_raw_fd();
        // SAFETY: this descriptor remains owned until Drop completes.
        unsafe {
            libc::tcsetattr(fd, libc::TCSANOW, &raw const self.original);
            libc::fcntl(fd, libc::F_SETFL, self.flags);
        }
    }
}

#[cfg(target_os = "macos")]
fn macos_fbreak(args: BreakArgs) -> i32 {
    use std::{
        process::Stdio,
        sync::{
            atomic::{AtomicBool, Ordering},
            Arc, Mutex,
        },
    };

    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "fbreak"),
    };
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => selector,
        Err(error) => return diagnostic(&error.to_string(), "fbreak"),
    };
    let terminal = match MacBreakTerminal::new() {
        Ok(terminal) => Arc::new(Mutex::new(terminal)),
        Err(error) => return diagnostic(error, "fbreak"),
    };
    let signals = match MacSignals::new() {
        Ok(signals) => signals,
        Err(error) => return macos_capture_status((error, 0), "fbreak"),
    };
    let mut child = fspy::Command::new(&args.command[0]);
    child
        .args(&args.command[1..])
        .envs(std::env::vars_os())
        .stdin(Stdio::null())
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit());
    let continue_all = Arc::new(AtomicBool::new(false));
    let user_quit = Arc::new(AtomicBool::new(false));
    let control_loss = Arc::new(AtomicBool::new(false));
    let admission = {
        let terminal = Arc::clone(&terminal);
        let cancelled = Arc::clone(&signals.cancelled);
        let continue_all = Arc::clone(&continue_all);
        let user_quit = Arc::clone(&user_quit);
        let control_loss = Arc::clone(&control_loss);
        move |frame: &crate::macos::Frame| {
            use crate::macos::Admission;
            let Some(operation) = crate::macos::operation(frame.operation) else {
                return Admission::Proceed(Duration::ZERO);
            };
            if continue_all.load(Ordering::SeqCst)
                || !frame.access_path.as_ref().is_some_and(|path| {
                    matches_selected(
                        operation,
                        std::slice::from_ref(path),
                        &selector,
                        &args.operations,
                    )
                })
            {
                return Admission::Proceed(Duration::ZERO);
            }
            let decision = terminal
                .lock()
                .map_err(|_| "control_channel_loss")
                .and_then(|mut terminal| {
                    if continue_all.load(Ordering::SeqCst) {
                        Ok(b'c')
                    } else if cancelled.load(Ordering::SeqCst) {
                        Ok(0)
                    } else {
                        terminal.decision(frame, &cancelled)
                    }
                });
            match decision {
                Ok(0) => Admission::Quit,
                Ok(b'n') => Admission::Proceed(Duration::ZERO),
                Ok(b'c') => {
                    continue_all.store(true, Ordering::SeqCst);
                    Admission::Proceed(Duration::ZERO)
                }
                Ok(b'q') => {
                    user_quit.store(true, Ordering::SeqCst);
                    cancelled.store(true, Ordering::SeqCst);
                    Admission::Quit
                }
                _ => {
                    control_loss.store(true, Ordering::SeqCst);
                    cancelled.store(true, Ordering::SeqCst);
                    Admission::Quit
                }
            }
        }
    };
    let result = crate::macos::supervise::capture_with_admission(
        child,
        &root,
        crate::macos::supervise::Limits {
            max_events: args.execution.max_events,
            max_bytes: args.execution.max_bytes,
            timeout: args.execution.timeout,
            kill_after: args.execution.kill_after,
        },
        &signals.cancelled,
        admission,
    );
    if control_loss.load(Ordering::SeqCst) {
        return diagnostic("control_channel_loss", "fbreak");
    }
    if user_quit.load(Ordering::SeqCst) {
        return 130;
    }
    match result {
        Ok(record) => child_status(&record),
        Err(error) => {
            macos_capture_status((error, signals.signal.load(Ordering::SeqCst)), "fbreak")
        }
    }
}

#[cfg(target_os = "macos")]
fn macos_capture_status(
    failure: (crate::macos::supervise::CaptureFailure, usize),
    action: &'static str,
) -> i32 {
    use crate::macos::supervise::CaptureFailure;

    let (error, signal) = failure;
    diagnostic(&error.to_string(), action);
    match error {
        CaptureFailure::Timeout => 124,
        CaptureFailure::Cancellation if signal == signal_hook::consts::SIGTERM as usize => 143,
        CaptureFailure::Cancellation => 130,
        _ => 1,
    }
}

#[cfg(target_os = "macos")]
fn macos_incomplete_record(
    root: &Path,
    failure: crate::macos::supervise::CaptureFailure,
) -> CompleteRecord {
    use std::os::unix::ffi::OsStrExt;

    use crate::{
        macos::supervise::CaptureFailure,
        record::{Backend, CoverageBoundary, FailureClass, Header, Platform, Summary},
    };

    let classification = match failure {
        CaptureFailure::Initialization | CaptureFailure::Spawn => FailureClass::TraceInitialization,
        CaptureFailure::TraceLoss | CaptureFailure::DescendantSurvived | CaptureFailure::Record => {
            FailureClass::TraceLoss
        }
        CaptureFailure::Timeout => FailureClass::Timeout,
        CaptureFailure::Cancellation => FailureClass::Cancellation,
        CaptureFailure::Cleanup => FailureClass::Cleanup,
    };
    CompleteRecord {
        header: Header {
            schema_version: record::SCHEMA_VERSION,
            execution_id: uuid::Uuid::now_v7(),
            platform: Platform::Macos,
            backend: Backend::Injection,
            root: NativePath::UnixBytes(root.as_os_str().as_bytes().to_vec()),
            coverage: CoverageBoundary::SynchronousFileOperationsV1,
        },
        operations: Vec::new(),
        summary: Summary {
            complete: false,
            child_exit_code: None,
            child_signal: None,
            operation_count: 0,
            failure_count: 0,
            failure: Some(classification),
        },
    }
}

#[cfg(target_os = "macos")]
fn macos_record(args: RecordArgs) -> i32 {
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "record"),
    };
    let capture = macos_capture(&args.command, &root, &args.execution);
    let (record, failure) = match capture {
        Ok(record) => (record, None),
        Err(failure) => (macos_incomplete_record(&root, failure.0), Some(failure)),
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
    failure.map_or_else(
        || child_status(&record),
        |failure| macos_capture_status(failure, "record"),
    )
}

#[cfg(target_os = "macos")]
fn macos_assetcov(args: AssetcovArgs) -> i32 {
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "assetcov"),
    };
    let denominator = match coverage::select_existing(&root, &args.include, &args.exclude) {
        Ok(value) => value,
        Err(error) => return diagnostic(&error.to_string(), "assetcov"),
    };
    let record = match macos_capture(&args.command, &root, &args.execution) {
        Ok(record) => record,
        Err(error) => return macos_capture_status(error, "assetcov"),
    };
    let report = match coverage::analyze(&denominator, &record) {
        Ok(report) => report,
        Err(error) => return diagnostic(&error.to_string(), "assetcov"),
    };
    if !args.quiet {
        let mut encoded = if args.json {
            match serde_json::to_vec_pretty(&serde_json::json!({
                "covered": report.covered.iter().map(|file| &file.logical).collect::<Vec<_>>(),
                "uncovered": report.uncovered.iter().map(|file| &file.logical).collect::<Vec<_>>(),
                "covered_count": report.covered.len(),
                "total_count": denominator.files.len(),
                "percentage": report.percentage,
                "child_exit_code": record.summary.child_exit_code,
                "child_signal": record.summary.child_signal,
            })) {
                Ok(bytes) => bytes,
                Err(_) => return diagnostic("report_encode", "assetcov"),
            }
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

#[cfg(target_os = "windows")]
static WINDOWS_CANCELLED: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

#[cfg(target_os = "windows")]
unsafe extern "system" fn windows_ctrl_handler(code: u32) -> i32 {
    use winapi::um::wincon::{CTRL_BREAK_EVENT, CTRL_C_EVENT};
    if matches!(code, CTRL_C_EVENT | CTRL_BREAK_EVENT) {
        WINDOWS_CANCELLED.store(true, std::sync::atomic::Ordering::SeqCst);
        1
    } else {
        0
    }
}

#[cfg(target_os = "windows")]
struct WindowsSignals {
    _lock: std::sync::MutexGuard<'static, ()>,
}

#[cfg(target_os = "windows")]
impl WindowsSignals {
    fn new() -> Result<Self, crate::windows::supervise::CaptureFailure> {
        use std::sync::Mutex;

        use winapi::um::consoleapi::SetConsoleCtrlHandler;
        static LOCK: Mutex<()> = Mutex::new(());
        let lock = LOCK
            .lock()
            .map_err(|_| crate::windows::supervise::CaptureFailure::Initialization)?;
        WINDOWS_CANCELLED.store(false, std::sync::atomic::Ordering::SeqCst);
        // SAFETY: this fixed process-wide handler only updates an atomic flag.
        if unsafe { SetConsoleCtrlHandler(Some(windows_ctrl_handler), 1) } == 0 {
            return Err(crate::windows::supervise::CaptureFailure::Initialization);
        }
        Ok(Self { _lock: lock })
    }
}

#[cfg(target_os = "windows")]
impl Drop for WindowsSignals {
    fn drop(&mut self) {
        use winapi::um::consoleapi::SetConsoleCtrlHandler;
        // SAFETY: the matching registration was made while holding the lock.
        unsafe { SetConsoleCtrlHandler(Some(windows_ctrl_handler), 0) };
    }
}

#[cfg(target_os = "windows")]
fn windows_capture(
    command: &[OsString],
    root: &Path,
    args: &ExecutionArgs,
) -> Result<CompleteRecord, crate::windows::supervise::CaptureFailure> {
    windows_capture_with(command, root, args, |_| Duration::ZERO)
}

#[cfg(target_os = "windows")]
fn windows_capture_with<F>(
    command: &[OsString],
    root: &Path,
    args: &ExecutionArgs,
    delay_for: F,
) -> Result<CompleteRecord, crate::windows::supervise::CaptureFailure>
where
    F: Fn(&crate::windows::Frame) -> Duration + Send + Sync + 'static,
{
    use std::{os::windows::io::AsHandle, process::Stdio};

    use crate::windows::supervise::{CaptureFailure, Limits};

    let Some(program) = command.first() else {
        return Err(CaptureFailure::Spawn);
    };
    let mut child = fspy::Command::new(program);
    child.args(&command[1..]).envs(std::env::vars_os());
    let stderr = io::stderr()
        .as_handle()
        .try_clone_to_owned()
        .map_err(|_| CaptureFailure::Spawn)?;
    child.stdout(Stdio::from(stderr)).stderr(Stdio::inherit());
    let _signals = WindowsSignals::new()?;
    crate::windows::supervise::capture_with_delay(
        child,
        root,
        Limits {
            max_events: args.max_events,
            max_bytes: args.max_bytes,
            timeout: args.timeout,
            kill_after: args.kill_after,
        },
        &WINDOWS_CANCELLED,
        delay_for,
    )
}

#[cfg(target_os = "windows")]
fn windows_capture_status(
    failure: crate::windows::supervise::CaptureFailure,
    action: &'static str,
) -> i32 {
    use crate::windows::supervise::CaptureFailure;
    diagnostic(&failure.to_string(), action);
    match failure {
        CaptureFailure::Timeout => 124,
        CaptureFailure::Cancellation => 130,
        _ => 1,
    }
}

#[cfg(target_os = "windows")]
fn windows_incomplete_record(
    root: &Path,
    failure: crate::windows::supervise::CaptureFailure,
) -> CompleteRecord {
    use std::os::windows::ffi::OsStrExt;

    use crate::{
        record::{Backend, CoverageBoundary, FailureClass, Header, Platform, Summary},
        windows::supervise::CaptureFailure,
    };

    let classification = match failure {
        CaptureFailure::Initialization | CaptureFailure::Spawn => FailureClass::TraceInitialization,
        CaptureFailure::TraceLoss | CaptureFailure::DescendantSurvived | CaptureFailure::Record => {
            FailureClass::TraceLoss
        }
        CaptureFailure::Timeout => FailureClass::Timeout,
        CaptureFailure::Cancellation => FailureClass::Cancellation,
        CaptureFailure::Cleanup => FailureClass::Cleanup,
    };
    CompleteRecord {
        header: Header {
            schema_version: record::SCHEMA_VERSION,
            execution_id: uuid::Uuid::now_v7(),
            platform: Platform::Windows,
            backend: Backend::Injection,
            root: NativePath::WindowsUtf16(root.as_os_str().encode_wide().collect()),
            coverage: CoverageBoundary::SynchronousFileOperationsV1,
        },
        operations: Vec::new(),
        summary: Summary {
            complete: false,
            child_exit_code: None,
            child_signal: None,
            operation_count: 0,
            failure_count: 0,
            failure: Some(classification),
        },
    }
}

#[cfg(target_os = "windows")]
fn windows_record(args: RecordArgs) -> i32 {
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "record"),
    };
    let capture = windows_capture(&args.command, &root, &args.execution);
    let (record, failure) = match capture {
        Ok(record) => (record, None),
        Err(failure) => (windows_incomplete_record(&root, failure), Some(failure)),
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
    failure.map_or_else(
        || child_status(&record),
        |failure| windows_capture_status(failure, "record"),
    )
}

#[cfg(target_os = "windows")]
fn windows_assetcov(args: AssetcovArgs) -> i32 {
    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "assetcov"),
    };
    let denominator = match coverage::select_existing(&root, &args.include, &args.exclude) {
        Ok(value) => value,
        Err(error) => return diagnostic(&error.to_string(), "assetcov"),
    };
    let record = match windows_capture(&args.command, &root, &args.execution) {
        Ok(record) => record,
        Err(failure) => return windows_capture_status(failure, "assetcov"),
    };
    let report = match coverage::analyze(&denominator, &record) {
        Ok(report) => report,
        Err(error) => return diagnostic(&error.to_string(), "assetcov"),
    };
    if !args.quiet {
        let mut encoded = if args.json {
            match serde_json::to_vec_pretty(&serde_json::json!({
                "covered": report.covered.iter().map(|file| &file.logical).collect::<Vec<_>>(),
                "uncovered": report.uncovered.iter().map(|file| &file.logical).collect::<Vec<_>>(),
                "covered_count": report.covered.len(),
                "total_count": denominator.files.len(),
                "percentage": report.percentage,
                "child_exit_code": record.summary.child_exit_code,
            })) {
                Ok(bytes) => bytes,
                Err(_) => return diagnostic("report_encode", "assetcov"),
            }
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

#[cfg(target_os = "windows")]
fn windows_latencylab(args: LatencyArgs) -> i32 {
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => std::sync::Arc::new(selector),
        Err(error) => return diagnostic(&error.to_string(), "latencylab"),
    };
    latencylab_with(args, move |command, root, execution, _, kinds, delay| {
        let selector = std::sync::Arc::clone(&selector);
        let kinds = kinds.to_vec();
        windows_capture_with(command, root, execution, move |frame| {
            let Some(operation) = crate::windows::operation_id(frame.operation) else {
                return Duration::ZERO;
            };
            let Some(path) = frame.access_path.as_ref() else {
                return Duration::ZERO;
            };
            if matches_selected(operation, std::slice::from_ref(path), &selector, &kinds) {
                delay
            } else {
                Duration::ZERO
            }
        })
        .map_err(|failure| windows_capture_status(failure, "latencylab"))
    })
}

#[cfg(target_os = "windows")]
fn windows_autowatch(args: AutowatchArgs) -> i32 {
    use std::{process::Stdio, sync::atomic::Ordering};

    let root = match execution_root(&args.execution) {
        Ok(root) => root,
        Err(error) => return diagnostic(error, "autowatch"),
    };
    let selector = match coverage::Selector::new(&args.include, &args.exclude) {
        Ok(selector) => selector,
        Err(error) => return diagnostic(&error.to_string(), "autowatch"),
    };
    let _signals = match WindowsSignals::new() {
        Ok(signals) => signals,
        Err(error) => return windows_capture_status(error, "autowatch"),
    };
    let mut watcher = crate::watch::WatchSession::new(root.clone());
    let mut previous = crate::watch::Dependencies::default();
    loop {
        if let Err(error) = watcher.start_discovery() {
            return diagnostic(&error.to_string(), "autowatch");
        }
        let mut child = fspy::Command::new(&args.command[0]);
        child
            .args(&args.command[1..])
            .envs(std::env::vars_os())
            .stdin(Stdio::inherit())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());
        let record = crate::windows::supervise::capture(
            child,
            &root,
            crate::windows::supervise::Limits {
                max_events: args.execution.max_events,
                max_bytes: args.execution.max_bytes,
                timeout: args.execution.timeout,
                kill_after: args.execution.kill_after,
            },
            &WINDOWS_CANCELLED,
        );
        let record = match record {
            Ok(record) => record,
            Err(error) => return windows_capture_status(error, "autowatch"),
        };
        let mut dependencies = crate::watch::Dependencies::from_record(&record, &selector);
        if child_status(&record) != 0 {
            dependencies.merge(previous);
        }
        if let Err(error) = watcher.install(&dependencies) {
            return diagnostic(&error.to_string(), "autowatch");
        }
        previous = dependencies;
        loop {
            if WINDOWS_CANCELLED.load(Ordering::SeqCst) {
                return 130;
            }
            match watcher.collect(&previous, args.debounce, Duration::from_millis(100)) {
                Ok(true) => break,
                Ok(false) => {}
                Err(error) => return diagnostic(&error.to_string(), "autowatch"),
            }
        }
    }
}

pub fn execute(command: Command) -> i32 {
    match command {
        Command::Compare(args) => compare(args),
        Command::Autowatch(args) => {
            #[cfg(target_os = "linux")]
            {
                autowatch(args)
            }
            #[cfg(target_os = "macos")]
            {
                macos_autowatch(args)
            }
            #[cfg(target_os = "windows")]
            {
                windows_autowatch(args)
            }
            #[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
            {
                let _ = args;
                diagnostic("unsupported_target", "autowatch")
            }
        }
        Command::Record(args) => {
            #[cfg(target_os = "linux")]
            {
                record(args)
            }
            #[cfg(target_os = "macos")]
            {
                macos_record(args)
            }
            #[cfg(target_os = "windows")]
            {
                windows_record(args)
            }
            #[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
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
            #[cfg(target_os = "macos")]
            {
                macos_assetcov(args)
            }
            #[cfg(target_os = "windows")]
            {
                windows_assetcov(args)
            }
            #[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
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
            #[cfg(target_os = "macos")]
            {
                macos_latencylab(args)
            }
            #[cfg(target_os = "windows")]
            {
                windows_latencylab(args)
            }
            #[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
            {
                let _ = args;
                diagnostic("unsupported_target", "latencylab")
            }
        }
        Command::MinRepro(args) => {
            #[cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]
            {
                min_repro(args)
            }
            #[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
            {
                let _ = args;
                diagnostic("unsupported_target", "min-repro")
            }
        }
        Command::Fbreak(args) => {
            #[cfg(target_os = "linux")]
            {
                fbreak(args)
            }
            #[cfg(target_os = "macos")]
            {
                macos_fbreak(args)
            }
            #[cfg(target_os = "windows")]
            {
                windows_fbreak(args)
            }
            #[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
            {
                let _ = args;
                diagnostic("unsupported_target", "fbreak")
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
    fn failed_record_publishes_framed_incomplete_result() {
        let directory = tempfile::tempdir().unwrap();
        let output = directory.path().join("failed.ndjson");
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("record"),
            OsString::from("--root"),
            directory.path().as_os_str().to_os_string(),
            OsString::from("--output"),
            output.as_os_str().to_os_string(),
            OsString::from("--"),
            OsString::from("./missing-command"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 1);
        let bytes = fs::read(output).unwrap();
        assert_eq!(bytes.iter().filter(|byte| **byte == b'\n').count(), 2);
        assert!(matches!(
            record::parse(
                BufReader::new(bytes.as_slice()),
                record::DEFAULT_EVENT_LIMIT,
                record::DEFAULT_BYTE_LIMIT
            ),
            Err(record::ParseFailure::Incomplete)
        ));
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

    #[cfg(any(target_os = "linux", target_os = "macos"))]
    #[test]
    fn verified_reproduction_uses_a_separate_working_directory() {
        #[cfg(target_os = "linux")]
        let _trace_lock = crate::linux::TRACE_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
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

    #[cfg(any(target_os = "linux", target_os = "macos"))]
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
        ];
        #[cfg(target_os = "linux")]
        arguments.extend([OsString::from("/bin/sh"), OsString::from("-c")]);
        #[cfg(target_os = "macos")]
        {
            std::env::set_var("CLIBOX_FSPY_REPRO_ROOT", &root);
            arguments.extend([
                std::env::current_exe().unwrap().into_os_string(),
                OsString::from("--exact"),
                OsString::from("cli::tests::repro_workload_fixture"),
                OsString::from("--nocapture"),
            ]);
        }
        #[cfg(target_os = "linux")]
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

    #[cfg(target_os = "macos")]
    #[test]
    fn repro_workload_fixture() {
        let Some(case) = std::env::var_os("CLIBOX_FSPY_REPRO_CHILD") else {
            return;
        };
        let root = PathBuf::from(std::env::var_os("CLIBOX_FSPY_REPRO_ROOT").unwrap());
        let path = match case.to_str().unwrap() {
            "blocked" => PathBuf::from(".env"),
            "original" | "metadata" => root.join("input.txt"),
            _ => PathBuf::from("input.txt"),
        };
        if case == "metadata" {
            fs::metadata(path).unwrap();
        } else {
            fs::read(path).unwrap();
        }
        eprintln!("EXPECTED");
        std::process::exit(42);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn breakpoint_controls_release_or_cancel_a_real_traced_child() {
        use std::{
            io::{Read, Write},
            os::{fd::FromRawFd, unix::process::CommandExt},
            thread,
            time::{Duration, Instant},
        };
        // Linux ptrace waitpid(-1) can consume another test's child status.
        // Keep the fixture process outside concurrent in-process trace sessions.
        let _trace_lock = crate::linux::TRACE_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("input.txt"), b"fixture").unwrap();
        for (case, key) in [("continue", b'c'), ("quit", b'q')] {
            let mut master = 0_i32;
            let mut slave = 0_i32;
            // SAFETY: openpty fills both descriptors or returns an error.
            assert_eq!(
                unsafe {
                    libc::openpty(
                        &raw mut master,
                        &raw mut slave,
                        std::ptr::null_mut(),
                        std::ptr::null_mut(),
                        std::ptr::null_mut(),
                    )
                },
                0
            );
            // SAFETY: openpty returned owned descriptors, transferred to File.
            let mut master = unsafe { fs::File::from_raw_fd(master) };
            let slave = unsafe { fs::File::from_raw_fd(slave) };
            let stdout = slave.try_clone().unwrap();
            let stderr = slave.try_clone().unwrap();
            let mut child = std::process::Command::new(std::env::current_exe().unwrap());
            child
                .arg("--exact")
                .arg("cli::tests::breakpoint_child_process")
                .env("CLIBOX_FSPY_BREAK_CHILD", case)
                .current_dir(directory.path())
                .stdin(std::process::Stdio::from(slave))
                .stdout(std::process::Stdio::from(stdout))
                .stderr(std::process::Stdio::from(stderr));
            // SAFETY: pre_exec uses only async-signal-safe libc calls to give
            // the test child its own controlling pseudoterminal.
            unsafe {
                child.pre_exec(|| {
                    if libc::setsid() < 0 || libc::ioctl(0, libc::TIOCSCTTY as _, 0) < 0 {
                        return Err(io::Error::last_os_error());
                    }
                    Ok(())
                });
            }
            let mut child = child.spawn().unwrap();
            thread::sleep(Duration::from_millis(200));
            master.write_all(&[key]).unwrap();
            let deadline = Instant::now() + Duration::from_secs(5);
            let status = loop {
                if let Some(status) = child.try_wait().unwrap() {
                    break status;
                }
                if Instant::now() >= deadline {
                    child.kill().unwrap();
                    panic!("breakpoint child did not finish");
                }
                thread::sleep(Duration::from_millis(10));
            };
            let fd = std::os::fd::AsRawFd::as_raw_fd(&master);
            // SAFETY: fcntl operates on the owned pty master descriptor.
            unsafe {
                libc::fcntl(fd, libc::F_SETFL, libc::O_NONBLOCK);
            }
            let mut output = Vec::new();
            let _ = master.read_to_end(&mut output);
            assert!(status.success(), "{}", String::from_utf8_lossy(&output));
            assert!(output
                .windows(b"break:".len())
                .any(|window| window == b"break:"));
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn breakpoint_child_process() {
        let Some(case) = std::env::var_os("CLIBOX_FSPY_BREAK_CHILD") else {
            return;
        };
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("fbreak"),
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--"),
            OsString::from("/bin/cat"),
            OsString::from("input.txt"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), if case == "quit" { 130 } else { 0 });
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn autowatch_reruns_after_an_observed_input_changes() {
        use std::{process::Stdio, thread, time::Instant};
        let _trace_lock = crate::linux::TRACE_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("input.txt"), b"first").unwrap();
        let mut child = std::process::Command::new(std::env::current_exe().unwrap())
            .arg("--exact")
            .arg("cli::tests::autowatch_child_process")
            .env("CLIBOX_FSPY_WATCH_CHILD", "1")
            .current_dir(directory.path())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let runs = directory.path().join("runs.txt");
        for expected in [1, 2] {
            let deadline = Instant::now() + Duration::from_secs(10);
            while fs::read(&runs).unwrap_or_default().len() < expected {
                if Instant::now() >= deadline {
                    child.kill().unwrap();
                    let output = child.wait_with_output().unwrap();
                    panic!(
                        "autowatch did not complete run {expected}: {}",
                        String::from_utf8_lossy(&output.stderr)
                    );
                }
                thread::sleep(Duration::from_millis(10));
            }
            if expected == 1 {
                thread::sleep(Duration::from_millis(400));
                fs::write(directory.path().join("input.txt"), b"second").unwrap();
            }
        }
        thread::sleep(Duration::from_millis(400));
        assert_eq!(fs::read(&runs).unwrap().len(), 2);
        // SAFETY: the child process is still owned by this test.
        unsafe { libc::kill(child.id() as i32, libc::SIGINT) };
        let status = child.wait().unwrap();
        assert!(status.success());
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn autowatch_reruns_when_a_missing_input_is_created() {
        use std::{process::Stdio, thread, time::Instant};
        let _trace_lock = crate::linux::TRACE_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        let directory = tempfile::tempdir().unwrap();
        let mut child = std::process::Command::new(std::env::current_exe().unwrap())
            .arg("--exact")
            .arg("cli::tests::autowatch_child_process")
            .env("CLIBOX_FSPY_WATCH_CHILD", "missing")
            .current_dir(directory.path())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let runs = directory.path().join("runs.txt");
        for expected in [1, 2] {
            let deadline = Instant::now() + Duration::from_secs(10);
            while fs::read(&runs).unwrap_or_default().len() < expected {
                if Instant::now() >= deadline {
                    child.kill().unwrap();
                    let output = child.wait_with_output().unwrap();
                    panic!(
                        "autowatch missed absent input on run {expected}: {}",
                        String::from_utf8_lossy(&output.stderr)
                    );
                }
                thread::sleep(Duration::from_millis(10));
            }
            if expected == 1 {
                thread::sleep(Duration::from_millis(400));
                fs::write(directory.path().join("missing.txt"), b"created").unwrap();
            }
        }
        // SAFETY: the child process is still owned by this test.
        unsafe { libc::kill(child.id() as i32, libc::SIGINT) };
        assert!(child.wait().unwrap().success());
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn autowatch_child_process() {
        let Some(case) = std::env::var_os("CLIBOX_FSPY_WATCH_CHILD") else {
            return;
        };
        let missing = case == "missing";
        let cli = TestCli::try_parse_from([
            "fspy",
            "autowatch",
            "--include",
            if missing { "missing.txt" } else { "input.txt" },
            "--debounce",
            "50ms",
            "--",
            "/bin/sh",
            "-c",
            if missing {
                "test -e missing.txt; printf x >> runs.txt"
            } else {
                "cat input.txt >/dev/null; printf x >> runs.txt"
            },
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 130);
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_record_coverage_and_latency_observe_child_reads() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let executable = std::env::current_exe().unwrap();
        // This fixture uses our own test binary because macOS system binaries
        // may reject dynamic-library injection under platform protections.
        unsafe { std::env::set_var("CLIBOX_FSPY_CLI_MAC_INPUT", &input) };
        let output = directory.path().join("trace.ndjson");
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("record"),
            OsString::from("--root"),
            directory.path().as_os_str().to_owned(),
            OsString::from("--output"),
            output.as_os_str().to_owned(),
            OsString::from("--"),
            executable.as_os_str().to_owned(),
            OsString::from("--exact"),
            OsString::from("cli::tests::macos_cli_read_fixture"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 0);
        let record = load(&output).unwrap();
        assert!(record.operations.iter().any(|pair| {
            pair.start.operation.is_content_read()
                && pair.completion.byte_count.is_some_and(|count| count > 0)
                && pair.start.paths.iter().any(|path| {
                    path.project_relative.as_ref()
                        == Some(&NativePath::UnixBytes(b"input.txt".to_vec()))
                })
        }));

        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("assetcov"),
            OsString::from("--root"),
            directory.path().as_os_str().to_owned(),
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--fail-under"),
            OsString::from("100"),
            OsString::from("--quiet"),
            OsString::from("--"),
            executable.as_os_str().to_owned(),
            OsString::from("--exact"),
            OsString::from("cli::tests::macos_cli_read_fixture"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 0);

        let experiment = directory.path().join("latency.json");
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("latencylab"),
            OsString::from("--root"),
            directory.path().as_os_str().to_owned(),
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--delay"),
            OsString::from("1ms"),
            OsString::from("--runs"),
            OsString::from("1"),
            OsString::from("--json"),
            OsString::from("--output"),
            experiment.as_os_str().to_owned(),
            OsString::from("--"),
            executable.into_os_string(),
            OsString::from("--exact"),
            OsString::from("cli::tests::macos_cli_read_fixture"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 0);
        let report: serde_json::Value =
            serde_json::from_slice(&fs::read(experiment).unwrap()).unwrap();
        assert_eq!(report["runs"][0]["condition"], "baseline");
        assert_eq!(report["runs"][1]["condition"], "delayed");
        assert!(report["runs"][1]["observed_delay_ns"].as_u64().unwrap() > 0);
        unsafe { std::env::remove_var("CLIBOX_FSPY_CLI_MAC_INPUT") };
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_cli_read_fixture() {
        let Some(input) = std::env::var_os("CLIBOX_FSPY_CLI_MAC_INPUT") else {
            return;
        };
        assert_eq!(fs::read(input).unwrap(), b"fixture");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_breakpoint_controls_a_real_injected_child() {
        use std::{
            io::{Read, Write},
            os::{fd::FromRawFd, unix::process::CommandExt},
            thread,
            time::{Duration, Instant},
        };

        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        for (case, key) in [("continue", b'c'), ("quit", b'q')] {
            let mut master = 0_i32;
            let mut slave = 0_i32;
            // SAFETY: openpty initializes both descriptors on success.
            assert_eq!(
                unsafe {
                    libc::openpty(
                        &raw mut master,
                        &raw mut slave,
                        std::ptr::null_mut(),
                        std::ptr::null_mut(),
                        std::ptr::null_mut(),
                    )
                },
                0
            );
            // SAFETY: ownership transfers from openpty to File.
            let mut master = unsafe { fs::File::from_raw_fd(master) };
            let slave = unsafe { fs::File::from_raw_fd(slave) };
            let stdout = slave.try_clone().unwrap();
            let stderr = slave.try_clone().unwrap();
            let mut child = std::process::Command::new(std::env::current_exe().unwrap());
            child
                .arg("--exact")
                .arg("cli::tests::macos_breakpoint_child")
                .env("CLIBOX_FSPY_MAC_BREAK_CHILD", case)
                .env("CLIBOX_FSPY_CLI_MAC_INPUT", &input)
                .current_dir(directory.path())
                .stdin(std::process::Stdio::from(slave))
                .stdout(std::process::Stdio::from(stdout))
                .stderr(std::process::Stdio::from(stderr));
            // SAFETY: setsid and TIOCSCTTY are async-signal-safe and attach
            // only this child to the new pseudoterminal.
            unsafe {
                child.pre_exec(|| {
                    if libc::setsid() < 0 || libc::ioctl(0, libc::TIOCSCTTY as _, 0) < 0 {
                        return Err(io::Error::last_os_error());
                    }
                    Ok(())
                });
            }
            let mut child = child.spawn().unwrap();
            let fd = std::os::fd::AsRawFd::as_raw_fd(&master);
            // SAFETY: fcntl reads and then updates status flags on this owned
            // pseudoterminal master without changing descriptor ownership.
            let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
            assert!(flags >= 0);
            assert!(unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } >= 0);
            let mut output = Vec::new();
            let mut sent = false;
            let deadline = Instant::now() + Duration::from_secs(10);
            let status = loop {
                let mut buffer = [0_u8; 4096];
                if let Ok(count) = master.read(&mut buffer) {
                    output.extend_from_slice(&buffer[..count]);
                }
                if !sent
                    && output
                        .windows(b"break:".len())
                        .any(|text| text == b"break:")
                {
                    master.write_all(&[key]).unwrap();
                    sent = true;
                }
                if let Some(status) = child.try_wait().unwrap() {
                    break status;
                }
                if Instant::now() >= deadline {
                    child.kill().unwrap();
                    panic!(
                        "macOS breakpoint child did not finish in {case}: {}",
                        String::from_utf8_lossy(&output)
                    );
                }
                thread::sleep(Duration::from_millis(10));
            };
            assert!(
                status.success(),
                "{case}: {}",
                String::from_utf8_lossy(&output)
            );
            assert!(sent, "{case} did not reach a breakpoint");
        }
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_breakpoint_child() {
        let Some(case) = std::env::var_os("CLIBOX_FSPY_MAC_BREAK_CHILD") else {
            return;
        };
        let executable = std::env::current_exe().unwrap();
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("fbreak"),
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--"),
            executable.into_os_string(),
            OsString::from("--exact"),
            OsString::from("cli::tests::macos_cli_read_fixture"),
            OsString::from("--nocapture"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), if case == "quit" { 130 } else { 0 });
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_autowatch_reruns_on_observed_input_change() {
        use std::{process::Stdio, thread, time::Instant};
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("input.txt"), b"first").unwrap();
        let mut child = std::process::Command::new(std::env::current_exe().unwrap())
            .arg("--exact")
            .arg("cli::tests::macos_autowatch_child")
            .env("CLIBOX_FSPY_MAC_WATCH_ROOT", directory.path())
            .current_dir(directory.path())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let runs = directory.path().join("runs.txt");
        for expected in [1, 2] {
            let deadline = Instant::now() + Duration::from_secs(10);
            while fs::read(&runs).unwrap_or_default().len() < expected {
                if Instant::now() >= deadline {
                    child.kill().unwrap();
                    let output = child.wait_with_output().unwrap();
                    panic!(
                        "macOS autowatch missed run {expected}: {}",
                        String::from_utf8_lossy(&output.stderr)
                    );
                }
                thread::sleep(Duration::from_millis(10));
            }
            if expected == 1 {
                thread::sleep(Duration::from_millis(300));
                fs::write(directory.path().join("input.txt"), b"second").unwrap();
            }
        }
        // SAFETY: this PID is the owned test child, not an arbitrary process.
        unsafe { libc::kill(child.id() as i32, libc::SIGINT) };
        assert!(child.wait().unwrap().success());
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_autowatch_child() {
        let Some(root) = std::env::var_os("CLIBOX_FSPY_MAC_WATCH_ROOT") else {
            return;
        };
        let executable = std::env::current_exe().unwrap();
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("autowatch"),
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--debounce"),
            OsString::from("50ms"),
            OsString::from("--root"),
            root,
            OsString::from("--"),
            executable.into_os_string(),
            OsString::from("--exact"),
            OsString::from("cli::tests::macos_watch_worker"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 130);
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_watch_worker() {
        if std::env::var_os("CLIBOX_FSPY_MAC_WATCH_ROOT").is_none() {
            return;
        }
        assert!(!fs::read("input.txt").unwrap().is_empty());
        use std::io::Write;
        fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open("runs.txt")
            .unwrap()
            .write_all(b"x")
            .unwrap();
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_rename_and_link_record_both_paths() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("source.txt"), b"fixture").unwrap();
        let output = directory.path().join("mutation.ndjson");
        let executable = std::env::current_exe().unwrap();
        unsafe { std::env::set_var("CLIBOX_FSPY_WIN_MUTATION_ROOT", directory.path()) };
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("record"),
            OsString::from("--root"),
            directory.path().as_os_str().to_owned(),
            OsString::from("--output"),
            output.as_os_str().to_owned(),
            OsString::from("--"),
            executable.into_os_string(),
            OsString::from("--exact"),
            OsString::from("cli::tests::windows_mutation_worker"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 0);
        unsafe { std::env::remove_var("CLIBOX_FSPY_WIN_MUTATION_ROOT") };
        let record = load(&output).unwrap();
        let relative = |name: &str| NativePath::WindowsUtf16(name.encode_utf16().collect());
        for (source, destination) in [("source.txt", "renamed.txt"), ("renamed.txt", "linked.txt")]
        {
            assert!(record.operations.iter().any(|pair| {
                pair.start.operation == crate::record::Operation::Mutation
                    && pair.completion.native_error.is_none()
                    && pair.start.paths.len() == 2
                    && pair.start.paths[0].project_relative.as_ref() == Some(&relative(source))
                    && pair.start.paths[1].project_relative.as_ref() == Some(&relative(destination))
            }));
        }
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_mutation_worker() {
        let Some(root) = std::env::var_os("CLIBOX_FSPY_WIN_MUTATION_ROOT") else {
            return;
        };
        let root = std::path::PathBuf::from(root);
        fs::rename(root.join("source.txt"), root.join("renamed.txt")).unwrap();
        fs::hard_link(root.join("renamed.txt"), root.join("linked.txt")).unwrap();
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_record_and_coverage_observe_native_reads() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let executable = std::env::current_exe().unwrap();
        // The self-owned test binary is reliably injectable on CI runners.
        unsafe { std::env::set_var("CLIBOX_FSPY_CLI_WIN_INPUT", &input) };
        let output = directory.path().join("trace.ndjson");
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("record"),
            OsString::from("--root"),
            directory.path().as_os_str().to_owned(),
            OsString::from("--output"),
            output.as_os_str().to_owned(),
            OsString::from("--"),
            executable.as_os_str().to_owned(),
            OsString::from("--exact"),
            OsString::from("cli::tests::windows_cli_read_fixture"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 0);
        let record = load(&output).unwrap();
        assert!(record.operations.iter().any(|pair| {
            pair.start.operation.is_content_read()
                && pair.completion.byte_count.is_some_and(|count| count > 0)
                && pair.start.paths.iter().any(|path| {
                    path.project_relative.as_ref()
                        == Some(&NativePath::WindowsUtf16(
                            "input.txt".encode_utf16().collect(),
                        ))
                })
        }));

        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("assetcov"),
            OsString::from("--root"),
            directory.path().as_os_str().to_owned(),
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--fail-under"),
            OsString::from("100"),
            OsString::from("--quiet"),
            OsString::from("--"),
            executable.into_os_string(),
            OsString::from("--exact"),
            OsString::from("cli::tests::windows_cli_read_fixture"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 0);
        unsafe { std::env::remove_var("CLIBOX_FSPY_CLI_WIN_INPUT") };
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_cli_read_fixture() {
        let Some(input) = std::env::var_os("CLIBOX_FSPY_CLI_WIN_INPUT") else {
            return;
        };
        assert_eq!(fs::read(input).unwrap(), b"fixture");
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_verified_reproduction_uses_a_separate_working_directory() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().join("project");
        fs::create_dir(&root).unwrap();
        fs::write(root.join("input.txt"), b"fixture").unwrap();
        fs::write(root.join(".env"), b"secret").unwrap();
        let bundle = directory.path().join("bundle");
        let output = std::process::Command::new(std::env::current_exe().unwrap())
            .arg("--exact")
            .arg("cli::tests::windows_reproduction_cli_child")
            .env("CLIBOX_FSPY_WIN_REPRO_ROOT", &root)
            .env("CLIBOX_FSPY_WIN_REPRO_BUNDLE", &bundle)
            .current_dir(&root)
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert_eq!(fs::read(bundle.join("input.txt")).unwrap(), b"fixture");
        assert!(bundle.join(".clibox-fspy-repro/manifest.json").exists());
        assert!(!bundle.join(".env").exists());
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_reproduction_cli_child() {
        let Some(root) = std::env::var_os("CLIBOX_FSPY_WIN_REPRO_ROOT") else {
            return;
        };
        let bundle = std::env::var_os("CLIBOX_FSPY_WIN_REPRO_BUNDLE").unwrap();
        let cli = TestCli::try_parse_from([
            OsString::from("fspy"),
            OsString::from("min-repro"),
            OsString::from("--root"),
            root,
            OsString::from("--include"),
            OsString::from("input.txt"),
            OsString::from("--bundle-dir"),
            bundle,
            OsString::from("--expect-exit"),
            OsString::from("42"),
            OsString::from("--expect-stderr"),
            OsString::from("EXPECTED"),
            OsString::from("--quiet"),
            OsString::from("--"),
            std::env::current_exe().unwrap().into_os_string(),
            OsString::from("--exact"),
            OsString::from("cli::tests::windows_reproduction_workload"),
            OsString::from("--nocapture"),
        ])
        .unwrap();
        assert_eq!(execute(cli.command), 0);
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_reproduction_workload() {
        if std::env::var_os("CLIBOX_FSPY_WIN_REPRO_ROOT").is_none() {
            return;
        }
        assert_eq!(fs::read("input.txt").unwrap(), b"fixture");
        eprintln!("EXPECTED");
        std::process::exit(42);
    }
}
