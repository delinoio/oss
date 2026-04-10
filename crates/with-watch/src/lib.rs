pub mod cli;
pub mod error;
pub mod logging;
pub mod parser;
pub mod runner;
pub mod snapshot;
pub mod watch;

use std::path::Path;

use cli::{Cli, CommandMode};
use error::{Result, WithWatchError};
use parser::parse_shell_expression;
use runner::{ExecutionPlan, RunnerOptions};
use snapshot::{ChangeDetectionMode, WatchInput, WatchInputKind};

pub fn run_cli(cli: Cli, options: RunnerOptions) -> Result<i32> {
    let mode = cli.command_mode()?;
    let cwd = std::env::current_dir().map_err(WithWatchError::CurrentDirectory)?;
    let detection_mode = cli.change_detection_mode();
    let plan = build_execution_plan(mode, detection_mode, &cwd)?;
    runner::run(plan, options)
}

fn build_execution_plan(
    mode: CommandMode,
    detection_mode: ChangeDetectionMode,
    cwd: &Path,
) -> Result<ExecutionPlan> {
    match mode {
        CommandMode::Passthrough { argv } => {
            let inputs = infer_watch_inputs_from_argv(&argv, cwd)?;
            Ok(ExecutionPlan::passthrough(argv, inputs, detection_mode))
        }
        CommandMode::Shell { expression } => {
            let parsed = parse_shell_expression(&expression)?;
            let inputs = infer_watch_inputs_from_values(&parsed.input_candidates, cwd)?;
            Ok(ExecutionPlan::shell(expression, inputs, detection_mode))
        }
        CommandMode::Exec { inputs, argv } => {
            let planned_inputs = explicit_watch_inputs(&inputs, cwd)?;
            Ok(ExecutionPlan::exec(argv, planned_inputs, detection_mode))
        }
    }
}

fn infer_watch_inputs_from_argv(
    argv: &[std::ffi::OsString],
    cwd: &Path,
) -> Result<Vec<WatchInput>> {
    if argv.is_empty() {
        return Err(WithWatchError::MissingCommand);
    }

    let mut values = Vec::new();
    for raw in argv.iter().skip(1) {
        push_watch_candidates_from_os(raw, &mut values);
    }

    infer_watch_inputs_from_values(&values, cwd)
}

fn infer_watch_inputs_from_values(values: &[String], cwd: &Path) -> Result<Vec<WatchInput>> {
    let mut inputs = Vec::new();

    for value in values {
        push_watch_input_from_token(value, cwd, &mut inputs)?;
    }

    if inputs.is_empty() {
        return Err(WithWatchError::NoWatchInputs);
    }

    Ok(inputs)
}

fn explicit_watch_inputs(raw_inputs: &[String], cwd: &Path) -> Result<Vec<WatchInput>> {
    let mut inputs = Vec::new();
    for raw in raw_inputs {
        let trimmed = raw.trim();
        if trimmed.is_empty() {
            continue;
        }
        let input = if has_glob_magic(trimmed) {
            WatchInput::glob(trimmed, cwd)?
        } else {
            WatchInput::path(trimmed, cwd, WatchInputKind::Explicit)?
        };
        push_unique_input(&mut inputs, input);
    }
    Ok(inputs)
}

fn push_watch_candidates_from_os(raw: &std::ffi::OsString, values: &mut Vec<String>) {
    let text = raw.to_string_lossy();
    push_watch_candidates_from_text(&text, values);
}

fn push_watch_candidates_from_text(raw: &str, values: &mut Vec<String>) {
    if raw.is_empty() {
        return;
    }

    if let Some((prefix, value)) = raw.split_once('=') {
        if prefix.starts_with('-') && !value.is_empty() {
            values.push(value.to_string());
            return;
        }
    }

    if raw.starts_with('-') {
        return;
    }

    values.push(raw.to_string());
}

fn push_watch_input_from_token(raw: &str, cwd: &Path, inputs: &mut Vec<WatchInput>) -> Result<()> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Ok(());
    }

    let input = if has_glob_magic(trimmed) {
        WatchInput::glob(trimmed, cwd)?
    } else {
        WatchInput::path(trimmed, cwd, WatchInputKind::Inferred)?
    };

    push_unique_input(inputs, input);
    Ok(())
}

fn push_unique_input(inputs: &mut Vec<WatchInput>, input: WatchInput) {
    if !inputs.contains(&input) {
        inputs.push(input);
    }
}

fn has_glob_magic(raw: &str) -> bool {
    raw.contains('*') || raw.contains('?') || raw.contains('[')
}

#[cfg(test)]
mod tests {
    use std::{ffi::OsString, fs, path::Path};

    use super::{
        explicit_watch_inputs, infer_watch_inputs_from_argv, infer_watch_inputs_from_values,
    };
    use crate::snapshot::WatchInputKind;

    #[test]
    fn infers_existing_and_missing_paths_from_passthrough_argv() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let existing = temp_dir.path().join("input.txt");
        fs::write(&existing, "hello").expect("write input");

        let inputs = infer_watch_inputs_from_argv(
            &[
                OsString::from("cp"),
                existing.as_os_str().to_os_string(),
                OsString::from("output.txt"),
            ],
            temp_dir.path(),
        )
        .expect("infer inputs");

        assert_eq!(inputs.len(), 2);
        assert!(inputs
            .iter()
            .any(|input| input.kind() == WatchInputKind::Inferred));
    }

    #[test]
    fn inferred_values_require_at_least_one_candidate() {
        let error =
            infer_watch_inputs_from_values(&[], Path::new(".")).expect_err("expected error");
        assert!(error
            .to_string()
            .contains("No watch inputs could be inferred"));
    }

    #[test]
    fn explicit_inputs_accept_globs_and_paths() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let inputs = explicit_watch_inputs(
            &["src/**/*.rs".to_string(), "README.md".to_string()],
            temp_dir.path(),
        )
        .expect("explicit inputs");

        assert_eq!(inputs.len(), 2);
    }
}
