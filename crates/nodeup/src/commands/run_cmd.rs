use std::ffi::OsString;

use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat},
    command_plan::{plan_delegated_command, DelegatedCommandMode},
    commands::print_output,
    errors::{NodeupError, Result},
    process::{run_command, DelegatedStdioPolicy},
    resolver::ResolvedRuntimeTarget,
    types::RuntimeSelectorSource,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct RunResponse {
    runtime: String,
    command: String,
    exit_code: i32,
}

pub fn execute(
    install: bool,
    runtime: &str,
    command: &[String],
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    if command.is_empty() {
        return Err(NodeupError::invalid_input_with_hint(
            format!(
                "Missing delegated command arguments for `nodeup run` (runtime={runtime}, \
                 delegated_argv_len={})",
                command.len()
            ),
            "Use `nodeup run [--install] <runtime> <command> [args...]`.",
        ));
    }

    let resolved = app
        .resolver
        .resolve_selector_with_source(runtime, RuntimeSelectorSource::Explicit)?;

    if let ResolvedRuntimeTarget::Version { version } = &resolved.target {
        if !app.store.is_installed(version) {
            if install {
                app.installer.ensure_installed(version, &app.releases)?;
            } else {
                return Err(NodeupError::not_found_with_hint(
                    format!("Runtime {version} is not installed"),
                    format!(
                        "Install it with `nodeup toolchain install {runtime}` or retry with \
                         `nodeup run --install {runtime} ...`."
                    ),
                ));
            }
        }
    }

    let delegated_command = &command[0];
    let delegated_args = command[1..]
        .iter()
        .map(OsString::from)
        .collect::<Vec<OsString>>();
    let cwd = std::env::current_dir()?;

    let plan = plan_delegated_command(
        &resolved,
        &app.store,
        delegated_command,
        &delegated_args,
        &cwd,
    )?;
    if plan.mode == DelegatedCommandMode::Direct && !plan.executable.exists() {
        return Err(NodeupError::not_found_with_hint(
            format!(
                "Command '{delegated_command}' is not available in runtime {}",
                resolved.runtime_id()
            ),
            format!(
                "Check available commands with `nodeup which --runtime {} {delegated_command}` or \
                 pick a runtime that provides it.",
                resolved.runtime_id()
            ),
        ));
    }

    let package_spec = plan.package_spec.as_deref().unwrap_or("none");
    let package_json_path = plan
        .package_json_path
        .as_ref()
        .map(|path| path.display().to_string())
        .unwrap_or_else(|| "none".to_string());

    info!(
        command_path = "nodeup.run",
        selector_source = resolved.source.as_str(),
        selector = %resolved.selector,
        runtime = %resolved.runtime_id(),
        delegated_command,
        mode = plan.mode.as_str(),
        package_spec,
        package_json_path = %package_json_path,
        reason = plan.reason.as_str(),
        args_len = delegated_args.len(),
        "Running delegated command"
    );

    let stdio_policy = match output {
        OutputFormat::Human => DelegatedStdioPolicy::Inherit,
        OutputFormat::Json => DelegatedStdioPolicy::StdoutToStderr,
    };

    let exit_code = run_command(
        &plan.executable,
        &plan.args,
        stdio_policy,
        "nodeup.run.process",
    )?;

    let response = RunResponse {
        runtime: resolved.runtime_id(),
        command: delegated_command.clone(),
        exit_code,
    };
    let human = format!(
        "Delegated command '{}' exited with status {}",
        delegated_command, exit_code
    );

    print_output(output, color, &human, &response)?;
    Ok(exit_code)
}
