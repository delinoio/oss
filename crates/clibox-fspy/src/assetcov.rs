use std::{
    collections::BTreeMap,
    ffi::OsString,
    path::PathBuf,
    sync::{Arc, atomic::AtomicBool},
    time::Duration,
};
#[cfg(target_os = "linux")]
use std::{collections::BTreeSet, fmt::Write as _};

use clap::Args;
#[cfg(target_os = "linux")]
use serde::Serialize;

use crate::{
    cli::{ReportOptions, fail, parse_duration, parse_positive},
    output,
    selector::Selector,
    trace,
};
#[cfg(target_os = "linux")]
use crate::{
    cli::{exit_code, render_path},
    trace::{EncodedPath, Operation, PathEncoding, PathScope},
};

#[derive(Args)]
pub struct Assetcov {
    /// Existing root for selection; child cwd is unchanged.
    #[arg(long, value_name = "DIR")]
    root: Option<PathBuf>,
    /// Required project-relative resource globs; repeat to union selections.
    #[arg(long, required = true, value_name = "GLOB")]
    include: Vec<String>,
    /// Project-relative resource globs to exclude.
    #[arg(long, value_name = "GLOB")]
    exclude: Vec<String>,
    /// Fail when coverage is below this percentage (0..=100).
    #[arg(long, value_parser = parse_percentage, value_name = "PERCENT")]
    fail_under: Option<f64>,
    #[command(flatten)]
    report: ReportOptions,
    /// Optional execution budget in integer ms/s/m/h.
    #[arg(long, value_parser = parse_duration, value_name = "DURATION")]
    timeout: Option<Duration>,
    /// Grace period before forceful descendant termination.
    #[arg(long, default_value = "5s", value_parser = parse_duration, value_name = "DURATION")]
    kill_after: Duration,
    /// Maximum traced operations.
    #[arg(long, default_value_t = trace::DEFAULT_MAX_EVENTS, value_parser = parse_positive)]
    max_events: usize,
    /// Maximum encoded trace bytes.
    #[arg(long, default_value_t = trace::DEFAULT_MAX_BYTES, value_parser = parse_positive)]
    max_trace_bytes: usize,
    /// Command and literal arguments; use -- before COMMAND.
    #[arg(last = true, required = true, num_args = 1.., value_name = "COMMAND [ARG...]")]
    command: Vec<OsString>,
}

fn parse_percentage(value: &str) -> Result<f64, &'static str> {
    value
        .parse::<f64>()
        .ok()
        .filter(|number| number.is_finite() && (0.0..=100.0).contains(number))
        .ok_or("Use a percentage from 0 through 100.")
}

#[cfg(target_os = "linux")]
#[derive(Serialize)]
struct CoverageReport {
    covered_count: usize,
    total_count: usize,
    percentage: f64,
    covered: Vec<EncodedPath>,
    uncovered: Vec<EncodedPath>,
}

#[expect(
    clippy::too_many_lines,
    reason = "Keep the coverage run and result gate together"
)]
pub fn execute(options: Assetcov) -> i32 {
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
    let Ok(selected) = selector.selected_files() else {
        return fail(1, "selection_failed", "Cannot enumerate selected files.");
    };
    let mut denominator = BTreeMap::new();
    for file in selected {
        denominator
            .entry(file.physical)
            .or_insert((file.logical, file.initially_empty));
    }
    if denominator.is_empty() {
        return fail(
            1,
            "empty_denominator",
            "No existing regular resource files match --include.",
        );
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
    tracing::info!(
        command = "assetcov",
        stage = "start",
        total = denominator.len(),
        "fspy_execution"
    );
    #[cfg(target_os = "linux")]
    let captured = crate::linux::capture(
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
            stderr_match: None,
            deny_rule: None,
        },
        &cancellation,
    );
    #[cfg(not(target_os = "linux"))]
    let captured: Result<(), std::io::Error> = Err(std::io::Error::other("unavailable"));
    #[cfg(target_os = "linux")]
    let Ok(captured) = captured else {
        return fail(
            1,
            "tracing_unavailable",
            "Cannot launch a complete traced execution.",
        );
    };
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (
            captured,
            program,
            arguments,
            cancellation,
            destination,
            denominator,
        );
        fail(
            1,
            "tracing_unavailable",
            "This platform has no complete file-operation backend yet.",
        )
    }
    #[cfg(target_os = "linux")]
    {
        if !captured.complete() {
            use crate::linux::LinuxTraceError;
            return match captured.failure {
                Some(LinuxTraceError::Timeout) => 124,
                Some(LinuxTraceError::Cancelled) => {
                    if termination.load(std::sync::atomic::Ordering::Relaxed) {
                        143
                    } else {
                        130
                    }
                }
                _ => fail(
                    1,
                    "tracing_incomplete",
                    "Cannot calculate coverage from an incomplete trace.",
                ),
            };
        }
        let mut covered = BTreeSet::new();
        let mut pending = BTreeMap::new();
        for event in &captured.events {
            match event {
                trace::Event::OperationStart(start) => {
                    pending.insert(start.operation_id, start);
                }
                trace::Event::OperationCompletion(completion) => {
                    let Some(start) = pending.remove(&completion.operation_id) else {
                        continue;
                    };
                    if !matches!(start.operation, Operation::Read | Operation::Pread)
                        || completion.native_error.is_some()
                    {
                        continue;
                    }
                    for path in start.paths.iter().chain(&completion.resolved_paths) {
                        if let Some(physical) = selector.resolve_trace_path(path)
                            && let Some((_, initially_empty)) = denominator.get(&physical)
                            && (completion.bytes.is_some_and(|bytes| bytes > 0)
                                || (completion.bytes == Some(0) && *initially_empty))
                        {
                            covered.insert(physical);
                        }
                    }
                }
                trace::Event::Header(_) | trace::Event::Summary(_) => {}
            }
        }
        let total_count = denominator.len();
        let covered_count = covered.len();
        #[expect(
            clippy::cast_precision_loss,
            reason = "More than 2^53 selected files cannot be enumerated in this process"
        )]
        let percentage = covered_count as f64 * 100.0 / total_count as f64;
        let encode = |relative: &PathBuf| {
            use std::os::unix::ffi::OsStrExt as _;
            EncodedPath::from_raw(
                PathScope::Project,
                PathEncoding::UnixBytes,
                relative.as_os_str().as_bytes(),
            )
        };
        let mut report = CoverageReport {
            covered_count,
            total_count,
            percentage,
            covered: Vec::new(),
            uncovered: Vec::new(),
        };
        for (physical, (logical, _)) in denominator {
            if covered.contains(&physical) {
                report.covered.push(encode(&logical));
            } else {
                report.uncovered.push(encode(&logical));
            }
        }
        if !options.report.quiet {
            let bytes = if options.report.json {
                serde_json::to_vec(&report).map(|mut bytes| {
                    bytes.push(b'\n');
                    bytes
                })
            } else {
                let mut output =
                    format!("Asset coverage: {covered_count}/{total_count} ({percentage:.2}%)\n");
                for (title, paths) in [
                    ("Covered", &report.covered),
                    ("Uncovered", &report.uncovered),
                ] {
                    let _ = writeln!(output, "{title}:");
                    for path in paths {
                        let _ = writeln!(output, "  {}", render_path(path));
                    }
                }
                Ok(output.into_bytes())
            };
            let Ok(bytes) = bytes else {
                return fail(1, "report_encoding", "Cannot encode coverage report.");
            };
            if output::write(&bytes, destination, options.report.force).is_err() {
                return fail(1, "output_failed", "Cannot publish coverage report.");
            }
        }
        tracing::info!(
            command = "assetcov",
            stage = "finish",
            covered = covered_count,
            total = total_count,
            "fspy_execution"
        );
        if termination.load(std::sync::atomic::Ordering::Relaxed) {
            return 143;
        }
        let child_code = captured.root_status.map_or(1, exit_code);
        if child_code != 0 {
            return child_code;
        }
        i32::from(
            options
                .fail_under
                .is_some_and(|threshold| percentage < threshold),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::parse_percentage;

    #[test]
    fn percentages_are_bounded_and_finite() {
        assert_eq!(parse_percentage("0"), Ok(0.0));
        assert_eq!(parse_percentage("100"), Ok(100.0));
        assert!(parse_percentage("NaN").is_err());
        assert!(parse_percentage("101").is_err());
        assert!(parse_percentage("-1").is_err());
    }
}
