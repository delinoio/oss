pub mod analysis;
pub mod cli;
pub mod error;
pub mod logging;
pub mod parser;
pub mod runner;
pub mod snapshot;
pub mod watch;

use std::path::Path;

use analysis::{analyze_argv, analyze_shell_expression, CommandAnalysis, CommandAnalysisStatus};
use cli::{Cli, CommandMode};
use error::{Result, WithWatchError};
use parser::parse_shell_expression;
use runner::{ExecutionMetadata, ExecutionPlan, RunnerOptions};
use snapshot::{ChangeDetectionMode, WatchInput, WatchInputKind};
use tracing::debug;

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
            let analysis = analyze_argv(&argv, cwd)?;
            log_analysis("passthrough", &analysis);
            let inputs = require_inferred_inputs(&analysis)?;
            Ok(ExecutionPlan::passthrough(
                argv,
                inputs,
                detection_mode,
                execution_metadata(&analysis),
            ))
        }
        CommandMode::Shell { expression } => {
            let parsed = parse_shell_expression(&expression)?;
            let analysis = analyze_shell_expression(&parsed, cwd)?;
            log_analysis("shell", &analysis);
            let inputs = require_inferred_inputs(&analysis)?;
            Ok(ExecutionPlan::shell(
                expression,
                inputs,
                detection_mode,
                execution_metadata(&analysis),
            ))
        }
        CommandMode::Exec { inputs, argv } => {
            let analysis = analyze_argv(&argv, cwd)?;
            log_analysis("exec", &analysis);
            let planned_inputs = explicit_watch_inputs(&inputs, cwd)?;
            Ok(ExecutionPlan::exec(
                argv,
                planned_inputs,
                detection_mode,
                execution_metadata(&analysis),
            ))
        }
    }
}

fn require_inferred_inputs(analysis: &CommandAnalysis) -> Result<Vec<WatchInput>> {
    if analysis.status == CommandAnalysisStatus::Resolved && !analysis.inputs.is_empty() {
        return Ok(analysis.inputs.clone());
    }

    debug!(
        adapter_id = analysis.adapter_field(),
        inference_status = analysis.status.as_str(),
        fallback_used = analysis.fallback_used,
        filtered_output_count = analysis.filtered_output_count,
        "Command analysis did not yield safe inferred inputs"
    );

    Err(WithWatchError::NoWatchInputs)
}

fn execution_metadata(analysis: &CommandAnalysis) -> ExecutionMetadata {
    ExecutionMetadata {
        adapter_ids: analysis.adapter_ids.clone(),
        fallback_used: analysis.fallback_used,
        default_watch_root_used: analysis.default_watch_root_used,
        filtered_output_count: analysis.filtered_output_count,
        side_effect_profile: analysis.side_effect_profile,
        status: analysis.status,
    }
}

fn log_analysis(mode: &str, analysis: &CommandAnalysis) {
    debug!(
        mode,
        adapter_id = analysis.adapter_field(),
        fallback_used = analysis.fallback_used,
        default_watch_root_used = analysis.default_watch_root_used,
        filtered_output_count = analysis.filtered_output_count,
        side_effect_profile = analysis.side_effect_profile.as_str(),
        inference_status = analysis.status.as_str(),
        inferred_input_count = analysis.inputs.len(),
        "Built command analysis"
    );
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
    use super::explicit_watch_inputs;

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
