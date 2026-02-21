use std::path::Path;

use serde::Serialize;

use crate::{
    cli::OutputFormat,
    commands::print_output,
    errors::{NodeupError, Result},
    resolver::ResolvedRuntimeTarget,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct WhichResponse {
    runtime: String,
    command: String,
    executable_path: String,
}

pub fn execute(
    runtime: Option<&str>,
    command: &str,
    output: OutputFormat,
    app: &NodeupApp,
) -> Result<i32> {
    let cwd = std::env::current_dir()?;
    let resolved = app.resolver.resolve_with_precedence(runtime, &cwd)?;

    if let ResolvedRuntimeTarget::Version { version } = &resolved.target {
        if !app.store.is_installed(version) {
            return Err(NodeupError::not_found(format!(
                "Runtime {} is not installed",
                version
            )));
        }
    }

    let executable = resolved.executable_path(&app.store, command);
    if !Path::new(&executable).exists() {
        return Err(NodeupError::not_found(format!(
            "Command '{command}' does not exist for runtime {}",
            resolved.runtime_id()
        )));
    }

    let response = WhichResponse {
        runtime: resolved.runtime_id(),
        command: command.to_string(),
        executable_path: executable.to_string_lossy().to_string(),
    };
    let human = response.executable_path.clone();
    print_output(output, &human, &response)?;

    Ok(0)
}
