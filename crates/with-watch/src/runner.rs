use std::{
    ffi::OsString,
    fs,
    io::{self, IsTerminal, Write},
    path::PathBuf,
    process::{Child, Command, ExitStatus, Stdio},
    thread,
    time::{Duration, Instant},
};

use tracing::{debug, info, warn};

use crate::{
    analysis::{CommandAdapterId, CommandAnalysisStatus, SideEffectProfile},
    error::{Result, WithWatchError},
    snapshot::{capture_snapshot, ChangeDetectionMode, CommandSource, SnapshotState, WatchInput},
    watch::{CollectedEvents, WatchLoop},
};

const DEFAULT_POLL_TIMEOUT: Duration = Duration::from_millis(50);
const DEFAULT_DEBOUNCE_WINDOW: Duration = Duration::from_millis(200);
const CLEAR_TERMINAL_SEQUENCE: &str = "\x1b[2J\x1b[H";
const WITH_WATCH_TEST_RUN_MARKER_DIR_ENV: &str = "WITH_WATCH_TEST_RUN_MARKER_DIR";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OutputRefreshMode {
    Preserve,
    ClearTerminal,
}

impl OutputRefreshMode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Preserve => "preserve",
            Self::ClearTerminal => "clear-terminal",
        }
    }
}

#[derive(Debug, Clone)]
pub struct ExecutionPlan {
    pub source: CommandSource,
    pub detection_mode: ChangeDetectionMode,
    pub output_refresh_mode: OutputRefreshMode,
    pub inputs: Vec<WatchInput>,
    pub delegated_command: DelegatedCommand,
    pub metadata: ExecutionMetadata,
}

impl ExecutionPlan {
    pub fn passthrough(
        argv: Vec<OsString>,
        inputs: Vec<WatchInput>,
        detection_mode: ChangeDetectionMode,
        output_refresh_mode: OutputRefreshMode,
        metadata: ExecutionMetadata,
    ) -> Self {
        Self {
            source: CommandSource::Argv,
            detection_mode,
            output_refresh_mode,
            inputs,
            delegated_command: DelegatedCommand::Argv(argv),
            metadata,
        }
    }

    pub fn shell(
        expression: String,
        inputs: Vec<WatchInput>,
        detection_mode: ChangeDetectionMode,
        output_refresh_mode: OutputRefreshMode,
        metadata: ExecutionMetadata,
    ) -> Self {
        Self {
            source: CommandSource::Shell,
            detection_mode,
            output_refresh_mode,
            inputs,
            delegated_command: DelegatedCommand::Shell(expression),
            metadata,
        }
    }

    pub fn exec(
        argv: Vec<OsString>,
        inputs: Vec<WatchInput>,
        detection_mode: ChangeDetectionMode,
        output_refresh_mode: OutputRefreshMode,
        metadata: ExecutionMetadata,
    ) -> Self {
        Self {
            source: CommandSource::Exec,
            detection_mode,
            output_refresh_mode,
            inputs,
            delegated_command: DelegatedCommand::Argv(argv),
            metadata,
        }
    }
}

#[derive(Debug, Clone)]
pub struct ExecutionMetadata {
    pub adapter_ids: Vec<CommandAdapterId>,
    pub fallback_used: bool,
    pub default_watch_root_used: bool,
    pub filtered_output_count: usize,
    pub side_effect_profile: SideEffectProfile,
    pub status: CommandAnalysisStatus,
}

impl ExecutionMetadata {
    pub fn adapter_field(&self) -> String {
        self.adapter_ids
            .iter()
            .map(|adapter| adapter.as_str())
            .collect::<Vec<_>>()
            .join(",")
    }
}

#[derive(Debug, Clone)]
pub enum DelegatedCommand {
    Argv(Vec<OsString>),
    Shell(String),
}

impl DelegatedCommand {
    fn spawn_log_summary(&self) -> DelegatedCommandLogSummary {
        match self {
            Self::Argv(argv) => {
                let program_name = argv
                    .first()
                    .map(program_name)
                    .unwrap_or_else(|| "<missing>".to_string());
                DelegatedCommandLogSummary {
                    execution_kind: "argv",
                    program_name,
                    arg_count: argv.len().saturating_sub(1),
                }
            }
            Self::Shell(_) => DelegatedCommandLogSummary {
                execution_kind: "shell",
                program_name: "sh".to_string(),
                arg_count: 2,
            },
        }
    }

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

#[derive(Debug, Clone, PartialEq, Eq)]
struct DelegatedCommandLogSummary {
    execution_kind: &'static str,
    program_name: String,
    arg_count: usize,
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
    let mut baseline =
        capture_snapshot_with_logging("initial-baseline", &plan.inputs, plan.detection_mode)?;
    // v1 contract: after inference, watcher setup, and baseline capture succeed,
    // the delegated command must run immediately once before waiting for any
    // filesystem change events.
    let mut child = Some(spawn_command(
        &plan.delegated_command,
        plan.output_refresh_mode,
    )?);
    let mut completed_runs = 0usize;
    let mut pending_rerun = false;
    let mut suppressed_self_change_snapshot = None::<SnapshotState>;

    info!(
        command_source = plan.source.as_str(),
        detection_mode = plan.detection_mode.as_str(),
        output_refresh_mode = plan.output_refresh_mode.as_str(),
        input_count = plan.inputs.len(),
        adapter_id = plan.metadata.adapter_field(),
        fallback_used = plan.metadata.fallback_used,
        default_watch_root_used = plan.metadata.default_watch_root_used,
        filtered_output_count = plan.metadata.filtered_output_count,
        side_effect_profile = plan.metadata.side_effect_profile.as_str(),
        analysis_status = plan.metadata.status.as_str(),
        initial_run_armed = true,
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
                let post_run_snapshot =
                    capture_snapshot_with_logging("post-run", &plan.inputs, plan.detection_mode)?;
                let inputs_changed_since_baseline =
                    post_run_snapshot.is_meaningfully_different(&baseline, plan.detection_mode);
                let additional_change_after_suppression = suppressed_self_change_snapshot
                    .as_ref()
                    .is_some_and(|snapshot| {
                        post_run_snapshot.is_meaningfully_different(snapshot, plan.detection_mode)
                    });
                let should_rerun = if plan.metadata.side_effect_profile
                    == SideEffectProfile::WritesWatchedInputs
                {
                    pending_rerun || additional_change_after_suppression
                } else {
                    pending_rerun && inputs_changed_since_baseline
                };

                if pending_rerun
                    && plan.metadata.side_effect_profile == SideEffectProfile::WritesWatchedInputs
                {
                    debug!(
                        rerun_queued = true,
                        side_effect_profile = plan.metadata.side_effect_profile.as_str(),
                        "Queued rerun after additional changes during self-mutating command \
                         activity"
                    );
                } else if additional_change_after_suppression
                    && plan.metadata.side_effect_profile == SideEffectProfile::WritesWatchedInputs
                {
                    debug!(
                        rerun_queued = true,
                        side_effect_profile = plan.metadata.side_effect_profile.as_str(),
                        "Queued rerun because post-run state diverged from the suppressed \
                         self-change snapshot"
                    );
                } else if suppressed_self_change_snapshot.is_some()
                    && plan.metadata.side_effect_profile == SideEffectProfile::WritesWatchedInputs
                {
                    debug!(
                        rerun_suppressed = true,
                        side_effect_profile = plan.metadata.side_effect_profile.as_str(),
                        "Suppressing rerun after self-mutating command activity"
                    );
                }

                baseline = post_run_snapshot;
                pending_rerun = false;
                suppressed_self_change_snapshot = None;
                child = None;
                write_test_run_marker(completed_runs);

                info!(
                    completed_runs,
                    last_exit_code,
                    command_source = plan.source.as_str(),
                    rerun_queued = should_rerun,
                    "Delegated command finished"
                );

                if options
                    .max_runs
                    .is_some_and(|limit| completed_runs >= limit)
                {
                    return Ok(last_exit_code);
                }

                if should_rerun {
                    child = Some(spawn_command(
                        &plan.delegated_command,
                        plan.output_refresh_mode,
                    )?);
                    continue;
                }
            }
        }

        if let Some(events) =
            watch_loop.collect_events(options.poll_timeout, options.debounce_window)
        {
            handle_watch_events(&events);

            let current_snapshot =
                capture_snapshot_with_logging("event-rescan", &plan.inputs, plan.detection_mode)?;
            let reference_snapshot = if child.is_some()
                && plan.metadata.side_effect_profile == SideEffectProfile::WritesWatchedInputs
            {
                suppressed_self_change_snapshot
                    .as_ref()
                    .unwrap_or(&baseline)
            } else {
                &baseline
            };

            if current_snapshot.is_meaningfully_different(reference_snapshot, plan.detection_mode) {
                debug!(
                    event_count = events.event_count,
                    path_count = events.path_count,
                    child_running = child.is_some(),
                    "Observed meaningful input changes"
                );

                if child.is_some() {
                    if plan.metadata.side_effect_profile == SideEffectProfile::WritesWatchedInputs
                        && suppressed_self_change_snapshot.is_none()
                    {
                        suppressed_self_change_snapshot = Some(current_snapshot);
                        debug!(
                            rerun_suppressed = true,
                            side_effect_profile = plan.metadata.side_effect_profile.as_str(),
                            "Suppressed the first in-run snapshot change for a self-mutating \
                             command"
                        );
                    } else {
                        pending_rerun = true;
                    }
                } else {
                    baseline = current_snapshot;
                    child = Some(spawn_command(
                        &plan.delegated_command,
                        plan.output_refresh_mode,
                    )?);
                }
            } else if child.is_some() {
                debug!(
                    rerun_suppressed = true,
                    "Ignored non-meaningful filesystem churn"
                );
            }
        } else if child.is_none() {
            thread::sleep(Duration::from_millis(10));
        }
    }
}

fn capture_snapshot_with_logging(
    phase: &str,
    inputs: &[WatchInput],
    detection_mode: ChangeDetectionMode,
) -> Result<SnapshotState> {
    let snapshot_modes = summarize_snapshot_modes(inputs);
    let started_at = Instant::now();
    let snapshot = capture_snapshot(inputs, detection_mode)?;

    debug!(
        phase,
        detection_mode = detection_mode.as_str(),
        snapshot_modes,
        input_count = inputs.len(),
        snapshot_entry_count = snapshot.len(),
        elapsed_ms = started_at.elapsed().as_millis() as u64,
        "Captured input snapshot"
    );

    Ok(snapshot)
}

fn summarize_snapshot_modes(inputs: &[WatchInput]) -> String {
    let mut modes = Vec::new();
    for input in inputs {
        let snapshot_mode = input.snapshot_mode_label();
        if !modes.contains(&snapshot_mode) {
            modes.push(snapshot_mode);
        }
    }

    modes.join(",")
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

fn spawn_command(
    command: &DelegatedCommand,
    output_refresh_mode: OutputRefreshMode,
) -> Result<Child> {
    prepare_output_for_run(output_refresh_mode)?;
    log_delegated_command_spawn(command);
    match command {
        DelegatedCommand::Argv(argv) => spawn_argv(argv),
        DelegatedCommand::Shell(expression) => spawn_shell(expression),
    }
}

fn prepare_output_for_run(output_refresh_mode: OutputRefreshMode) -> Result<()> {
    let mut stdout = std::io::stdout();
    let stdout_is_terminal = stdout.is_terminal();
    let terminal_cleared =
        refresh_output_before_run(output_refresh_mode, stdout_is_terminal, &mut stdout)
            .map_err(WithWatchError::StdoutRefresh)?;

    debug!(
        output_refresh_mode = output_refresh_mode.as_str(),
        stdout_is_terminal, terminal_cleared, "Prepared stdout for delegated command"
    );

    Ok(())
}

fn refresh_output_before_run<W: Write>(
    output_refresh_mode: OutputRefreshMode,
    stdout_is_terminal: bool,
    output: &mut W,
) -> io::Result<bool> {
    if output_refresh_mode != OutputRefreshMode::ClearTerminal || !stdout_is_terminal {
        return Ok(false);
    }

    output.write_all(CLEAR_TERMINAL_SEQUENCE.as_bytes())?;
    output.flush()?;
    Ok(true)
}

fn log_delegated_command_spawn(command: &DelegatedCommand) {
    let summary = command.spawn_log_summary();
    info!(
        execution_kind = summary.execution_kind,
        program = summary.program_name,
        arg_count = summary.arg_count,
        "Spawning delegated command"
    );
}

fn spawn_argv(argv: &[OsString]) -> Result<Child> {
    let program = argv
        .first()
        .cloned()
        .ok_or(WithWatchError::MissingCommand)?;

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

fn program_name(program: &OsString) -> String {
    std::path::Path::new(program)
        .file_name()
        .unwrap_or(program.as_os_str())
        .to_string_lossy()
        .into_owned()
}

fn exit_code_from_status(status: ExitStatus) -> i32 {
    status.code().unwrap_or(1)
}

fn write_test_run_marker(completed_runs: usize) {
    let Ok(marker_dir) = std::env::var(WITH_WATCH_TEST_RUN_MARKER_DIR_ENV) else {
        return;
    };

    let marker_path = PathBuf::from(marker_dir).join(format!("run-{completed_runs}.done"));
    if let Some(parent) = marker_path.parent() {
        if let Err(error) = fs::create_dir_all(parent) {
            warn!(
                path = parent.display().to_string(),
                %error,
                "Failed to create test run marker directory"
            );
            return;
        }
    }

    if let Err(error) = fs::write(&marker_path, completed_runs.to_string()) {
        warn!(
            path = marker_path.display().to_string(),
            %error,
            "Failed to write test run marker"
        );
    }
}

#[cfg(test)]
mod tests {
    use std::{
        ffi::OsString,
        io::{self, Write},
        sync::{Arc, Mutex},
    };

    use tracing::Level;

    use super::{
        log_delegated_command_spawn, refresh_output_before_run, DelegatedCommand,
        OutputRefreshMode, CLEAR_TERMINAL_SEQUENCE,
    };

    #[test]
    fn argv_spawn_logging_omits_argument_values() {
        let output = capture_logs(|| {
            log_delegated_command_spawn(&DelegatedCommand::Argv(vec![
                OsString::from("env"),
                OsString::from("TOKEN=secret"),
                OsString::from("cmd"),
            ]));
        });

        assert!(output.contains("execution_kind=\"argv\""));
        assert!(output.contains("program=\"env\""));
        assert!(output.contains("arg_count=2"));
        assert!(!output.contains("TOKEN=secret"));
        assert!(!output.contains("cmd"));
    }

    #[test]
    fn shell_spawn_logging_omits_expression_text() {
        let output = capture_logs(|| {
            log_delegated_command_spawn(&DelegatedCommand::Shell(
                "TOKEN=secret grep -f patterns.txt file.txt".to_string(),
            ));
        });

        assert!(output.contains("execution_kind=\"shell\""));
        assert!(output.contains("program=\"sh\""));
        assert!(output.contains("arg_count=2"));
        assert!(!output.contains("TOKEN=secret"));
        assert!(!output.contains("patterns.txt"));
    }

    #[test]
    fn clear_refresh_mode_writes_escape_sequence_and_flushes_for_terminals() {
        let mut writer = FlushTrackingWriter::default();

        let cleared =
            refresh_output_before_run(OutputRefreshMode::ClearTerminal, true, &mut writer)
                .expect("clear terminal output");

        assert!(cleared);
        assert_eq!(writer.buffer, CLEAR_TERMINAL_SEQUENCE.as_bytes());
        assert_eq!(writer.flush_count, 1);
    }

    #[test]
    fn preserve_refresh_mode_does_not_write_escape_sequence() {
        let mut writer = FlushTrackingWriter::default();

        let cleared = refresh_output_before_run(OutputRefreshMode::Preserve, true, &mut writer)
            .expect("skip refresh");

        assert!(!cleared);
        assert!(writer.buffer.is_empty());
        assert_eq!(writer.flush_count, 0);
    }

    #[test]
    fn clear_refresh_mode_skips_non_terminal_outputs() {
        let mut writer = FlushTrackingWriter::default();

        let cleared =
            refresh_output_before_run(OutputRefreshMode::ClearTerminal, false, &mut writer)
                .expect("skip non-terminal refresh");

        assert!(!cleared);
        assert!(writer.buffer.is_empty());
        assert_eq!(writer.flush_count, 0);
    }

    fn capture_logs(callback: impl FnOnce()) -> String {
        let buffer = Arc::new(Mutex::new(Vec::new()));
        let writer = SharedWriter(buffer.clone());
        let subscriber = tracing_subscriber::fmt()
            .with_ansi(false)
            .with_target(false)
            .with_level(false)
            .without_time()
            .with_max_level(Level::INFO)
            .with_writer(move || writer.clone())
            .finish();

        tracing::subscriber::with_default(subscriber, callback);

        let output = buffer.lock().expect("lock buffer").clone();
        String::from_utf8(output).expect("utf8 log output")
    }

    #[derive(Clone)]
    struct SharedWriter(Arc<Mutex<Vec<u8>>>);

    impl Write for SharedWriter {
        fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
            self.0
                .lock()
                .expect("lock log buffer")
                .extend_from_slice(buf);
            Ok(buf.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    #[derive(Default)]
    struct FlushTrackingWriter {
        buffer: Vec<u8>,
        flush_count: usize,
    }

    impl Write for FlushTrackingWriter {
        fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
            self.buffer.extend_from_slice(buf);
            Ok(buf.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            self.flush_count += 1;
            Ok(())
        }
    }
}
