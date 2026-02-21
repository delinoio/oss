use std::ffi::OsString;

use serde::Serialize;
use tracing::info;

use crate::{
    cli::OutputFormat,
    commands::print_output,
    errors::{NodeupError, Result},
    process::run_command,
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
    app: &NodeupApp,
) -> Result<i32> {
    if command.is_empty() {
        return Err(NodeupError::invalid_input(
            "nodeup run requires delegated command arguments",
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
                return Err(NodeupError::not_found(format!(
                    "Runtime {} is not installed. Re-run with --install or run nodeup toolchain \
                     install {}",
                    version, runtime
                )));
            }
        }
    }

    let delegated_command = &command[0];
    let delegated_args = command[1..]
        .iter()
        .map(OsString::from)
        .collect::<Vec<OsString>>();

    let executable = resolved.executable_path(&app.store, delegated_command);
    if !executable.exists() {
        return Err(NodeupError::not_found(format!(
            "Command '{}' is not available in runtime {}",
            delegated_command,
            resolved.runtime_id()
        )));
    }

    info!(
        command_path = "nodeup.run",
        selector_source = ?resolved.source,
        selector = %resolved.selector,
        runtime = %resolved.runtime_id(),
        delegated_command,
        args_len = delegated_args.len(),
        "Running delegated command"
    );

    let exit_code = run_command(&executable, &delegated_args, "nodeup.run.process")?;

    let response = RunResponse {
        runtime: resolved.runtime_id(),
        command: delegated_command.clone(),
        exit_code,
    };
    let human = format!(
        "Delegated command '{}' exited with status {}",
        delegated_command, exit_code
    );

    print_output(output, &human, &response)?;
    Ok(exit_code)
}
