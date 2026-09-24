#[cfg(target_os = "linux")]
use std::{
    collections::{BTreeMap, BTreeSet},
    fmt::Write as _,
    sync::atomic::Ordering,
    time::Instant,
};
use std::{
    ffi::OsString,
    path::PathBuf,
    sync::{Arc, atomic::AtomicBool},
    time::Duration,
};

use clap::Args;
#[cfg(target_os = "linux")]
use serde::Serialize;

use crate::{
    cli::{ReportOptions, fail, parse_duration, parse_positive},
    output,
    selector::Selector,
    trace::{self, Operation},
};

#[derive(Args)]
pub struct Latencylab {
    /// Existing root for selection; child cwd is unchanged.
    #[arg(long, value_name = "DIR")]
    root: Option<PathBuf>,
    /// Required project-relative globs; repeat to union selections.
    #[arg(long, required = true, value_name = "GLOB")]
    include: Vec<String>,
    /// Project-relative globs to exclude.
    #[arg(long, value_name = "GLOB")]
    exclude: Vec<String>,
    /// Positive delay before each matching operation.
    #[arg(long, required = true, value_parser = parse_duration, value_name = "DURATION")]
    delay: Duration,
    /// Selected operation kinds; default is read.
    #[arg(long = "op", value_enum, value_name = "OP")]
    operations: Vec<Operation>,
    /// Number of baseline/delayed pairs (default: 3).
    #[arg(long, default_value_t = 3, value_parser = parse_positive)]
    runs: usize,
    #[command(flatten)]
    report: ReportOptions,
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
#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "kebab-case")]
enum Condition {
    Baseline,
    Delayed,
}

#[cfg(target_os = "linux")]
#[derive(Serialize)]
struct RunMeasurement {
    pair: usize,
    condition: Condition,
    execution_ns: u64,
    matching_operations: usize,
    requested_delay_ns: u128,
    observed_injected_delay_ns: u128,
    operation_time_ns: u128,
}

#[cfg(target_os = "linux")]
#[derive(Serialize)]
struct LatencyReport {
    runs: Vec<RunMeasurement>,
    baseline_median_ns: u64,
    delayed_median_ns: u64,
    slowdown_ratio: f64,
}

#[expect(
    clippy::too_many_lines,
    reason = "Keep paired execution and first-failure handling together"
)]
pub fn execute(options: Latencylab) -> i32 {
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
    let operations = if options.operations.is_empty() {
        vec![Operation::Read]
    } else {
        options.operations
    };
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
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (
            selector,
            operations,
            program,
            arguments,
            destination,
            cancellation,
        );
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
        let matches = |start: &trace::Start| {
            operations.contains(&start.operation)
                && start
                    .paths
                    .iter()
                    .any(|path| selector.matches_trace_path(path, &selected_physical))
        };
        let delay_rule = |start: &trace::Start| {
            if matches(start) {
                options.delay
            } else {
                Duration::ZERO
            }
        };
        let mut runs = Vec::new();
        let mut baseline = Vec::new();
        let mut delayed = Vec::new();
        for pair in 1..=options.runs {
            for condition in [Condition::Baseline, Condition::Delayed] {
                let requested = if matches!(condition, Condition::Delayed) {
                    Some(&delay_rule as &dyn Fn(&trace::Start) -> Duration)
                } else {
                    None
                };
                tracing::info!(
                    command = "latencylab",
                    stage = "run_start",
                    pair,
                    delayed = requested.is_some(),
                    "fspy_execution"
                );
                let started = Instant::now();
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
                        delay_rule: requested,
                        break_control: None,
                    },
                    &cancellation,
                );
                let elapsed = u64::try_from(started.elapsed().as_nanos()).unwrap_or(u64::MAX);
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
                            "An experiment run has an incomplete trace.",
                        ),
                    };
                }
                if !captured.root_status.is_some_and(|status| status.success()) {
                    return fail(
                        1,
                        "child_failed",
                        "A paired experiment run failed; later runs were not started.",
                    );
                }
                let mut pending = BTreeMap::new();
                let mut matching_operations = 0usize;
                let mut observed_injected_delay_ns = 0u128;
                let mut operation_time_ns = 0u128;
                for event in &captured.events {
                    match event {
                        trace::Event::OperationStart(start) => {
                            pending.insert(start.operation_id, start);
                        }
                        trace::Event::OperationCompletion(done) => {
                            if let Some(start) = pending.remove(&done.operation_id)
                                && matches(start)
                            {
                                matching_operations += 1;
                                observed_injected_delay_ns += u128::from(done.injected_delay_ns);
                                operation_time_ns +=
                                    u128::from(done.monotonic_ns - start.monotonic_ns);
                            }
                        }
                        trace::Event::Header(_) | trace::Event::Summary(_) => {}
                    }
                }
                if matching_operations == 0 {
                    return fail(
                        1,
                        "no_matching_operations",
                        "The selected experiment observed no matching operations.",
                    );
                }
                let requested_delay_ns = if requested.is_some() {
                    options
                        .delay
                        .as_nanos()
                        .saturating_mul(matching_operations as u128)
                } else {
                    0
                };
                runs.push(RunMeasurement {
                    pair,
                    condition,
                    execution_ns: elapsed,
                    matching_operations,
                    requested_delay_ns,
                    observed_injected_delay_ns,
                    operation_time_ns,
                });
                if requested.is_some() {
                    delayed.push(elapsed);
                } else {
                    baseline.push(elapsed);
                }
                tracing::info!(
                    command = "latencylab",
                    stage = "run_finish",
                    pair,
                    delayed = requested.is_some(),
                    matching_operations,
                    execution_ns = elapsed,
                    "fspy_execution"
                );
            }
        }
        let baseline_median_ns = median(&mut baseline);
        let delayed_median_ns = median(&mut delayed);
        #[expect(
            clippy::cast_precision_loss,
            reason = "Nanosecond timings are bounded by practical process execution"
        )]
        let slowdown_ratio = delayed_median_ns as f64 / baseline_median_ns.max(1) as f64;
        let report = LatencyReport {
            runs,
            baseline_median_ns,
            delayed_median_ns,
            slowdown_ratio,
        };
        if !options.report.quiet {
            let bytes = if options.report.json {
                serde_json::to_vec(&report).map(|mut bytes| {
                    bytes.push(b'\n');
                    bytes
                })
            } else {
                Ok(render_human(&report).into_bytes())
            };
            let Ok(bytes) = bytes else {
                return fail(1, "report_encoding", "Cannot encode latency report.");
            };
            if output::write(&bytes, destination, options.report.force).is_err() {
                return fail(1, "output_failed", "Cannot publish latency report.");
            }
        }
        if termination.load(Ordering::Relaxed) {
            143
        } else {
            0
        }
    }
}

#[cfg(target_os = "linux")]
fn median(values: &mut [u64]) -> u64 {
    values.sort_unstable();
    let middle = values.len() / 2;
    if values.len().is_multiple_of(2) {
        values[middle - 1].midpoint(values[middle])
    } else {
        values[middle]
    }
}

#[cfg(target_os = "linux")]
fn render_human(report: &LatencyReport) -> String {
    let mut text = String::from("File-operation latency experiment\n");
    for run in &report.runs {
        let condition = match run.condition {
            Condition::Baseline => "baseline",
            Condition::Delayed => "delayed",
        };
        let _ = writeln!(
            text,
            "pair {} {condition}: execution {} ns; matching {}; requested delay {} ns; observed \
             injected {} ns; operation time {} ns",
            run.pair,
            run.execution_ns,
            run.matching_operations,
            run.requested_delay_ns,
            run.observed_injected_delay_ns,
            run.operation_time_ns
        );
    }
    let _ = writeln!(
        text,
        "median baseline {} ns; median delayed {} ns; slowdown {:.3}x",
        report.baseline_median_ns, report.delayed_median_ns, report.slowdown_ratio
    );
    text
}

#[cfg(test)]
mod tests {
    #[cfg(target_os = "linux")]
    #[test]
    fn median_even_and_odd_are_stable() {
        assert_eq!(super::median(&mut [9, 3, 7]), 7);
        assert_eq!(super::median(&mut [10, 4, 8, 6]), 7);
    }
}
