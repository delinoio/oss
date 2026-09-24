#[cfg(target_os = "linux")]
use std::{
    collections::{BTreeMap, BTreeSet},
    ffi::{CString, OsStr},
    fs, io,
    os::unix::ffi::OsStrExt as _,
    path::{Component, Path},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
};
use std::{ffi::OsString, path::PathBuf, time::Duration};

use clap::Args;
#[cfg(target_os = "linux")]
use serde::Serialize;
#[cfg(target_os = "linux")]
use uuid::Uuid;

use crate::{
    cli::{ReportOptions, fail, parse_duration, parse_positive},
    output,
    selector::Selector,
    trace,
};
#[cfg(target_os = "linux")]
use crate::{
    snapshot::{self, Snapshot, SnapshotError},
    trace::{Completion, EncodedPath, Event, Operation, PathEncoding, PathScope, Start},
};

#[derive(Args)]
pub struct MinRepro {
    /// Existing root for selected inputs; original child cwd is unchanged.
    #[arg(long, value_name = "DIR")]
    root: Option<PathBuf>,
    /// Required project-relative input globs; repeat to union selections.
    #[arg(long, required = true, value_name = "GLOB")]
    include: Vec<String>,
    /// Project-relative input globs to exclude.
    #[arg(long, value_name = "GLOB")]
    exclude: Vec<String>,
    /// New destination directory for the verified bundle.
    #[arg(long, required = true, value_name = "DIR")]
    bundle_dir: PathBuf,
    /// Required nonzero child exit code for original and candidate.
    #[arg(long, required = true, value_parser = parse_expected_exit, value_name = "CODE")]
    expect_exit: i32,
    /// Required fixed substring of child stderr; never stored in the bundle.
    #[arg(long, required = true, value_parser = parse_nonempty, value_name = "TEXT")]
    expect_stderr: String,
    #[command(flatten)]
    report: ReportOptions,
    /// Optional execution budget for each of the two runs.
    #[arg(long, value_parser = parse_duration, value_name = "DURATION")]
    timeout: Option<Duration>,
    /// Grace period before forceful descendant termination.
    #[arg(long, default_value = "5s", value_parser = parse_duration, value_name = "DURATION")]
    kill_after: Duration,
    /// Maximum traced operations per run.
    #[arg(long, default_value_t = trace::DEFAULT_MAX_EVENTS, value_parser = parse_positive)]
    max_events: usize,
    /// Maximum encoded trace bytes per run.
    #[arg(long, default_value_t = trace::DEFAULT_MAX_BYTES, value_parser = parse_positive)]
    max_trace_bytes: usize,
    /// Maximum private snapshot bytes (default: 1 GiB).
    #[arg(long, default_value_t = 1024 * 1024 * 1024, value_parser = parse_positive_u64)]
    max_snapshot_bytes: u64,
    /// Maximum private snapshot files and links (default: 100000).
    #[arg(long, default_value_t = 100_000, value_parser = parse_positive)]
    max_snapshot_files: usize,
    /// Maximum result bytes including metadata (default: 1 GiB).
    #[arg(long, default_value_t = 1024 * 1024 * 1024, value_parser = parse_positive_u64)]
    max_result_bytes: u64,
    /// Maximum result files and links including metadata (default: 100000).
    #[arg(long, default_value_t = 100_000, value_parser = parse_positive)]
    max_result_files: usize,
    /// Command and literal arguments; use -- before COMMAND.
    #[arg(last = true, required = true, num_args = 1.., value_name = "COMMAND [ARG...]")]
    command: Vec<OsString>,
}

fn parse_expected_exit(value: &str) -> Result<i32, &'static str> {
    value
        .parse::<i32>()
        .ok()
        .filter(|code| (1..=255).contains(code))
        .ok_or("Use an expected exit code from 1 through 255.")
}

fn parse_nonempty(value: &str) -> Result<String, &'static str> {
    if value.is_empty() {
        Err("Expected stderr substring must be nonempty.")
    } else {
        Ok(value.to_owned())
    }
}

fn parse_positive_u64(value: &str) -> Result<u64, &'static str> {
    value
        .parse::<u64>()
        .ok()
        .filter(|number| *number > 0)
        .ok_or("Provide a positive integer.")
}

#[cfg(target_os = "linux")]
#[derive(Serialize)]
struct BundleFile {
    path: EncodedPath,
    sha256: String,
    size: u64,
}

#[cfg(target_os = "linux")]
#[derive(Serialize)]
struct BundleLink {
    path: EncodedPath,
    target: EncodedPath,
}

#[cfg(target_os = "linux")]
#[derive(Serialize)]
struct BundleManifest {
    schema_version: u32,
    files: Vec<BundleFile>,
    links: Vec<BundleLink>,
    absent_project_paths: Vec<EncodedPath>,
    generated_project_paths: Vec<EncodedPath>,
    external_dependencies: Vec<EncodedPath>,
}

#[cfg(target_os = "linux")]
#[derive(Serialize)]
struct BundleReport {
    bundle: EncodedPath,
    collected_files: usize,
    collected_links: usize,
    external_dependencies: usize,
    total_bytes: u64,
}

#[cfg(target_os = "linux")]
struct Inputs {
    required: BTreeSet<PathBuf>,
    absent: BTreeSet<PathBuf>,
    external: BTreeSet<EncodedPath>,
    generated: BTreeSet<PathBuf>,
}

#[expect(
    clippy::too_many_lines,
    reason = "Keep the two-run verification and atomic publication in one flow"
)]
pub fn execute(options: MinRepro) -> i32 {
    let destination =
        match output::destination(options.report.output.as_ref(), options.report.force) {
            Ok(destination) => destination,
            Err(message) => return fail(2, "invalid_output", message),
        };
    let root = match options.root {
        Some(root) => root,
        None => match std::env::current_dir() {
            Ok(root) => root,
            Err(_) => {
                return fail(
                    1,
                    "working_directory",
                    "Cannot resolve the current directory.",
                );
            }
        },
    };
    let selector = match Selector::new(&root, &options.include, &options.exclude) {
        Ok(selector) => selector,
        Err(message) => return fail(2, "invalid_selector", message),
    };
    let Some((program, arguments)) = options.command.split_first() else {
        return fail(2, "missing_command", "Provide a command after --.");
    };
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (selector, program, arguments, destination);
        fail(
            1,
            "tracing_unavailable",
            "This platform has no complete file-operation backend yet.",
        )
    }
    #[cfg(target_os = "linux")]
    {
        let bundle = match bundle_destination(&options.bundle_dir) {
            Ok(bundle) => bundle,
            Err(message) => return fail(2, "invalid_bundle_dir", message),
        };
        if destination.is_some_and(|output| {
            let absolute = if output.is_absolute() {
                output.to_path_buf()
            } else {
                std::env::current_dir().unwrap_or_default().join(output)
            };
            absolute.starts_with(&bundle)
        }) {
            return fail(
                2,
                "invalid_output",
                "Report output must be outside the new bundle directory.",
            );
        }
        let cancellation = Arc::new(AtomicBool::new(false));
        let termination = Arc::new(AtomicBool::new(false));
        if signal_hook::flag::register(signal_hook::consts::SIGINT, Arc::clone(&cancellation))
            .is_err()
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
        tracing::info!(
            command = "min-repro",
            stage = "snapshot_start",
            "fspy_execution"
        );
        let snapshot = match Snapshot::create(
            &selector,
            options.max_snapshot_bytes,
            options.max_snapshot_files,
        ) {
            Ok(snapshot) => snapshot,
            Err(error) => {
                return fail(
                    1,
                    error.classification(),
                    "Cannot make a bounded private input snapshot.",
                );
            }
        };
        let selected_physical: BTreeSet<PathBuf> = snapshot
            .files()
            .keys()
            .map(|path| selector.root().join(path))
            .collect();
        tracing::info!(
            command = "min-repro",
            stage = "original_start",
            files = snapshot.files().len(),
            "fspy_execution"
        );
        let original = crate::linux::capture(
            crate::linux::CaptureRequest {
                root: selector.root(),
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
                stderr_match: Some(&options.expect_stderr),
                deny_rule: None,
            },
            &cancellation,
        );
        let Ok(original) = original else {
            return fail(
                1,
                "tracing_unavailable",
                "Cannot launch the original traced execution.",
            );
        };
        if !original.complete() {
            return capture_failure(original.failure.as_ref(), &termination);
        }
        if original.root_status.and_then(|status| status.code()) != Some(options.expect_exit)
            || !original.stderr_matched
        {
            return fail(
                1,
                "original_predicate_mismatch",
                "The original run did not meet both specified failure predicates.",
            );
        }
        let inputs = match gather_inputs(&selector, &selected_physical, &snapshot, &original.events)
        {
            Ok(inputs) => inputs,
            Err(error) => {
                return fail(
                    1,
                    error.classification(),
                    "A required original input cannot be collected safely.",
                );
            }
        };
        let parent = bundle.parent().expect("validated bundle parent");
        let Ok(stage) = tempfile::Builder::new()
            .prefix(".clibox-fspy-repro-")
            .tempdir_in(parent)
        else {
            return fail(
                1,
                "staging_failed",
                "Cannot create a private bundle staging directory.",
            );
        };
        let mut collected_files = BTreeSet::new();
        let mut collected_links = BTreeSet::new();
        for relative in &inputs.required {
            if let Err(error) = snapshot.collect(
                relative,
                stage.path(),
                &mut collected_files,
                &mut collected_links,
            ) {
                return fail(
                    1,
                    error.classification(),
                    "A required input changed or became unavailable before collection.",
                );
            }
        }
        for absent in &inputs.absent {
            if stage.path().join(absent).exists() {
                return fail(
                    1,
                    "unstable_absence",
                    "A path absent in the original run exists in the candidate bundle.",
                );
            }
        }
        let original_root = selector.root().as_os_str().as_bytes().to_vec();
        let candidate_root = stage.path().as_os_str().as_bytes().to_vec();
        let deny_original = |start: &Start| {
            start.paths.iter().any(|path| {
                path.scope == PathScope::External
                    && path.decode().is_ok_and(|bytes| {
                        under_root(&bytes, &original_root) && !under_root(&bytes, &candidate_root)
                    })
            })
        };
        tracing::info!(
            command = "min-repro",
            stage = "candidate_start",
            files = collected_files.len(),
            "fspy_execution"
        );
        let candidate = crate::linux::capture(
            crate::linux::CaptureRequest {
                root: stage.path(),
                program: program.as_os_str(),
                arguments,
                child_io: crate::linux::ChildIo::Report,
                timeout: options.timeout,
                kill_after: options.kill_after,
                max_events: options.max_events,
                max_bytes: options.max_trace_bytes,
                delay_rule: None,
                break_control: None,
                child_cwd: Some(stage.path()),
                stderr_match: Some(&options.expect_stderr),
                deny_rule: Some(&deny_original),
            },
            &cancellation,
        );
        let Ok(candidate) = candidate else {
            return fail(
                1,
                "tracing_unavailable",
                "Cannot launch the candidate traced execution.",
            );
        };
        if !candidate.complete() {
            return match candidate.failure {
                Some(crate::linux::LinuxTraceError::CandidateBoundary) => fail(
                    1,
                    "original_tree_access",
                    "The candidate attempted to use the original project tree.",
                ),
                other => capture_failure(other.as_ref(), &termination),
            };
        }
        if candidate.root_status.and_then(|status| status.code()) != Some(options.expect_exit)
            || !candidate.stderr_matched
        {
            return fail(
                1,
                "reproduction_mismatch",
                "The collected candidate did not meet both specified failure predicates.",
            );
        }
        if !candidate_inputs_collected(&candidate.events, stage.path(), &collected_files) {
            return fail(
                1,
                "uncollected_input",
                "The candidate used a project input that was not collected.",
            );
        }
        if let Err(error) = clean_and_limit(
            stage.path(),
            &collected_files,
            &collected_links,
            options.max_result_bytes,
            options.max_result_files,
        ) {
            return fail(
                1,
                error.classification(),
                "The candidate result exceeds its limit or contains unsafe output.",
            );
        }
        for relative in &collected_files {
            let Some(expected) = snapshot.files().get(relative) else {
                return fail(
                    1,
                    "uncollected_input",
                    "An input is missing from the private snapshot.",
                );
            };
            if !snapshot::file_matches(&stage.path().join(relative), expected) {
                return fail(
                    1,
                    "unstable_input",
                    "The candidate changed a collected input.",
                );
            }
        }
        let metadata_dir = stage
            .path()
            .join(format!(".clibox-fspy-bundle-{}", Uuid::now_v7()));
        if fs::create_dir(&metadata_dir).is_err() {
            return fail(
                1,
                "bundle_metadata_failed",
                "Cannot create bundle metadata.",
            );
        }
        let manifest = manifest(&snapshot, &collected_files, &collected_links, &inputs);
        let Ok(manifest_bytes) = serde_json::to_vec_pretty(&manifest) else {
            return fail(
                1,
                "bundle_metadata_failed",
                "Cannot encode bundle metadata.",
            );
        };
        if fs::write(metadata_dir.join("manifest.json"), manifest_bytes).is_err()
            || fs::write(
                metadata_dir.join("README.txt"),
                format!(
                    "Verified observed-input reproduction.\nRun the original command from this \
                     directory using your installed runtime.\nExpected exit code: {}. The \
                     expected stderr substring and complete argv are intentionally not \
                     stored.\nExternal runtime/system dependencies are listed in manifest.json \
                     and are not bundled.\nThis directory is not an OS sandbox or a cross-machine \
                     portable runtime.\n",
                    options.expect_exit
                ),
            )
            .is_err()
        {
            return fail(1, "bundle_metadata_failed", "Cannot write bundle metadata.");
        }
        let total_bytes = match enforce_limits(
            stage.path(),
            options.max_result_bytes,
            options.max_result_files,
        ) {
            Ok(bytes) => bytes,
            Err(error) => {
                return fail(
                    1,
                    error.classification(),
                    "The verified bundle exceeds its result limit.",
                );
            }
        };
        if fs::symlink_metadata(&bundle).is_ok() {
            return fail(
                1,
                "bundle_exists",
                "The bundle directory was created during verification; choose a new destination.",
            );
        }
        if atomic_publish_directory(stage.path(), &bundle).is_err() {
            return fail(
                1,
                "bundle_publish_failed",
                "Cannot atomically publish the verified bundle without replacing an existing \
                 destination.",
            );
        }
        let report = BundleReport {
            bundle: encode_absolute(&bundle),
            collected_files: collected_files.len(),
            collected_links: collected_links.len(),
            external_dependencies: inputs.external.len(),
            total_bytes,
        };
        if !options.report.quiet {
            let bytes = if options.report.json {
                serde_json::to_vec(&report).map(|mut bytes| {
                    bytes.push(b'\n');
                    bytes
                })
            } else {
                Ok(format!(
                    "Verified reproduction: {} files, {} links, {} external dependencies, {} \
                     bytes\n",
                    report.collected_files,
                    report.collected_links,
                    report.external_dependencies,
                    report.total_bytes
                )
                .into_bytes())
            };
            let Ok(bytes) = bytes else {
                return fail(1, "report_encoding", "Cannot encode reproduction report.");
            };
            if output::write(&bytes, destination, options.report.force).is_err() {
                return fail(
                    1,
                    "output_failed",
                    "The bundle was verified, but its report could not be published.",
                );
            }
        }
        tracing::info!(
            command = "min-repro",
            stage = "finish",
            files = report.collected_files,
            bytes = report.total_bytes,
            "fspy_execution"
        );
        if termination.load(Ordering::Relaxed) {
            143
        } else {
            0
        }
    }
}

#[cfg(target_os = "linux")]
fn capture_failure(
    failure: Option<&crate::linux::LinuxTraceError>,
    termination: &AtomicBool,
) -> i32 {
    match failure {
        Some(crate::linux::LinuxTraceError::Timeout) => 124,
        Some(crate::linux::LinuxTraceError::Cancelled) => {
            if termination.load(Ordering::Relaxed) {
                143
            } else {
                130
            }
        }
        _ => fail(
            1,
            "tracing_incomplete",
            "A required execution produced an incomplete trace.",
        ),
    }
}

#[cfg(target_os = "linux")]
fn bundle_destination(value: &Path) -> Result<PathBuf, &'static str> {
    let parent = value
        .parent()
        .filter(|parent| !parent.as_os_str().is_empty())
        .unwrap_or_else(|| Path::new("."));
    let parent =
        fs::canonicalize(parent).map_err(|_| "Bundle parent must be an existing directory.")?;
    if !parent.is_dir() || value.file_name().is_none() {
        return Err("--bundle-dir must name a new directory under an existing parent.");
    }
    let bundle = parent.join(value.file_name().unwrap());
    match fs::symlink_metadata(&bundle) {
        Ok(_) => Err("--bundle-dir must name a new directory."),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(bundle),
        Err(_) => Err("Cannot inspect --bundle-dir."),
    }
}

#[cfg(target_os = "linux")]
fn pairs(events: &[Event]) -> Vec<(&Start, &Completion)> {
    let mut pending = BTreeMap::new();
    let mut pairs = Vec::new();
    for event in events {
        match event {
            Event::OperationStart(start) => {
                pending.insert(start.operation_id, start);
            }
            Event::OperationCompletion(done) => {
                if let Some(start) = pending.remove(&done.operation_id) {
                    pairs.push((start, done));
                }
            }
            Event::Header(_) | Event::Summary(_) => {}
        }
    }
    pairs
}

#[cfg(target_os = "linux")]
fn relative(path: &EncodedPath) -> Option<PathBuf> {
    if path.scope != PathScope::Project || path.encoding != PathEncoding::UnixBytes {
        return None;
    }
    let raw = path.decode().ok()?;
    let value = Path::new(OsStr::from_bytes(&raw));
    if value.is_absolute()
        || value
            .components()
            .any(|part| matches!(part, Component::ParentDir | Component::Prefix(_)))
    {
        None
    } else {
        Some(value.to_path_buf())
    }
}

#[cfg(target_os = "linux")]
fn gather_inputs(
    selector: &Selector,
    selected_physical: &BTreeSet<PathBuf>,
    snapshot: &Snapshot,
    events: &[Event],
) -> Result<Inputs, SnapshotError> {
    let completed = pairs(events);
    let mut writes = BTreeMap::<PathBuf, u64>::new();
    for (start, done) in &completed {
        if done.native_error.is_none()
            && matches!(
                start.operation,
                Operation::Write
                    | Operation::Pwrite
                    | Operation::Create
                    | Operation::Remove
                    | Operation::Rename
                    | Operation::Link
                    | Operation::OtherMutation
            )
        {
            for path in &start.paths {
                if let Some(relative) = relative(path) {
                    writes.entry(relative).or_insert(start.sequence);
                }
            }
        }
    }
    let mut inputs = Inputs {
        required: BTreeSet::new(),
        absent: BTreeSet::new(),
        external: BTreeSet::new(),
        generated: BTreeSet::new(),
    };
    for (start, done) in completed {
        for path in start.paths.iter().chain(&done.resolved_paths) {
            if path.scope == PathScope::External {
                inputs.external.insert(path.clone());
            }
        }
        for path in &start.paths {
            if !selector.matches_trace_path(path, selected_physical) {
                continue;
            }
            let Some(relative) = relative(path) else {
                continue;
            };
            if start.operation == Operation::Open
                && done
                    .resolved_paths
                    .iter()
                    .any(|path| path.scope == PathScope::External)
            {
                return Err(SnapshotError::EscapingLink);
            }
            if start.operation == Operation::Open
                && done.native_error == Some(i64::from(libc::ENOENT))
            {
                inputs.absent.insert(relative);
                continue;
            }
            let needed = match start.operation {
                Operation::Read | Operation::Pread | Operation::Metadata | Operation::Directory => {
                    done.native_error.is_none()
                }
                Operation::Open => done.native_error.is_none() && !writes.contains_key(&relative),
                _ => false,
            };
            if !needed {
                continue;
            }
            if let Some(write_sequence) = writes.get(&relative)
                && *write_sequence < start.sequence
                && matches!(
                    snapshot.verify_required(&relative),
                    Err(SnapshotError::Unavailable)
                )
            {
                inputs.generated.insert(relative);
                continue;
            }
            snapshot.verify_required(&relative)?;
            inputs.required.insert(relative);
        }
    }
    Ok(inputs)
}

#[cfg(target_os = "linux")]
fn candidate_inputs_collected(events: &[Event], stage: &Path, files: &BTreeSet<PathBuf>) -> bool {
    let completed = pairs(events);
    let mut writes = BTreeMap::<PathBuf, u64>::new();
    for (start, done) in &completed {
        if done.native_error.is_none()
            && matches!(
                start.operation,
                Operation::Write
                    | Operation::Pwrite
                    | Operation::Create
                    | Operation::Remove
                    | Operation::Rename
                    | Operation::Link
                    | Operation::OtherMutation
            )
        {
            for path in &start.paths {
                if let Some(relative) = relative(path) {
                    writes.entry(relative).or_insert(start.sequence);
                }
            }
        }
    }
    for (start, done) in completed {
        if done.native_error.is_some() {
            continue;
        }
        if !matches!(
            start.operation,
            Operation::Read
                | Operation::Pread
                | Operation::Open
                | Operation::Metadata
                | Operation::Directory
        ) {
            continue;
        }
        for path in &start.paths {
            let Some(relative) = relative(path) else {
                continue;
            };
            if relative == Path::new(".") {
                continue;
            }
            if start.operation == Operation::Open && writes.contains_key(&relative) {
                continue;
            }
            if writes
                .get(&relative)
                .is_some_and(|sequence| *sequence < start.sequence)
            {
                continue;
            }
            // Check the physical target as well as the observed spelling. A
            // collected link alone is not evidence that its target was copied.
            let Ok(physical) = fs::canonicalize(stage.join(&relative)) else {
                return false;
            };
            let Ok(physical) = physical.strip_prefix(stage) else {
                return false;
            };
            if files.contains(physical)
                || (stage.join(physical).is_dir()
                    && files.iter().any(|file| file.starts_with(physical)))
                || writes
                    .get(physical)
                    .is_some_and(|sequence| *sequence < start.sequence)
            {
                continue;
            }
            return false;
        }
    }
    true
}

#[cfg(target_os = "linux")]
fn under_root(path: &[u8], root: &[u8]) -> bool {
    path == root
        || (path.starts_with(root) && (root.ends_with(b"/") || path.get(root.len()) == Some(&b'/')))
}

#[cfg(target_os = "linux")]
fn encoded(relative: &Path) -> EncodedPath {
    EncodedPath::from_raw(
        PathScope::Project,
        PathEncoding::UnixBytes,
        relative.as_os_str().as_bytes(),
    )
}

#[cfg(target_os = "linux")]
fn encode_absolute(path: &Path) -> EncodedPath {
    EncodedPath::from_raw(
        PathScope::External,
        PathEncoding::UnixBytes,
        path.as_os_str().as_bytes(),
    )
}

#[cfg(target_os = "linux")]
fn manifest(
    snapshot: &Snapshot,
    files: &BTreeSet<PathBuf>,
    links: &BTreeSet<PathBuf>,
    inputs: &Inputs,
) -> BundleManifest {
    BundleManifest {
        schema_version: 1,
        files: files
            .iter()
            .filter_map(|path| {
                snapshot.files().get(path).map(|file| BundleFile {
                    path: encoded(path),
                    sha256: file.sha256.clone(),
                    size: file.size,
                })
            })
            .collect(),
        links: links
            .iter()
            .filter_map(|path| {
                snapshot.links().get(path).map(|target| BundleLink {
                    path: encoded(path),
                    target: encoded(target),
                })
            })
            .collect(),
        absent_project_paths: inputs.absent.iter().map(|path| encoded(path)).collect(),
        generated_project_paths: inputs.generated.iter().map(|path| encoded(path)).collect(),
        external_dependencies: inputs.external.iter().cloned().collect(),
    }
}

#[cfg(target_os = "linux")]
fn scan_tree(
    root: &Path,
    max_bytes: u64,
    max_files: usize,
) -> Result<(u64, Vec<PathBuf>, Vec<PathBuf>), SnapshotError> {
    let mut bytes = 0u64;
    let mut count = 0usize;
    let mut entries = Vec::new();
    let mut directories = Vec::new();
    let mut pending = vec![root.to_path_buf()];
    while let Some(directory) = pending.pop() {
        for entry in fs::read_dir(directory)? {
            let entry = entry?;
            let path = entry.path();
            let relative = path
                .strip_prefix(root)
                .map_err(|_| SnapshotError::EscapingLink)?
                .to_path_buf();
            if snapshot::blocked(&relative) {
                return Err(SnapshotError::Blocked);
            }
            let metadata = fs::symlink_metadata(&path)?;
            if metadata.is_dir() {
                pending.push(path);
                directories.push(relative);
            } else if metadata.is_file() || metadata.file_type().is_symlink() {
                count += 1;
                if count > max_files {
                    return Err(SnapshotError::ResourceLimit);
                }
                if metadata.file_type().is_symlink() {
                    bytes = bytes
                        .checked_add(fs::read_link(&path)?.as_os_str().as_bytes().len() as u64)
                        .ok_or(SnapshotError::ResourceLimit)?;
                    if bytes > max_bytes {
                        return Err(SnapshotError::ResourceLimit);
                    }
                    let resolved =
                        fs::canonicalize(&path).map_err(|_| SnapshotError::EscapingLink)?;
                    if !resolved.starts_with(root) {
                        return Err(SnapshotError::EscapingLink);
                    }
                } else {
                    bytes = bytes
                        .checked_add(metadata.len())
                        .ok_or(SnapshotError::ResourceLimit)?;
                    if bytes > max_bytes {
                        return Err(SnapshotError::ResourceLimit);
                    }
                }
                entries.push(relative);
            } else {
                return Err(SnapshotError::Unavailable);
            }
        }
    }
    Ok((bytes, entries, directories))
}

#[cfg(target_os = "linux")]
fn clean_and_limit(
    root: &Path,
    files: &BTreeSet<PathBuf>,
    links: &BTreeSet<PathBuf>,
    max_bytes: u64,
    max_files: usize,
) -> Result<(), SnapshotError> {
    let (_, entries, mut directories) = scan_tree(root, max_bytes, max_files)?;
    for entry in entries {
        if !files.contains(&entry) && !links.contains(&entry) {
            fs::remove_file(root.join(entry))?;
        }
    }
    directories.sort_by_key(|path| std::cmp::Reverse(path.components().count()));
    for directory in directories {
        let _ = fs::remove_dir(root.join(directory));
    }
    Ok(())
}

#[cfg(target_os = "linux")]
fn enforce_limits(root: &Path, max_bytes: u64, max_files: usize) -> Result<u64, SnapshotError> {
    scan_tree(root, max_bytes, max_files).map(|(bytes, _, _)| bytes)
}

#[cfg(target_os = "linux")]
fn atomic_publish_directory(source: &Path, destination: &Path) -> io::Result<()> {
    let source = CString::new(source.as_os_str().as_bytes()).map_err(io::Error::other)?;
    let destination = CString::new(destination.as_os_str().as_bytes()).map_err(io::Error::other)?;
    // SAFETY: both NUL-terminated pathnames are live for the syscall. The
    // no-replace flag prevents a race from overwriting another directory.
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
    if result == -1 {
        Err(io::Error::last_os_error())
    } else {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::{parse_expected_exit, parse_nonempty};

    #[test]
    fn failure_predicates_are_explicit() {
        assert_eq!(parse_expected_exit("1"), Ok(1));
        assert_eq!(parse_expected_exit("255"), Ok(255));
        assert!(parse_expected_exit("0").is_err());
        assert!(parse_expected_exit("256").is_err());
        assert!(parse_nonempty("").is_err());
    }
}
