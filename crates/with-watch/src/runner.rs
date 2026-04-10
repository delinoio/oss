use std::{
    ffi::OsString,
    process::{Child, Command, ExitStatus, Stdio},
    thread,
    time::Duration,
};

use tracing::{debug, info, warn};

use crate::{
    error::{Result, WithWatchError},
    snapshot::{capture_snapshot, ChangeDetectionMode, CommandSource, SnapshotState, WatchInput},
    watch::{CollectedEvents, WatchLoop},
};

const DEFAULT_POLL_TIMEOUT: Duration = Duration::from_millis(50);
const DEFAULT_DEBOUNCE_WINDOW: Duration = Duration::from_millis(200);

#[derive(Debug, Clone)]
pub struct ExecutionPlan {
    pub source: CommandSource,
    pub detection_mode: ChangeDetectionMode,
    pub inputs: Vec<WatchInput>,
    pub delegated_command: DelegatedCommand,
}

impl ExecutionPlan {
    pub fn passthrough(
        argv: Vec<OsString>,
        inputs: Vec<WatchInput>,
        detection_mode: ChangeDetectionMode,
    ) -> Self {
        Self {
            source: CommandSource::Argv,
            detection_mode,
            inputs,
            delegated_command: DelegatedCommand::Argv(argv),
        }
    }

    pub fn shell(
        expression: String,
        inputs: Vec<WatchInput>,
        detection_mode: ChangeDetectionMode,
    ) -> Self {
        Self {
            source: CommandSource::Shell,
            detection_mode,
            inputs,
            delegated_command: DelegatedCommand::Shell(expression),
        }
    }

    pub fn exec(
        argv: Vec<OsString>,
        inputs: Vec<WatchInput>,
        detection_mode: ChangeDetectionMode,
    ) -> Self {
        Self {
            source: CommandSource::Exec,
            detection_mode,
            inputs,
            delegated_command: DelegatedCommand::Argv(argv),
        }
    }
}

#[derive(Debug, Clone)]
pub enum DelegatedCommand {
    Argv(Vec<OsString>),
    Shell(String),
}

impl DelegatedCommand {
    fn display_name(&self) -> String {
        match self {
            Self::Argv(argv) => argv
                .iter()
                .map(|value| value.to_string_lossy().into_owned())
                .collect::<Vec<_>>()
                .join(" "),
            Self::Shell(expression) => expression.clone(),
        }
    }
}

#[derive(Debug, Clone, Copy)]
pub struct RunnerOptions {
    pub debounce_window: Duration,
    pub poll_timeout: Duration,
    pub max_runs: Option<usize>,
}

impl Default for RunnerOptions {
    fn default() -> Self {
        Self {
            debounce_window: DEFAULT_DEBOUNCE_WINDOW,
            poll_timeout: DEFAULT_POLL_TIMEOUT,
            max_runs: None,
        }
    }
}

impl RunnerOptions {
    pub fn from_environment() -> Self {
        let mut options = Self::default();

        // Test-only hooks for deterministic integration coverage. They keep the public
        // CLI surface stable while allowing `cargo test` to stop the
        // long-running watch loop and shorten debounce windows. Remove them
        // when we have a better end-to-end harness.
        if let Ok(raw_max_runs) = std::env::var("WITH_WATCH_TEST_MAX_RUNS") {
            if let Ok(parsed) = raw_max_runs.parse::<usize>() {
                options.max_runs = Some(parsed);
            }
        }

        if let Ok(raw_debounce_ms) = std::env::var("WITH_WATCH_TEST_DEBOUNCE_MS") {
            if let Ok(parsed) = raw_debounce_ms.parse::<u64>() {
                options.debounce_window = Duration::from_millis(parsed);
            }
        }

        options
    }
}

pub fn run(plan: ExecutionPlan, options: RunnerOptions) -> Result<i32> {
    let mut watch_loop = WatchLoop::new(&plan.inputs)?;
    let mut baseline = capture_snapshot(&plan.inputs, plan.detection_mode)?;
    let mut child = Some(spawn_command(&plan.delegated_command)?);
    let mut completed_runs = 0usize;
    let mut queued_snapshot: Option<SnapshotState> = None;

    info!(
        command_source = plan.source.as_str(),
        detection_mode = plan.detection_mode.as_str(),
        input_count = plan.inputs.len(),
        "Starting with-watch run loop"
    );

    loop {
        if let Some(active_child) = child.as_mut() {
            if let Some(status) =
                active_child
                    .try_wait()
                    .map_err(|source| WithWatchError::Wait {
                        command: plan.delegated_command.display_name(),
                        source,
                    })?
            {
                completed_runs += 1;
                let last_exit_code = exit_code_from_status(status);
                info!(
                    completed_runs,
                    last_exit_code,
                    command_source = plan.source.as_str(),
                    "Delegated command finished"
                );
                child = None;

                if options
                    .max_runs
                    .is_some_and(|limit| completed_runs >= limit)
                {
                    return Ok(last_exit_code);
                }

                if let Some(next_snapshot) = queued_snapshot.take() {
                    baseline = next_snapshot;
                    child = Some(spawn_command(&plan.delegated_command)?);
                    continue;
                }
            }
        }

        if let Some(events) =
            watch_loop.collect_events(options.poll_timeout, options.debounce_window)
        {
            handle_watch_events(&events);

            let current_snapshot = capture_snapshot(&plan.inputs, plan.detection_mode)?;
            if current_snapshot.is_meaningfully_different(&baseline, plan.detection_mode) {
                debug!(
                    event_count = events.event_count,
                    path_count = events.path_count,
                    child_running = child.is_some(),
                    "Observed meaningful input changes"
                );

                if child.is_some() {
                    queued_snapshot = Some(current_snapshot);
                } else {
                    baseline = current_snapshot;
                    child = Some(spawn_command(&plan.delegated_command)?);
                }
            } else {
                queued_snapshot = None;
            }
        } else if child.is_none() {
            thread::sleep(Duration::from_millis(10));
        }
    }
}

fn handle_watch_events(events: &CollectedEvents) {
    if events.error_count > 0 {
        warn!(
            error_count = events.error_count,
            event_count = events.event_count,
            path_count = events.path_count,
            "Watcher reported recoverable errors; forcing a rescan"
        );
    } else {
        debug!(
            event_count = events.event_count,
            path_count = events.path_count,
            "Collected filesystem events"
        );
    }
}

fn spawn_command(command: &DelegatedCommand) -> Result<Child> {
    match command {
        DelegatedCommand::Argv(argv) => spawn_argv(argv),
        DelegatedCommand::Shell(expression) => spawn_shell(expression),
    }
}

fn spawn_argv(argv: &[OsString]) -> Result<Child> {
    let program = argv
        .first()
        .cloned()
        .ok_or(WithWatchError::MissingCommand)?;
    let display_name = argv
        .iter()
        .map(|value| value.to_string_lossy().into_owned())
        .collect::<Vec<_>>()
        .join(" ");

    info!(command = display_name, "Spawning delegated argv command");

    Command::new(&program)
        .args(argv.iter().skip(1))
        .stdin(Stdio::inherit())
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit())
        .spawn()
        .map_err(|source| WithWatchError::Spawn {
            command: program.to_string_lossy().into_owned(),
            source,
        })
}

fn spawn_shell(expression: &str) -> Result<Child> {
    #[cfg(not(unix))]
    {
        let _ = expression;
        Err(WithWatchError::UnsupportedShellPlatform)
    }

    #[cfg(unix)]
    {
        info!(expression, "Spawning delegated shell command");
        Command::new("/bin/sh")
            .arg("-c")
            .arg(expression)
            .stdin(Stdio::inherit())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit())
            .spawn()
            .map_err(|source| WithWatchError::Spawn {
                command: expression.to_string(),
                source,
            })
    }
}

fn exit_code_from_status(status: ExitStatus) -> i32 {
    status.code().unwrap_or(1)
}
