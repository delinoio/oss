use std::path::Path;

use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat},
    command_plan::{plan_delegated_command, DelegatedCommandMode},
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
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    let cwd = std::env::current_dir()?;
    let resolved = app.resolver.resolve_with_precedence(runtime, &cwd)?;

    if let ResolvedRuntimeTarget::Version { version } = &resolved.target {
        if !app.store.is_installed(version) {
            return Err(NodeupError::not_found_with_hint(
                format!("Runtime {version} is not installed"),
                "Install it with `nodeup toolchain install <runtime>` and retry `nodeup which \
                 ...`.",
            ));
        }
    }

    let plan = plan_delegated_command(&resolved, &app.store, command, &[], &cwd)?;

    if plan.mode == DelegatedCommandMode::Direct && !Path::new(&plan.executable).exists() {
        return Err(NodeupError::not_found_with_hint(
            format!(
                "Command '{command}' does not exist for runtime {}",
                resolved.runtime_id()
            ),
            "Use `nodeup show active-runtime` to confirm the runtime, then install or relink a \
             runtime that provides the command.",
        ));
    }

    let package_spec = plan.package_spec.as_deref().unwrap_or("none");
    let package_json_path = plan
        .package_json_path
        .as_ref()
        .map(|path| path.display().to_string())
        .unwrap_or_else(|| "none".to_string());

    info!(
        command_path = "nodeup.which",
        selector_source = resolved.source.as_str(),
        selector = %resolved.selector,
        runtime = %resolved.runtime_id(),
        delegated_command = command,
        mode = plan.mode.as_str(),
        package_spec,
        package_json_path = %package_json_path,
        reason = plan.reason.as_str(),
        executable = %plan.executable.display(),
        "Resolved delegated executable"
    );

    let response = WhichResponse {
        runtime: resolved.runtime_id(),
        command: command.to_string(),
        executable_path: plan.executable.to_string_lossy().to_string(),
    };
    let human = response.executable_path.clone();
    print_output(output, color, &human, &response)?;

    Ok(0)
}
