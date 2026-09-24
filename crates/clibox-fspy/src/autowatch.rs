#[cfg(target_os = "linux")]
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    os::unix::ffi::OsStrExt as _,
    path::{Component, Path},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
        mpsc,
    },
    time::Instant,
};
use std::{ffi::OsString, path::PathBuf, time::Duration};

use clap::Args;
#[cfg(target_os = "linux")]
use notify::{EventKind, RecursiveMode, Watcher as _};

#[cfg(target_os = "linux")]
use crate::trace::{EncodedPath, Event, Operation, PathEncoding, PathScope};
use crate::{
    cli::{fail, parse_duration, parse_positive},
    selector::Selector,
    trace,
};

#[derive(Args)]
pub struct Autowatch {
    /// Existing root for selection; child cwd is unchanged.
    #[arg(long, value_name = "DIR")]
    root: Option<PathBuf>,
    /// Optional project-relative globs; omitted selects all observed project
    /// inputs.
    #[arg(long, value_name = "GLOB")]
    include: Vec<String>,
    /// Project-relative globs to exclude.
    #[arg(long, value_name = "GLOB")]
    exclude: Vec<String>,
    /// Minimum quiet interval after a relevant file change.
    #[arg(long, default_value = "200ms", value_parser = parse_duration, value_name = "DURATION")]
    debounce: Duration,
    /// Optional execution budget for each run.
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
    /// Command and literal arguments; use -- before COMMAND.
    #[arg(last = true, required = true, num_args = 1.., value_name = "COMMAND [ARG...]")]
    command: Vec<OsString>,
}

#[cfg(target_os = "linux")]
#[derive(Clone, Copy, Eq, PartialEq)]
enum DependencyKind {
    File,
    Directory,
}

#[cfg(target_os = "linux")]
type Dependencies = BTreeMap<PathBuf, DependencyKind>;

#[cfg(target_os = "linux")]
struct Observed {
    dependencies: Dependencies,
    writes: BTreeSet<PathBuf>,
}

#[expect(
    clippy::too_many_lines,
    reason = "Keep the serial watch and execution lifecycle together"
)]
pub fn execute(options: Autowatch) -> i32 {
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
    let includes = if options.include.is_empty() {
        vec!["**".to_owned()]
    } else {
        options.include
    };
    let selector = match Selector::new(&root, &includes, &options.exclude) {
        Ok(selector) => selector,
        Err(message) => return fail(2, "invalid_selector", message),
    };
    let Some((program, arguments)) = options.command.split_first() else {
        return fail(2, "missing_command", "Provide a command after --.");
    };
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (selector, program, arguments);
        fail(
            1,
            "tracing_unavailable",
            "This platform has no complete file-operation backend yet.",
        )
    }
    #[cfg(target_os = "linux")]
    {
        let selected_physical: BTreeSet<PathBuf> = match selector.selected_files() {
            Ok(files) => files.into_iter().map(|file| file.physical).collect(),
            Err(_) => return fail(1, "selection_failed", "Cannot enumerate selected files."),
        };
        let (sender, receiver) = mpsc::channel();
        // Subscribe before the first run and keep this subscription across
        // dependency replacement. Events are filtered by observed inputs,
        // never treated as a fallback dependency on the whole root.
        let Ok(mut watcher) = notify::recommended_watcher(move |event| {
            let _ = sender.send(event);
        }) else {
            return fail(
                1,
                "watch_unavailable",
                "Cannot establish filesystem notifications for --root.",
            );
        };
        if watcher
            .watch(selector.root(), RecursiveMode::Recursive)
            .is_err()
        {
            return fail(
                1,
                "watch_unavailable",
                "Cannot watch the selected root; check permissions and watch limits.",
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
        let mut dependencies = Dependencies::new();
        let mut pending = true;
        let mut last_change = None;
        let mut run_number = 0u64;
        loop {
            if cancellation.load(Ordering::Relaxed) {
                return if termination.load(Ordering::Relaxed) {
                    143
                } else {
                    130
                };
            }
            if pending && last_change.is_none_or(|time: Instant| time.elapsed() >= options.debounce)
            {
                pending = false;
                last_change = None;
                run_number += 1;
                tracing::info!(
                    command = "autowatch",
                    stage = "run_start",
                    run_number,
                    "fspy_execution"
                );
                let captured = crate::linux::capture(
                    crate::linux::CaptureRequest {
                        root: selector.root(),
                        program: program.as_os_str(),
                        arguments,
                        child_io: crate::linux::ChildIo::Inherit,
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
                let Ok(captured) = captured else {
                    return fail(
                        1,
                        "tracing_unavailable",
                        "Cannot launch a complete traced execution.",
                    );
                };
                if !captured.complete() {
                    return match captured.failure {
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
                            "A watch run has an incomplete trace.",
                        ),
                    };
                }
                let observed = derive(&selector, &selected_physical, &captured.events);
                let successful = captured.root_status.is_some_and(|status| status.success());
                if successful {
                    dependencies = observed.dependencies;
                } else {
                    dependencies.extend(observed.dependencies);
                }
                if dependencies.is_empty() {
                    return fail(
                        1,
                        "no_watchable_inputs",
                        "No observed project input matched the selectors; choose project inputs \
                         with --include.",
                    );
                }
                if ambiguous_writes(&dependencies, &observed.writes) {
                    return fail(
                        1,
                        "ambiguous_self_writes",
                        "The child writes an observed input; select separate inputs and outputs \
                         to avoid an unsafe watch loop.",
                    );
                }
                tracing::info!(
                    command = "autowatch",
                    stage = "run_finish",
                    run_number,
                    successful,
                    watched = dependencies.len(),
                    "fspy_execution"
                );
                match drain_changes(&receiver, &dependencies, selector.root()) {
                    Ok(changed) if changed => {
                        pending = true;
                        last_change = Some(Instant::now());
                    }
                    Ok(_) => {}
                    Err(()) => {
                        return fail(
                            1,
                            "watch_failed",
                            "Filesystem notifications failed during execution.",
                        );
                    }
                }
            }
            let wait = if let Some(last) = last_change {
                options
                    .debounce
                    .saturating_sub(last.elapsed())
                    .min(Duration::from_millis(100))
            } else {
                Duration::from_millis(100)
            };
            match receiver.recv_timeout(wait) {
                Ok(Ok(event)) if event_changed(&event, &dependencies, selector.root()) => {
                    pending = true;
                    last_change = Some(Instant::now());
                }
                Ok(Ok(_)) | Err(mpsc::RecvTimeoutError::Timeout) => {}
                Ok(Err(_)) | Err(mpsc::RecvTimeoutError::Disconnected) => {
                    return fail(
                        1,
                        "watch_failed",
                        "Filesystem notifications stopped; no further runs can be trusted.",
                    );
                }
            }
        }
    }
}

#[cfg(target_os = "linux")]
fn derive(
    selector: &Selector,
    selected_physical: &BTreeSet<PathBuf>,
    events: &[Event],
) -> Observed {
    let mut dependencies = Dependencies::new();
    let mut writes = BTreeSet::new();
    let mut pending = BTreeMap::new();
    let mut completed = Vec::new();
    for event in events {
        match event {
            Event::OperationStart(start) => {
                pending.insert(start.operation_id, start);
            }
            Event::OperationCompletion(done) => {
                if let Some(start) = pending.remove(&done.operation_id) {
                    completed.push((start, done));
                }
            }
            Event::Header(_) | Event::Summary(_) => {}
        }
    }
    for (start, done) in &completed {
        if done.native_error.is_some() {
            continue;
        }
        if matches!(
            start.operation,
            Operation::Write
                | Operation::Pwrite
                | Operation::Create
                | Operation::Remove
                | Operation::Rename
                | Operation::Link
                | Operation::OtherMutation
        ) {
            for path in &start.paths {
                if let Some(path) = selected_path(selector, selected_physical, path) {
                    writes.insert(path);
                }
            }
        }
    }
    for (start, done) in completed {
        for path in &start.paths {
            let Some(path) = selected_path(selector, selected_physical, path) else {
                continue;
            };
            let include = match start.operation {
                Operation::Read | Operation::Pread => done.native_error.is_none(),
                Operation::Metadata | Operation::Directory => true,
                Operation::Open => {
                    done.native_error == Some(i64::from(libc::ENOENT))
                        || (done.native_error.is_none() && !writes.contains(&path))
                }
                _ => false,
            };
            if include {
                let is_directory = start.operation == Operation::Directory || path.is_dir();
                dependencies.insert(
                    path.clone(),
                    if is_directory {
                        DependencyKind::Directory
                    } else {
                        DependencyKind::File
                    },
                );
            }
        }
    }
    Observed {
        dependencies,
        writes,
    }
}

#[cfg(target_os = "linux")]
fn selected_path(
    selector: &Selector,
    selected_physical: &BTreeSet<PathBuf>,
    path: &EncodedPath,
) -> Option<PathBuf> {
    if !selector.matches_trace_path(path, selected_physical)
        || path.scope != PathScope::Project
        || path.encoding != PathEncoding::UnixBytes
    {
        return None;
    }
    let bytes = path.decode().ok()?;
    let relative = Path::new(std::ffi::OsStr::from_bytes(&bytes));
    let logical = normalize(&selector.root().join(relative));
    if !logical.starts_with(selector.root()) {
        return None;
    }
    Some(fs::canonicalize(&logical).unwrap_or(logical))
}

#[cfg(target_os = "linux")]
fn ambiguous_writes(dependencies: &Dependencies, writes: &BTreeSet<PathBuf>) -> bool {
    dependencies.iter().any(|(dependency, kind)| {
        writes.iter().any(|write| {
            write == dependency
                || (*kind == DependencyKind::Directory && write.starts_with(dependency))
        })
    })
}

#[cfg(target_os = "linux")]
fn drain_changes(
    receiver: &mpsc::Receiver<notify::Result<notify::Event>>,
    dependencies: &Dependencies,
    root: &Path,
) -> Result<bool, ()> {
    let mut changed = false;
    loop {
        match receiver.try_recv() {
            Ok(Ok(event)) => changed |= event_changed(&event, dependencies, root),
            Ok(Err(_)) | Err(mpsc::TryRecvError::Disconnected) => return Err(()),
            Err(mpsc::TryRecvError::Empty) => return Ok(changed),
        }
    }
}

#[cfg(target_os = "linux")]
fn event_changed(event: &notify::Event, dependencies: &Dependencies, root: &Path) -> bool {
    if matches!(event.kind, EventKind::Access(_)) {
        return false;
    }
    event.paths.iter().any(|path| {
        let absolute = if path.is_absolute() {
            normalize(path)
        } else {
            normalize(&root.join(path))
        };
        if !absolute.starts_with(root) {
            return false;
        }
        let physical = fs::canonicalize(&absolute).unwrap_or(absolute);
        dependencies.iter().any(|(dependency, kind)| {
            physical == *dependency
                || (*kind == DependencyKind::Directory && physical.starts_with(dependency))
        })
    })
}

#[cfg(target_os = "linux")]
fn normalize(path: &Path) -> PathBuf {
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::ParentDir => {
                normalized.pop();
            }
            Component::CurDir => {}
            other => normalized.push(other.as_os_str()),
        }
    }
    normalized
}

#[cfg(all(test, target_os = "linux"))]
mod tests {
    use std::collections::{BTreeMap, BTreeSet};

    use super::{DependencyKind, ambiguous_writes};

    #[test]
    fn input_output_overlap_is_ambiguous_but_separate_output_is_not() {
        let mut dependencies = BTreeMap::new();
        dependencies.insert("/root/input".into(), DependencyKind::File);
        let mut writes = BTreeSet::new();
        writes.insert("/root/output".into());
        assert!(!ambiguous_writes(&dependencies, &writes));
        writes.insert("/root/input".into());
        assert!(ambiguous_writes(&dependencies, &writes));
    }
}
